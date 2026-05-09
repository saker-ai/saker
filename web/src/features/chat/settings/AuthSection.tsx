import { useState } from "react";
import type { WebAuthConfig, UserAuth } from "@/features/rpc/types";
import { useT } from "@/features/i18n";
import { PasswordInput } from "./shared";

// --- Combined Auth & Users Section ---

export function AuthAndUsersSection({
  config,
  onSave,
  onDelete,
  users,
  onCreateUser,
  onDeleteUser,
  showToast,
}: {
  config?: WebAuthConfig;
  onSave?: (username: string, password: string) => Promise<void>;
  onDelete?: () => Promise<void>;
  users?: UserAuth[];
  onCreateUser?: (username: string, password: string) => Promise<void>;
  onDeleteUser?: (username: string) => Promise<void>;
  showToast?: (text: string, type: "success" | "error") => void;
}) {
  const { t } = useT();

  return (
    <div className="settings-card" id="auth" data-section="auth">
      <div className="settings-card-title"><span>{t("settings.authAndUsers")}</span></div>

      {/* Admin Credentials subsection */}
      <AdminCredentials
        config={config}
        onSave={onSave}
        onDelete={onDelete}
        showToast={showToast}
      />

      {/* Divider */}
      <div className="settings-auth-divider" />

      {/* User Management subsection */}
      <UserManagement
        users={users}
        onCreateUser={onCreateUser}
        onDeleteUser={onDeleteUser}
        showToast={showToast}
      />
    </div>
  );
}

// --- Admin Credentials ---

function AdminCredentials({
  config,
  onSave,
  onDelete,
  showToast,
}: {
  config?: WebAuthConfig;
  onSave?: (username: string, password: string) => Promise<void>;
  onDelete?: () => Promise<void>;
  showToast?: (text: string, type: "success" | "error") => void;
}) {
  const { t } = useT();
  const [editing, setEditing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");

  const handleEdit = () => {
    setUsername(config?.username || "admin");
    setPassword("");
    setError("");
    setEditing(true);
  };

  const handleCancel = () => {
    setEditing(false);
    setError("");
  };

  const handleSave = async () => {
    if (!onSave || !password) return;
    setSaving(true);
    setError("");
    try {
      await onSave(username || "admin", password);
      setEditing(false);
      showToast?.(t("settings.saved"), "success");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async () => {
    if (!onDelete) return;
    setSaving(true);
    try {
      await onDelete();
      setEditing(false);
      showToast?.(t("settings.saved"), "success");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  const configured = !!config?.username;

  return (
    <div className="settings-subsection">
      <div className="settings-subsection-header">
        <span className="settings-subtitle">{t("settings.adminCredentials")}</span>
        {!editing && (
          <button className="settings-btn-edit" onClick={handleEdit}>
            {configured ? t("settings.edit") : t("settings.configure")}
          </button>
        )}
      </div>

      {!editing ? (
        configured ? (
          <div className="settings-field-row">
            <span className="settings-field-label">{t("auth.username")}</span>
            <span className="settings-field-value">{config?.username || "admin"}</span>
          </div>
        ) : (
          <p className="settings-muted">{t("settings.webAuthNotConfigured")}</p>
        )
      ) : (
        <div>
          {error && <div className="settings-error">{error}</div>}
          <label className="settings-field">
            <span className="settings-field-label">{t("auth.username")}</span>
            <input
              className="settings-input"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder="admin"
            />
          </label>
          <label className="settings-field">
            <span className="settings-field-label">{t("auth.newPassword")}</span>
            <PasswordInput
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={t("auth.enterNewPassword")}
              required
            />
          </label>
          <div className="settings-card-actions">
            {configured && (
              <button className="settings-btn-danger" onClick={handleDelete} disabled={saving}>
                {t("settings.delete")}
              </button>
            )}
            <div className="settings-spacer" />
            <button className="settings-btn-cancel" onClick={handleCancel}>
              {t("settings.cancel")}
            </button>
            <button className="settings-btn-save" onClick={handleSave} disabled={saving || !password}>
              {saving ? t("settings.saving") : t("settings.save")}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

// --- User Management ---

function UserManagement({
  users,
  onCreateUser,
  onDeleteUser,
  showToast,
}: {
  users?: UserAuth[];
  onCreateUser?: (username: string, password: string) => Promise<void>;
  onDeleteUser?: (username: string) => Promise<void>;
  showToast?: (text: string, type: "success" | "error") => void;
}) {
  const { t } = useT();
  const [adding, setAdding] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [newUsername, setNewUsername] = useState("");
  const [newPassword, setNewPassword] = useState("");

  const handleAdd = async () => {
    if (!onCreateUser || !newUsername || !newPassword) return;
    setSaving(true);
    setError("");
    try {
      await onCreateUser(newUsername, newPassword);
      setAdding(false);
      setNewUsername("");
      setNewPassword("");
      showToast?.(t("settings.saved"), "success");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async (username: string) => {
    if (!onDeleteUser) return;
    if (!window.confirm(t("settings.confirmDeleteUser").replace("{username}", username))) return;
    setSaving(true);
    try {
      await onDeleteUser(username);
      showToast?.(t("settings.saved"), "success");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="settings-subsection">
      <div className="settings-subsection-header">
        <span className="settings-subtitle">{t("settings.users")}</span>
        {!adding && (
          <button className="settings-btn-edit" onClick={() => { setAdding(true); setError(""); }}>
            {t("settings.addUser")}
          </button>
        )}
      </div>

      <p className="settings-muted settings-mb-sm">{t("settings.usersDesc")}</p>

      {error && <div className="settings-error">{error}</div>}

      {(!users || users.length === 0) && !adding && (
        <p className="settings-muted">{t("settings.noUsers")}</p>
      )}

      {users && users.length > 0 && (
        <table className="settings-users-table">
          <caption className="sr-only">{t("settings.usersTableCaption")}</caption>
          <thead>
            <tr>
              <th>{t("auth.username")}</th>
              <th>{t("settings.userRole")}</th>
              <th className="col-actions"></th>
            </tr>
          </thead>
          <tbody>
            {users.map((u) => (
              <tr key={u.username}>
                <td>{u.username}</td>
                <td className="role-muted">{t("settings.user")}</td>
                <td className="col-actions">
                  <button
                    className="settings-btn-danger btn-small"
                    onClick={() => handleDelete(u.username)}
                    disabled={saving}
                    aria-label={t("settings.deleteUserAriaLabel").replace("{username}", u.username)}
                  >
                    {t("settings.deleteUser")}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {adding && (
        <div className="settings-mt-sm">
          <label className="settings-field">
            <span className="settings-field-label">{t("auth.username")}</span>
            <input
              className="settings-input"
              value={newUsername}
              onChange={(e) => setNewUsername(e.target.value.toLowerCase().replace(/[^a-z0-9_-]/g, ""))}
              placeholder="username"
              autoFocus
            />
          </label>
          <label className="settings-field">
            <span className="settings-field-label">{t("auth.newPassword")}</span>
            <PasswordInput
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              placeholder={t("auth.enterNewPassword")}
            />
          </label>
          <div className="settings-card-actions">
            <div className="settings-spacer" />
            <button className="settings-btn-cancel" onClick={() => { setAdding(false); setError(""); }}>
              {t("settings.cancel")}
            </button>
            <button className="settings-btn-save" onClick={handleAdd} disabled={saving || !newUsername || !newPassword}>
              {saving ? t("settings.saving") : t("settings.save")}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
