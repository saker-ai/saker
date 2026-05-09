"use client";

import { useState } from "react";
import { UserPlus, Trash2, Crown } from "lucide-react";

import { useT } from "@/features/i18n";
import { useProjectStore } from "./projectStore";
import { usePermissions } from "./usePermissions";
import { InviteDialog } from "./InviteDialog";
import { MemberList } from "./MemberList";
import { DeleteProjectDialog } from "./DeleteProjectDialog";
import { TransferOwnershipDialog } from "./TransferOwnershipDialog";

/**
 * ProjectSettingsPage is the per-project admin surface — currently just
 * member management. Designed to live inside the chat shell as a side
 * panel rather than a separate route, so RPC scope stays consistent.
 */
export function ProjectSettingsPage() {
  const { t } = useT();
  const projects = useProjectStore((s) => s.projects);
  const currentId = useProjectStore((s) => s.currentProjectId);
  const perms = usePermissions();
  const [inviteOpen, setInviteOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [transferOpen, setTransferOpen] = useState(false);
  const [memberRefreshKey, setMemberRefreshKey] = useState(0);

  const current = projects.find((p) => p.id === currentId);

  if (!current) {
    return (
      <div className="project-settings-page">
        <p className="muted">{t("project.none")}</p>
      </div>
    );
  }

  return (
    <div className="project-settings-page">
      <header className="project-settings-header">
        <div>
          <h2 className="project-settings-title">{current.name}</h2>
          <div className="project-settings-meta">
            <span className={`role-badge role-${current.role}`}>
              {t(`role.${current.role}`)}
            </span>
            <span className="muted">{t(`project.kind.${current.kind}`)}</span>
          </div>
        </div>
        {perms.canInvite && (
          <button
            className="modal-btn modal-btn-primary"
            onClick={() => setInviteOpen(true)}
          >
            <UserPlus size={14} strokeWidth={1.75} />
            <span>{t("invite.button")}</span>
          </button>
        )}
      </header>

      <section key={memberRefreshKey}>
        <MemberList />
      </section>

      {perms.canTransferOrDelete && current.kind !== "personal" && (
        <section className="danger-zone">
          <header>
            <h3 className="danger-zone-title">{t("project.danger.title")}</h3>
            <p className="danger-zone-body">{t("project.danger.body")}</p>
          </header>
          <div className="danger-zone-actions">
            <button
              className="modal-btn modal-btn-secondary"
              onClick={() => setTransferOpen(true)}
            >
              <Crown size={14} strokeWidth={1.75} />
              <span>{t("project.transfer")}</span>
            </button>
            <button
              className="modal-btn modal-btn-danger"
              onClick={() => setDeleteOpen(true)}
            >
              <Trash2 size={14} strokeWidth={1.75} />
              <span>{t("project.delete")}</span>
            </button>
          </div>
        </section>
      )}

      <InviteDialog
        open={inviteOpen}
        onClose={() => setInviteOpen(false)}
        onInvited={() => setMemberRefreshKey((k) => k + 1)}
      />
      <DeleteProjectDialog
        open={deleteOpen}
        projectId={current.id}
        projectName={current.name}
        onClose={() => setDeleteOpen(false)}
      />
      <TransferOwnershipDialog
        open={transferOpen}
        projectId={current.id}
        projectName={current.name}
        onClose={() => setTransferOpen(false)}
        onTransferred={() => setMemberRefreshKey((k) => k + 1)}
      />
    </div>
  );
}
