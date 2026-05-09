"use client";

import { useState } from "react";
import { toast } from "sonner";

import { useT } from "@/features/i18n";
import { useRpcStore } from "@/features/rpc/rpcStore";
import { RpcError } from "@/features/rpc/client";
import { useProjectStore } from "./projectStore";

interface Props {
  open: boolean;
  onClose: () => void;
}

/**
 * CreateProjectDialog issues `project/create` and switches to the new project
 * on success. Kept minimal — the goal is to validate the round-trip; a richer
 * dialog (templates, description, members) can come later.
 */
export function CreateProjectDialog({ open, onClose }: Props) {
  const { t } = useT();
  const rpc = useRpcStore((s) => s.rpc);
  const refresh = useProjectStore((s) => s.refresh);
  const setCurrent = useProjectStore((s) => s.setCurrent);
  const [name, setName] = useState("");
  const [submitting, setSubmitting] = useState(false);

  if (!open) return null;

  const submit = async () => {
    const trimmed = name.trim();
    if (!trimmed || !rpc) return;
    setSubmitting(true);
    try {
      const res = await rpc.request<{ id: string }>("project/create", {
        name: trimmed,
      });
      await refresh();
      setCurrent(res.id);
      setName("");
      onClose();
    } catch (err) {
      const msg = err instanceof RpcError ? err.message : String(err);
      toast.error(t("project.create.error") + ": " + msg);
    } finally {
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
        <h2 className="modal-title">{t("project.create.title")}</h2>
        <input
          autoFocus
          type="text"
          className="modal-input"
          placeholder={t("project.create.placeholder")}
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") submit();
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
            className="modal-btn modal-btn-primary"
            onClick={submit}
            disabled={submitting || !name.trim()}
          >
            {submitting ? t("project.create.submitting") : t("project.create")}
          </button>
        </div>
      </div>
    </div>
  );
}
