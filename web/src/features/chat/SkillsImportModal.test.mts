import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const modalSource = readFileSync(path.join(here, "SkillsImportModal.tsx"), "utf8");
const catalogSource = readFileSync(path.join(here, "SkillsCatalog.tsx"), "utf8");

test("skills import uses a dedicated modal component", () => {
  assert.match(catalogSource, /import\s+\{\s*SkillsImportModal\s*\}\s+from\s+"\.\/SkillsImportModal"/);
  assert.match(catalogSource, /<SkillsImportModal/);
});

test("skills import modal supports preview and conflict strategy controls", () => {
  assert.match(modalSource, /onPreview:/);
  assert.match(modalSource, /skills\.importConflictStrategy/);
  assert.match(modalSource, /skills\.previewImport/);
  assert.match(modalSource, /skills\.importPreview/);
});
