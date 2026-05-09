"use client";

import { useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { Crown } from "lucide-react";

import { useT } from "@/features/i18n";
import { useRpcStore } from "@/features/rpc/rpcStore";
import { RpcError } from "@/features/rpc/client";
import { useProjectStore } from "./projectStore";
import type { ProjectRole } from "./usePermissions";

interface Member {
  userId: string;
  role: ProjectRole;
  username?: string;
  displayName?: string;
  email?: string;
}

interface Props {
  open: boolean;
  projectId: string;
  projectName: string;
  onClose: () => void;
  onTransferred?: () => void;
}

/**
 * TransferOwnershipDialog hands the owner role to another existing member.
 * The dialog deliberately requires the owner to retype the project name
 * because ownership transfer is irreversible from the previous owner's
 * perspective — only the new owner can transfer it back.
 *
 * Members are loaded fresh on open (not from a parent prop) so the picker
 * always reflects the current member set even if someone joined since the
 * settings page was first rendered.
 */
export function TransferOwnershipDialog({
  open,
  projectId,
  projectName,
  onClose,
  onTransferred,
}: Props) {
  const { t } = useT();
  const rpc = useRpcStore((s) => s.rpc);
  const refresh = useProjectStore((s) => s.refresh);
  const [members, setMembers] = useState<Member[]>([]);
  const [targetId, setTargetId] = useState("");
  const [confirm, setConfirm] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [loadingMembers, setLoadingMembers] = useState(false);

  // Reset transient state on close so reopening starts fresh.
  useEffect(() => {
    if (!open) {
      setTargetId("");
      setConfirm("");
      setSubmitting(false);
    }
  }, [open]);

  useEffect(() => {
    if (!open || !rpc) return;
    let cancelled = false;
    const load = async () => {
      setLoadingMembers(true);
      try {
        const res = await rpc.request<{ members: Member[] }>(
          "project/member/list",
        );
        if (cancelled) return;
        setMembers(Array.isArray(res?.members) ? res.members : []);
      } catch (err) {
        if (cancelled) return;
        const msg = err instanceof RpcError ? err.message : String(err);
        toast.error(t("members.load.error") + ": " + msg);
      } finally {
        if (!cancelled) setLoadingMembers(false);
      }
    };
    void load();
    return () => {
      cancelled = true;
    };
  }, [open, rpc, t]);

  // Owner can transfer to anyone *but* themselves. The store API treats
  // self-transfer as a no-op, but the dropdown should never offer it.
  const candidates = useMemo(
    () => members.filter((m) => m.role !== "owner"),
    [members],
  );

  if (!open) return null;

  const targetLabel = (() => {
    const t = members.find((m) => m.userId === targetId);
    return t?.displayName || t?.username || "";
  })();
  const matches = confirm.trim() === projectName;
  const canSubmit = !submitting && !!targetId && matches;

  const submit = async () => {
    if (!rpc || !canSubmit) return;
    setSubmitting(true);
    try {
      await rpc.request<{ ok: boolean }>("project/transfer", {
        projectId,
        targetUserId: targetId,
      });
      // The caller's role just dropped from owner→admin. Refresh so
      // usePermissions / TopBar re-derives capabilities immediately.
      await refresh();
      toast.success(
        t("project.transfer.success").replace("{name}", targetLabel),
      );
      onTransferred?.();
      onClose();
    } catch (err) {
      const msg = err instanceof RpcError ? err.message : String(err);
      toast.error(t("project.transfer.error") + ": " + msg);
      setSubmitting(false);
    }
  };

  return (
    <div
      className="modal-backdrop"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="modal-card" role="dialog" aria-modal="true">
        <h2 className="modal-title danger-title">
          <Crown size={16} strokeWidth={1.75} />
          <span>{t("project.transfer")}</span>
        </h2>
        <p className="modal-body danger-body">
          {t("project.transfer.warning")}
        </p>

        <label className="modal-label">{t("project.transfer.target")}</label>
        {loadingMembers ? (
          <p className="muted">{t("members.loading")}</p>
        ) : candidates.length === 0 ? (
          <p className="muted">{t("project.transfer.empty")}</p>
        ) : (
          <select
            className="modal-input"
            value={targetId}
            onChange={(e) => setTargetId(e.target.value)}
            disabled={submitting}
          >
            <option value="">{t("project.transfer.choose")}</option>
            {candidates.map((m) => {
              const label = m.displayName || m.username || m.userId;
              return (
                <option key={m.userId} value={m.userId}>
                  {label} ({t(`role.${m.role}`)})
                </option>
              );
            })}
          </select>
        )}

        <p className="modal-body">
          {t("project.delete.confirmInput")}{" "}
          <code className="danger-name">{projectName}</code>
        </p>
        <input
          type="text"
          className="modal-input"
          placeholder={projectName}
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && canSubmit) submit();
            if (e.key === "Escape") onClose();
          }}
          disabled={submitting || !targetId}
        />

        <div className="modal-actions">
          <button
            className="modal-btn modal-btn-secondary"
            onClick={onClose}
            disabled={submitting}
          >
            {t("project.cancel")}
          </button>
          <button
            className="modal-btn modal-btn-danger"
            onClick={submit}
            disabled={!canSubmit}
          >
            {submitting
              ? t("project.transfer.submitting")
              : t("project.transfer")}
          </button>
        </div>
      </div>
    </div>
  );
}
