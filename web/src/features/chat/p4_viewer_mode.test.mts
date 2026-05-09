import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const here = path.dirname(fileURLToPath(import.meta.url));
const composerSrc = readFileSync(path.join(here, "Composer.tsx"), "utf8");
const threadPanelSrc = readFileSync(path.join(here, "ThreadPanel.tsx"), "utf8");
const canvasSrc = readFileSync(
  path.join(here, "../canvas/CanvasView.tsx"),
  "utf8",
);
const i18nSrc = readFileSync(
  path.join(here, "../i18n/index.tsx"),
  "utf8",
);

test("Composer imports usePermissions and derives readOnly from canEdit", () => {
  // Single source of truth — mirrors usePermissions.ts. If a refactor renames
  // the hook or moves the derivation elsewhere, viewer mode silently breaks.
  assert.match(
    composerSrc,
    /import \{ usePermissions \} from "@\/features\/project\/usePermissions"/,
  );
  assert.match(composerSrc, /const perms = usePermissions\(\)/);
  assert.match(composerSrc, /const readOnly = !perms\.canEdit/);
  assert.match(composerSrc, /const inputDisabled = disabled \|\| readOnly/);
});

test("Composer hides send button entirely when readOnly", () => {
  // The original pattern was `disabled={disabled}` on the send button which
  // would have been technically locked but visually identical — viewers
  // would think the network is just slow. Hide it instead.
  assert.match(composerSrc, /\) : !readOnly \? \(\s*<button[\s\S]*?className="gemini-send-btn"/);
});

test("Composer hides attach button when readOnly", () => {
  // Pasting/dropping files is a write — gate the entry point.
  assert.match(composerSrc, /\{!readOnly && \(\s*<button[\s\S]*?className="gemini-attach-btn"/);
});

test("Composer textarea disables and shows viewerReadOnly placeholder", () => {
  assert.match(composerSrc, /placeholder=\{readOnly \? t\("composer\.viewerReadOnly"\) : t\("composer\.askSaker"\)\}/);
  assert.match(composerSrc, /disabled=\{inputDisabled\}\s*\n\s*rows=\{1\}/);
});

test("Composer.handleSend short-circuits on inputDisabled (covers viewer)", () => {
  // The combined check folds disabled (offline) and readOnly (viewer) so a
  // future caller wiring the hook itself can't slip through Enter-to-send.
  assert.match(composerSrc, /&& readyAttachments\.length === 0\) \|\| inputDisabled\) return/);
});

test("ThreadPanel hides new-chat button for viewers", () => {
  assert.match(threadPanelSrc, /import \{ usePermissions \} from "@\/features\/project\/usePermissions"/);
  assert.match(threadPanelSrc, /const perms = usePermissions\(\)/);
  assert.match(threadPanelSrc, /\{perms\.canEdit && \(\s*<div className="new-chat-btn-wrapper">/);
});

test("ThreadPanel hides delete button + confirm dialog for viewers", () => {
  // Both branches of the trinary need the gate so a viewer who somehow
  // entered confirm state (state migration, dev tools) still can't delete.
  assert.match(threadPanelSrc, /\{perms\.canEdit && confirmId === th\.id \?/);
  assert.match(threadPanelSrc, /: perms\.canEdit && th\.id === activeThreadId \?/);
});

test("CanvasView wires usePermissions into ReactFlow draggable/connectable", () => {
  assert.match(canvasSrc, /import \{ usePermissions \} from "@\/features\/project\/usePermissions"/);
  assert.match(canvasSrc, /const perms = usePermissions\(\)/);
  assert.match(canvasSrc, /const canEdit = perms\.canEdit/);
  assert.match(canvasSrc, /nodesDraggable=\{canEdit\}/);
  assert.match(canvasSrc, /nodesConnectable=\{canEdit\}/);
  assert.match(canvasSrc, /edgesReconnectable=\{canEdit\}/);
});

test("CanvasView guards onPaneContextMenu and onDrop for viewers", () => {
  // Right-click → quick-add menu is a write path; drop-to-create same.
  // Without the guard the read-only flag on ReactFlow wouldn't be enough
  // because these handlers bypass it.
  assert.match(canvasSrc, /onPaneContextMenu = useCallback\(\s*\(event[^)]*\) => \{\s*event\.preventDefault\(\);\s*if \(!canEdit\) return;/);
  assert.match(canvasSrc, /onDrop = useCallback\([\s\S]*?if \(!canEdit\) return;/);
});

test("CanvasView short-circuits mutation hotkeys for viewers", () => {
  // The keyboard handler runs Delete, Ctrl+L, Ctrl+G, Ctrl+D, undo, redo —
  // all of which mutate. One umbrella early-return is cheaper than gating
  // each branch and harder to break later.
  assert.match(
    canvasSrc,
    /All shortcuts below mutate the canvas[\s\S]*?if \(!canEdit\) return;/,
  );
});

test("CanvasView blocks paste-image when read-only", () => {
  // The global paste handler creates image nodes from clipboard — pure
  // write side effect that would otherwise bypass canEdit.
  assert.match(canvasSrc, /handler = \(e: ClipboardEvent\) => \{[\s\S]*?if \(!canEdit\) return;/);
});

test("CanvasView hides the left toolbar add-node button for viewers", () => {
  // Asset library / history / templates are read-only browsers and should
  // stay; only the "create new node" entry needs to be hidden.
  assert.match(canvasSrc, /\{canEdit && \(\s*<div className="canvas-add-node-wrapper">/);
});

test("CanvasView root container gets canvas-readonly class when locked", () => {
  // Drives any CSS overrides that want to dim controls or remove pointer
  // affordances without reaching into every node component.
  assert.match(canvasSrc, /canvas-readonly/);
});

test("i18n carries the new viewerReadOnly key in EN and ZH", () => {
  assert.match(i18nSrc, /"composer\.viewerReadOnly":\s*\{\s*en:/);
  assert.match(i18nSrc, /"composer\.viewerReadOnly":[^\n]*\bzh:/);
});
