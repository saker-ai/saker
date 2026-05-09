"use client";

import { useState } from "react";
import { LogIn, LogOut, User, WifiOff } from "lucide-react";
import type { RPCClient } from "@/features/rpc/client";
import type { SkillhubConfig } from "@/features/rpc/types";
import { useT } from "@/features/i18n";
import { SkillhubLoginModal } from "./SkillhubLoginModal";
import { useSkillhubRpc } from "./useSkillhubRpc";

interface Props {
  rpc: RPCClient | null;
  config: SkillhubConfig | null;
  onChange?: () => void;
}

export function SkillhubStatusChip({ rpc, config, onChange }: Props) {
  const { t } = useT();
  const api = useSkillhubRpc(rpc);
  const [loginOpen, setLoginOpen] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);

  const handleLogout = async () => {
    setMenuOpen(false);
    try {
      await api.logout();
      onChange?.();
    } catch {
      // ignore — surface via toast in caller if needed
    }
  };

  if (!config) {
    return (
      <span className="skillhub-chip skillhub-chip-loading">
        <span className="skills-analytics-stat-label">{t("plaza.loading")}</span>
      </span>
    );
  }

  if (config.offline) {
    return (
      <span className="skillhub-chip skillhub-chip-offline" title={t("skillhub.offlineWarn")}>
        <WifiOff size={14} />
        <span>{t("skillhub.offline")}</span>
      </span>
    );
  }

  if (!config.loggedIn) {
    return (
      <>
        <button
          type="button"
          className="skillhub-chip skillhub-chip-anon"
          onClick={() => setLoginOpen(true)}
        >
          <LogIn size={14} />
          <span>{t("skillhub.login")}</span>
        </button>
        <SkillhubLoginModal
          open={loginOpen}
          rpc={rpc}
          registry={config.registry}
          onClose={() => setLoginOpen(false)}
          onSuccess={() => {
            setLoginOpen(false);
            onChange?.();
          }}
        />
      </>
    );
  }

  return (
    <div className="skillhub-chip-wrap">
      <button
        type="button"
        className="skillhub-chip skillhub-chip-user"
        onClick={() => setMenuOpen((v) => !v)}
      >
        <User size={14} />
        <span>@{config.handle || "user"}</span>
      </button>
      {menuOpen && (
        <>
          <div className="skillhub-chip-backdrop" onClick={() => setMenuOpen(false)} />
          <div className="skillhub-chip-menu">
            <div className="skillhub-chip-menu-item-readonly">
              <span className="skills-analytics-stat-label">{t("skillhub.registry")}</span>
              <span className="skillhub-chip-menu-value">{config.registry}</span>
            </div>
            <button
              type="button"
              className="skillhub-chip-menu-item"
              onClick={() => void handleLogout()}
            >
              <LogOut size={14} />
              <span>{t("skillhub.logout")}</span>
            </button>
          </div>
        </>
      )}
    </div>
  );
}
