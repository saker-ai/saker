import type { SandboxConfig } from "@/features/rpc/types";
import { useT } from "@/features/i18n";

export function SandboxSection({
  config,
  onSave,
  showToast,
}: {
  config?: SandboxConfig;
  onSave?: (sandbox: SandboxConfig) => Promise<void>;
  showToast?: (text: string, type: "success" | "error") => void;
}) {
  const { t } = useT();
  const enabled = config?.enabled ?? false;

  const handleToggle = async () => {
    if (!onSave) return;
    try {
      await onSave({ ...config, enabled: !enabled });
      showToast?.(t("settings.saved"), "success");
    } catch {
      showToast?.(t("settings.saveFailed"), "error");
    }
  };

  return (
    <div className="settings-card" id="sandbox" data-section="sandbox">
      <div className="settings-card-title"><span>{t("settings.sandbox")}</span></div>
      <div className="settings-row">
        <span className="settings-label">{t("settings.fsIsolation")}</span>
        <label className="settings-toggle">
          <input
            type="checkbox"
            checked={enabled}
            onChange={handleToggle}
            disabled={!onSave}
          />
          <span className="settings-toggle-slider" />
          <span className="settings-toggle-label">{enabled ? t("settings.enabled") : t("settings.disabled")}</span>
        </label>
      </div>
      <div className="settings-hint">
        {enabled ? t("settings.sandboxEnabled") : t("settings.sandboxDisabled")}
      </div>
    </div>
  );
}
