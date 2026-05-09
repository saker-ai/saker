import test from "node:test";
import assert from "node:assert/strict";

import {
  ComposeCorsError,
  ComposeUnsupportedError,
  composeToMp4Blob,
  isComposeSupported,
} from "./webavCompose.ts";

test("isComposeSupported returns false in node (no window)", () => {
  assert.equal(isComposeSupported(), false);
});

test("composeToMp4Blob rejects empty input list", async () => {
  await assert.rejects(() => composeToMp4Blob([]), /no inputs/);
});

test("composeToMp4Blob throws ComposeUnsupportedError when WebCodecs missing", async () => {
  // node has no VideoDecoder/OffscreenCanvas → guard kicks in before dynamic import
  await assert.rejects(
    () => composeToMp4Blob([{ url: "https://example.com/a.mp4" }]),
    (err: unknown) => err instanceof ComposeUnsupportedError,
  );
});

test("ComposeCorsError carries url and name", () => {
  const err = new ComposeCorsError("https://x.test/v.mp4");
  assert.equal(err.name, "ComposeCorsError");
  assert.equal(err.url, "https://x.test/v.mp4");
  assert.match(err.message, /https:\/\/x\.test\/v\.mp4/);
});

test("ComposeUnsupportedError carries name", () => {
  const err = new ComposeUnsupportedError("nope");
  assert.equal(err.name, "ComposeUnsupportedError");
  assert.equal(err.message, "nope");
});
