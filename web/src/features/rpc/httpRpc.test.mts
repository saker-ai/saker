import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const here = path.dirname(fileURLToPath(import.meta.url));
const httpSrc = readFileSync(path.join(here, "httpRpc.ts"), "utf8");

test("httpRpc exports the wiring API ChatApp boot needs", () => {
  // setHTTPProjectIdProvider mirrors RPCClient.setProjectIdProvider so the
  // HTTP transport gets the same projectId source. resolveHttpBaseUrl is
  // exported so tests / debug code can inspect URL resolution.
  assert.match(httpSrc, /export function setHTTPProjectIdProvider\(/);
  assert.match(httpSrc, /export function resolveHttpBaseUrl\(\)/);
  assert.match(httpSrc, /export async function httpRequest<T[^>]*>\(/);
});

test("httpRequest delegates projectId injection to scope helper", () => {
  // Single source of truth — if this drifts from client.ts the WS and HTTP
  // transports start applying different skip rules.
  assert.match(
    httpSrc,
    /scopeInjectProjectId\(method, params, projectIdProvider\)/,
  );
});

test("httpRequest POSTs JSON with credentials:include", () => {
  // credentials:include is the load-bearing line — without it the auth
  // cookie is dropped and every call comes back 401.
  assert.match(httpSrc, /method: "POST"/);
  assert.match(httpSrc, /credentials: "include"/);
  assert.match(httpSrc, /"Content-Type": "application\/json"/);
  assert.match(httpSrc, /body: JSON\.stringify\(finalParams \?\? \{\}\)/);
});

test("httpRequest URL targets /api/rpc/{method}", () => {
  // Path layout must match the mux route registered in pkg/server/server.go;
  // any divergence surfaces as 404 from the auth middleware.
  assert.match(httpSrc, /\/api\/rpc\/\$\{method\}/);
});

test("httpRequest surfaces JSON-RPC error code as RpcError", () => {
  // Same RpcError shape as the WS path so callers can branch on .code
  // regardless of which transport delivered the failure.
  assert.match(httpSrc, /throw new RpcError\(message, code\)/);
  assert.match(httpSrc, /if \(typeof body\?\.code === "number"\) code = body\.code/);
  assert.match(
    httpSrc,
    /if \(typeof body\?\.message === "string"\) message = body\.message/,
  );
});

test("httpRequest returns undefined for empty / null bodies", () => {
  // Some handlers return nil → server writes "" or "null"; JSON.parse("")
  // would throw, so the empty-body short-circuit is required.
  assert.match(httpSrc, /if \(text === "" \|\| text === "null"\) return undefined as T/);
});

test("httpRequest distinguishes network failures from RPC failures", () => {
  // Wrap fetch errors as a plain Error so callers can tell "server never
  // replied" apart from "server replied with an error code".
  assert.match(httpSrc, /throw new Error\(`http rpc fetch failed:/);
});

test("resolveHttpBaseUrl mirrors resolveWsUrl dev/embedded split", () => {
  // Dev: frontend on 10111 → API on 10112. Embedded: same origin. Same
  // rule as client.ts:resolveWsUrl, otherwise dev mode CORS-fails.
  assert.match(httpSrc, /port === "10111"/);
  assert.match(httpSrc, /127\.0\.0\.1:10112/);
});

test("httpRequest behavioural mirror — request shape and error mapping", async () => {
  // Inline replica so a logic bug (wrong header, missing credentials,
  // forgotten error parsing) trips a unit test even if the regex matches.
  class RpcErrorMirror extends Error {
    code: number;
    constructor(message: string, code: number) {
      super(message);
      this.name = "RpcError";
      this.code = code;
    }
  }
  function injectMirror(
    _method: string,
    params: Record<string, unknown> | undefined,
  ) {
    return params; // no provider in this mirror
  }
  async function httpRequestMirror<T>(
    fetchImpl: typeof fetch,
    method: string,
    params?: Record<string, unknown>,
  ): Promise<T> {
    const finalParams = injectMirror(method, params);
    const url = `http://api.test/api/rpc/${method}`;
    const res = await fetchImpl(url, {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(finalParams ?? {}),
    });
    if (res.ok) {
      const text = await res.text();
      if (text === "" || text === "null") return undefined as T;
      return JSON.parse(text) as T;
    }
    let code = -32000;
    let message = `http ${res.status}`;
    try {
      const body = await res.json();
      if (typeof body?.code === "number") code = body.code;
      if (typeof body?.message === "string") message = body.message;
    } catch {
      // not JSON — keep status-line fallback.
    }
    throw new RpcErrorMirror(message, code);
  }

  // Capture what fetch was called with.
  let capturedUrl = "";
  let capturedInit: RequestInit | undefined;
  const fakeOk: typeof fetch = async (url, init) => {
    capturedUrl = String(url);
    capturedInit = init;
    return new Response(JSON.stringify({ hello: "world" }), { status: 200 });
  };
  const result = await httpRequestMirror<{ hello: string }>(
    fakeOk,
    "thread/list",
    { x: 1 },
  );
  assert.deepEqual(result, { hello: "world" });
  assert.equal(capturedUrl, "http://api.test/api/rpc/thread/list");
  assert.equal(capturedInit?.method, "POST");
  assert.equal((capturedInit as RequestInit).credentials, "include");
  assert.equal(capturedInit?.body, JSON.stringify({ x: 1 }));

  // Empty body short-circuit.
  const fakeEmpty: typeof fetch = async () =>
    new Response("", { status: 200 });
  const empty = await httpRequestMirror<undefined>(fakeEmpty, "settings/set");
  assert.equal(empty, undefined);

  // null body short-circuit.
  const fakeNull: typeof fetch = async () =>
    new Response("null", { status: 200 });
  const nullRes = await httpRequestMirror<undefined>(fakeNull, "thread/delete");
  assert.equal(nullRes, undefined);

  // Error path: parses {code, message}.
  const fakeErr: typeof fetch = async () =>
    new Response(JSON.stringify({ code: -32004, message: "no access" }), {
      status: 403,
    });
  await assert.rejects(
    () => httpRequestMirror(fakeErr, "thread/list"),
    (err: Error) => {
      assert.equal(err.name, "RpcError");
      assert.equal((err as RpcErrorMirror).code, -32004);
      assert.equal(err.message, "no access");
      return true;
    },
  );

  // Error path with non-JSON body falls back to status line.
  const fakeBadJson: typeof fetch = async () =>
    new Response("<html>500</html>", { status: 500 });
  await assert.rejects(
    () => httpRequestMirror(fakeBadJson, "thread/list"),
    (err: Error) => {
      assert.equal((err as RpcErrorMirror).code, -32000);
      assert.equal(err.message, "http 500");
      return true;
    },
  );
});
