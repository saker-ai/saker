"use client";

import { useCallback, useState } from "react";
import { Cloud, LogOut, RefreshCw } from "lucide-react";
import type { RPCClient } from "@/features/rpc/client";
import { useT } from "@/features/i18n";
import { useSkillhubConfig, useSkillhubRpc } from "@/features/skills/shared/useSkillhubRpc";
import { SkillhubLoginModal } from "@/features/skills/shared/SkillhubLoginModal";

interface Props {
  rpc: RPCClient | null;
  showToast?: (text: string, type: "success" | "error") => void;
}

export function SkillhubSection({ rpc, showToast }: Props) {
  const { t } = useT();
  const { config, refresh } = useSkillhubConfig(rpc);
  const api = useSkillhubRpc(rpc);
  const [registry, setRegistry] = useState("");
  const [savingRegistry, setSavingRegistry] = useState(false);
  const [loginOpen, setLoginOpen] = useState(false);
  const [syncing, setSyncing] = useState(false);

  const handleSyncNow = useCallback(async () => {
    setSyncing(true);
    try {
      const res = await api.sync();
      const updated = res.results.filter((r) => r.status === "updated").length;
      showToast?.(`${t("plaza.syncDone")} (${updated})`, "success");
      await refresh();
    } catch (e) {
      showToast?.(e instanceof Error ? e.message : String(e), "error");
    } finally {
      setSyncing(false);
    }
  }, [api, refresh, showToast, t]);

  const formatLastSync = useCallback((iso?: string) => {
    if (!iso) return t("plaza.lastSyncNever");
    const ts = Date.parse(iso);
    if (Number.isNaN(ts)) return t("plaza.lastSyncNever");
    const sec = Math.max(0, Math.floor((Date.now() - ts) / 1000));
    if (sec < 60) return t("plaza.justNow");
    const min = Math.floor(sec / 60);
    if (min < 60) return `${min} ${t("plaza.minutesAgo")}`;
    const h = Math.floor(min / 60);
    if (h < 24) return `${h} ${t("plaza.hoursAgo")}`;
    return `${Math.floor(h / 24)} ${t("plaza.daysAgo")}`;
  }, [t]);

  const effectiveRegistry = registry || config?.registry || "";

  const handleSaveRegistry = useCallback(async () => {
    if (!rpc) return;
    setSavingRegistry(true);
    try {
      await api.updateConfig({ registry: registry.trim() });
      await refresh();
      showToast?.(t("settings.saved"), "success");
    } catch (e) {
      showToast?.(e instanceof Error ? e.message : String(e), "error");
    } finally {
      setSavingRegistry(false);
    }
  }, [api, refresh, registry, rpc, showToast, t]);

  const handleToggle = useCallback(
    async (key: "autoSync" | "learnedAutoPublish" | "offline", value: boolean) => {
      try {
        await api.updateConfig({ [key]: value });
        await refresh();
        showToast?.(t("settings.saved"), "success");
      } catch (e) {
        showToast?.(e instanceof Error ? e.message : String(e), "error");
      }
    },
    [api, refresh, showToast, t]
  );

  const handleLogout = useCallback(async () => {
    try {
      await api.logout();
      await refresh();
      showToast?.(t("settings.saved"), "success");
    } catch (e) {
      showToast?.(e instanceof Error ? e.message : String(e), "error");
    }
  }, [api, refresh, showToast, t]);

  if (!config) {
    return (
      <div className="settings-card" id="skillhub" data-section="skillhub">
        <div className="settings-card-title">
          <Cloud size={18} />
          <span>{t("skillhub.title")}</span>
        </div>
        <div className="settings-row">
          <span className="settings-label">{t("plaza.loading")}</span>
        </div>
      </div>
    );
  }

  return (
    <div className="settings-card" id="skillhub" data-section="skillhub">
      <div className="settings-card-title">
        <Cloud size={18} />
        <span>{t("skillhub.title")}</span>
      </div>

      {/* Registry */}
      <div className="settings-row">
        <span className="settings-label">{t("skillhub.registry")}</span>
        <div className="skillhub-section-row">
          <input
            type="url"
            className="skills-search-input"
            value={effectiveRegistry}
            placeholder={t("skillhub.registryPlaceholder")}
            onChange={(e) => setRegistry(e.target.value)}
          />
          <button
            type="button"
            className="settings-btn-save"
            disabled={savingRegistry || !registry.trim() || registry.trim() === config.registry}
            onClick={() => void handleSaveRegistry()}
          >
            {savingRegistry ? t("settings.saving") : t("settings.save")}
          </button>
        </div>
      </div>

      {/* Login state */}
      <div className="settings-row">
        <span className="settings-label">{t("settings.status")}</span>
        {config.loggedIn ? (
          <div className="skillhub-section-row">
            <span className={`settings-status online`}>
              <span className="settings-status-dot" />
              {t("skillhub.loggedInAs")} <strong>@{config.handle || "user"}</strong>
            </span>
            <button
              type="button"
              className="settings-btn-cancel skillhub-login-icon-btn"
              onClick={() => void handleLogout()}
            >
              <LogOut size={14} />
              <span>{t("skillhub.logout")}</span>
            </button>
          </div>
        ) : (
          <div className="skillhub-section-row">
            <span className="settings-status offline">
              <span className="settings-status-dot" />
              {t("skillhub.notLoggedIn")}
            </span>
            <button
              type="button"
              className="settings-btn-save"
              onClick={() => setLoginOpen(true)}
              disabled={config.offline}
            >
              {t("skillhub.login")}
            </button>
          </div>
        )}
      </div>

      {/* Auto sync toggle */}
      <div className="settings-row">
        <span className="settings-label">{t("skillhub.autoSync")}</span>
        <label className="settings-toggle">
          <input
            type="checkbox"
            checked={config.autoSync}
            onChange={(e) => void handleToggle("autoSync", e.target.checked)}
          />
          <span className="settings-toggle-slider" />
          <span className="settings-toggle-label">
            {config.autoSync ? t("settings.enabled") : t("settings.disabled")}
          </span>
        </label>
      </div>
      <div className="settings-hint">{t("skillhub.autoSyncDesc")}</div>

      {/* Sync now + last-sync indicator */}
      <div className="settings-row">
        <span className="settings-label">{t("plaza.lastSync")}</span>
        <div className="skillhub-section-row">
          <span className="settings-hint">
            {formatLastSync(config.lastSyncAt)}
            {config.lastSyncStatus && (
              <span className={`plaza-sync-status plaza-sync-status-${config.lastSyncStatus}`}>
                {" "}
                ({t(("plaza.lastSync" + config.lastSyncStatus.charAt(0).toUpperCase() + config.lastSyncStatus.slice(1)) as Parameters<typeof t>[0])})
              </span>
            )}
          </span>
          <button
            type="button"
            className="settings-btn-cancel skillhub-login-icon-btn"
            onClick={() => void handleSyncNow()}
            disabled={syncing || config.offline || config.subscriptions.length === 0}
            title={t("plaza.sync")}
          >
            <RefreshCw size={14} className={syncing ? "spin" : ""} />
            <span>{syncing ? t("plaza.syncRunning") : t("plaza.sync")}</span>
          </button>
        </div>
      </div>

      {/* Learned auto-publish toggle */}
      <div className="settings-row">
        <span className="settings-label">{t("skillhub.learnedAutoPublish")}</span>
        <label className="settings-toggle">
          <input
            type="checkbox"
            checked={config.learnedAutoPublish}
            onChange={(e) => void handleToggle("learnedAutoPublish", e.target.checked)}
            disabled={!config.loggedIn}
          />
          <span className="settings-toggle-slider" />
          <span className="settings-toggle-label">
            {config.learnedAutoPublish ? t("settings.enabled") : t("settings.disabled")}
          </span>
        </label>
      </div>
      <div className="settings-hint">{t("skillhub.learnedAutoPublishDesc")}</div>

      {/* Offline mode */}
      <div className="settings-row">
        <span className="settings-label">{t("skillhub.offline")}</span>
        <label className="settings-toggle">
          <input
            type="checkbox"
            checked={config.offline}
            onChange={(e) => void handleToggle("offline", e.target.checked)}
          />
          <span className="settings-toggle-slider" />
          <span className="settings-toggle-label">
            {config.offline ? t("settings.enabled") : t("settings.disabled")}
          </span>
        </label>
      </div>
      <div className="settings-hint">{t("skillhub.offlineDesc")}</div>

      <SkillhubLoginModal
        open={loginOpen}
        rpc={rpc}
        registry={config.registry}
        onClose={() => setLoginOpen(false)}
        onSuccess={() => {
          setLoginOpen(false);
          void refresh();
        }}
      />
    </div>
  );
}
