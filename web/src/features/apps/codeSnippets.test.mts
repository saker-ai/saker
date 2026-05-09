import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { curlSnippet, jsSnippet, pythonSnippet, type SnippetContext } from "./codeSnippets.ts";

const ctx: SnippetContext = {
  baseUrl: "https://example.com",
  appId: "abc123",
  apiKey: "ak_deadbeef00000000000000000000000000",
  inputs: [
    { nodeId: "n1", variable: "topic", label: "Topic", type: "text" },
    { nodeId: "n2", variable: "count", label: "Count", type: "number" },
    {
      nodeId: "n3",
      variable: "style",
      label: "Style",
      type: "select",
      options: ["formal", "casual"],
    },
  ],
};

describe("curlSnippet", () => {
  it("contains the correct URL", () => {
    const snippet = curlSnippet(ctx);
    assert.ok(
      snippet.includes("https://example.com/api/apps/abc123/run"),
      `Expected URL in snippet, got:\n${snippet}`,
    );
  });

  it("contains the Authorization header with the API key", () => {
    const snippet = curlSnippet(ctx);
    assert.ok(
      snippet.includes(`Bearer ${ctx.apiKey}`),
      `Expected Bearer token in snippet, got:\n${snippet}`,
    );
  });

  it("contains the inputs body", () => {
    const snippet = curlSnippet(ctx);
    assert.ok(
      snippet.includes('"topic"') && snippet.includes('"example"'),
      `Expected topic:example in snippet, got:\n${snippet}`,
    );
    assert.ok(
      snippet.includes('"count"') && snippet.includes("0"),
      `Expected count:0 in snippet, got:\n${snippet}`,
    );
    assert.ok(
      snippet.includes('"style"') && snippet.includes('"formal"'),
      `Expected style:formal in snippet, got:\n${snippet}`,
    );
  });
});

describe("jsSnippet", () => {
  it("contains the correct URL", () => {
    const snippet = jsSnippet(ctx);
    assert.ok(
      snippet.includes("https://example.com/api/apps/abc123/run"),
      `Expected URL in snippet, got:\n${snippet}`,
    );
  });

  it("contains the Authorization header", () => {
    const snippet = jsSnippet(ctx);
    assert.ok(
      snippet.includes(`Bearer ${ctx.apiKey}`),
      `Expected Bearer token in snippet, got:\n${snippet}`,
    );
  });

  it("contains the inputs body", () => {
    const snippet = jsSnippet(ctx);
    assert.ok(
      snippet.includes('"topic"') && snippet.includes('"example"'),
      `Expected topic:example in snippet, got:\n${snippet}`,
    );
  });
});

describe("pythonSnippet", () => {
  it("contains the correct URL", () => {
    const snippet = pythonSnippet(ctx);
    assert.ok(
      snippet.includes("https://example.com/api/apps/abc123/run"),
      `Expected URL in snippet, got:\n${snippet}`,
    );
  });

  it("contains the Authorization header", () => {
    const snippet = pythonSnippet(ctx);
    assert.ok(
      snippet.includes(`Bearer ${ctx.apiKey}`),
      `Expected Bearer token in snippet, got:\n${snippet}`,
    );
  });

  it("contains the inputs body", () => {
    const snippet = pythonSnippet(ctx);
    assert.ok(
      snippet.includes('"topic"') && snippet.includes('"example"'),
      `Expected topic:example in snippet, got:\n${snippet}`,
    );
  });
});

describe("snippets with projectId", () => {
  it("curlSnippet includes projectId in URL", () => {
    const ctxWithProject: SnippetContext = { ...ctx, projectId: "proj42" };
    const snippet = curlSnippet(ctxWithProject);
    assert.ok(
      snippet.includes("https://example.com/api/apps/proj42/abc123/run"),
      `Expected project-scoped URL, got:\n${snippet}`,
    );
  });
});

describe("snippets with no apiKey", () => {
  it("curlSnippet uses placeholder when apiKey is absent", () => {
    const ctxNoKey: SnippetContext = { ...ctx, apiKey: undefined };
    const snippet = curlSnippet(ctxNoKey);
    assert.ok(
      snippet.includes("<your-api-key>"),
      `Expected placeholder in snippet, got:\n${snippet}`,
    );
  });
});
