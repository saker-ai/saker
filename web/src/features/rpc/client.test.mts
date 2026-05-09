import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const here = path.dirname(fileURLToPath(import.meta.url));
const clientSrc = readFileSync(path.join(here, "client.ts"), "utf8");

test("RPCClient exposes setProjectIdProvider", () => {
  // The wiring API ChatApp uses to bind zustand → client. If renamed, the
  // injection silently breaks because there's no compile-time consumer
  // outside chat boot.
  assert.match(clientSrc, /setProjectIdProvider\(fn: ProjectIdProvider \| null\)/);
});

test("RPCClient.request injects projectId via injectProjectId helper", () => {
  // Single-call-site invariant — the message body must use finalParams,
  // not the original params, or auto-injection is dead code.
  assert.match(
    clientSrc,
    /const finalParams = this\.injectProjectId\(method, params\);/,
  );
  assert.match(clientSrc, /params: finalParams/);
});

test("RPCClient.injectProjectId delegates to shared scope helper", () => {
  // Skip-list and provider semantics live in scope.ts so the WS and HTTP
  // transports can't drift. The thin method here must just forward.
  assert.match(
    clientSrc,
    /scopeInjectProjectId\(method, params, this\.projectIdProvider\)/,
  );
});
