import type { RPCRequest, RPCResponse, RPCNotification } from "./types";
import {
  injectProjectId as scopeInjectProjectId,
  type ProjectIdProvider,
} from "./scope";

type NotificationHandler = (params: unknown) => void;

// RpcError preserves the JSON-RPC error code + data so callers can branch on
// stable codes (e.g. -32010 for skillhub auth-required) instead of regex on
// English error strings. `instanceof RpcError` detects RPC failures vs
// connection / parse errors.
export class RpcError extends Error {
  code: number;
  data?: unknown;
  constructor(message: string, code: number, data?: unknown) {
    super(message);
    this.name = "RpcError";
    this.code = code;
    this.data = data;
  }
}

export class RPCClient {
  private ws: WebSocket | null = null;
  private url: string;
  private nextId = 1;
  private pending = new Map<
    string,
    { resolve: (v: unknown) => void; reject: (e: Error) => void }
  >();
  private handlers = new Map<string, NotificationHandler[]>();
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private reconnectAttempt = 0;
  private _connected = false;
  private projectIdProvider: ProjectIdProvider | null = null;

  constructor(url: string) {
    this.url = url;
  }

  /**
   * Bind a function that returns the active projectId. The client invokes it
   * on every request() call (after auth/skip-list filtering) and merges the
   * value into params. Pass null to clear (e.g. on logout).
   */
  setProjectIdProvider(fn: ProjectIdProvider | null) {
    this.projectIdProvider = fn;
  }

  get connected() {
    return this._connected;
  }

  connect() {
    if (
      this.ws &&
      (this.ws.readyState === WebSocket.OPEN ||
        this.ws.readyState === WebSocket.CONNECTING)
    ) {
      return;
    }
    this.ws = new WebSocket(this.url);

    this.ws.onopen = () => {
      this._connected = true;
      this.reconnectAttempt = 0;
      this.emit("_connected", null);
    };

    this.ws.onclose = () => {
      this._connected = false;
      this.ws = null;
      this.emit("_disconnected", null);
      // Reject all pending requests.
      const err = new Error("WebSocket disconnected");
      for (const p of this.pending.values()) p.reject(err);
      this.pending.clear();
      this.scheduleReconnect();
    };

    this.ws.onerror = (event) => {
      console.error("WebSocket error", event);
      this.ws?.close();
    };

    this.ws.onmessage = (event) => {
      this.parseAndDispatch(event.data);
    };
  }

  disconnect() {
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.ws?.close();
    this.ws = null;
  }

  async request<T = unknown>(
    method: string,
    params?: Record<string, unknown>
  ): Promise<T> {
    await this.ensureConnected();
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      throw new Error("not connected");
    }
    const finalParams = this.injectProjectId(method, params);
    const id = this.nextId++;
    const msg: RPCRequest = { jsonrpc: "2.0", id, method, params: finalParams };
    return new Promise<T>((resolve, reject) => {
      this.pending.set(String(id), {
        resolve: resolve as (v: unknown) => void,
        reject,
      });
      this.ws!.send(JSON.stringify(msg));
    });
  }

  private injectProjectId(
    method: string,
    params?: Record<string, unknown>,
  ): Record<string, unknown> | undefined {
    return scopeInjectProjectId(method, params, this.projectIdProvider);
  }

  on(method: string, handler: NotificationHandler) {
    const list = this.handlers.get(method) || [];
    list.push(handler);
    this.handlers.set(method, list);
    return () => {
      const arr = this.handlers.get(method);
      if (arr) {
        const idx = arr.indexOf(handler);
        if (idx >= 0) arr.splice(idx, 1);
      }
    };
  }

  private emit(method: string, params: unknown) {
    const list = this.handlers.get(method);
    if (list) {
      for (const h of list) {
        try {
          h(params);
        } catch (err) {
          console.error("rpc handler error", method, err);
        }
      }
    }
  }

  private async parseAndDispatch(raw: unknown) {
    try {
      let text: string;
      if (typeof raw === "string") {
        text = raw;
      } else if (typeof Blob !== "undefined" && raw instanceof Blob) {
        text = await raw.text();
      } else {
        return;
      }
      const data = JSON.parse(text);
      if ("id" in data && data.id != null) {
        const p = this.pending.get(String(data.id));
        if (p) {
          this.pending.delete(String(data.id));
          if (data.error) {
            p.reject(
              new RpcError(
                data.error.message ?? "rpc error",
                typeof data.error.code === "number" ? data.error.code : -32000,
                data.error.data
              )
            );
          } else {
            p.resolve(data.result);
          }
        }
      } else if ("method" in data) {
        this.emit(data.method, data.params);
      }
    } catch {
      // ignore parse errors
    }
  }

  private async ensureConnected(timeoutMs = 3000): Promise<void> {
    if (
      !this.ws ||
      this.ws.readyState === WebSocket.CLOSED ||
      this.ws.readyState === WebSocket.CLOSING
    ) {
      this.connect();
    }
    if (!this.ws) throw new Error("not connected");
    if (this.ws.readyState === WebSocket.OPEN) return;
    if (this.ws.readyState !== WebSocket.CONNECTING)
      throw new Error("not connected");

    await new Promise<void>((resolve, reject) => {
      const ws = this.ws;
      if (!ws) return reject(new Error("not connected"));
      const cleanup = () => {
        ws.removeEventListener("open", onOpen);
        ws.removeEventListener("error", onErr);
        clearTimeout(timer);
      };
      const onOpen = () => {
        cleanup();
        resolve();
      };
      const onErr = () => {
        cleanup();
        reject(new Error("connection failed"));
      };
      const timer = setTimeout(() => {
        cleanup();
        reject(new Error("connection timeout"));
      }, timeoutMs);
      ws.addEventListener("open", onOpen);
      ws.addEventListener("error", onErr);
    });
  }

  private scheduleReconnect() {
    if (this.reconnectTimer) return;
    // Exponential backoff with jitter: base 1s, doubles each attempt, capped at 30s.
    const base = Math.min(1000 * Math.pow(2, this.reconnectAttempt), 30000);
    const delay = base * (0.75 + Math.random() * 0.5); // ±25% jitter
    this.reconnectAttempt++;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      this.connect();
    }, delay);
  }
}

/** Resolve WebSocket URL based on current page context. */
export function resolveWsUrl(): string {
  if (typeof window === "undefined") return "ws://127.0.0.1:10112/ws";
  const { protocol, hostname, host, port } = window.location;
  const wsProt = protocol === "https:" ? "wss:" : "ws:";
  // Dev mode: frontend on 10111, server on 10112
  if (port === "10111") return `${wsProt}//${hostname}:10112/ws`;
  // Embedded mode: same origin
  return `${wsProt}//${host}/ws`;
}
