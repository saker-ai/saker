import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const imageGenSource = readFileSync(path.join(here, "nodes/ImageGenNode.tsx"), "utf8");
const videoGenSource = readFileSync(path.join(here, "nodes/VideoGenNode.tsx"), "utf8");
const voiceGenSource = readFileSync(path.join(here, "nodes/VoiceGenNode.tsx"), "utf8");
const useGenerateSource = readFileSync(path.join(here, "nodes/useGenerate.ts"), "utf8");
const layoutActionsSource = readFileSync(path.join(here, "layoutActions.ts"), "utf8");

test("generator nodes trigger canvas auto-layout after successful output creation", () => {
  assert.match(layoutActionsSource, /export function autoLayoutCanvasAfterGeneration\(\)/);
  assert.match(layoutActionsSource, /autoLayoutGraph\(store\.nodes,\s*store\.edges\)/);
  assert.match(layoutActionsSource, /store\.triggerFitView\(\)/);

  assert.match(useGenerateSource, /autoLayoutCanvasAfterGeneration\(\)/);
  assert.match(imageGenSource, /useGenerate\(/);
  assert.match(videoGenSource, /useGenerate\(/);
  assert.match(voiceGenSource, /autoLayoutCanvasAfterGeneration\(\)/);
});
