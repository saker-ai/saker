"use client";

import { useEffect, useState } from "react";
import { toast } from "sonner";
import { AlertTriangle } from "lucide-react";

import { useT } from "@/features/i18n";
import { useRpcStore } from "@/features/rpc/rpcStore";
import { RpcError } from "@/features/rpc/client";
import { useProjectStore } from "./projectStore";

interface Props {
  open: boolean;
  projectId: string;
  projectName: string;
  onClose: () => void;
}

/**
 * DeleteProjectDialog asks the user to retype the project name before issuing
 * `project/delete`. The retype gate matches what the server permits (owner
 * only, non-personal projects) and prevents a single misclick from wiping a
 * shared workspace.
 *
 * On success we refresh the project list, which causes the store to fall back
 * to the user's personal project as the new current selection.
 */
export function DeleteProjectDialog({
  open,
  projectId,
  projectName,
  onClose,
}: Props) {
  const { t } = useT();
  const rpc = useRpcStore((s) => s.rpc);
  const refresh = useProjectStore((s) => s.refresh);
  const [confirm, setConfirm] = useState("");
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (!open) {
      setConfirm("");
      setSubmitting(false);
    }
  }, [open]);

  if (!open) return null;

  const matches = confirm.trim() === projectName;

  const submit = async () => {
    if (!rpc || !matches || submitting) return;
    setSubmitting(true);
    try {
      await rpc.request<{ ok: boolean }>("project/delete", {
        projectId,
      });
      await refresh();
      toast.success(t("project.delete.success"));
      onClose();
    } catch (err) {
      const msg = err instanceof RpcError ? err.message : String(err);
      toast.error(t("project.delete.error") + ": " + msg);
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
          <AlertTriangle size={16} strokeWidth={1.75} />
          <span>{t("project.delete")}</span>
        </h2>
        <p className="modal-body danger-body">{t("project.delete.confirm")}</p>
        <p className="modal-body">
          {t("project.delete.confirmInput")}{" "}
          <code className="danger-name">{projectName}</code>
        </p>
        <input
          autoFocus
          type="text"
          className="modal-input"
          placeholder={projectName}
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && matches) submit();
            if (e.key === "Escape") onClose();
          }}
          disabled={submitting}
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
            disabled={submitting || !matches}
          >
            {submitting ? t("project.delete.deleting") : t("project.delete")}
          </button>
        </div>
      </div>
    </div>
  );
}
