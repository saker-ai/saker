import test from "node:test";
import assert from "node:assert/strict";

import {
  createDefaultManuscript,
  detectManuscriptEditorMode,
  extractManuscriptEntities,
  getManuscriptText,
  normalizeManuscriptData,
  updateManuscriptContent,
} from "./manuscript.ts";

test("normalizeManuscriptData migrates legacy content into structured manuscript fields", () => {
  const data = {
    nodeType: "aiTypo" as const,
    label: "Legacy",
    status: "done" as const,
    content: "第一段关于[雨夜街头]\n\n第二段关于[霓虹灯]",
  };

  const normalized = normalizeManuscriptData(data);

  assert.equal(normalized.manuscriptTitle, "Legacy");
  assert.equal(normalized.manuscriptSections?.length, 2);
  assert.equal(normalized.manuscriptSections?.[0].text, "第一段关于[雨夜街头]");
  assert.equal(normalized.manuscriptSections?.[1].text, "第二段关于[霓虹灯]");
  assert.equal(normalized.manuscriptEntities?.length, 2);
  assert.deepEqual(
    normalized.manuscriptEntities?.map((entity) => entity.label),
    ["雨夜街头", "霓虹灯"],
  );
});

test("getManuscriptText mirrors structured sections into legacy content", () => {
  const manuscript = createDefaultManuscript("标题", "alpha\n\nbeta");

  assert.equal(getManuscriptText(manuscript), "alpha\n\nbeta");
});

test("updateManuscriptContent replaces sections and refreshes entities", () => {
  const manuscript = createDefaultManuscript("标题", "old [A]");
  const updated = updateManuscriptContent(manuscript, "new [B]\n\nnext [C]");

  assert.equal(getManuscriptText(updated), "new [B]\n\nnext [C]");
  assert.deepEqual(
    extractManuscriptEntities(updated).map((entity) => entity.label),
    ["B", "C"],
  );
});

test("manuscript defaults to markdown editor mode and detects markdown syntax", () => {
  assert.equal(detectManuscriptEditorMode("# Title\n\n- item"), "markdown");
  assert.equal(createDefaultManuscript("标题", "plain text").manuscriptEditorMode, "markdown");
  assert.equal(normalizeManuscriptData({
    nodeType: "aiTypo",
    label: "Legacy",
    status: "done",
    content: "## Title\n\n> quote",
  }).manuscriptEditorMode, "markdown");
});
