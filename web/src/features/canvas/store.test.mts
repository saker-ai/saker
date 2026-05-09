import test from "node:test";
import assert from "node:assert/strict";

import { sanitizeLoadedCanvas } from "./persistence.ts";

test("sanitizeLoadedCanvas keeps multiple saved image nodes and only drops broken media nodes", () => {
  const { nodes, edges } = sanitizeLoadedCanvas(
    [
      {
        id: "node_gen",
        type: "imageGen",
        position: { x: 0, y: 0 },
        data: { nodeType: "imageGen", label: "Image Gen", status: "pending" },
      },
      {
        id: "node_img_1",
        type: "image",
        position: { x: 300, y: 0 },
        data: { nodeType: "image", label: "First", status: "done", mediaType: "image", mediaUrl: "https://example.com/1.png" },
      },
      {
        id: "node_img_2",
        type: "image",
        position: { x: 300, y: 160 },
        data: { nodeType: "image", label: "Second", status: "done", mediaType: "image", mediaUrl: "https://example.com/2.png" },
      },
      {
        id: "node_broken",
        type: "image",
        position: { x: 300, y: 320 },
        data: { nodeType: "image", label: "Broken", status: "done", mediaType: "image", mediaUrl: "" },
      },
    ],
    [
      { id: "edge_1", source: "node_gen", target: "node_img_1", type: "flow" },
      { id: "edge_2", source: "node_gen", target: "node_img_2", type: "flow" },
      { id: "edge_3", source: "node_gen", target: "node_broken", type: "flow" },
    ],
  );

  const imageNodes = nodes.filter((node) => node.type === "image");

  assert.equal(imageNodes.length, 2);
  assert.deepEqual(
    imageNodes.map((node) => node.id),
    ["node_img_1", "node_img_2"],
  );
  assert.deepEqual(
    edges.map((edge) => edge.id),
    ["edge_1", "edge_2"],
  );
});
