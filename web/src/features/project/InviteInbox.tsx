"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { Bell, Check, X } from "lucide-react";

import { useT } from "@/features/i18n";
import { RpcError } from "@/features/rpc/client";
import { httpRequest } from "@/features/rpc/httpRpc";
import { useProjectStore } from "./projectStore";
import type { ProjectRole } from "./usePermissions";

export interface PendingInvite {
  id: string;
  projectId: string;
  projectName?: string;
  projectKind?: "personal" | "team";
  username: string;
  userId: string;
  role: ProjectRole;
  invitedBy: string;
  inviterUsername?: string;
  inviterDisplayName?: string;
  status: string;
  createdAt: string;
}

const POLL_MS = 60_000;

/**
 * InviteInbox sits in the TopBar. It polls `project/invite/list-for-me` every
 * 60s while the tab is visible, surfaces a count badge when invites are
 * pending, and offers Accept / Decline actions in a dropdown panel.
 *
 * On accept, the project list is refreshed and the new project is selected so
 * the user lands inside the freshly-joined workspace immediately. On decline,
 * the row is removed from the local list optimistically; if the server later
 * complains (already declined / revoked), we re-fetch to recover.
 */
export function InviteInbox() {
  const { t } = useT();
  const setCurrent = useProjectStore((s) => s.setCurrent);
  const refreshProjects = useProjectStore((s) => s.refresh);
  const [open, setOpen] = useState(false);
  const [invites, setInvites] = useState<PendingInvite[]>([]);
  const [busyId, setBusyId] = useState<string | null>(null);
  const panelRef = useRef<HTMLDivElement>(null);

  const reload = useCallback(async () => {
    try {
      const res = await httpRequest<{ invites: PendingInvite[] }>(
        "project/invite/list-for-me",
      );
      setInvites(Array.isArray(res?.invites) ? res.invites : []);
    } catch {
      // Silent on the badge — the inbox refreshes on the next poll cycle. We
      // don't want a network hiccup to spam the user with toasts.
    }
  }, []);

  useEffect(() => {
    void reload();
    const tick = () => {
      if (typeof document !== "undefined" && document.hidden) return;
      void reload();
    };
    const id = window.setInterval(tick, POLL_MS);
    return () => window.clearInterval(id);
  }, [reload]);

  // Click-outside dismiss for the dropdown.
  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      if (!panelRef.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDoc);
    return () => document.removeEventListener("mousedown", onDoc);
  }, [open]);

  const accept = async (inv: PendingInvite) => {
    setBusyId(inv.id);
    try {
      await httpRequest("project/invite/accept", { inviteId: inv.id });
      toast.success(
        t("invite.acceptSuccess").replace(
          "{name}",
          inv.projectName || inv.projectId,
        ),
      );
      // Refresh both lists, then jump into the project so the user lands
      // inside the workspace they just joined.
      await refreshProjects();
      setCurrent(inv.projectId);
      await reload();
      // If that was the last invite, close the panel so the badge clears.
      setOpen((cur) => (invites.length <= 1 ? false : cur));
    } catch (err) {
      const msg = err instanceof RpcError ? err.message : String(err);
      toast.error(t("invite.acceptError") + ": " + msg);
      // Refresh in case the invite was revoked between list and click.
      void reload();
    } finally {
      setBusyId(null);
    }
  };

  const decline = async (inv: PendingInvite) => {
    setBusyId(inv.id);
    // Optimistic removal — UI feels instant. We re-sync on error.
    setInvites((cur) => cur.filter((x) => x.id !== inv.id));
    try {
      await httpRequest("project/invite/decline", { inviteId: inv.id });
      setOpen((cur) => (invites.length <= 1 ? false : cur));
    } catch (err) {
      const msg = err instanceof RpcError ? err.message : String(err);
      toast.error(t("invite.declineError") + ": " + msg);
      void reload();
    } finally {
      setBusyId(null);
    }
  };

  if (invites.length === 0) {
    // Hide the bell entirely when there's nothing pending — keeps the TopBar
    // quiet for the common case (most users have no pending invites).
    return null;
  }

  return (
    <div className="invite-inbox" ref={panelRef}>
      <button
        type="button"
        className="invite-inbox-trigger"
        onClick={() => setOpen((v) => !v)}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={t("invite.inboxTitle")}
        title={t("invite.inboxTitle")}
      >
        <Bell size={14} strokeWidth={1.75} />
        <span className="invite-inbox-badge">{invites.length}</span>
      </button>
      {open && (
        <div className="invite-inbox-panel" role="menu">
          <div className="invite-inbox-header">{t("invite.inboxTitle")}</div>
          {invites.map((inv) => {
            const inviter =
              inv.inviterDisplayName || inv.inviterUsername || inv.invitedBy;
            const projectLabel = inv.projectName || inv.projectId;
            return (
              <div key={inv.id} className="invite-inbox-row">
                <div className="invite-inbox-row-main">
                  <div className="invite-inbox-row-title">{projectLabel}</div>
                  <div className="invite-inbox-row-meta">
                    {t("invite.from")} <strong>{inviter}</strong>
                    {" · "}
                    <span className={`role-badge role-${inv.role}`}>
                      {t(`role.${inv.role}`)}
                    </span>
                  </div>
                </div>
                <div className="invite-inbox-row-actions">
                  <button
                    type="button"
                    className="modal-btn modal-btn-primary invite-inbox-btn"
                    onClick={() => accept(inv)}
                    disabled={busyId === inv.id}
                    aria-label={t("invite.accept")}
                    title={t("invite.accept")}
                  >
                    <Check size={12} strokeWidth={2} />
                    <span>{t("invite.accept")}</span>
                  </button>
                  <button
                    type="button"
                    className="modal-btn modal-btn-secondary invite-inbox-btn"
                    onClick={() => decline(inv)}
                    disabled={busyId === inv.id}
                    aria-label={t("invite.decline")}
                    title={t("invite.decline")}
                  >
                    <X size={12} strokeWidth={2} />
                    <span>{t("invite.decline")}</span>
                  </button>
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
