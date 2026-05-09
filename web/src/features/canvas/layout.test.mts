import test from "node:test";
import assert from "node:assert/strict";

import { autoLayoutGraph } from "./layout.ts";

test("autoLayoutGraph centers branch outputs around their parent layer", () => {
  const nodes = [
    {
      id: "prompt",
      type: "prompt",
      position: { x: 0, y: 200 },
      data: { nodeType: "prompt", label: "Prompt", status: "done" },
    },
    {
      id: "agent",
      type: "agent",
      position: { x: 320, y: 200 },
      data: { nodeType: "agent", label: "Agent", status: "done" },
    },
    {
      id: "img_a",
      type: "image",
      position: { x: 640, y: 0 },
      data: { nodeType: "image", label: "A", status: "done" },
    },
    {
      id: "img_b",
      type: "image",
      position: { x: 640, y: 420 },
      data: { nodeType: "image", label: "B", status: "done" },
    },
  ];
  const edges = [
    { id: "e1", source: "prompt", target: "agent", type: "flow" },
    { id: "e2", source: "agent", target: "img_a", type: "flow" },
    { id: "e3", source: "agent", target: "img_b", type: "flow" },
  ];

  const laidOut = autoLayoutGraph(nodes, edges);
  const agent = laidOut.find((node) => node.id === "agent")!;
  const imgA = laidOut.find((node) => node.id === "img_a")!;
  const imgB = laidOut.find((node) => node.id === "img_b")!;

  assert.equal(imgA.position.x, imgB.position.x);
  assert.ok(imgA.position.x > agent.position.x);

  const agentCenterY = agent.position.y + 90;
  const branchCenterY = ((imgA.position.y + 140) + (imgB.position.y + 140)) / 2;
  assert.equal(branchCenterY, agentCenterY);
});

test("autoLayoutGraph preserves positions of pinned nodes", () => {
  const nodes = [
    {
      id: "prompt",
      type: "prompt",
      position: { x: 0, y: 0 },
      data: { nodeType: "prompt", label: "Prompt", status: "done" },
    },
    {
      id: "agent",
      type: "agent",
      position: { x: 500, y: 300 },
      data: { nodeType: "agent", label: "Agent", status: "done", pinned: true },
    },
    {
      id: "image",
      type: "image",
      position: { x: 640, y: 0 },
      data: { nodeType: "image", label: "Image", status: "done" },
    },
  ];
  const edges = [
    { id: "e1", source: "prompt", target: "agent", type: "flow" },
    { id: "e2", source: "agent", target: "image", type: "flow" },
  ];

  const laidOut = autoLayoutGraph(nodes, edges);
  const agent = laidOut.find((node) => node.id === "agent")!;

  // Pinned node must keep its exact original position.
  assert.equal(agent.position.x, 500);
  assert.equal(agent.position.y, 300);

  // Unpinned nodes should still be laid out (positions may differ from original).
  assert.equal(laidOut.length, 3);
});

test("autoLayoutGraph with all root nodes pinned returns nodes unchanged", () => {
  const nodes = [
    {
      id: "a",
      type: "prompt",
      position: { x: 100, y: 200 },
      data: { nodeType: "prompt", label: "A", status: "done", pinned: true },
    },
    {
      id: "b",
      type: "agent",
      position: { x: 400, y: 500 },
      data: { nodeType: "agent", label: "B", status: "done", pinned: true },
    },
  ];
  const edges = [{ id: "e1", source: "a", target: "b", type: "flow" }];

  const laidOut = autoLayoutGraph(nodes, edges);
  const a = laidOut.find((n) => n.id === "a")!;
  const b = laidOut.find((n) => n.id === "b")!;

  assert.equal(a.position.x, 100);
  assert.equal(a.position.y, 200);
  assert.equal(b.position.x, 400);
  assert.equal(b.position.y, 500);
});

test("autoLayoutGraph places isolated nodes in a right-side vertical band", () => {
  const nodes = [
    {
      id: "prompt",
      type: "prompt",
      position: { x: 0, y: 0 },
      data: { nodeType: "prompt", label: "Prompt", status: "done" },
    },
    {
      id: "agent",
      type: "agent",
      position: { x: 320, y: 0 },
      data: { nodeType: "agent", label: "Agent", status: "done" },
    },
    {
      id: "image",
      type: "image",
      position: { x: 640, y: 0 },
      data: { nodeType: "image", label: "Image", status: "done" },
    },
    {
      id: "isolated_audio",
      type: "audio",
      position: { x: 2000, y: 900 },
      data: { nodeType: "audio", label: "Audio", status: "done" },
    },
    {
      id: "isolated_text",
      type: "text",
      position: { x: 2400, y: 200 },
      data: { nodeType: "text", label: "Note", status: "done" },
    },
  ];
  const edges = [
    { id: "e1", source: "prompt", target: "agent", type: "flow" },
    { id: "e2", source: "agent", target: "image", type: "flow" },
  ];

  const laidOut = autoLayoutGraph(nodes, edges);
  const mainMaxX = Math.max(
    ...laidOut
      .filter((node) => node.id !== "isolated_audio" && node.id !== "isolated_text")
      .map((node) => node.position.x),
  );
  const isolatedAudio = laidOut.find((node) => node.id === "isolated_audio")!;
  const isolatedText = laidOut.find((node) => node.id === "isolated_text")!;

  assert.ok(isolatedAudio.position.x > mainMaxX);
  assert.equal(isolatedAudio.position.x, isolatedText.position.x);
  assert.ok(isolatedText.position.y > isolatedAudio.position.y);
});
