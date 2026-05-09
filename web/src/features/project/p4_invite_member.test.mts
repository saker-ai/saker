import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const here = path.dirname(fileURLToPath(import.meta.url));
const inviteSrc = readFileSync(path.join(here, "InviteDialog.tsx"), "utf8");
const memberSrc = readFileSync(path.join(here, "MemberList.tsx"), "utf8");
const settingsSrc = readFileSync(path.join(here, "ProjectSettingsPage.tsx"), "utf8");
const permsSrc = readFileSync(path.join(here, "usePermissions.ts"), "utf8");
const switcherSrc = readFileSync(path.join(here, "ProjectSwitcher.tsx"), "utf8");
const topbarSrc = readFileSync(path.join(here, "../chat/TopBar.tsx"), "utf8");
const i18nSrc = readFileSync(path.join(here, "../i18n/index.tsx"), "utf8");

test("usePermissions exposes role-derived gates", () => {
  // The matrix below mirrors pkg/server/handler.go's methodMinRole — keep
  // these identifiers visible so a grep across both ends catches drift.
  assert.match(permsSrc, /export function rolePermissions\(role: ProjectRole \| null\)/);
  assert.match(permsSrc, /canEdit/);
  assert.match(permsSrc, /canDelete/);
  assert.match(permsSrc, /canInvite/);
  assert.match(permsSrc, /canManageProject/);
  assert.match(permsSrc, /canTransferOrDelete/);
  // Owner-only must be `role === "owner"` exactly so admin can't escalate.
  assert.match(permsSrc, /isOwner = role === "owner"/);
});

test("InviteDialog calls project/invite with username + role", () => {
  // Single-call-site invariant — a refactor that drops the params object
  // would silently invite "" to the wrong role on the backend.
  assert.match(inviteSrc, /rpc\.request\("project\/invite", \{\s*username: trimmed,\s*role,\s*\}\)/);
});

test("InviteDialog highlights field on user-not-found / already-member errors", () => {
  // Inline field error keeps the UX tight — toast for everything would
  // hide the most common failure mode (typo in username).
  assert.match(inviteSrc, /not found/);
  assert.match(inviteSrc, /already/);
  assert.match(inviteSrc, /setFieldError\(msg\)/);
  assert.match(inviteSrc, /aria-invalid=\{fieldError \? true : undefined\}/);
});

test("InviteDialog uses ProjectRole select restricted to admin/member/viewer", () => {
  // Owner can't be assigned via invite — only via transfer ownership.
  assert.match(inviteSrc, /const ROLES: ProjectRole\[\] = \["admin", "member", "viewer"\]/);
});

test("MemberList round-trips via project/member/list, /update-role, /remove", () => {
  assert.match(memberSrc, /rpc\.request<\{ members: Member\[\] \}>\(\s*"project\/member\/list"/);
  assert.match(memberSrc, /rpc\.request\("project\/member\/update-role", \{ userId, role \}\)/);
  assert.match(memberSrc, /rpc\.request\("project\/member\/remove", \{ userId \}\)/);
});

test("MemberList refreshes projectStore after role/remove changes", () => {
  // Self-role changes flip canEdit/canManageProject — the store must be
  // re-fetched so usePermissions re-derives, otherwise the UI lies.
  assert.match(memberSrc, /await refreshProjects\(\)/);
});

test("MemberList hides edit controls for non-managers and for owner row", () => {
  // perms.canManageProject keeps viewer/member from seeing role select;
  // !isOwner guards against admin demoting the owner here.
  assert.match(memberSrc, /editable = perms\.canManageProject && !isOwner/);
});

test("ProjectSettingsPage gates invite button on canInvite", () => {
  assert.match(settingsSrc, /perms\.canInvite/);
  assert.match(settingsSrc, /<MemberList \/>/);
  assert.match(settingsSrc, /<InviteDialog/);
});

test("ProjectSwitcher exposes onOpenSettings entry visible to managers", () => {
  // The settings entry only appears for admin/owner — viewer/member must
  // not see it because the underlying RPCs would fail anyway.
  assert.match(switcherSrc, /onOpenSettings\?: \(\) => void/);
  assert.match(switcherSrc, /perms\.canManageProject/);
  assert.match(switcherSrc, /project-switcher-settings/);
});

test("TopBar wires ProjectSettingsPage modal via onOpenSettings", () => {
  assert.match(topbarSrc, /import \{ ProjectSettingsPage \} from/);
  assert.match(topbarSrc, /onOpenSettings=\{\(\) => setSettingsOpen\(true\)\}/);
  assert.match(topbarSrc, /<ProjectSettingsPage \/>/);
});

test("i18n carries the new invite/members keys both EN and ZH", () => {
  // Spot-check the keys most likely to drift if the file is reformatted.
  for (const key of [
    "invite.title",
    "invite.username.placeholder",
    "invite.success",
    "members.col.user",
    "members.remove.confirm",
  ]) {
    assert.match(
      i18nSrc,
      new RegExp(`"${key.replace(/\./g, "\\.")}":\\s*\\{\\s*en:`),
      `${key} missing en`,
    );
    assert.match(
      i18nSrc,
      new RegExp(`"${key.replace(/\./g, "\\.")}":[^\\n]*\\bzh:`),
      `${key} missing zh`,
    );
  }
});
