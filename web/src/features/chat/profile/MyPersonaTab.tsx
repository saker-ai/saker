import { useState, useCallback, useEffect } from "react";
import { Plus, Sparkles, Trash2, Check, Crown } from "lucide-react";
import type { PersonaProfile, UserPersonaListResult } from "@/features/rpc/types";
import type { RPCClient } from "@/features/rpc/client";
import { useT, type TKey } from "@/features/i18n";
import { useToast, Toast, ConfirmDialog } from "../settings/shared";

interface Props {
  rpc: RPCClient | null;
}

interface EditingPersona {
  id: string;
  profile: PersonaProfile;
}

export function MyPersonaTab({ rpc }: Props) {
  const { t } = useT();
  const { toast, showToast } = useToast();
  const [data, setData] = useState<UserPersonaListResult | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [editing, setEditing] = useState<EditingPersona | null>(null);
  const [showNew, setShowNew] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!rpc) return;
    try {
      const res = await rpc.request<UserPersonaListResult>("persona/user-list");
      setData(res);
    } catch {
      showToast(t("settings.saveFailed"), "error");
    } finally {
      setLoading(false);
    }
  }, [rpc, showToast, t]);

  useEffect(() => { load(); }, [load]);

  if (loading || !data) {
    return <div className="settings-empty">{t("settings.loading")}</div>;
  }

  const { globalProfiles, globalDefault, userProfiles, active } = data;
  const globalIds = Object.keys(globalProfiles).sort();
  const userIds = Object.keys(userProfiles).sort();

  const handleActivate = async (id: string) => {
    if (!rpc || saving) return;
    setSaving(true);
    try {
      await rpc.request("persona/user-set-active", { id: active === id ? "" : id });
      showToast(t("settings.saved"), "success");
      await load();
    } catch {
      showToast(t("settings.saveFailed"), "error");
    } finally {
      setSaving(false);
    }
  };

  const handleSaveNew = async (id: string, profile: PersonaProfile) => {
    if (!rpc || !id.trim() || saving) return;
    setSaving(true);
    try {
      await rpc.request("persona/user-save", { id, profile });
      showToast(t("settings.saved"), "success");
      setShowNew(false);
      setEditing(null);
      await load();
    } catch {
      showToast(t("settings.saveFailed"), "error");
    } finally {
      setSaving(false);
    }
  };

  const handleSaveEdit = async () => {
    if (!rpc || !editing || saving) return;
    setSaving(true);
    try {
      await rpc.request("persona/user-save", { id: editing.id, profile: editing.profile });
      showToast(t("settings.saved"), "success");
      setEditing(null);
      await load();
    } catch {
      showToast(t("settings.saveFailed"), "error");
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async (id: string) => {
    if (!rpc || saving) return;
    setSaving(true);
    try {
      await rpc.request("persona/user-delete", { id });
      showToast(t("settings.saved"), "success");
      if (editing?.id === id) setEditing(null);
      await load();
    } catch {
      showToast(t("settings.saveFailed"), "error");
    } finally {
      setSaving(false);
      setConfirmDelete(null);
    }
  };

  return (
    <div className="settings-tab-stack">
      <Toast msg={toast} />

      <ConfirmDialog
        open={confirmDelete !== null}
        title={t("persona.confirmDeleteTitle")}
        message={t("persona.confirmDelete")}
        confirmLabel={t("persona.confirm")}
        cancelLabel={t("persona.cancel")}
        danger
        onConfirm={() => confirmDelete && handleDelete(confirmDelete)}
        onCancel={() => setConfirmDelete(null)}
      />

      {/* Active persona indicator */}
      <div className="settings-card-v2">
        <div className="settings-card-v2-header">
          <Check size={18} />
          <span>{t("profile.activePersona")}</span>
        </div>
        <div className="settings-card-v2-body">
          {active ? (
            <div className="profile-active-persona">
              <span className="persona-card-emoji" role="img" aria-label={globalProfiles[active]?.name || userProfiles[active]?.name || "persona"}>
                {(globalProfiles[active]?.emoji || userProfiles[active]?.emoji) || "🤖"}
              </span>
              <span className="persona-card-name">
                {(globalProfiles[active]?.name || userProfiles[active]?.name) || active}
              </span>
              <button
                className="persona-btn"
                onClick={() => handleActivate(active)}
                disabled={saving}
                type="button"
              >
                {t("profile.deactivate")}
              </button>
            </div>
          ) : (
            <p className="persona-subtitle">{t("profile.noActive")}</p>
          )}
        </div>
      </div>

      {/* Global personas (read-only, selectable) */}
      {globalIds.length > 0 && (
        <div className="settings-card-v2">
          <div className="settings-card-v2-header">
            <Crown size={16} />
            <span>{t("profile.globalPersonas")}</span>
          </div>
          <div className="settings-card-v2-body">
            <p className="persona-subtitle">{t("profile.readOnly")}</p>
            <div className="profile-persona-grid">
              {globalIds.map((id) => {
                const p = globalProfiles[id];
                const isActive = active === id;
                const isDefault = globalDefault === id;
                return (
                  <button
                    key={id}
                    className={`profile-persona-card ${isActive ? "active" : ""}`}
                    onClick={() => handleActivate(id)}
                    disabled={saving}
                    type="button"
                    title={t("profile.clickToActivate")}
                  >
                    <span className="profile-persona-card-emoji" role="img" aria-label={p.name || id}>{p.emoji || "🤖"}</span>
                    <span className="profile-persona-card-name">{p.name || id}</span>
                    {p.description && <span className="profile-persona-card-desc">{p.description}</span>}
                    {isDefault && <span className="persona-card-default-badge">{t("persona.isDefault")}</span>}
                    {isActive && <span className="profile-persona-card-check"><Check size={14} /></span>}
                  </button>
                );
              })}
            </div>
          </div>
        </div>
      )}

      {/* User's own personas (editable) */}
      <div className="settings-card-v2">
        <div className="settings-card-v2-header">
          <Sparkles size={16} />
          <span>{t("profile.userPersonas")}</span>
          <button className="persona-add-btn" onClick={() => setShowNew(true)} type="button">
            <Plus size={14} />
            <span>{t("profile.createPersona")}</span>
          </button>
        </div>
        <div className="settings-card-v2-body">
          {/* New persona form */}
          {showNew && (
            <NewPersonaForm
              onSave={handleSaveNew}
              onCancel={() => setShowNew(false)}
              existingIds={[...globalIds, ...userIds]}
              saving={saving}
              t={t}
            />
          )}

          {/* User persona list */}
          {userIds.length === 0 && !showNew && (
            <p className="persona-subtitle">{t("persona.noPersonas")}</p>
          )}

          <div className="profile-persona-grid">
            {userIds.map((id) => {
              const p = userProfiles[id];
              const isActive = active === id;
              const isEditing = editing?.id === id;
              return (
                <div key={id} className={`profile-persona-card user-card ${isActive ? "active" : ""}`}>
                  <div
                    className="profile-persona-card-main"
                    onClick={() => handleActivate(id)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" || e.key === " ") {
                        e.preventDefault();
                        handleActivate(id);
                      }
                    }}
                    role="button"
                    tabIndex={0}
                  >
                    <span className="profile-persona-card-emoji" role="img" aria-label={p.name || id}>{p.emoji || "🤖"}</span>
                    <span className="profile-persona-card-name">{p.name || id}</span>
                    {p.description && <span className="profile-persona-card-desc">{p.description}</span>}
                    {isActive && <span className="profile-persona-card-check"><Check size={14} /></span>}
                  </div>
                  <div className="profile-persona-card-actions" onClick={(e) => e.stopPropagation()}>
                    <button
                      className="persona-icon-btn"
                      onClick={() => setEditing(isEditing ? null : { id, profile: { ...p } })}
                      aria-label={t("persona.edit")}
                      type="button"
                    >
                      {t("persona.edit")}
                    </button>
                    <button
                      className="persona-icon-btn danger"
                      onClick={() => setConfirmDelete(id)}
                      aria-label={`${t("persona.delete")} ${p.name || id}`}
                      type="button"
                    >
                      <Trash2 size={14} />
                    </button>
                  </div>
                  {isEditing && editing && (
                    <div className="profile-persona-edit-inline" onClick={(e) => e.stopPropagation()}>
                      <PersonaEditForm
                        profile={editing.profile}
                        onChange={(profile) => setEditing({ ...editing, profile })}
                        onSave={handleSaveEdit}
                        onCancel={() => setEditing(null)}
                        saving={saving}
                        t={t}
                      />
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        </div>
      </div>
    </div>
  );
}

function NewPersonaForm({
  onSave,
  onCancel,
  existingIds,
  saving,
  t,
}: {
  onSave: (id: string, profile: PersonaProfile) => void;
  onCancel: () => void;
  existingIds: string[];
  saving: boolean;
  t: (key: TKey) => string;
}) {
  const [id, setId] = useState("");
  const [profile, setProfile] = useState<PersonaProfile>({ name: "", emoji: "", soul: "" });

  const isDuplicate = existingIds.includes(id);
  const isEmpty = !id.trim();
  const canSave = !isEmpty && !isDuplicate && !saving;

  return (
    <div className="persona-form" style={{ marginBottom: 16 }}>
      <div className="persona-form-row">
        <label>{t("persona.id")}</label>
        <input
          value={id}
          onChange={(e) => setId(e.target.value.replace(/[^a-z0-9_-]/gi, "").toLowerCase())}
          placeholder="my-persona"
          className={`persona-input ${isDuplicate ? "persona-input-error" : ""}`}
        />
        {isDuplicate && <span className="persona-field-error">{t("persona.idDuplicate")}</span>}
      </div>
      <div className="persona-form-grid">
        <div className="persona-form-row">
          <label>{t("persona.emoji")}</label>
          <input
            value={profile.emoji ?? ""}
            onChange={(e) => setProfile({ ...profile, emoji: e.target.value })}
            placeholder="🤖"
            className="persona-input persona-input-sm"
          />
        </div>
        <div className="persona-form-row">
          <label>{t("persona.name")}</label>
          <input
            value={profile.name ?? ""}
            onChange={(e) => setProfile({ ...profile, name: e.target.value })}
            placeholder="My Bot"
            className="persona-input"
          />
        </div>
      </div>
      <div className="persona-form-row">
        <label>{t("persona.description")}</label>
        <input
          value={profile.description ?? ""}
          onChange={(e) => setProfile({ ...profile, description: e.target.value })}
          className="persona-input"
        />
      </div>
      <div className="persona-form-row">
        <label>{t("persona.soul")}</label>
        <textarea
          value={profile.soul ?? ""}
          onChange={(e) => setProfile({ ...profile, soul: e.target.value })}
          placeholder={t("persona.soulPlaceholder")}
          className="persona-textarea"
          rows={4}
        />
      </div>
      <div className="persona-form-grid">
        <div className="persona-form-row">
          <label>{t("persona.model")}</label>
          <input
            value={profile.model ?? ""}
            onChange={(e) => setProfile({ ...profile, model: e.target.value })}
            placeholder="claude-sonnet-4-5"
            className="persona-input"
          />
        </div>
        <div className="persona-form-row">
          <label>{t("persona.language")}</label>
          <input
            value={profile.language ?? ""}
            onChange={(e) => setProfile({ ...profile, language: e.target.value })}
            placeholder="Chinese"
            className="persona-input"
          />
        </div>
      </div>
      <div className="persona-form-actions">
        <button
          className="persona-btn persona-btn-primary"
          onClick={() => onSave(id, profile)}
          disabled={!canSave}
          type="button"
        >
          {t("persona.save")}
        </button>
        <button className="persona-btn" onClick={onCancel} type="button">
          {t("persona.cancel")}
        </button>
      </div>
    </div>
  );
}

function PersonaEditForm({
  profile,
  onChange,
  onSave,
  onCancel,
  saving,
  t,
}: {
  profile: PersonaProfile;
  onChange: (p: PersonaProfile) => void;
  onSave: () => void;
  onCancel: () => void;
  saving: boolean;
  t: (key: TKey) => string;
}) {
  return (
    <div className="persona-form">
      <div className="persona-form-grid">
        <div className="persona-form-row">
          <label>{t("persona.emoji")}</label>
          <input
            value={profile.emoji ?? ""}
            onChange={(e) => onChange({ ...profile, emoji: e.target.value })}
            className="persona-input persona-input-sm"
          />
        </div>
        <div className="persona-form-row">
          <label>{t("persona.name")}</label>
          <input
            value={profile.name ?? ""}
            onChange={(e) => onChange({ ...profile, name: e.target.value })}
            className="persona-input"
          />
        </div>
      </div>
      <div className="persona-form-row">
        <label>{t("persona.description")}</label>
        <input
          value={profile.description ?? ""}
          onChange={(e) => onChange({ ...profile, description: e.target.value })}
          className="persona-input"
        />
      </div>
      <div className="persona-form-row">
        <label>{t("persona.soul")}</label>
        <textarea
          value={profile.soul ?? ""}
          onChange={(e) => onChange({ ...profile, soul: e.target.value })}
          className="persona-textarea"
          rows={4}
        />
      </div>
      <div className="persona-form-grid">
        <div className="persona-form-row">
          <label>{t("persona.model")}</label>
          <input
            value={profile.model ?? ""}
            onChange={(e) => onChange({ ...profile, model: e.target.value })}
            className="persona-input"
          />
        </div>
        <div className="persona-form-row">
          <label>{t("persona.language")}</label>
          <input
            value={profile.language ?? ""}
            onChange={(e) => onChange({ ...profile, language: e.target.value })}
            className="persona-input"
          />
        </div>
      </div>
      <div className="persona-form-actions">
        <button className="persona-btn persona-btn-primary" onClick={onSave} disabled={saving} type="button">
          {t("persona.save")}
        </button>
        <button className="persona-btn" onClick={onCancel} type="button">
          {t("persona.cancel")}
        </button>
      </div>
    </div>
  );
}
