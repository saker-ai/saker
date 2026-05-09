import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync, existsSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const typoNodeSource = readFileSync(path.join(here, "nodes/AITypoNode.tsx"), "utf8");
const overlaySource = readFileSync(path.join(here, "nodes/ManuscriptEditorOverlay.tsx"), "utf8");
const draftHookSource = readFileSync(path.join(here, "hooks/useManuscriptDraft.ts"), "utf8");
const imagesHookSource = readFileSync(path.join(here, "hooks/useManuscriptImages.ts"), "utf8");
const copilotHookSource = readFileSync(path.join(here, "hooks/useManuscriptCopilot.ts"), "utf8");

// Combined source: overlay + all hooks (features may live in either)
const allManuscriptSource = overlaySource + draftHookSource + imagesHookSource + copilotHookSource;

test("inspiration manuscript renders markdown in card and fullscreen preview surfaces", () => {
  assert.match(typoNodeSource, /renderMarkdown/);
  assert.match(typoNodeSource, /dangerouslySetInnerHTML/);
  assert.match(allManuscriptSource, /renderMarkdown/);
  assert.match(allManuscriptSource, /manuscript-preview/);
  assert.match(allManuscriptSource, /insertImageUrl/);
  assert.match(allManuscriptSource, /insertSelectedCanvasImage/);
  assert.match(allManuscriptSource, /handleImageFile/);
  assert.match(allManuscriptSource, /type="file"/);
  assert.match(allManuscriptSource, /onDrop=\{handleMarkdownDrop\}/);
  assert.match(allManuscriptSource, /onPaste=\{handleMarkdownPaste\}/);
  assert.match(allManuscriptSource, /manuscript-ai-command/);
  assert.match(allManuscriptSource, /SELECTION_ACTIONS/);
  assert.match(allManuscriptSource, /manuscript-entity-actions/);
  assert.match(allManuscriptSource, /manuscript-copilot/);
  assert.match(allManuscriptSource, /runCopilot/);
  assert.match(allManuscriptSource, /copilotScope/);
  assert.match(allManuscriptSource, /editorMode === "structured"/);
  assert.match(allManuscriptSource, /useState\(true\)/);
  assert.match(allManuscriptSource, /manuscript-ai-result/);
  assert.match(allManuscriptSource, /applyProposal/);
  assert.match(allManuscriptSource, /manuscript-copilot-proposal/);
});

test("manuscript hooks exist as separate files", () => {
  const hooksDir = path.join(here, "hooks");
  assert.ok(existsSync(path.join(hooksDir, "useManuscriptDraft.ts")));
  assert.ok(existsSync(path.join(hooksDir, "useManuscriptImages.ts")));
  assert.ok(existsSync(path.join(hooksDir, "useManuscriptCopilot.ts")));
});

test("overlay imports hooks instead of inlining logic", () => {
  assert.match(overlaySource, /useManuscriptDraft/);
  assert.match(overlaySource, /useManuscriptImages/);
  assert.match(overlaySource, /useManuscriptCopilot/);
});
