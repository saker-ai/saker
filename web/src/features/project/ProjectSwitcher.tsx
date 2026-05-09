"use client";

import { Plus, Folder, Settings as SettingsIcon } from "lucide-react";

import { useT } from "@/features/i18n";
import { useRpcStore } from "@/features/rpc/rpcStore";
import { useProjectStore } from "./projectStore";
import { usePermissions } from "./usePermissions";

interface Props {
  /** Fires after the user picks switch / create / settings so the parent
   * menu (TopBar's user dropdown) can collapse itself. */
  onAction?: () => void;
  /** Optional callback when "create new project" is clicked. */
  onCreate?: () => void;
  /** Optional callback when "project settings" is clicked. Visible to admin/owner. */
  onOpenSettings?: () => void;
}

/**
 * ProjectSwitcher renders the membership list (active project highlighted),
 * an optional settings entry, and an optional "create new project" entry as
 * an inline vertical menu. It is meant to be embedded inside another
 * dropdown — TopBar's user menu — so it has no trigger button or own
 * popover. Clicking any row calls onAction to let the parent close itself.
 */
export function ProjectSwitcher({ onAction, onCreate, onOpenSettings }: Props) {
  const { t } = useT();
  const rpc = useRpcStore((s) => s.rpc);
  const projects = useProjectStore((s) => s.projects);
  const currentId = useProjectStore((s) => s.currentProjectId);
  const setCurrent = useProjectStore((s) => s.setCurrent);
  const refresh = useProjectStore((s) => s.refresh);
  const perms = usePermissions();

  // Empty state: nothing to switch yet — surface a refresh affordance so
  // the user isn't stuck if the boot-time fetch failed.
  if (projects.length === 0) {
    return (
      <button
        className="project-switcher-item project-switcher-empty"
        onClick={() => refresh()}
        aria-label={t("project.empty")}
      >
        <Folder size={14} strokeWidth={1.75} />
        <span>{t("project.empty")}</span>
      </button>
    );
  }

  return (
    <div className="project-switcher-list" role="listbox">
      {projects.map((p) => (
        <button
          key={p.id}
          role="option"
          aria-selected={p.id === currentId}
          className={`project-switcher-item ${
            p.id === currentId ? "active" : ""
          }`}
          onClick={() => {
            setCurrent(p.id);
            onAction?.();
          }}
        >
          <Folder size={14} strokeWidth={1.75} />
          <span className="project-switcher-item-name">{p.name}</span>
          <span className={`role-badge role-${p.role}`}>
            {t(`role.${p.role}`)}
          </span>
          {p.kind === "personal" && (
            <span className="project-kind-badge">
              {t("project.kind.personal")}
            </span>
          )}
        </button>
      ))}
      {(onCreate || (onOpenSettings && perms.canManageProject)) && (
        <div className="project-switcher-sep" />
      )}
      {onOpenSettings && perms.canManageProject && (
        <button
          className="project-switcher-item project-switcher-settings"
          onClick={() => {
            onAction?.();
            onOpenSettings();
          }}
        >
          <SettingsIcon size={14} strokeWidth={1.75} />
          <span>{t("project.settings")}</span>
        </button>
      )}
      {onCreate && (
        <button
          className="project-switcher-item project-switcher-create"
          onClick={() => {
            onAction?.();
            onCreate();
          }}
        >
          <Plus size={14} strokeWidth={1.75} />
          <span>{t("project.create")}</span>
        </button>
      )}
    </div>
  );
}
