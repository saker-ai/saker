import { useState, useEffect, useCallback } from "react";
import { Save, HardDrive, Server, Cloud } from "lucide-react";
import { useT } from "@/features/i18n";
import { PasswordInput, maskKey } from "./shared";
import type {
  StorageConfig,
  StorageBackend,
  StorageEmbeddedConfig,
  StorageEmbeddedMode,
  StorageOSFSConfig,
  StorageS3Config,
} from "@/features/rpc/types";

// Default backend when settings.storage is empty — matches server defaults
// (storage.Open falls back to osfs at <dataDir>/media when Backend is "").
const DEFAULT_BACKEND: StorageBackend = "osfs";

// Build a draft we can mutate locally without aliasing the prop.
function cloneDraft(src: StorageConfig | undefined): StorageConfig {
  return {
    backend: src?.backend || DEFAULT_BACKEND,
    publicBaseURL: src?.publicBaseURL ?? "",
    tenantPrefix: src?.tenantPrefix ?? "",
    osfs: { root: src?.osfs?.root ?? "" },
    embedded: {
      mode: src?.embedded?.mode || "external",
      addr: src?.embedded?.addr ?? "",
      root: src?.embedded?.root ?? "",
      bucket: src?.embedded?.bucket ?? "",
      accessKey: src?.embedded?.accessKey ?? "",
      // SecretKey is NEVER sent back from the server (config strips it on
      // settings/get). We start blank — saving with an empty secret leaves
      // the existing value alone if the server treats "" as no-change. To
      // be safe, we only include the secretKey field in the patch when the
      // user actually typed something (see buildPatch).
      secretKey: "",
    },
    s3: {
      endpoint: src?.s3?.endpoint ?? "",
      region: src?.s3?.region ?? "",
      bucket: src?.s3?.bucket ?? "",
      accessKeyID: src?.s3?.accessKeyID ?? "",
      secretAccessKey: "",
      usePathStyle: src?.s3?.usePathStyle ?? false,
      publicBaseURL: src?.s3?.publicBaseURL ?? "",
    },
  };
}

// buildPatch trims empty optional fields so MergeSettings (server side) keeps
// the previous value instead of clobbering it with "". Only the actively
// selected backend's sub-block is sent — the unselected blocks stay nil so
// they don't override existing config.
function buildPatch(draft: StorageConfig, originalSecrets: { embedded: boolean; s3: boolean }): StorageConfig {
  const out: StorageConfig = { backend: draft.backend };
  if (draft.publicBaseURL) out.publicBaseURL = draft.publicBaseURL;
  if (draft.tenantPrefix) out.tenantPrefix = draft.tenantPrefix;
  switch (draft.backend) {
    case "osfs":
      out.osfs = { root: draft.osfs?.root || "" };
      break;
    case "embedded": {
      const e: StorageEmbeddedConfig = {
        mode: draft.embedded?.mode || "external",
      };
      if (draft.embedded?.addr) e.addr = draft.embedded.addr;
      if (draft.embedded?.root) e.root = draft.embedded.root;
      if (draft.embedded?.bucket) e.bucket = draft.embedded.bucket;
      if (draft.embedded?.accessKey) e.accessKey = draft.embedded.accessKey;
      // Only push secretKey when the user typed something; otherwise the
      // server keeps whatever it already has (server-side merge treats ""
      // as "no change" for scalar string fields).
      if (draft.embedded?.secretKey) e.secretKey = draft.embedded.secretKey;
      else if (!originalSecrets.embedded) e.secretKey = "";
      out.embedded = e;
      break;
    }
    case "s3": {
      const s: StorageS3Config = {};
      if (draft.s3?.endpoint) s.endpoint = draft.s3.endpoint;
      if (draft.s3?.region) s.region = draft.s3.region;
      if (draft.s3?.bucket) s.bucket = draft.s3.bucket;
      if (draft.s3?.accessKeyID) s.accessKeyID = draft.s3.accessKeyID;
      if (draft.s3?.secretAccessKey) s.secretAccessKey = draft.s3.secretAccessKey;
      else if (!originalSecrets.s3) s.secretAccessKey = "";
      if (draft.s3?.usePathStyle) s.usePathStyle = true;
      if (draft.s3?.publicBaseURL) s.publicBaseURL = draft.s3.publicBaseURL;
      out.s3 = s;
      break;
    }
  }
  return out;
}

export function StorageSection({
  config,
  onSave,
  showToast,
}: {
  config?: StorageConfig;
  onSave?: (storage: StorageConfig) => Promise<void>;
  showToast?: (text: string, type: "success" | "error") => void;
}) {
  const { t } = useT();
  const [draft, setDraft] = useState<StorageConfig>(() => cloneDraft(config));
  const [saving, setSaving] = useState(false);
  const [dirty, setDirty] = useState(false);

  // Track whether the server originally had a secret on file. We compare the
  // live patch against this so the "secret cleared by user" case can be
  // distinguished from "user never touched the field".
  const originalSecrets = {
    embedded: !!config?.embedded?.accessKey,
    s3: !!config?.s3?.accessKeyID,
  };

  useEffect(() => {
    setDraft(cloneDraft(config));
    setDirty(false);
  }, [config]);

  const updateBackend = (backend: StorageBackend) => {
    setDraft((d) => ({ ...d, backend }));
    setDirty(true);
  };

  const updateTopLevel = (key: "publicBaseURL" | "tenantPrefix", value: string) => {
    setDraft((d) => ({ ...d, [key]: value }));
    setDirty(true);
  };

  const updateOSFS = (patch: Partial<StorageOSFSConfig>) => {
    setDraft((d) => ({ ...d, osfs: { ...d.osfs, ...patch } }));
    setDirty(true);
  };

  const updateEmbedded = (patch: Partial<StorageEmbeddedConfig>) => {
    setDraft((d) => ({ ...d, embedded: { ...d.embedded, ...patch } }));
    setDirty(true);
  };

  const updateS3 = (patch: Partial<StorageS3Config>) => {
    setDraft((d) => ({ ...d, s3: { ...d.s3, ...patch } }));
    setDirty(true);
  };

  const handleSave = useCallback(async () => {
    if (!onSave || saving) return;
    setSaving(true);
    try {
      await onSave(buildPatch(draft, originalSecrets));
      showToast?.(t("settings.storage.savedHotApplied"), "success");
      setDirty(false);
    } catch (e) {
      showToast?.(`${t("settings.storage.saveFailed")}: ${String(e)}`, "error");
    } finally {
      setSaving(false);
    }
  }, [onSave, saving, draft, originalSecrets, showToast, t]);

  const canEdit = !!onSave;
  const backend = draft.backend || DEFAULT_BACKEND;

  return (
    <div className="settings-card" id="storage" data-section="storage">
      <div className="settings-card-title">
        <span>{t("settings.storage")}</span>
      </div>
      <div className="settings-hint">{t("settings.storage.hint")}</div>

      {/* Backend picker */}
      <div className="settings-row">
        <span className="settings-label">{t("settings.storage.backend")}</span>
        <div className="settings-theme-options">
          <BackendButton
            current={backend}
            value="osfs"
            label={t("settings.storage.backend.osfs")}
            icon={<HardDrive size={14} />}
            disabled={!canEdit}
            onClick={() => updateBackend("osfs")}
          />
          <BackendButton
            current={backend}
            value="embedded"
            label={t("settings.storage.backend.embedded")}
            icon={<Server size={14} />}
            disabled={!canEdit}
            onClick={() => updateBackend("embedded")}
          />
          <BackendButton
            current={backend}
            value="s3"
            label={t("settings.storage.backend.s3")}
            icon={<Cloud size={14} />}
            disabled={!canEdit}
            onClick={() => updateBackend("s3")}
          />
        </div>
      </div>
      {/* Plain muted description rather than another tinted callout — keeps the
       * card top from stacking two pill-shaped hint banners on top of each
       * other (the overall section hint already lives above). */}
      <p className="settings-muted storage-backend-desc">{backendDescription(backend, t)}</p>

      {/* Shared top-level fields */}
      <FieldRow label={t("settings.storage.publicBaseURL")}>
        <input
          className="settings-input"
          type="text"
          placeholder={t("settings.storage.publicBaseURL.placeholder")}
          value={draft.publicBaseURL ?? ""}
          disabled={!canEdit}
          onChange={(e) => updateTopLevel("publicBaseURL", e.target.value)}
        />
      </FieldRow>
      <FieldRow label={t("settings.storage.tenantPrefix")}>
        <input
          className="settings-input"
          type="text"
          placeholder={t("settings.storage.tenantPrefix.placeholder")}
          value={draft.tenantPrefix ?? ""}
          disabled={!canEdit}
          onChange={(e) => updateTopLevel("tenantPrefix", e.target.value)}
        />
      </FieldRow>

      {/* Backend-specific subform */}
      {backend === "osfs" && (
        <FieldRow label={t("settings.storage.osfs.root")}>
          <input
            className="settings-input"
            type="text"
            placeholder={t("settings.storage.osfs.root.placeholder")}
            value={draft.osfs?.root ?? ""}
            disabled={!canEdit}
            onChange={(e) => updateOSFS({ root: e.target.value })}
          />
        </FieldRow>
      )}

      {backend === "embedded" && (
        <>
          <FieldRow label={t("settings.storage.embedded.mode")}>
            <select
              className="settings-input"
              value={draft.embedded?.mode || "external"}
              disabled={!canEdit}
              onChange={(e) => updateEmbedded({ mode: e.target.value as StorageEmbeddedMode })}
            >
              <option value="external">{t("settings.storage.embedded.mode.external")}</option>
              <option value="standalone">{t("settings.storage.embedded.mode.standalone")}</option>
            </select>
          </FieldRow>
          {draft.embedded?.mode === "standalone" && (
            <FieldRow label={t("settings.storage.embedded.addr")}>
              <input
                className="settings-input"
                type="text"
                placeholder={t("settings.storage.embedded.addr.placeholder")}
                value={draft.embedded?.addr ?? ""}
                disabled={!canEdit}
                onChange={(e) => updateEmbedded({ addr: e.target.value })}
              />
            </FieldRow>
          )}
          <FieldRow label={t("settings.storage.embedded.root")}>
            <input
              className="settings-input"
              type="text"
              placeholder={t("settings.storage.embedded.root.placeholder")}
              value={draft.embedded?.root ?? ""}
              disabled={!canEdit}
              onChange={(e) => updateEmbedded({ root: e.target.value })}
            />
          </FieldRow>
          <FieldRow label={t("settings.storage.embedded.bucket")}>
            <input
              className="settings-input"
              type="text"
              placeholder={t("settings.storage.embedded.bucket.placeholder")}
              value={draft.embedded?.bucket ?? ""}
              disabled={!canEdit}
              onChange={(e) => updateEmbedded({ bucket: e.target.value })}
            />
          </FieldRow>
          <FieldRow label={t("settings.storage.embedded.accessKey")}>
            <input
              className="settings-input"
              type="text"
              value={draft.embedded?.accessKey ?? ""}
              disabled={!canEdit}
              onChange={(e) => updateEmbedded({ accessKey: e.target.value })}
            />
          </FieldRow>
          <FieldRow label={t("settings.storage.embedded.secretKey")}>
            <SecretField
              hasExisting={originalSecrets.embedded}
              maskedHint={config?.embedded?.accessKey ? maskKey(config.embedded.accessKey) : ""}
              value={draft.embedded?.secretKey ?? ""}
              disabled={!canEdit}
              placeholder={t("settings.storage.embedded.secretKey.placeholder")}
              onChange={(v) => updateEmbedded({ secretKey: v })}
            />
          </FieldRow>
        </>
      )}

      {backend === "s3" && (
        <>
          <FieldRow label={t("settings.storage.s3.endpoint")}>
            <input
              className="settings-input"
              type="url"
              placeholder={t("settings.storage.s3.endpoint.placeholder")}
              value={draft.s3?.endpoint ?? ""}
              disabled={!canEdit}
              onChange={(e) => updateS3({ endpoint: e.target.value })}
            />
          </FieldRow>
          <FieldRow label={t("settings.storage.s3.region")}>
            <input
              className="settings-input"
              type="text"
              placeholder={t("settings.storage.s3.region.placeholder")}
              value={draft.s3?.region ?? ""}
              disabled={!canEdit}
              onChange={(e) => updateS3({ region: e.target.value })}
            />
          </FieldRow>
          <FieldRow label={t("settings.storage.s3.bucket")}>
            <input
              className="settings-input"
              type="text"
              placeholder={t("settings.storage.s3.bucket.placeholder")}
              value={draft.s3?.bucket ?? ""}
              disabled={!canEdit}
              onChange={(e) => updateS3({ bucket: e.target.value })}
            />
          </FieldRow>
          <FieldRow label={t("settings.storage.s3.accessKeyID")}>
            <input
              className="settings-input"
              type="text"
              value={draft.s3?.accessKeyID ?? ""}
              disabled={!canEdit}
              onChange={(e) => updateS3({ accessKeyID: e.target.value })}
            />
          </FieldRow>
          <FieldRow label={t("settings.storage.s3.secretAccessKey")}>
            <SecretField
              hasExisting={originalSecrets.s3}
              maskedHint={config?.s3?.accessKeyID ? maskKey(config.s3.accessKeyID) : ""}
              value={draft.s3?.secretAccessKey ?? ""}
              disabled={!canEdit}
              onChange={(v) => updateS3({ secretAccessKey: v })}
            />
          </FieldRow>
          <FieldRow label={t("settings.storage.s3.usePathStyle")}>
            <label className="settings-toggle">
              <input
                type="checkbox"
                checked={!!draft.s3?.usePathStyle}
                disabled={!canEdit}
                onChange={(e) => updateS3({ usePathStyle: e.target.checked })}
              />
              <span className="settings-toggle-slider" />
              <span className="settings-toggle-label">
                {t("settings.storage.s3.usePathStyle.hint")}
              </span>
            </label>
          </FieldRow>
          <FieldRow label={t("settings.storage.s3.publicBaseURL")}>
            <input
              className="settings-input"
              type="url"
              placeholder={t("settings.storage.s3.publicBaseURL.placeholder")}
              value={draft.s3?.publicBaseURL ?? ""}
              disabled={!canEdit}
              onChange={(e) => updateS3({ publicBaseURL: e.target.value })}
            />
          </FieldRow>
        </>
      )}

      {/* Save action */}
      {canEdit && (
        <div className="settings-row" style={{ justifyContent: "flex-end" }}>
          <button
            className="settings-btn-save"
            onClick={handleSave}
            disabled={!dirty || saving}
            type="button"
          >
            <Save size={14} /> {t("settings.storage.save")}
          </button>
        </div>
      )}
    </div>
  );
}

function backendDescription(backend: StorageBackend, t: (k: import("@/features/i18n").TKey) => string): string {
  switch (backend) {
    case "osfs":
      return t("settings.storage.backend.osfs.desc");
    case "embedded":
      return t("settings.storage.backend.embedded.desc");
    case "s3":
      return t("settings.storage.backend.s3.desc");
    default:
      return "";
  }
}

function FieldRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="settings-field-row">
      <label className="settings-field-label">{label}</label>
      {children}
    </div>
  );
}

function BackendButton({
  current,
  value,
  label,
  icon,
  disabled,
  onClick,
}: {
  current: StorageBackend;
  value: StorageBackend;
  label: string;
  icon: React.ReactNode;
  disabled?: boolean;
  onClick: () => void;
}) {
  const active = current === value;
  return (
    <button
      type="button"
      className={`settings-theme-btn ${active ? "active" : ""}`}
      disabled={disabled}
      onClick={onClick}
    >
      {icon}
      <span>{label}</span>
    </button>
  );
}

// SecretField masks pre-existing secrets behind a "Use existing" hint with
// a "Replace" button. When the user wants to type a new secret they get the
// PasswordInput with Show/Hide; an empty submit leaves the previous value
// untouched (see buildPatch).
function SecretField({
  hasExisting,
  maskedHint,
  value,
  placeholder,
  disabled,
  onChange,
}: {
  hasExisting: boolean;
  maskedHint?: string;
  value: string;
  placeholder?: string;
  disabled?: boolean;
  onChange: (value: string) => void;
}) {
  const { t } = useT();
  const [editing, setEditing] = useState(!hasExisting);

  if (!editing) {
    return (
      <div className="settings-input-masked">
        <span className="settings-masked-value">
          {maskedHint ? `(${maskedHint})` : "********"}
        </span>
        <button
          type="button"
          className="settings-btn-clear"
          disabled={disabled}
          onClick={() => setEditing(true)}
        >
          {t("settings.storage.reveal")}
        </button>
      </div>
    );
  }

  return (
    <PasswordInput
      value={value}
      placeholder={placeholder}
      disabled={disabled}
      onChange={(e) => onChange(e.target.value)}
    />
  );
}
