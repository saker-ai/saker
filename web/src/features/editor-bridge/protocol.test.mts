import test from "node:test";
import assert from "node:assert/strict";

// Minimal window + localStorage shim so the protocol's typeof window check passes.
const storage = new Map<string, string>();
const localStorageShim = {
  getItem: (k: string) => (storage.has(k) ? storage.get(k)! : null),
  setItem: (k: string, v: string) => {
    storage.set(k, v);
  },
  removeItem: (k: string) => {
    storage.delete(k);
  },
  key: (i: number) => Array.from(storage.keys())[i] ?? null,
  get length() {
    return storage.size;
  },
  clear: () => storage.clear(),
};

(globalThis as { window?: object }).window = {
  btoa: globalThis.btoa,
  atob: globalThis.atob,
  localStorage: localStorageShim,
} as object;

const {
  buildEditorImportUrl,
  decodeImportPayload,
  encodeImportPayload,
  readStoredImport,
  isEditorExportMessage,
  EDITOR_EXPORT_MESSAGE_TYPE,
} = await import("./protocol.ts");

test("encode/decode round-trip preserves assets", () => {
  const payload = {
    assets: [
      { url: "https://x.test/a.mp4", type: "video" as const, label: "Clip A" },
      { url: "blob:https://x.test/abc", type: "audio" as const, durationMs: 12_345 },
    ],
  };
  const encoded = encodeImportPayload(payload);
  assert.match(encoded, /^[A-Za-z0-9_-]+$/);
  assert.deepEqual(decodeImportPayload(encoded), payload);
});

test("decodeImportPayload returns null for malformed input", () => {
  assert.equal(decodeImportPayload("@@@not-base64@@@"), null);
  assert.equal(decodeImportPayload(""), null);
});

test("buildEditorImportUrl returns base path when no assets", () => {
  assert.equal(buildEditorImportUrl([]), "/editor/");
});

test("buildEditorImportUrl uses inline: prefix for small payloads", () => {
  storage.clear();
  const url = buildEditorImportUrl([
    { url: "https://x.test/v.mp4", type: "video", label: "small" },
  ]);
  const m = url.match(/^\/editor\/\?import=inline:(.+)$/);
  assert.ok(m, `expected inline URL, got ${url}`);
  const decoded = decodeImportPayload(m![1]!);
  assert.equal(decoded?.assets[0]?.url, "https://x.test/v.mp4");
});

test("buildEditorImportUrl falls back to ls: storage for large payloads", () => {
  storage.clear();
  const bigLabel = "A".repeat(8000);
  const url = buildEditorImportUrl([
    { url: "https://x.test/v.mp4", type: "video", label: bigLabel },
  ]);
  const m = url.match(/^\/editor\/\?import=ls:(.+)$/);
  assert.ok(m, `expected ls: URL, got ${url}`);
  const restored = readStoredImport(m![1]!);
  assert.equal(restored?.assets[0]?.label, bigLabel);
  // Storage entry should be consumed (deleted on read)
  assert.equal(readStoredImport(m![1]!), null);
});

test("isEditorExportMessage accepts dataUrl form", () => {
  assert.equal(
    isEditorExportMessage({
      type: EDITOR_EXPORT_MESSAGE_TYPE,
      filename: "x.mp4",
      dataUrl: "data:video/mp4;base64,AAA",
    }),
    true,
  );
});

test("isEditorExportMessage accepts Blob form", () => {
  if (typeof Blob === "undefined") return; // older runtimes
  const blob = new Blob([new Uint8Array([1, 2, 3])], { type: "video/mp4" });
  assert.equal(
    isEditorExportMessage({
      type: EDITOR_EXPORT_MESSAGE_TYPE,
      filename: "x.mp4",
      blob,
    }),
    true,
  );
});

test("isEditorExportMessage rejects malformed", () => {
  assert.equal(isEditorExportMessage({ type: "wrong", filename: "x", dataUrl: "y" }), false);
  assert.equal(isEditorExportMessage(null), false);
  assert.equal(isEditorExportMessage("string"), false);
  assert.equal(
    isEditorExportMessage({ type: EDITOR_EXPORT_MESSAGE_TYPE, filename: "x" }),
    false,
  );
});

test("encodeImportPayload handles UTF-8 labels", () => {
  const payload = { assets: [{ url: "/a.mp4", type: "video" as const, label: "中文测试 🎬" }] };
  const decoded = decodeImportPayload(encodeImportPayload(payload));
  assert.equal(decoded?.assets[0]?.label, "中文测试 🎬");
});
