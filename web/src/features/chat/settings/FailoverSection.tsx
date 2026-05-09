import { useState, useCallback, useEffect } from "react";
import { Plus, X, Save, ChevronDown, ChevronRight } from "lucide-react";
import type { FailoverConfig, FailoverModelEntry } from "@/features/rpc/types";
import { useT } from "@/features/i18n";
import { maskKey } from "./shared";

const FAILOVER_PROVIDERS = ["anthropic", "openai", "dashscope"] as const;

function FailoverModelCard({
  entry,
  index,
  canEdit,
  onUpdate,
  onRemove,
}: {
  entry: FailoverModelEntry;
  index: number;
  canEdit: boolean;
  onUpdate: (idx: number, field: keyof FailoverModelEntry, value: string) => void;
  onRemove: (idx: number) => void;
}) {
  const { t } = useT();
  const [expanded, setExpanded] = useState(false);

  return (
    <div className="settings-provider-card failover-model-card">
      <div className="provider-card-header" onClick={() => setExpanded(!expanded)} onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); setExpanded(!expanded); } }} role="button" tabIndex={0}>
        <span className={`settings-provider-card-status ${entry.model ? "configured" : ""}`} />
        <span className="provider-card-name">{entry.provider}/{entry.model || "..."}</span>
        {entry.apiKey && <span className="provider-card-stat">{maskKey(entry.apiKey)}</span>}
        <span className="provider-card-chevron">
          {expanded ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
        </span>
      </div>

      {expanded && (
        <div className="provider-card-body">
          {canEdit ? (
            <>
              <div className="settings-field-row">
                <label className="settings-field-label">{t("settings.failoverProvider")}</label>
                <select className="settings-select" value={entry.provider} onChange={(e) => onUpdate(index, "provider", e.target.value)}>
                  {FAILOVER_PROVIDERS.map((p) => (
                    <option key={p} value={p}>{p}</option>
                  ))}
                </select>
              </div>
              <div className="settings-field-row">
                <label className="settings-field-label">{t("settings.failoverModel")}</label>
                <input
                  className="settings-input"
                  type="text"
                  value={entry.model}
                  placeholder={t("settings.failoverModelPlaceholder")}
                  onChange={(e) => onUpdate(index, "model", e.target.value)}
                />
              </div>
              <div className="settings-field-row">
                <label className="settings-field-label">{t("settings.apiKey")}</label>
                <input
                  className="settings-input"
                  type="password"
                  value={entry.apiKey ?? ""}
                  placeholder={t("settings.apiKeyPlaceholder")}
                  onChange={(e) => onUpdate(index, "apiKey", e.target.value)}
                />
              </div>
              <div className="settings-field-row">
                <label className="settings-field-label">{t("settings.baseUrl")}</label>
                <input
                  className="settings-input"
                  type="url"
                  value={entry.baseUrl ?? ""}
                  placeholder={t("settings.baseUrlPlaceholder")}
                  onChange={(e) => onUpdate(index, "baseUrl", e.target.value)}
                />
              </div>
              <div className="provider-card-footer">
                <button className="settings-btn-remove" onClick={() => onRemove(index)} title={t("settings.failoverRemoveModel")}>
                  <X size={14} /> {t("settings.failoverRemoveModel")}
                </button>
              </div>
            </>
          ) : (
            <div className="provider-card-readonly">
              <div className="settings-field-row">
                <span className="settings-field-label">{t("settings.failoverProvider")}</span>
                <span className="settings-value">{entry.provider}</span>
              </div>
              <div className="settings-field-row">
                <span className="settings-field-label">{t("settings.failoverModel")}</span>
                <span className="settings-value">{entry.model}</span>
              </div>
              {entry.apiKey && (
                <div className="settings-field-row">
                  <span className="settings-field-label">{t("settings.apiKey")}</span>
                  <span className="settings-value">{maskKey(entry.apiKey)}</span>
                </div>
              )}
              {entry.baseUrl && (
                <div className="settings-field-row">
                  <span className="settings-field-label">{t("settings.baseUrl")}</span>
                  <span className="settings-value">{entry.baseUrl}</span>
                </div>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

export function FailoverSection({
  config,
  onSave,
  showToast,
}: {
  config?: FailoverConfig;
  onSave?: (failover: FailoverConfig) => Promise<void>;
  showToast?: (text: string, type: "success" | "error") => void;
}) {
  const { t, locale } = useT();
  const [saving, setSaving] = useState(false);
  const [dirty, setDirty] = useState(false);

  const [enabled, setEnabled] = useState(() => config?.enabled ?? false);
  const [models, setModels] = useState<FailoverModelEntry[]>(() => config?.models ?? []);
  const [maxRetries, setMaxRetries] = useState(() => config?.maxRetries ?? 2);

  // Sync from config when it changes externally
  useEffect(() => {
    setEnabled(config?.enabled ?? false);
    setModels(config?.models ?? []);
    setMaxRetries(config?.maxRetries ?? 2);
    setDirty(false);
  }, [config]);

  const canEdit = !!onSave;

  const handleSave = async () => {
    if (!onSave) return;
    setSaving(true);
    try {
      await onSave({ enabled, models, maxRetries });
      setDirty(false);
      showToast?.(t("settings.saved"), "success");
    } catch (e) {
      showToast?.(String(e), "error");
    } finally {
      setSaving(false);
    }
  };

  const toggleEnabled = () => {
    if (enabled) {
      const msg = locale === "zh"
        ? "确定要禁用故障转移吗？主模型失败时将不会自动切换备用模型。"
        : "Disable failover? The system will not fall back to alternate models when the primary fails.";
      if (!window.confirm(msg)) return;
    }
    setEnabled(!enabled);
    setDirty(true);
  };

  const addModel = () => {
    setModels((m) => [...m, { provider: "anthropic", model: "" }]);
    setDirty(true);
  };

  const removeModel = (idx: number) => {
    setModels((m) => m.filter((_, i) => i !== idx));
    setDirty(true);
  };

  const updateModel = (idx: number, field: keyof FailoverModelEntry, value: string) => {
    setModels((m) => m.map((entry, i) => i === idx ? { ...entry, [field]: value } : entry));
    setDirty(true);
  };

  return (
    <div className="settings-card" id="failover" data-section="failover">
      <div className="settings-card-title">
        <span>{t("settings.failoverTitle")}</span>
      </div>
      <p className="settings-card-desc">{t("settings.failoverDesc")}</p>

      {/* Enable toggle + max retries */}
      <div className="failover-controls">
        <div className="settings-row">
          <span className="settings-label">{t("settings.failoverEnabled")}</span>
          <label className="settings-toggle">
            <input type="checkbox" checked={enabled} onChange={toggleEnabled} disabled={!canEdit} />
            <span className="settings-toggle-slider" />
            <span className="settings-toggle-label">{enabled ? t("settings.enabled") : t("settings.disabled")}</span>
          </label>
        </div>
        {canEdit && (
          <div className="settings-row">
            <span className="settings-label">{t("settings.failoverMaxRetries")}</span>
            <input
              type="number"
              min={0}
              max={10}
              value={maxRetries}
              onChange={(e) => { setMaxRetries(parseInt(e.target.value) || 0); setDirty(true); }}
              className="settings-input settings-input-narrow"
            />
          </div>
        )}
      </div>

      {/* Empty state */}
      {models.length === 0 && canEdit && (
        <div className="settings-empty-hint">
          {locale === "zh"
            ? "暂无备用模型。添加备用模型以在主模型失败时自动切换。"
            : "No fallback models configured. Add models to enable automatic failover."}
        </div>
      )}

      {/* Model cards */}
      {models.length > 0 && (
        <div className="settings-failover-models">
          {models.map((m, idx) => (
            <FailoverModelCard
              key={idx}
              entry={m}
              index={idx}
              canEdit={canEdit}
              onUpdate={updateModel}
              onRemove={removeModel}
            />
          ))}
        </div>
      )}

      {/* Add + Save */}
      {canEdit && (
        <div className="provider-card-footer">
          <button className="settings-btn-add" onClick={addModel}>
            <Plus size={14} /> {t("settings.failoverAddModel")}
          </button>
          {dirty && (
            <button className="settings-btn-save" onClick={handleSave} disabled={saving}>
              <Save size={14} /> {saving ? t("settings.saving") : t("settings.save")}
            </button>
          )}
        </div>
      )}
    </div>
  );
}
