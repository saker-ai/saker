import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const imageNodeSource = readFileSync(path.join(here, "nodes/ImageNode.tsx"), "utf8");
const useGenerateSource = readFileSync(path.join(here, "nodes/useGenerate.ts"), "utf8");
const sessionBridgeSource = readFileSync(path.join(here, "SessionCanvasBridge.ts"), "utf8");
const videoGenSource = readFileSync(path.join(here, "nodes/VideoGenNode.tsx"), "utf8");
const mediaCacheSource = readFileSync(path.join(here, "mediaCache.ts"), "utf8");

test("canvas caches generated images into stable local media records", () => {
  assert.match(mediaCacheSource, /request<.*>\("media\/cache"/s);
  assert.match(useGenerateSource, /cacheCanvasMedia\(mediaUrl,\s*mediaType\)/);
  assert.match(imageNodeSource, /cacheCanvasMedia\(res\.structured\.media_url,\s*"image"\)/);
  assert.match(sessionBridgeSource, /cacheCanvasMedia\(rawUrl,\s*mediaType\)/);
});

test("video generation resolves linked image references from stable cached media", () => {
  assert.match(mediaCacheSource, /request<.*>\("media\/data_url"/s);
  assert.match(videoGenSource, /resolveCanvasReferenceUrl\(node,\s*"image"\)/);
  assert.match(videoGenSource, /collectLinkedImageNodes/);
});
