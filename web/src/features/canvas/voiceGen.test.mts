import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const canvasViewSource = readFileSync(path.join(here, "CanvasView.tsx"), "utf8");
const typesSource = readFileSync(path.join(here, "types.ts"), "utf8");
const i18nSource = readFileSync(path.join(here, "../i18n/index.tsx"), "utf8");
const voiceNodeSource = readFileSync(path.join(here, "nodes/VoiceGenNode.tsx"), "utf8");

test("canvas registers voice generation nodes in types and add menu", () => {
  assert.match(typesSource, /"voiceGen"/);
  assert.match(canvasViewSource, /VoiceGenNode/);
  assert.match(canvasViewSource, /voiceGen:\s*VoiceGenNode/);
  assert.match(canvasViewSource, /createGenNode\(type as "imageGen" \| "videoGen" \| "voiceGen" \| "imageEdit" \| "videoEdit"\)/);
  assert.match(canvasViewSource, /t\("canvas\.audioGen"\)/);
  assert.match(i18nSource, /"canvas\.audioGen":\s*\{/);
});

test("voice generation node uses text_to_speech and emits audio nodes", () => {
  assert.match(voiceNodeSource, /useToolSchema\("text_to_speech",\s*selectedEngine\)/);
  assert.match(voiceNodeSource, /const toolName = isMusic \? "generate_music" : "text_to_speech"/);
  assert.match(voiceNodeSource, /submitAndPollTask\(toolName/);
  assert.match(voiceNodeSource, /type:\s*"audio"/);
  assert.match(voiceNodeSource, /mediaType:\s*"audio"/);
  assert.match(voiceNodeSource, /t\("canvas\.audioGen"\)/);
});
