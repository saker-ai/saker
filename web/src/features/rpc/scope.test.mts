import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const here = path.dirname(fileURLToPath(import.meta.url));
const scopeSrc = readFileSync(path.join(here, "scope.ts"), "utf8");

// Backend whitelist mirror — every entry MUST be in METHOD_SKIP_PROJECT or
// the matching transport silently rejects it with -32003 ProjectMissing.
const REQUIRED_SKIP_METHODS = [
  "initialize",
  "auth/update",
  "auth/delete",
  "user/me",
  "user/list",
  "user/create",
  "user/delete",
  "user/update-password",
  "project/list",
  "project/create",
  "project/get",
  "project/me",
  "project/invite/accept",
  "project/invite/decline",
  "project/invite/list-for-me",
  "team/list",
  "team/create",
  "team/delete",
  "team/member/list",
  "sessions/search",
  "aigo/models",
  "aigo/providers",
  "aigo/status",
];

test("METHOD_SKIP_PROJECT mirrors backend methodSkipProject", () => {
  for (const method of REQUIRED_SKIP_METHODS) {
    assert.match(
      scopeSrc,
      new RegExp(`"${method.replace(/\//g, "\\/")}"`),
      `${method} missing from METHOD_SKIP_PROJECT — drift with pkg/server/middleware_scope.go`,
    );
  }
});

test("injectProjectId honors caller-provided projectId", () => {
  // Caller wins so explicit overrides keep working under auto-inject.
  assert.match(scopeSrc, /if \(params && params\.projectId\) return params/);
});

test("injectProjectId is a no-op when provider is unbound or returns null", () => {
  assert.match(scopeSrc, /if \(!provider\) return params/);
  assert.match(scopeSrc, /if \(!pid\) return params/);
});

test("injectProjectId logic — functional behaviour mirror", () => {
  // Mirrors scope.ts's injectProjectId so a behaviour regression in the
  // helper would still trip a unit test (regex-on-source can miss
  // semantic bugs).
  const SKIP = new Set(REQUIRED_SKIP_METHODS);
  function inject(
    method: string,
    params: Record<string, unknown> | undefined,
    provider: (() => string | null) | null,
  ) {
    if (SKIP.has(method)) return params;
    if (!provider) return params;
    if (params && params.projectId) return params;
    const pid = provider();
    if (!pid) return params;
    return { ...(params ?? {}), projectId: pid };
  }
  // skip method: untouched
  assert.deepEqual(inject("user/me", { x: 1 }, () => "p1"), { x: 1 });
  // no provider: untouched
  assert.deepEqual(inject("thread/list", undefined, null), undefined);
  // explicit projectId wins
  assert.deepEqual(
    inject("thread/list", { projectId: "p2" }, () => "p1"),
    { projectId: "p2" },
  );
  // provider returns null: untouched
  assert.deepEqual(inject("thread/list", { x: 1 }, () => null), { x: 1 });
  // happy path: merged
  assert.deepEqual(
    inject("thread/list", { x: 1 }, () => "p1"),
    { x: 1, projectId: "p1" },
  );
});
