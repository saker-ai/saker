import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const here = path.dirname(fileURLToPath(import.meta.url));
const storeSrc = readFileSync(path.join(here, "projectStore.ts"), "utf8");
const switcherSrc = readFileSync(path.join(here, "ProjectSwitcher.tsx"), "utf8");
const dialogSrc = readFileSync(path.join(here, "CreateProjectDialog.tsx"), "utf8");

test("projectStore exports useProjectStore + projectIdProvider", () => {
  assert.match(storeSrc, /export const useProjectStore = create</);
  assert.match(storeSrc, /export function projectIdProvider\(\)/);
});

test("projectStore persists currentProjectId to localStorage", () => {
  // Persistence is what makes the dropdown survive page reloads — verify the
  // contract via STORAGE_KEY + localStorage I/O, not just function names.
  assert.match(storeSrc, /const STORAGE_KEY = "saker\.currentProjectId"/);
  assert.match(storeSrc, /window\.localStorage\.setItem\(STORAGE_KEY/);
  assert.match(storeSrc, /window\.localStorage\.getItem\(STORAGE_KEY/);
  assert.match(storeSrc, /window\.localStorage\.removeItem\(STORAGE_KEY/);
});

test("projectStore.refresh calls project/list over HTTP and picks default", () => {
  // The refresh fn is the boot-time data path; if it stops calling
  // project/list the dropdown silently goes empty. HTTP transport is
  // load-bearing — switching back to rpc.request would re-introduce the
  // idle-WS connection we removed.
  assert.match(storeSrc, /httpRequest<\{ projects: ProjectSummary\[\] \}>\(\s*"project\/list"/);
  assert.match(storeSrc, /function pickDefault/);
  // Personal project must win over arbitrary order.
  assert.match(storeSrc, /p\.kind === "personal"/);
});

test("projectStore prefers existing currentProjectId when still valid", () => {
  // pickDefault returns `preferred` when present in the list — otherwise
  // a refresh would clobber the user's last selection on every reload.
  assert.match(
    storeSrc,
    /if \(preferred && projects\.some\(\(p\) => p\.id === preferred\)\) return preferred/,
  );
});

test("ProjectSwitcher consumes useProjectStore + setCurrent + refresh", () => {
  // The switcher is the user's entry point — guarantee it reads from the
  // canonical store rather than holding local state that drifts.
  assert.match(switcherSrc, /useProjectStore\(\(s\) => s\.projects\)/);
  assert.match(switcherSrc, /useProjectStore\(\(s\) => s\.currentProjectId\)/);
  assert.match(switcherSrc, /useProjectStore\(\(s\) => s\.setCurrent\)/);
  // Empty state recovers via refresh.
  assert.match(switcherSrc, /useProjectStore\(\(s\) => s\.refresh\)/);
});

test("ProjectSwitcher renders role badges and personal kind hint", () => {
  assert.match(switcherSrc, /role-badge role-\$\{/);
  assert.match(switcherSrc, /project\.kind\.personal/);
});

test("CreateProjectDialog issues project/create and refreshes the store", () => {
  // The dialog is the only path that mutates the project list — the
  // round-trip (create → refresh → setCurrent) must stay intact.
  assert.match(dialogSrc, /rpc\.request<\{ id: string \}>\(\s*"project\/create"/);
  assert.match(dialogSrc, /await refresh\(\)/);
  assert.match(dialogSrc, /setCurrent\(res\.id\)/);
});
