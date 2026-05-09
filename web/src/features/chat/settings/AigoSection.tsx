import { useState, useCallback, useEffect, useRef } from "react";
import type { AigoConfig, AigoProvider, AigoProviderInfo, ProviderStatus } from "@/features/rpc/types";
import { useRpcStore } from "@/features/rpc/rpcStore";
import { useT, type Locale } from "@/features/i18n";
import { maskKey } from "./shared";
import DOMPurify from "dompurify";
import { X, Save, Pencil } from "lucide-react";

function displayName(info: AigoProviderInfo | undefined, locale: Locale): string {
  if (info?.displayName) {
    return locale === "zh" ? info.displayName.zh : info.displayName.en;
  }
  return info?.name ?? "";
}

function allModelsForProvider(info: AigoProviderInfo | undefined): string[] {
  if (!info?.models) return [];
  const seen = new Set<string>();
  for (const models of Object.values(info.models)) {
    for (const ref of models) {
      const slash = ref.indexOf("/");
      const model = slash >= 0 ? ref.slice(slash + 1) : ref;
      seen.add(model);
    }
  }
  return Array.from(seen);
}

/** Get capability labels for a specific model across all capabilities */
function modelCapabilities(info: AigoProviderInfo | undefined, model: string): string[] {
  if (!info?.models) return [];
  const caps: string[] = [];
  for (const [cap, refs] of Object.entries(info.models)) {
    for (const ref of refs) {
      const slash = ref.indexOf("/");
      const m = slash >= 0 ? ref.slice(slash + 1) : ref;
      if (m === model) { caps.push(cap); break; }
    }
  }
  return caps;
}

// --- Provider Edit Modal ---

function ProviderEditModal({
  name,
  provider,
  schema,
  locale,
  onClose,
  onSave,
}: {
  name: string;
  provider: AigoProvider;
  schema: AigoProviderInfo | undefined;
  locale: Locale;
  onClose: () => void;
  onSave: (updated: AigoProvider) => void;
}) {
  const { t } = useT();
  const [draft, setDraft] = useState<AigoProvider>({ ...provider });
  const [dirty, setDirty] = useState(false);
  const modalRef = useRef<HTMLDivElement>(null);
  const [closing, setClosing] = useState(false);

  const handleClose = useCallback(() => {
    setClosing(true);
    setTimeout(() => onClose(), 150);
  }, [onClose]);

  // Autofocus first input on open + Escape to close
  useEffect(() => {
    const firstInput = modalRef.current?.querySelector<HTMLInputElement>(
      'input:not([type="checkbox"]):not([type="hidden"])'
    );
    if (firstInput) firstInput.focus();
    else modalRef.current?.focus();
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") handleClose();
    };
    document.addEventListener("keydown", handleKey);
    return () => document.removeEventListener("keydown", handleKey);
  }, [handleClose]);

  const models = allModelsForProvider(schema);
  const dn = displayName(schema, locale) || name;
  const isEnabled = draft.enabled !== false;

  const update = (field: string, value: string) => {
    const knownFields: (keyof AigoProvider)[] = ["apiKey", "baseUrl"];
    if (knownFields.includes(field as keyof AigoProvider)) {
      setDraft((d) => ({ ...d, [field]: value }));
    } else {
      setDraft((d) => ({
        ...d,
        metadata: { ...(d.metadata ?? {}), [field]: value },
      }));
    }
    setDirty(true);
  };

  const toggleEnabled = () => {
    if (isEnabled) {
      const msg = locale === "zh"
        ? `确定要禁用 ${dn} 吗？使用该供应商的路由将停止工作。`
        : `Disable ${dn}? Active routes using this provider will stop working.`;
      if (!window.confirm(msg)) return;
    }
    setDraft((d) => ({ ...d, enabled: d.enabled === false }));
    setDirty(true);
  };

  const toggleModelDisabled = (model: string) => {
    setDraft((d) => {
      const disabled = d.disabledModels ?? [];
      const isDisabled = disabled.includes(model);
      return {
        ...d,
        disabledModels: isDisabled
          ? disabled.filter((m) => m !== model)
          : [...disabled, model],
      };
    });
    setDirty(true);
  };

  /** Build placeholder: prefer env var hint, then default, then description */
  const fieldPlaceholder = (f: { envVar?: string; default?: string; description?: string; label: string }) => {
    if (f.envVar) return `\${${f.envVar}}`;
    if (f.default) return f.default;
    return f.description ?? f.label;
  };

  return (
    <div className={`provider-modal-overlay${closing ? " closing" : ""}`} onClick={handleClose}>
      <div className="provider-modal" ref={modalRef} tabIndex={-1} onClick={(e) => e.stopPropagation()}>
        {/* Modal header */}
        <div className="provider-modal-header">
          <span className={`settings-provider-card-status ${draft.apiKey ? "configured" : ""}`} />
          <span className="provider-modal-title">{dn}</span>
          <span className="provider-card-type-badge">{draft.type}</span>
          <div className="provider-modal-header-actions">
            <label className="settings-toggle" title={isEnabled ? t("settings.disableProvider") : t("settings.enableProvider")}>
              <input type="checkbox" checked={isEnabled} onChange={toggleEnabled} />
              <span className="settings-toggle-slider" />
            </label>
            <button className="provider-modal-close" onClick={handleClose} aria-label="Close">
              <X size={18} />
            </button>
          </div>
        </div>

        {/* Config fields */}
        <div className="provider-modal-body">
          <div className="provider-modal-section">
            <div className="provider-modal-section-title">{t("settings.configure")}</div>

            {schema ? (
              schema.fields.map((f) => {
                const rawVal = f.key === "apiKey" ? (draft.apiKey ?? "")
                  : f.key === "baseUrl" ? (draft.baseUrl ?? "")
                  : (draft.metadata?.[f.key] ?? "");
                const isSecret = f.type === "secret";
                const showMasked = isSecret && !!rawVal;
                return (
                  <div key={f.key} className="settings-field-row">
                    <label className="settings-field-label" title={f.description}>
                      {f.label}{f.required ? " *" : ""}{f.envVar ? ` (${f.envVar})` : ""}
                    </label>
                    {showMasked ? (
                      <div className="settings-input-masked">
                        <span className="settings-masked-value">{maskKey(rawVal)}</span>
                        <button
                          className="settings-btn-clear"
                          onClick={() => update(f.key, "")}
                          title={locale === "zh" ? "清除" : "Clear"}
                        >
                          <X size={14} />
                        </button>
                      </div>
                    ) : (
                      <input
                        className="settings-input"
                        type={isSecret ? "password" : f.type === "url" ? "url" : "text"}
                        placeholder={fieldPlaceholder(f)}
                        value={rawVal}
                        onChange={(e) => update(f.key, e.target.value)}
                      />
                    )}
                  </div>
                );
              })
            ) : (
              <>
                <div className="settings-field-row">
                  <label className="settings-field-label">{t("settings.apiKey")}</label>
                  {draft.apiKey ? (
                    <div className="settings-input-masked">
                      <span className="settings-masked-value">{maskKey(draft.apiKey)}</span>
                      <button
                        className="settings-btn-clear"
                        onClick={() => update("apiKey", "")}
                        title={locale === "zh" ? "清除" : "Clear"}
                      >
                        <X size={14} />
                      </button>
                    </div>
                  ) : (
                    <input
                      className="settings-input"
                      type="password"
                      placeholder={t("settings.apiKeyPlaceholder")}
                      value=""
                      onChange={(e) => update("apiKey", e.target.value)}
                    />
                  )}
                </div>
                <div className="settings-field-row">
                  <label className="settings-field-label">{t("settings.baseUrl")}</label>
                  <input
                    className="settings-input"
                    type="url"
                    placeholder={t("settings.baseUrlPlaceholder")}
                    value={draft.baseUrl ?? ""}
                    onChange={(e) => update("baseUrl", e.target.value)}
                  />
                </div>
              </>
            )}
          </div>

          {/* Models section */}
          {models.length > 0 && (
            <div className="provider-modal-section">
              <div className="provider-modal-section-title">
                {t("settings.models")} ({models.length})
                {(draft.disabledModels?.length ?? 0) > 0 && (
                  <span className="settings-disabled-count">
                    {draft.disabledModels!.length} {t("settings.modelsDisabled")}
                  </span>
                )}
              </div>
              <div className="provider-modal-model-list">
                {models.map((m) => {
                  const isModelDisabled = draft.disabledModels?.includes(m);
                  const caps = modelCapabilities(schema, m);
                  return (
                    <div key={m} className={`provider-modal-model-item${isModelDisabled ? " disabled" : ""}`}>
                      <label className="provider-modal-model-toggle">
                        <input
                          type="checkbox"
                          checked={!isModelDisabled}
                          onChange={() => toggleModelDisabled(m)}
                        />
                        <span className="provider-modal-model-name">{m}</span>
                      </label>
                      {caps.length > 0 && (
                        <div className="provider-modal-model-caps">
                          {caps.map((c) => (
                            <span key={c} className="provider-modal-cap-tag">{c}</span>
                          ))}
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>
            </div>
          )}
        </div>

        {/* Modal footer */}
        <div className="provider-modal-footer">
          <button className="settings-btn-save" onClick={() => { onSave(draft); handleClose(); }} disabled={!dirty}>
            <Save size={14} /> {t("settings.save")}
          </button>
        </div>
      </div>
    </div>
  );
}

// --- Main Section ---

export function AigoSection({
  config,
  onSave,
  showToast,
}: {
  config?: AigoConfig;
  onSave?: (aigo: AigoConfig) => Promise<void>;
  showToast?: (text: string, type: "success" | "error") => void;
}) {
  const { t, locale } = useT();
  const [saving, setSaving] = useState(false);

  const [availableModels, setAvailableModels] = useState<Record<string, Record<string, string[]>>>({});
  const [providers, setProviders] = useState<Record<string, AigoProvider>>(() => config?.providers ?? {});

  const [providerSchemas, setProviderSchemas] = useState<AigoProviderInfo[]>([]);
  const [connectivityMap, setConnectivityMap] = useState<Record<string, ProviderStatus>>({});
  const [editingProvider, setEditingProvider] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    setProviders(config?.providers ?? {});
  }, [config]);

  const fetchModels = useCallback(() => {
    const rpc = useRpcStore.getState().rpc;
    if (!rpc) { setLoading(false); return; }
    Promise.all([
      rpc.request<Record<string, Record<string, string[]>>>("aigo/models")
        .then(setAvailableModels)
        .catch(() => {}),
      rpc.request<AigoProviderInfo[]>("aigo/providers")
        .then(setProviderSchemas)
        .catch(() => {}),
      rpc.request<ProviderStatus[]>("aigo/status")
        .then((statuses) => {
          if (statuses) {
            const map: Record<string, ProviderStatus> = {};
            for (const s of statuses) map[s.name] = s;
            setConnectivityMap(map);
          }
        })
        .catch(() => {}),
    ]).finally(() => setLoading(false));
  }, []);

  useEffect(() => { fetchModels(); }, []); // eslint-disable-line react-hooks/exhaustive-deps

  // Refresh connectivity every 60s
  useEffect(() => {
    const timer = setInterval(() => {
      const rpc = useRpcStore.getState().rpc;
      if (!rpc) return;
      rpc.request<ProviderStatus[]>("aigo/status")
        .then((statuses) => {
          if (statuses) {
            const map: Record<string, ProviderStatus> = {};
            for (const s of statuses) map[s.name] = s;
            setConnectivityMap(map);
          }
        })
        .catch(() => {});
    }, 60_000);
    return () => clearInterval(timer);
  }, []);

  // Save full config
  const doSave = useCallback(async (overrideProviders?: Record<string, AigoProvider>) => {
    if (!onSave) return;
    setSaving(true);
    try {
      await onSave({ providers: overrideProviders ?? providers });
      showToast?.(t("settings.saved"), "success");
    } catch (e) {
      showToast?.(String(e), "error");
    } finally {
      setSaving(false);
    }
  }, [onSave, providers, showToast, t]);

  // Save a single provider update
  const saveProvider = useCallback((name: string, updated: AigoProvider) => {
    const next = { ...providers, [name]: updated };
    setProviders(next);
    doSave(next);
  }, [providers, doSave]);

  // Merge config + auto-discovered provider names, stable alphabetical sort
  const allProviderNames = Array.from(
    new Set([...Object.keys(providers), ...Object.keys(availableModels)])
  ).sort();

  const canEdit = !!onSave;

  return (
    <div className="settings-card" id="aigo" data-section="aigo">
      <div className="settings-card-title">
        <span>{t("settings.aigoTitle")}</span>
      </div>

      {loading ? (
        <div className="settings-aigo-providers">
          {[1, 2, 3].map((i) => (
            <div key={i} className="settings-provider-card skeleton" />
          ))}
        </div>
      ) : allProviderNames.length === 0 ? (
        <div className="settings-empty-hint" dangerouslySetInnerHTML={{ __html: DOMPurify.sanitize(t("settings.noProviders"), { ALLOWED_TAGS: ["strong"] }) }} />
      ) : (
        <div className="settings-aigo-providers">
          {allProviderNames.map((name) => {
            const p = providers[name];
            const schema = providerSchemas.find((s) => s.name === (p?.type ?? name));
            const dn = displayName(schema, locale) || name;
            const hasKey = !!p?.apiKey;
            const backendAvailable = !!availableModels[name];
            const statusClass = (hasKey || backendAvailable) ? "configured" : "";
            const isEnabled = p?.enabled !== false;
            const connectivity = connectivityMap[name];
            const models = allModelsForProvider(schema);
            const disabledCount = p?.disabledModels?.length ?? 0;

            const openProvider = () => {
              if (!canEdit) return;
              if (!p) {
                const newProv: AigoProvider = { type: name as AigoProvider["type"] };
                setProviders((prev) => ({ ...prev, [name]: newProv }));
              }
              setEditingProvider(name);
            };

            return (
              <div
                key={name}
                className={`settings-provider-card provider-type-${p?.type ?? name}${!isEnabled ? " disabled" : ""}`}
                onClick={openProvider}
                onKeyDown={(e) => {
                  if (e.key === "Enter" || e.key === " ") {
                    e.preventDefault();
                    openProvider();
                  }
                }}
                role={canEdit ? "button" : undefined}
                tabIndex={canEdit ? 0 : undefined}
              >
                <div className="settings-provider-card-header">
                  <span className={`settings-provider-card-status ${statusClass}`} />
                  <span className="settings-provider-card-name">{dn}</span>
                  {!isEnabled && <span className="settings-tag-disabled">{t("settings.providerDisabled")}</span>}
                </div>
                <div className="settings-provider-card-type">{p?.type ?? name}</div>
                <div className="settings-provider-card-meta">
                  {p?.apiKey && (
                    <span className="settings-provider-card-key">{maskKey(p.apiKey)}</span>
                  )}
                  <span className={`settings-provider-config-tag ${statusClass ? "configured" : "not-configured"}`}>
                    {statusClass ? t("settings.providerConfigured") : t("settings.providerNotConfigured")}
                  </span>
                  <span className="settings-provider-card-routes">
                    {models.length > 0 && <>{models.length} {t("settings.models").toLowerCase()}</>}
                  </span>
                </div>
                {connectivity && (
                  <div className={`settings-provider-connectivity ${connectivity.reachable ? "reachable" : "unreachable"}`}>
                    <span>{connectivity.reachable ? "✓" : "✗"}</span>
                    <span>{connectivity.reachable ? t("settings.reachable") : t("settings.unreachable")}</span>
                    {connectivity.baseUrl && <span className="settings-provider-baseurl" title={connectivity.baseUrl}>{connectivity.baseUrl}</span>}
                  </div>
                )}
                {disabledCount > 0 && (
                  <div className="settings-provider-card-disabled-hint">
                    {disabledCount} {t("settings.modelsDisabled")}
                  </div>
                )}
                {canEdit && (
                  <span className="settings-provider-card-edit" aria-label={t("settings.configure")}>
                    <Pencil size={14} />
                  </span>
                )}
              </div>
            );
          })}
        </div>
      )}

      {/* Edit Modal */}
      {editingProvider && providers[editingProvider] && (
        <ProviderEditModal
          name={editingProvider}
          provider={providers[editingProvider]}
          schema={providerSchemas.find((s) => s.name === providers[editingProvider].type)}
          locale={locale}
          onClose={() => setEditingProvider(null)}
          onSave={(updated) => saveProvider(editingProvider, updated)}
        />
      )}
    </div>
  );
}
