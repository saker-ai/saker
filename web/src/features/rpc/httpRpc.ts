import { RpcError } from "./client";
import {
  injectProjectId as scopeInjectProjectId,
  type ProjectIdProvider,
} from "./scope";

// Module-level provider so the bootstrap path doesn't need to thread the
// projectStore through every helper. ChatApp wires it once at mount, the
// same way it wires the WebSocket client's provider.
let projectIdProvider: ProjectIdProvider | null = null;

export function setHTTPProjectIdProvider(fn: ProjectIdProvider | null) {
  projectIdProvider = fn;
}

/**
 * Resolve the HTTP base URL from the current page context, mirroring
 * resolveWsUrl(): in dev (port 10111) talk to the API server on 10112,
 * in embedded mode use the same origin.
 */
export function resolveHttpBaseUrl(): string {
  if (typeof window === "undefined") return "http://127.0.0.1:10112";
  const { protocol, hostname, host, port } = window.location;
  if (port === "10111") return `${protocol}//${hostname}:10112`;
  return `${protocol}//${host}`;
}

/**
 * Issue a single JSON-RPC method call over HTTP via the /api/rpc/{method}
 * adapter. Same projectId injection rules as the WebSocket client so a
 * call moved between transports behaves identically. On HTTP error the
 * response body is parsed as `{code, message}` and surfaced as an RpcError
 * (so callers can branch on the same error codes regardless of transport).
 */
export async function httpRequest<T = unknown>(
  method: string,
  params?: Record<string, unknown>,
): Promise<T> {
  const finalParams = scopeInjectProjectId(method, params, projectIdProvider);
  const base = resolveHttpBaseUrl();
  const url = `${base}/api/rpc/${method}`;

  let res: Response;
  try {
    res = await fetch(url, {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(finalParams ?? {}),
    });
  } catch (err) {
    // Network error — wrap as a transport-level Error (NOT RpcError, since
    // the server never replied). Callers that wrap in try/catch will see a
    // distinguishable shape.
    throw new Error(`http rpc fetch failed: ${(err as Error).message}`);
  }

  if (res.ok) {
    const text = await res.text();
    if (text === "" || text === "null") return undefined as T;
    return JSON.parse(text) as T;
  }

  // Error path: handler_rpc_rest.go writes {code, message} on failure.
  let code = -32000;
  let message = `http ${res.status}`;
  try {
    const body = await res.json();
    if (typeof body?.code === "number") code = body.code;
    if (typeof body?.message === "string") message = body.message;
  } catch {
    // body wasn't JSON — fall back to the status-line message.
  }
  throw new RpcError(message, code);
}
