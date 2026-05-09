"use client";

import { useCallback, useState } from "react";
import { Store, Boxes } from "lucide-react";
import type { RPCClient } from "@/features/rpc/client";
import type {
  GenericTaskStatus,
  SkillContentResult,
  SkillImportPayload,
  SkillImportPreviewResult,
  SkillInfo,
  SkillStats,
} from "@/features/rpc/types";
import { useT, type TKey } from "@/features/i18n";
import { MySkillsView } from "./my-skills/MySkillsView";
import { SkillPlazaView } from "./plaza/SkillPlazaView";
import { SkillhubStatusChip } from "./shared/SkillhubStatusChip";
import { useSkillhubConfig } from "./shared/useSkillhubRpc";

type Tab = "plaza" | "mine";

interface Props {
  rpc: RPCClient | null;
  // Pass-through props for MySkillsView / SkillsCatalog
  skills: SkillInfo[];
  disabledSkills?: string[];
  onRemove?: (name: string) => Promise<void>;
  onPromote?: (name: string) => Promise<void>;
  onToggleSkill?: (name: string, disabled: boolean) => Promise<void>;
  onLoadContent?: (name: string) => Promise<SkillContentResult>;
  onLoadAnalytics?: () => Promise<Record<string, SkillStats> | null>;
  onSelectRelated?: (name: string) => void;
  onImport?: (payload: SkillImportPayload) => Promise<{ taskId: string }>;
  onPreviewImport?: (payload: SkillImportPayload) => Promise<SkillImportPreviewResult>;
  onTaskStatus?: (taskId: string) => Promise<GenericTaskStatus>;
  onRefreshSkills?: () => Promise<SkillInfo[]>;
  onShowToast?: (msg: string, kind: "success" | "error") => void;
}

const TABS: Array<{ id: Tab; labelKey: TKey; icon: React.ReactNode }> = [
  { id: "plaza", labelKey: "skills.tab.plaza", icon: <Store size={14} /> },
  { id: "mine", labelKey: "skills.tab.mine", icon: <Boxes size={14} /> },
];

export function SkillsPage(props: Props) {
  const { t } = useT();
  const { config, refresh } = useSkillhubConfig(props.rpc);
  const [tab, setTab] = useState<Tab>(() => {
    if (typeof window === "undefined") return "mine";
    const saved = window.localStorage.getItem("skills-page-tab");
    return saved === "plaza" || saved === "mine" ? (saved as Tab) : "mine";
  });

  const switchTab = (next: Tab) => {
    setTab(next);
    try {
      window.localStorage.setItem("skills-page-tab", next);
    } catch {
      // ignore
    }
  };

  const handleInstalled = useCallback(async () => {
    await props.onRefreshSkills?.();
  }, [props.onRefreshSkills]);

  return (
    <div className="skills-page">
      <header className="skills-page-header">
        <nav className="skills-page-tabs" role="tablist" aria-label="Skills view">
          {TABS.map((tabDef) => (
            <button
              key={tabDef.id}
              type="button"
              role="tab"
              aria-selected={tab === tabDef.id}
              className={`skills-page-tab ${tab === tabDef.id ? "active" : ""}`}
              onClick={() => switchTab(tabDef.id)}
            >
              {tabDef.icon}
              <span>{t(tabDef.labelKey)}</span>
            </button>
          ))}
        </nav>
        <div className="skills-page-header-right">
          <SkillhubStatusChip rpc={props.rpc} config={config} onChange={refresh} />
        </div>
      </header>

      <div className="skills-page-body">
        {tab === "plaza" && (
          <SkillPlazaView
            rpc={props.rpc}
            config={config}
            onConfigChange={refresh}
            onInstalled={handleInstalled}
            onShowToast={props.onShowToast}
          />
        )}
        {tab === "mine" && (
          <MySkillsView
            skills={props.skills}
            disabledSkills={props.disabledSkills}
            onRemove={props.onRemove}
            onPromote={props.onPromote}
            onToggleSkill={props.onToggleSkill}
            onLoadContent={props.onLoadContent}
            onLoadAnalytics={props.onLoadAnalytics}
            onSelectRelated={props.onSelectRelated}
            onImport={props.onImport}
            onPreviewImport={props.onPreviewImport}
            onTaskStatus={props.onTaskStatus}
            onRefreshSkills={props.onRefreshSkills}
          />
        )}
      </div>
    </div>
  );
}
