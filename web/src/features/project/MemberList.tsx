"use client";

import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";

import { useT } from "@/features/i18n";
import { useRpcStore } from "@/features/rpc/rpcStore";
import { RpcError } from "@/features/rpc/client";
import { useProjectStore } from "./projectStore";
import { usePermissions, type ProjectRole } from "./usePermissions";

interface Member {
  projectId: string;
  userId: string;
  role: ProjectRole;
  username?: string;
  displayName?: string;
  email?: string;
  joinedAt?: string;
}

const EDITABLE_ROLES: ProjectRole[] = ["admin", "member", "viewer"];

/**
 * MemberList renders the project's members with inline role editing and
 * removal. Owner/admin see edit controls; everyone else gets a read-only view.
 * Owner cannot be downgraded here — transfer ownership lives elsewhere.
 */
export function MemberList() {
  const { t } = useT();
  const rpc = useRpcStore((s) => s.rpc);
  const projectId = useProjectStore((s) => s.currentProjectId);
  const refreshProjects = useProjectStore((s) => s.refresh);
  const perms = usePermissions();
  const [members, setMembers] = useState<Member[]>([]);
  const [loading, setLoading] = useState(false);

  const reload = useCallback(async () => {
    if (!rpc || !projectId) return;
    setLoading(true);
    try {
      const res = await rpc.request<{ members: Member[] }>(
        "project/member/list",
      );
      setMembers(Array.isArray(res?.members) ? res.members : []);
    } catch (err) {
      const msg = err instanceof RpcError ? err.message : String(err);
      toast.error(t("members.load.error") + ": " + msg);
    } finally {
      setLoading(false);
    }
  }, [rpc, projectId, t]);

  useEffect(() => {
    void reload();
  }, [reload]);

  const updateRole = async (userId: string, role: ProjectRole) => {
    if (!rpc) return;
    try {
      await rpc.request("project/member/update-role", { userId, role });
      await reload();
      // Role changes for *this* user shift the project's effective role —
      // keep the list synced so usePermissions re-derives correctly.
      await refreshProjects();
    } catch (err) {
      const msg = err instanceof RpcError ? err.message : String(err);
      toast.error(t("members.update.error") + ": " + msg);
    }
  };

  const removeMember = async (userId: string, label: string) => {
    if (!rpc) return;
    if (!window.confirm(t("members.remove.confirm").replace("{name}", label))) {
      return;
    }
    try {
      await rpc.request("project/member/remove", { userId });
      await reload();
      await refreshProjects();
    } catch (err) {
      const msg = err instanceof RpcError ? err.message : String(err);
      toast.error(t("members.remove.error") + ": " + msg);
    }
  };

  if (!projectId) {
    return <p className="muted">{t("project.none")}</p>;
  }

  return (
    <div className="member-list">
      <div className="member-list-header">
        <h3 className="member-list-title">{t("members.title")}</h3>
        <button
          className="modal-btn modal-btn-secondary"
          onClick={reload}
          disabled={loading}
        >
          {loading ? t("members.loading") : t("members.refresh")}
        </button>
      </div>
      {members.length === 0 ? (
        <p className="muted">
          {loading ? t("members.loading") : t("members.empty")}
        </p>
      ) : (
        <table className="member-table">
          <thead>
            <tr>
              <th>{t("members.col.user")}</th>
              <th>{t("members.col.role")}</th>
              <th>{t("members.col.joined")}</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {members.map((m) => {
              const label = m.displayName || m.username || m.userId;
              const isOwner = m.role === "owner";
              const editable = perms.canManageProject && !isOwner;
              return (
                <tr key={m.userId}>
                  <td>
                    <div className="member-name">{label}</div>
                    {m.email && <div className="member-email">{m.email}</div>}
                  </td>
                  <td>
                    {editable ? (
                      <select
                        className="modal-input member-role-select"
                        value={m.role}
                        onChange={(e) =>
                          updateRole(m.userId, e.target.value as ProjectRole)
                        }
                      >
                        {EDITABLE_ROLES.map((r) => (
                          <option key={r} value={r}>
                            {t(`role.${r}`)}
                          </option>
                        ))}
                      </select>
                    ) : (
                      <span className={`role-badge role-${m.role}`}>
                        {t(`role.${m.role}`)}
                      </span>
                    )}
                  </td>
                  <td className="member-joined">
                    {m.joinedAt ? new Date(m.joinedAt).toLocaleDateString() : "—"}
                  </td>
                  <td>
                    {editable && (
                      <button
                        className="member-remove"
                        onClick={() => removeMember(m.userId, label)}
                        title={t("members.remove")}
                        aria-label={t("members.remove")}
                      >
                        {t("members.remove")}
                      </button>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </div>
  );
}
