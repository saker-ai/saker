"use client";

import { useState } from "react";
import { toast } from "sonner";

import { useT } from "@/features/i18n";
import { useRpcStore } from "@/features/rpc/rpcStore";
import { RpcError } from "@/features/rpc/client";
import type { ProjectRole } from "./usePermissions";

interface Props {
  open: boolean;
  onClose: () => void;
  // Caller refreshes the member/invite lists after success — the dialog itself
  // does not own that state so it stays reusable from any container.
  onInvited?: () => void;
}

const ROLES: ProjectRole[] = ["admin", "member", "viewer"];

/**
 * InviteDialog calls `project/invite` with username + role. The username must
 * already exist in the users table (backend rejects with -32004 otherwise),
 * so the error path surfaces "user not found" inline rather than as a toast.
 */
export function InviteDialog({ open, onClose, onInvited }: Props) {
  const { t } = useT();
  const rpc = useRpcStore((s) => s.rpc);
  const [username, setUsername] = useState("");
  const [role, setRole] = useState<ProjectRole>("member");
  const [submitting, setSubmitting] = useState(false);
  const [fieldError, setFieldError] = useState<string | null>(null);

  if (!open) return null;

  const reset = () => {
    setUsername("");
    setRole("member");
    setFieldError(null);
  };

  const submit = async () => {
    const trimmed = username.trim();
    if (!trimmed || !rpc) return;
    setSubmitting(true);
    setFieldError(null);
    try {
      await rpc.request("project/invite", {
        username: trimmed,
        role,
      });
      onInvited?.();
      toast.success(t("invite.success"));
      reset();
      onClose();
    } catch (err) {
      const msg = err instanceof RpcError ? err.message : String(err);
      // Highlight username field for "user not found" / "already a member".
      if (
        msg.toLowerCase().includes("not found") ||
        msg.toLowerCase().includes("already")
      ) {
        setFieldError(msg);
      } else {
        toast.error(t("invite.error") + ": " + msg);
      }
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
        <h2 className="modal-title">{t("invite.title")}</h2>
        <label className="modal-label">{t("invite.username")}</label>
        <input
          autoFocus
          type="text"
          className={`modal-input${fieldError ? " modal-input-error" : ""}`}
          placeholder={t("invite.username.placeholder")}
          value={username}
          onChange={(e) => {
            setUsername(e.target.value);
            if (fieldError) setFieldError(null);
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter") submit();
            if (e.key === "Escape") onClose();
          }}
          disabled={submitting}
          aria-invalid={fieldError ? true : undefined}
          aria-describedby={fieldError ? "invite-username-error" : undefined}
        />
        {fieldError && (
          <p id="invite-username-error" className="modal-field-error">
            {fieldError}
          </p>
        )}
        <label className="modal-label">{t("invite.role")}</label>
        <select
          className="modal-input"
          value={role}
          onChange={(e) => setRole(e.target.value as ProjectRole)}
          disabled={submitting}
        >
          {ROLES.map((r) => (
            <option key={r} value={r}>
              {t(`role.${r}`)}
            </option>
          ))}
        </select>
        <div className="modal-actions">
          <button
            className="modal-btn modal-btn-secondary"
            onClick={() => {
              reset();
              onClose();
            }}
            disabled={submitting}
          >
            {t("project.cancel")}
          </button>
          <button
            className="modal-btn modal-btn-primary"
            onClick={submit}
            disabled={submitting || !username.trim()}
          >
            {submitting ? t("invite.submitting") : t("invite.submit")}
          </button>
        </div>
      </div>
    </div>
  );
}
