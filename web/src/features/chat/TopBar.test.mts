import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const here = path.dirname(fileURLToPath(import.meta.url));
const topbarSrc = readFileSync(path.join(here, "TopBar.tsx"), "utf8");
const chatAppSrc = readFileSync(path.join(here, "ChatApp.tsx"), "utf8");

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

test("TopBar renders ProjectSwitcher and CreateProjectDialog", () => {
  assert.match(topbarSrc, /import \{ ProjectSwitcher \} from/);
  assert.match(topbarSrc, /import \{ CreateProjectDialog \} from/);
  assert.match(topbarSrc, /<ProjectSwitcher[\s\S]*?onCreate=/);
});

test("TopBar exposes user menu with logout when onLogout provided", () => {
  // Logout is the only action in the menu — the menu wrapping ensures we
  // don't accidentally drop the only way to leave a session.
  assert.match(topbarSrc, /onLogout\?: \(\) => Promise<void> \| void/);
  assert.match(topbarSrc, /role="menu"/);
  assert.match(topbarSrc, /t\("auth\.logout"\)/);
});

test("ChatApp mounts TopBar inside the .app container", () => {
  // TopBar must be a sibling of IconNav so the header is always visible.
  assert.match(chatAppSrc, /<div className="app">\s*<TopBar/);
  assert.match(chatAppSrc, /username=\{currentUser\.username\}/);
  assert.match(chatAppSrc, /role=\{currentUser\.role\}/);
  assert.match(chatAppSrc, /onLogout=\{onLogout\}/);
});

test("ChatApp binds RPCClient.setProjectIdProvider on init", () => {
  // Auto-injection only works if the provider is bound before any
  // request fires. Loss of this line silently degrades to legacy mode.
  assert.match(chatAppSrc, /rpc\.setProjectIdProvider\(projectIdProvider\)/);
});

test("ChatApp refreshes project list on _connected", () => {
  // Without this, the dropdown never populates because nothing else
  // calls project/list at boot time.
  assert.match(chatAppSrc, /useProjectStore\.getState\(\)\.refresh\(rpc\)/);
});

test("ChatApp subscribes to projectId changes and clears scope-bound state", () => {
  // Switching projects MUST drop the previous thread/canvas/messages or
  // the user sees stale data from the prior project.
  assert.match(chatAppSrc, /useProjectStore\.subscribe\(\(state\) =>/);
  assert.match(chatAppSrc, /setActiveThreadId\(""\)/);
  assert.match(chatAppSrc, /resetCanvas\(\)/);
  // Reload thread/list under the new project.
  assert.match(chatAppSrc, /rpc\s*\.request<\{ threads: Thread\[\] \}>\("thread\/list"\)/);
});

test("ChatApp skips boot-time null→firstId transition", () => {
  // Without this guard, the very first refresh would clobber the freshly
  // loaded thread list with another redundant fetch.
  assert.match(chatAppSrc, /if \(prev === null\) return/);
});

test("TopBar wires keyboard navigation for the user dropdown", () => {
  // ArrowDown on trigger opens the menu; arrow keys cycle items; Escape
  // closes and restores focus to the trigger. These hooks are required by
  // WAI-ARIA Authoring Practices for menu buttons.
  assert.match(topbarSrc, /handleTriggerKeyDown/);
  assert.match(topbarSrc, /handleMenuKeyDown/);
  assert.match(topbarSrc, /case "ArrowDown":/);
  assert.match(topbarSrc, /case "ArrowUp":/);
  assert.match(topbarSrc, /case "Escape":/);
  // Focus returns to the trigger button after Escape so the user doesn't
  // lose their place in the tab order.
  assert.match(topbarSrc, /triggerRef\.current\?\.focus\(\)/);
  // First focusable item must be auto-focused when the menu opens via
  // keyboard so subsequent ArrowUp/Down work without an extra Tab.
  assert.match(topbarSrc, /first\?\.focus\(\)/);
});

test("globals.css carries TopBar layout and project-switcher styling", () => {
  assert.match(globalsCss, /\.topbar\s*\{[\s\S]+?position: fixed/);
  // Project list now lives inside the user dropdown — verify the embedded
  // list container and the dropdown shell that hosts it both have styles.
  assert.match(globalsCss, /\.topbar-user-menu\s*\{/);
  assert.match(globalsCss, /\.project-switcher-list\s*\{/);
  assert.match(globalsCss, /\.project-switcher-item\s*\{/);
  // Inline indicator on the user button so the active project is visible
  // without opening the menu.
  assert.match(globalsCss, /\.topbar-current-project\s*\{/);
  assert.match(globalsCss, /\.role-badge\.role-owner/);
  assert.match(globalsCss, /\.role-badge\.role-viewer/);
  assert.match(globalsCss, /\.modal-card/);
  // App content must clear the fixed TopBar.
  assert.match(globalsCss, /\.app\s*\{[\s\S]+?padding-top: 44px/);
  assert.match(globalsCss, /\.icon-nav\s*\{[\s\S]+?top: 44px/);
});
