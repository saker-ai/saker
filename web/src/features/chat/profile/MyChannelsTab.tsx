import { useState, useCallback, useEffect } from "react";
import { Radio, ChevronDown, ChevronRight, Trash2, Eye, EyeOff } from "lucide-react";
import type { ChannelsListResult, ChannelInfo, PersonaProfile } from "@/features/rpc/types";
import type { RPCClient } from "@/features/rpc/client";
import { useT } from "@/features/i18n";
import { useToast, Toast, ConfirmDialog } from "../settings/shared";

interface Props {
  rpc: RPCClient | null;
}

export function MyChannelsTab({ rpc }: Props) {
  const { t } = useT();
  const { toast, showToast } = useToast();
  const [channels, setChannels] = useState<ChannelInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [expandedPlatform, setExpandedPlatform] = useState<string | null>(null);
  const [editingPlatform, setEditingPlatform] = useState<string | null>(null);
  const [editValues, setEditValues] = useState<Record<string, string>>({});
  const [personas, setPersonas] = useState<Record<string, PersonaProfile>>({});
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [secretVisible, setSecretVisible] = useState<Record<string, boolean>>({});

  const load = useCallback(async () => {
    if (!rpc) {
      setLoading(false);
      return;
    }
    try {
      const res = await rpc.request<ChannelsListResult>("channels/list");
      setChannels(res.channels || []);
      const pRes = await rpc.request<{ profiles: Record<string, PersonaProfile> }>("persona/list");
      setPersonas(pRes.profiles || {});
    } catch {
      showToast(t("settings.saveFailed"), "error");
    } finally {
      setLoading(false);
    }
  }, [rpc, showToast, t]);

  useEffect(() => { load(); }, [load]);

  if (loading) {
    return <div className="settings-empty">{t("settings.loading")}</div>;
  }

  const sortedChannels = [...channels].sort((a, b) => {
    if (a.configured && !b.configured) return -1;
    if (!a.configured && b.configured) return 1;
    return a.name.localeCompare(b.name);
  });

  const startEdit = (ch: ChannelInfo) => {
    setEditingPlatform(ch.platform);
    setExpandedPlatform(ch.platform);
    const vals: Record<string, string> = {};
    for (const f of ch.fields) {
      vals[f.key] = "";
    }
    setEditValues(vals);
    setSecretVisible({});
  };

  const handleSave = async (platform: string) => {
    if (!rpc || saving) return;
    setSaving(true);
    try {
      await rpc.request("channels/save", { platform, credentials: editValues });
      showToast(t("settings.saved"), "success");
      setEditingPlatform(null);
      await load();
    } catch {
      showToast(t("settings.saveFailed"), "error");
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async (platform: string) => {
    if (!rpc || saving) return;
    setSaving(true);
    try {
      await rpc.request("channels/delete", { platform });
      showToast(t("settings.saved"), "success");
      if (expandedPlatform === platform) setExpandedPlatform(null);
      await load();
    } catch {
      showToast(t("settings.saveFailed"), "error");
    } finally {
      setSaving(false);
      setConfirmDelete(null);
    }
  };

  const handleToggle = async (platform: string, enabled: boolean) => {
    if (!rpc || saving) return;
    setSaving(true);
    try {
      await rpc.request("channels/toggle", { platform, enabled });
      showToast(t("settings.saved"), "success");
      await load();
    } catch {
      showToast(t("settings.saveFailed"), "error");
    } finally {
      setSaving(false);
    }
  };

  const handleRouteChange = async (channel: string, persona: string) => {
    if (!rpc || saving) return;
    setSaving(true);
    try {
      await rpc.request("channels/route-set", { channel, persona });
      showToast(t("settings.saved"), "success");
      await load();
    } catch {
      showToast(t("settings.saveFailed"), "error");
    } finally {
      setSaving(false);
    }
  };

  const personaIds = Object.keys(personas).sort();

  return (
    <div className="settings-tab-stack">
      <Toast msg={toast} />

      <ConfirmDialog
        open={confirmDelete !== null}
        title={t("channels.confirmDeleteTitle")}
        message={t("channels.confirmDelete")}
        confirmLabel={t("channels.confirm")}
        cancelLabel={t("channels.cancel")}
        danger
        onConfirm={() => confirmDelete && handleDelete(confirmDelete)}
        onCancel={() => setConfirmDelete(null)}
      />

      <div className="settings-card-v2">
        <div className="settings-card-v2-header">
          <Radio size={18} />
          <span>{t("channels.title")}</span>
        </div>
        <div className="settings-card-v2-body">
          <p className="persona-subtitle">{t("channels.subtitle")}</p>
        </div>
      </div>

      {sortedChannels.map((ch) => {
        const isExpanded = expandedPlatform === ch.platform;
        const isEditing = editingPlatform === ch.platform;

        return (
          <div key={ch.platform} className={`settings-card-v2 channel-card ${ch.configured ? "channel-configured" : ""}`}>
            <div
              className="settings-card-v2-header channel-card-header"
              onClick={() => {
                if (isExpanded) {
                  setExpandedPlatform(null);
                  if (isEditing) setEditingPlatform(null);
                } else {
                  setExpandedPlatform(ch.platform);
                }
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter" || e.key === " ") {
                  e.preventDefault();
                  if (isExpanded) {
                    setExpandedPlatform(null);
                    if (isEditing) setEditingPlatform(null);
                  } else {
                    setExpandedPlatform(ch.platform);
                  }
                }
              }}
              role="button"
              tabIndex={0}
            >
              <span className="persona-card-toggle">
                {isExpanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
              </span>
              <span className="channel-card-name">{ch.name}</span>
              {ch.configured ? (
                <span className={`channel-status-badge ${ch.enabled ? "enabled" : "disabled"}`}>
                  {ch.enabled ? t("channels.enabled") : t("channels.disabled")}
                </span>
              ) : (
                <span className="channel-status-badge unconfigured">
                  {t("channels.notConfigured")}
                </span>
              )}
              {ch.route && (
                <span className="persona-card-badge">{ch.route}</span>
              )}
              {ch.configured && (
                <span className="persona-card-actions" onClick={(e) => e.stopPropagation()}>
                  <button
                    className={`persona-icon-btn ${ch.enabled ? "active" : ""}`}
                    onClick={() => handleToggle(ch.platform, !ch.enabled)}
                    aria-label={ch.enabled ? t("channels.disabled") : t("channels.enabled")}
                    aria-pressed={ch.enabled}
                    disabled={saving}
                    type="button"
                  >
                    {ch.enabled ? <Eye size={14} /> : <EyeOff size={14} />}
                  </button>
                  <button
                    className="persona-icon-btn danger"
                    onClick={() => setConfirmDelete(ch.platform)}
                    aria-label={`${t("channels.delete")} ${ch.name}`}
                    disabled={saving}
                    type="button"
                  >
                    <Trash2 size={14} />
                  </button>
                </span>
              )}
            </div>

            {isExpanded && (
              <div className="settings-card-v2-body">
                {isEditing ? (
                  <div className="persona-form">
                    {ch.fields.map((f) => (
                      <div key={f.key} className="persona-form-row">
                        <label>{f.label || f.key}</label>
                        <div className={f.secret ? "settings-password-wrap" : ""}>
                          <input
                            type={f.secret && !secretVisible[f.key] ? "password" : "text"}
                            value={editValues[f.key] ?? ""}
                            onChange={(e) => setEditValues({ ...editValues, [f.key]: e.target.value })}
                            placeholder={ch.values[f.key] || f.label || f.key}
                            className={f.secret ? "settings-input" : "persona-input"}
                            autoComplete={f.secret ? "off" : undefined}
                          />
                          {f.secret && (
                            <button
                              type="button"
                              className="settings-password-toggle"
                              onClick={() => setSecretVisible({ ...secretVisible, [f.key]: !secretVisible[f.key] })}
                              aria-label={secretVisible[f.key] ? "Hide" : "Show"}
                            >
                              {secretVisible[f.key] ? <EyeOff size={16} /> : <Eye size={16} />}
                            </button>
                          )}
                        </div>
                      </div>
                    ))}
                    <div className="persona-form-actions">
                      <button
                        className="persona-btn persona-btn-primary"
                        onClick={() => handleSave(ch.platform)}
                        disabled={saving}
                        type="button"
                      >
                        {t("channels.save")}
                      </button>
                      <button
                        className="persona-btn"
                        onClick={() => setEditingPlatform(null)}
                        type="button"
                      >
                        {t("channels.cancel")}
                      </button>
                    </div>
                  </div>
                ) : (
                  <div className="persona-detail">
                    {ch.configured && (
                      <div className="channel-values">
                        {ch.fields.map((f) => (
                          ch.values[f.key] && (
                            <div key={f.key} className="persona-detail-row">
                              <label>{f.label || f.key}</label>
                              <span>{ch.values[f.key]}</span>
                            </div>
                          )
                        ))}
                      </div>
                    )}

                    {ch.configured && personaIds.length > 0 && (
                      <div className="persona-form-row" style={{ marginTop: 8 }}>
                        <label>{t("channels.routePersona")}</label>
                        <select
                          value={ch.route || ""}
                          onChange={(e) => handleRouteChange(ch.platform, e.target.value)}
                          className="persona-input"
                          disabled={saving}
                        >
                          <option value="">{t("channels.noRoute")}</option>
                          {personaIds.map((pid) => (
                            <option key={pid} value={pid}>
                              {personas[pid]?.emoji || ""} {personas[pid]?.name || pid}
                            </option>
                          ))}
                        </select>
                      </div>
                    )}

                    <button
                      className="persona-edit-btn"
                      onClick={() => startEdit(ch)}
                      type="button"
                    >
                      {t("channels.edit")}
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
