import test from "node:test";
import assert from "node:assert/strict";

import { renderMarkdown } from "./markdown.ts";

test("renderMarkdown wraps fenced code blocks with the custom code UI", () => {
  const html = renderMarkdown("```js\nconsole.log(1)\n```");

  assert.match(html, /code-block-wrapper/);
  assert.match(html, /language-js/);
  assert.match(html, /console\.log\(1\)/);
  assert.match(html, /copy-btn/);
});

test("renderMarkdown preserves safe inline html blocks instead of escaping them", () => {
  const html = renderMarkdown("<p>a<br>b</p>");

  assert.match(html, /<p>a<br\s*\/?>b<\/p>/);
});

test("renderMarkdown sanitizes suspicious code language labels", () => {
  const html = renderMarkdown("```foo\" onclick=\"alert(1)\nx\n```");

  assert.doesNotMatch(html, /onclick=/);
  assert.match(html, /language-fooonclickalert1/);
});
