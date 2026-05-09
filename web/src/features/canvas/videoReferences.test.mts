import test from "node:test";
import assert from "node:assert/strict";

import { collectVideoReferences, collectReferenceNodes } from "./videoReferences.ts";

test("collectVideoReferences keeps multiple upstream images in edge order and one video reference", () => {
  const refs = collectVideoReferences(
    "video_gen_1",
    [
      { id: "edge_1", source: "image_1", target: "video_gen_1" },
      { id: "edge_2", source: "video_1", target: "video_gen_1" },
      { id: "edge_3", source: "image_2", target: "video_gen_1" },
      { id: "edge_4", source: "image_1", target: "video_gen_1" },
    ],
    [
      { id: "image_1", data: { mediaType: "image", mediaUrl: "https://example.com/a.png" } },
      { id: "image_2", data: { mediaType: "image", mediaUrl: "https://example.com/b.png" } },
      { id: "video_1", data: { mediaType: "video", mediaUrl: "https://example.com/input.mp4" } },
    ],
  );

  assert.deepEqual(refs.imageUrls, [
    "https://example.com/a.png",
    "https://example.com/b.png",
  ]);
  assert.deepEqual(refs.videoUrls, [
    "https://example.com/input.mp4",
  ]);
});

test("collectReferenceNodes returns bundles with inline mediaUrl, refType and strength", () => {
  const bundles = collectReferenceNodes(
    "image_gen_1",
    [
      { source: "ref_1", target: "image_gen_1" },
      { source: "ref_2", target: "image_gen_1" },
    ],
    [
      {
        id: "ref_1",
        data: {
          nodeType: "reference",
          refType: "style",
          refStrength: 0.7,
          mediaUrl: "https://example.com/style.png",
          mediaType: "image",
        },
      },
      {
        id: "ref_2",
        data: {
          nodeType: "reference",
          refType: "character",
          refStrength: 0.4,
          mediaUrl: "https://example.com/char.png",
          mediaType: "image",
        },
      },
    ],
  );

  assert.equal(bundles.length, 2);
  assert.equal(bundles[0].refType, "style");
  assert.equal(bundles[0].strength, 0.7);
  assert.equal(bundles[0].mediaUrl, "https://example.com/style.png");
  assert.equal(bundles[1].refType, "character");
  assert.equal(bundles[1].strength, 0.4);
});

test("collectReferenceNodes walks one hop upstream when reference node has no media", () => {
  const bundles = collectReferenceNodes(
    "image_gen_1",
    [
      { source: "ref_1", target: "image_gen_1" },
      { source: "image_src", target: "ref_1" },
    ],
    [
      {
        id: "image_src",
        data: {
          nodeType: "image",
          mediaUrl: "https://example.com/upstream.png",
          mediaType: "image",
        },
      },
      {
        id: "ref_1",
        data: {
          nodeType: "reference",
          refType: "pose",
          refStrength: 0.8,
        },
      },
    ],
  );

  assert.equal(bundles.length, 1);
  assert.equal(bundles[0].refType, "pose");
  assert.equal(bundles[0].strength, 0.8);
  assert.equal(bundles[0].mediaUrl, "https://example.com/upstream.png");
  assert.equal(bundles[0].mediaType, "image");
});

test("collectReferenceNodes applies defaults (refType=style, strength=1) when fields are missing", () => {
  const bundles = collectReferenceNodes(
    "image_gen_1",
    [{ source: "ref_1", target: "image_gen_1" }],
    [
      {
        id: "ref_1",
        data: {
          nodeType: "reference",
          mediaUrl: "https://example.com/default.png",
        },
      },
    ],
  );

  assert.equal(bundles.length, 1);
  assert.equal(bundles[0].refType, "style");
  assert.equal(bundles[0].strength, 1);
});

test("collectReferenceNodes returns empty array when no reference nodes are attached", () => {
  const bundles = collectReferenceNodes(
    "image_gen_1",
    [{ source: "image_1", target: "image_gen_1" }],
    [
      {
        id: "image_1",
        data: {
          nodeType: "image",
          mediaUrl: "https://example.com/plain.png",
          mediaType: "image",
        },
      },
    ],
  );

  assert.deepEqual(bundles, []);
});
