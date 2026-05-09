import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const here = path.dirname(fileURLToPath(import.meta.url));
const threadPanelSource = readFileSync(path.join(here, "ThreadPanel.tsx"), "utf8");
const chatAppSource = readFileSync(path.join(here, "ChatApp.tsx"), "utf8");

// globals.css is now an @import index; inline every shard so existing
// regex assertions keep matching. Order follows the @import declarations
// to mirror the cascade the browser actually sees.
const globalsCssEntry = readFileSync(path.join(here, "../../app/globals.css"), "utf8");
const stylesDir = path.join(here, "../../app/styles");
const globalsCss = Array.from(
  globalsCssEntry.matchAll(/@import\s+"\.\/styles\/([^"]+)"/g),
)
  .map((m) => readFileSync(path.join(stylesDir, m[1]), "utf8"))
  .join("\n");

test("thread panel does not render a separate collapse button", () => {
  assert.doesNotMatch(threadPanelSource, /className="collapse-sidebar-btn"/);
});

test("chat header owns the shared thread panel toggle button", () => {
  assert.match(chatAppSource, /const renderThreadPanelToggle = useCallback\(\(\) => \{/);
  // Accept either a literal className="thread-panel-toggle-btn" or a template
  // string like className={`thread-panel-toggle-btn${...}`} that appends a
  // modifier (e.g. --open).
  assert.match(chatAppSource, /className=\{?["`'][^"`']*\bthread-panel-toggle-btn\b/);
  assert.match(chatAppSource, /t\("thread\.collapsePanel"\)/);
  assert.match(chatAppSource, /t\("chat\.openChatList"\)/);
  assert.match(chatAppSource, /t\("thread\.expandPanel"\)/);
  assert.doesNotMatch(globalsCss, /\.collapse-sidebar-btn\s*\{/);
});
