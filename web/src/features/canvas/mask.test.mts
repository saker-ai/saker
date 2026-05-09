import test from "node:test";
import assert from "node:assert/strict";

/** Mirrors the Stroke shape persisted by MaskNode in `data.maskData` (JSON string). */
interface Stroke {
  tool: "pen" | "eraser";
  points: Array<[number, number]>;
  width: number;
}

function parseMaskData(raw: string | undefined): Stroke[] {
  try {
    return raw ? (JSON.parse(raw) as Stroke[]) : [];
  } catch {
    return [];
  }
}

test("mask stroke JSON roundtrips pen and eraser strokes with widths and points", () => {
  const original: Stroke[] = [
    { tool: "pen", width: 20, points: [[10, 10], [20, 15], [25, 30]] },
    { tool: "eraser", width: 40, points: [[100, 100], [110, 120]] },
  ];

  const serialized = JSON.stringify(original);
  const restored = parseMaskData(serialized);

  assert.deepEqual(restored, original);
  assert.equal(restored.length, 2);
  assert.equal(restored[0].tool, "pen");
  assert.equal(restored[1].tool, "eraser");
});

test("parseMaskData returns empty array for undefined or invalid JSON", () => {
  assert.deepEqual(parseMaskData(undefined), []);
  assert.deepEqual(parseMaskData(""), []);
  assert.deepEqual(parseMaskData("{not json"), []);
});

test("mask single-point stroke is preserved (represents a dot-click)", () => {
  const original: Stroke[] = [{ tool: "pen", width: 10, points: [[5, 5]] }];
  const restored = parseMaskData(JSON.stringify(original));
  assert.equal(restored.length, 1);
  assert.equal(restored[0].points.length, 1);
  assert.deepEqual(restored[0].points[0], [5, 5]);
});
