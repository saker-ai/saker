import { useState, useCallback, useEffect } from "react";
import { Users, Plus, Trash2, ChevronDown, ChevronRight, Star, Sparkles } from "lucide-react";
import type { PersonaProfile, PersonasConfig } from "@/features/rpc/types";
import type { RPCClient } from "@/features/rpc/client";
import { useT, type TKey } from "@/features/i18n";
import { useToast, Toast } from "./shared";

interface Props {
  rpc: RPCClient | null;
}

interface EditingPersona {
  id: string;
  profile: PersonaProfile;
  isNew: boolean;
}

export function PersonaSection({ rpc }: Props) {
  const { t } = useT();
  const { toast, showToast } = useToast();
  const [config, setConfig] = useState<PersonasConfig | null>(null);
  const [loading, setLoading] = useState(true);
  const [editing, setEditing] = useState<EditingPersona | null>(null);
  const [expandedId, setExpandedId] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!rpc) return;
    try {
      const res = await rpc.request("persona/list", {});
      setConfig(res as PersonasConfig);
    } catch {
      showToast(t("settings.saveFailed"));
    } finally {
      setLoading(false);
    }
  }, [rpc, showToast, t]);

  // Load on mount.
  useEffect(() => { load(); }, [load]);

  const profiles = config?.profiles ?? {};
  const defaultId = config?.default ?? "";
  const ids = Object.keys(profiles).sort();

  const handleSave = async () => {
    if (!rpc || !editing) return;
    const { id, profile } = editing;
    if (!id.trim()) return;
    try {
      await rpc.request("persona/save", { id, profile });
      showToast(t("settings.saved"));
      setEditing(null);
      await load();
    } catch {
      showToast(t("settings.saveFailed"));
    }
  };

  const handleDelete = async (id: string) => {
    if (!rpc || !confirm(t("persona.confirmDelete"))) return;
    try {
      await rpc.request("persona/delete", { id });
      showToast(t("settings.saved"));
      if (expandedId === id) setExpandedId(null);
      if (editing?.id === id) setEditing(null);
      await load();
    } catch {
      showToast(t("settings.saveFailed"));
    }
  };

  const handleSetDefault = async (id: string) => {
    if (!rpc) return;
    try {
      await rpc.request("persona/set-default", { id: defaultId === id ? "" : id });
      showToast(t("settings.saved"));
      await load();
    } catch {
      showToast(t("settings.saveFailed"));
    }
  };

  const startNew = () => {
    setEditing({
      id: "",
      profile: { name: "", emoji: "", soul: "" },
      isNew: true,
    });
    setExpandedId(null);
  };

  const startEdit = (id: string) => {
    setEditing({
      id,
      profile: { ...profiles[id] },
      isNew: false,
    });
    setExpandedId(id);
  };

  const updateField = (field: keyof PersonaProfile, value: string) => {
    if (!editing) return;
    setEditing({
      ...editing,
      profile: { ...editing.profile, [field]: value },
    });
  };

  if (loading) {
    return <div className="settings-empty">{t("settings.loading")}</div>;
  }

  return (
    <div className="settings-tab-stack">
      <Toast msg={toast} />

      {/* Header */}
      <div className="settings-card-v2">
        <div className="settings-card-v2-header">
          <Users size={18} />
          <span>{t("persona.title")}</span>
          <button className="persona-add-btn" onClick={startNew} type="button">
            <Plus size={14} />
            <span>{t("persona.addNew")}</span>
          </button>
        </div>
        <div className="settings-card-v2-body">
          <p className="persona-subtitle">{t("persona.subtitle")}</p>
        </div>
      </div>

      {/* New persona form */}
      {editing?.isNew && (
        <div className="settings-card-v2 persona-editor-card">
          <div className="settings-card-v2-header">
            <Sparkles size={16} />
            <span>{t("persona.addNew")}</span>
          </div>
          <div className="settings-card-v2-body">
            <PersonaForm
              editing={editing}
              onUpdateId={(id) => setEditing({ ...editing, id })}
              onUpdateField={updateField}
              onSave={handleSave}
              onCancel={() => setEditing(null)}
              existingIds={ids}
              t={t}
            />
          </div>
        </div>
      )}

      {/* Persona list */}
      {ids.length === 0 && !editing?.isNew && (
        <div className="settings-card-v2">
          <div className="settings-card-v2-body">
            <p className="persona-empty">{t("persona.noPersonas")}</p>
          </div>
        </div>
      )}

      {ids.map((id) => {
        const p = profiles[id];
        const isExpanded = expandedId === id;
        const isEditing = editing?.id === id && !editing.isNew;
        const isDefault = defaultId === id;

        return (
          <div key={id} className={`settings-card-v2 persona-card ${isDefault ? "persona-default" : ""}`}>
            <div
              className="settings-card-v2-header persona-card-header"
              onClick={() => {
                if (isExpanded) {
                  setExpandedId(null);
                  if (isEditing) setEditing(null);
                } else {
                  setExpandedId(id);
                }
              }}
              role="button"
              tabIndex={0}
            >
              <span className="persona-card-toggle">
                {isExpanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
              </span>
              <span className="persona-card-emoji">{p.emoji || "🤖"}</span>
              <span className="persona-card-name">{p.name || id}</span>
              {p.description && <span className="persona-card-desc">— {p.description}</span>}
              {p.model && <span className="persona-card-badge">{p.model}</span>}
              {isDefault && <span className="persona-card-default-badge">{t("persona.isDefault")}</span>}
              <span className="persona-card-actions" onClick={(e) => e.stopPropagation()}>
                <button
                  className={`persona-icon-btn ${isDefault ? "active" : ""}`}
                  onClick={() => handleSetDefault(id)}
                  title={t("persona.setDefault")}
                  type="button"
                >
                  <Star size={14} />
                </button>
                <button
                  className="persona-icon-btn danger"
                  onClick={() => handleDelete(id)}
                  title={t("persona.delete")}
                  type="button"
                >
                  <Trash2 size={14} />
                </button>
              </span>
            </div>

            {isExpanded && (
              <div className="settings-card-v2-body">
                {isEditing ? (
                  <PersonaForm
                    editing={editing}
                    onUpdateField={updateField}
                    onSave={handleSave}
                    onCancel={() => { setEditing(null); setExpandedId(null); }}
                    existingIds={ids}
                    t={t}
                  />
                ) : (
                  <div className="persona-detail">
                    {p.soul && (
                      <div className="persona-detail-section">
                        <label>{t("persona.soul")}</label>
                        <pre className="persona-soul-preview">{p.soul}</pre>
                      </div>
                    )}
                    {p.language && <div className="persona-detail-row"><label>{t("persona.language")}</label><span>{p.language}</span></div>}
                    {p.inherit && <div className="persona-detail-row"><label>{t("persona.inherit")}</label><span>{p.inherit}</span></div>}
                    <button className="persona-edit-btn" onClick={() => startEdit(id)} type="button">
                      {t("persona.edit")}
                    </button>
                  </div>
                )}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}

// --- Form ---

function PersonaForm({
  editing,
  onUpdateId,
  onUpdateField,
  onSave,
  onCancel,
  existingIds,
  t,
}: {
  editing: EditingPersona;
  onUpdateId?: (id: string) => void;
  onUpdateField: (field: keyof PersonaProfile, value: string) => void;
  onSave: () => void;
  onCancel: () => void;
  existingIds: string[];
  t: (key: TKey) => string;
}) {
  const { id, profile, isNew } = editing;

  return (
    <div className="persona-form">
      {isNew && (
        <div className="persona-form-row">
          <label>{t("persona.id")}</label>
          <input
            value={id}
            onChange={(e) => onUpdateId?.(e.target.value.replace(/[^a-z0-9_-]/gi, "").toLowerCase())}
            placeholder="my-persona"
            className="persona-input"
          />
        </div>
      )}
      <div className="persona-form-grid">
        <div className="persona-form-row">
          <label>{t("persona.emoji")}</label>
          <input
            value={profile.emoji ?? ""}
            onChange={(e) => onUpdateField("emoji", e.target.value)}
            placeholder="🤖"
            className="persona-input persona-input-sm"
          />
        </div>
        <div className="persona-form-row">
          <label>{t("persona.name")}</label>
          <input
            value={profile.name ?? ""}
            onChange={(e) => onUpdateField("name", e.target.value)}
            placeholder="Aria"
            className="persona-input"
          />
        </div>
      </div>
      <div className="persona-form-row">
        <label>{t("persona.description")}</label>
        <input
          value={profile.description ?? ""}
          onChange={(e) => onUpdateField("description", e.target.value)}
          placeholder="A creative and warm AI assistant"
          className="persona-input"
        />
      </div>
      <div className="persona-form-row">
        <label>{t("persona.soul")}</label>
        <textarea
          value={profile.soul ?? ""}
          onChange={(e) => onUpdateField("soul", e.target.value)}
          placeholder={t("persona.soulPlaceholder")}
          className="persona-textarea"
          rows={5}
        />
      </div>
      <div className="persona-form-grid">
        <div className="persona-form-row">
          <label>{t("persona.model")}</label>
          <input
            value={profile.model ?? ""}
            onChange={(e) => onUpdateField("model", e.target.value)}
            placeholder="claude-sonnet-4-5"
            className="persona-input"
          />
        </div>
        <div className="persona-form-row">
          <label>{t("persona.language")}</label>
          <input
            value={profile.language ?? ""}
            onChange={(e) => onUpdateField("language", e.target.value)}
            placeholder="Chinese"
            className="persona-input"
          />
        </div>
      </div>
      {existingIds.length > 0 && (
        <div className="persona-form-row">
          <label>{t("persona.inherit")}</label>
          <select
            value={profile.inherit ?? ""}
            onChange={(e) => onUpdateField("inherit", e.target.value)}
            className="persona-input"
          >
            <option value="">{t("persona.none")}</option>
            {existingIds.filter((i) => i !== id).map((i) => (
              <option key={i} value={i}>{i}</option>
            ))}
          </select>
        </div>
      )}
      <div className="persona-form-actions">
        <button className="persona-btn persona-btn-primary" onClick={onSave} disabled={isNew && !id.trim()} type="button">
          {t("persona.save")}
        </button>
        <button className="persona-btn" onClick={onCancel} type="button">
          {t("persona.cancel")}
        </button>
      </div>
    </div>
  );
}
