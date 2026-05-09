import { useState } from "react";
import { Settings, Shield, Wrench, Zap, Terminal, Sun, Wifi, Box, Monitor, Moon, Globe, BarChart3, Users, User, Radio, Brain } from "lucide-react";
import type {
  ServerSettings,
  SandboxConfig,
  AigoConfig,
  FailoverConfig,
  StorageConfig,
} from "@/features/rpc/types";
import { resolveWsUrl } from "@/features/rpc/client";
import { useTheme } from "../ThemeProvider";
import { useT, type Locale, type TKey } from "@/features/i18n";
import { Section, Row, groupTools, truncateDesc, useToast, Toast } from "./shared";
import { SandboxSection } from "./SandboxSection";
import { AuthAndUsersSection } from "./AuthSection";
import { FailoverSection } from "./FailoverSection";
import { AigoSection } from "./AigoSection";
import { SkillsAnalyticsSection } from "./SkillsAnalyticsSection";
import { PersonaSection } from "./PersonaSection";
import { MyPersonaTab } from "../profile/MyPersonaTab";
import { MyChannelsTab } from "../profile/MyChannelsTab";
import { MemorySection } from "./MemorySection";
import { SkillhubSection } from "./SkillhubSection";
import { StorageSection } from "./StorageSection";
import type { RPCClient } from "@/features/rpc/client";

interface EmbedBackend {
  name: string;
  env_key: string;
  available: boolean;
}

interface Props {
  settings: ServerSettings | null;
  connected: boolean;
  registeredTools?: { name: string; description: string; category: string }[];
  embedBackends?: EmbedBackend[];
  isAdmin?: boolean;
  onUpdateAigo?: (aigo: AigoConfig) => Promise<void>;
  onUpdateFailover?: (failover: FailoverConfig) => Promise<void>;
  onUpdateSandbox?: (sandbox: SandboxConfig) => Promise<void>;
  onUpdateStorage?: (storage: StorageConfig) => Promise<void>;
  onUpdateAuth?: (username: string, password: string) => Promise<void>;
  onDeleteAuth?: () => Promise<void>;
  onCreateUser?: (username: string, password: string) => Promise<void>;
  onDeleteUser?: (username: string) => Promise<void>;
  rpc?: RPCClient | null;
}

// --- Tab definitions ---

type TabId = "general" | "my-persona" | "memory" | "security" | "tools" | "engines" | "environment" | "personas" | "channels" | "skills";

interface TabDef {
  id: TabId;
  labelKey: TKey;
  icon: React.ReactNode;
  adminOnly?: boolean;
}

const TABS: TabDef[] = [
  {
    id: "general",
    labelKey: "settings.tabGeneral",
    icon: <Settings size={16} />,
  },
  {
    id: "my-persona",
    labelKey: "profile.myPersona",
    icon: <User size={16} />,
  },
  {
    id: "memory",
    labelKey: "settings.tabMemory",
    icon: <Brain size={16} />,
  },
  {
    id: "security",
    labelKey: "settings.tabSecurity",
    icon: <Shield size={16} />,
    adminOnly: true,
  },
  {
    id: "tools",
    labelKey: "settings.tabTools",
    icon: <Wrench size={16} />,
    adminOnly: true,
  },
  {
    id: "engines",
    labelKey: "settings.tabEngines",
    icon: <Zap size={16} />,
    adminOnly: true,
  },
  {
    id: "environment",
    labelKey: "settings.tabEnvironment",
    icon: <Terminal size={16} />,
    adminOnly: true,
  },
  {
    id: "personas",
    labelKey: "settings.tabPersonas",
    icon: <Users size={16} />,
    adminOnly: true,
  },
  {
    id: "channels",
    labelKey: "settings.tabChannels",
    icon: <Radio size={16} />,
  },
  {
    id: "skills",
    labelKey: "settings.tabSkills",
    icon: <BarChart3 size={16} />,
    adminOnly: true,
  },
];

// --- Tab bar ---

function TabBar({ activeTab, onChange, isAdmin }: { activeTab: TabId; onChange: (tab: TabId) => void; isAdmin: boolean }) {
  const { t } = useT();
  const visibleTabs = TABS.filter(tab => !tab.adminOnly || isAdmin);

  return (
    <nav className="settings-tabs" role="tablist" aria-label="Settings tabs">
      {visibleTabs.map((tab) => (
        <button
          key={tab.id}
          role="tab"
          aria-selected={activeTab === tab.id}
          className={`settings-tab ${activeTab === tab.id ? "active" : ""}`}
          onClick={() => onChange(tab.id)}
          type="button"
        >
          {tab.icon}
          <span>{t(tab.labelKey)}</span>
        </button>
      ))}
    </nav>
  );
}

// --- Main Panel ---

export function SettingsPanel({ settings, connected, registeredTools, embedBackends, isAdmin = true, onUpdateAigo, onUpdateFailover, onUpdateSandbox, onUpdateStorage, onUpdateAuth, onDeleteAuth, onCreateUser, onDeleteUser, rpc }: Props) {
  const { theme, setTheme } = useTheme();
  const { locale, setLocale, t } = useT();
  const { toast, showToast } = useToast();
  const [activeTab, setActiveTab] = useState<TabId>(() => {
    const saved = typeof window !== "undefined" ? localStorage.getItem("settings-tab") : null;
    if (saved && TABS.some(t => t.id === saved && (!t.adminOnly || isAdmin))) {
      return saved as TabId;
    }
    return "general";
  });

  const handleTabChange = (tab: TabId) => {
    setActiveTab(tab);
    try { localStorage.setItem("settings-tab", tab); } catch {}
  };

  const categoryLabelsMap: Record<string, string> = {
    builtin: t("settings.core"),
    aigo: t("settings.aigoMedia"),
  };

  function categoryLabel(cat: string): string {
    if (categoryLabelsMap[cat]) return categoryLabelsMap[cat];
    if (cat.startsWith("mcp:")) return `MCP: ${cat.slice(4)}`;
    if (cat === "mcp") return "MCP";
    return cat;
  }

  return (
    <div className="settings-page">
      <TabBar activeTab={activeTab} onChange={handleTabChange} isAdmin={isAdmin} />
      <Toast msg={toast} />

      <div className="settings-tab-content" role="tabpanel">
        {!settings && activeTab !== "general" ? (
          <div className="settings-empty">{t("settings.loading")}</div>
        ) : (
          <>
            {activeTab === "general" && (
              <GeneralTab
                theme={theme}
                setTheme={setTheme}
                locale={locale}
                setLocale={setLocale}
                connected={connected}
                settings={settings}
                t={t}
              />
            )}

            {activeTab === "security" && settings && (
              <SecurityTab
                settings={settings}
                isAdmin={isAdmin}
                onUpdateSandbox={onUpdateSandbox}
                onUpdateAuth={onUpdateAuth}
                onDeleteAuth={onDeleteAuth}
                onCreateUser={onCreateUser}
                onDeleteUser={onDeleteUser}
                showToast={showToast}
                t={t}
              />
            )}

            {activeTab === "tools" && settings && (
              <ToolsTab
                settings={settings}
                registeredTools={registeredTools}
                categoryLabel={categoryLabel}
                t={t}
              />
            )}

            {activeTab === "engines" && settings && (
              <EnginesTab
                settings={settings}
                embedBackends={embedBackends}
                isAdmin={isAdmin}
                onUpdateAigo={onUpdateAigo}
                onUpdateFailover={onUpdateFailover}
                onUpdateStorage={onUpdateStorage}
                showToast={showToast}
                t={t}
              />
            )}

            {activeTab === "environment" && settings && (
              <EnvironmentTab settings={settings} t={t} />
            )}

            {activeTab === "my-persona" && (
              <MyPersonaTab rpc={rpc ?? null} />
            )}

            {activeTab === "memory" && (
              <MemorySection rpc={rpc ?? null} />
            )}

            {activeTab === "personas" && (
              <PersonaSection rpc={rpc ?? null} />
            )}

            {activeTab === "channels" && (
              <MyChannelsTab rpc={rpc ?? null} />
            )}

            {activeTab === "skills" && (
              <div className="settings-tab-stack">
                <SkillhubSection rpc={rpc ?? null} showToast={showToast} />
                <SkillsAnalyticsSection rpc={rpc ?? null} />
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );
}

// --- Tab: General ---

function GeneralTab({
  theme,
  setTheme,
  locale,
  setLocale,
  connected,
  settings,
  t,
}: {
  theme: string;
  setTheme: (t: import("../ThemeProvider").Theme) => void;
  locale: string;
  setLocale: (l: "en" | "zh") => void;
  connected: boolean;
  settings: ServerSettings | null;
  t: (key: TKey) => string;
}) {
  return (
    <div className="settings-tab-grid">
      {/* Appearance Card */}
      <div className="settings-card-v2">
        <div className="settings-card-v2-header">
          <Sun size={18} />
          <span>{t("settings.appearance")}</span>
        </div>
        <div className="settings-card-v2-body">
          <div className="settings-row">
            <span className="settings-label">{t("settings.theme")}</span>
            <div className="settings-theme-options">
              {(["system", "dark", "light"] as const).map((th) => (
                <button
                  key={th}
                  className={`settings-theme-btn ${theme === th ? "active" : ""}`}
                  onClick={() => setTheme(th)}
                >
                  {th === "system" && <Monitor size={14} />}
                  {th === "dark" && <Moon size={14} />}
                  {th === "light" && <Sun size={14} />}
                  <span>{th === "system" ? t("settings.system") : th === "dark" ? t("settings.dark") : t("settings.light")}</span>
                </button>
              ))}
            </div>
          </div>
          <div className="settings-row">
            <span className="settings-label">{t("settings.language")}</span>
            <div className="settings-theme-options">
              {([["en", "settings.langEn"], ["zh", "settings.langZh"]] as const).map(([loc, labelKey]) => (
                <button
                  key={loc}
                  className={`settings-theme-btn ${locale === loc ? "active" : ""}`}
                  onClick={() => setLocale(loc as "en" | "zh")}
                >
                  <Globe size={14} />
                  <span>{t(labelKey)}</span>
                </button>
              ))}
            </div>
          </div>
        </div>
      </div>

      {/* Connection Card */}
      <div className="settings-card-v2">
        <div className="settings-card-v2-header">
          <Wifi size={18} />
          <span>{t("settings.connection")}</span>
        </div>
        <div className="settings-card-v2-body">
          <Row label="WebSocket" value={resolveWsUrl()} />
          <div className="settings-row">
            <span className="settings-label">{t("settings.status")}</span>
            <span className={`settings-status ${connected ? "online" : "offline"}`}>
              <span className="settings-status-dot" />
              {connected ? t("settings.connected") : t("settings.disconnected")}
            </span>
          </div>
        </div>
      </div>

      {/* Model Card */}
      {settings && (
        <div className="settings-card-v2">
          <div className="settings-card-v2-header">
            <Box size={18} />
            <span>{t("settings.model")}</span>
          </div>
          <div className="settings-card-v2-body">
            <div className="settings-model-display">
              {settings.model || t("settings.default")}
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

// --- Tab: Security ---

function SecurityTab({
  settings,
  isAdmin,
  onUpdateSandbox,
  onUpdateAuth,
  onDeleteAuth,
  onCreateUser,
  onDeleteUser,
  showToast,
  t,
}: {
  settings: ServerSettings;
  isAdmin: boolean;
  onUpdateSandbox?: (sandbox: SandboxConfig) => Promise<void>;
  onUpdateAuth?: (username: string, password: string) => Promise<void>;
  onDeleteAuth?: () => Promise<void>;
  onCreateUser?: (username: string, password: string) => Promise<void>;
  onDeleteUser?: (username: string) => Promise<void>;
  showToast: (text: string, type: "success" | "error") => void;
  t: (key: TKey) => string;
}) {
  return (
    <div className="settings-tab-stack">
      <SandboxSection
        config={settings.sandbox}
        onSave={isAdmin ? onUpdateSandbox : undefined}
        showToast={showToast}
      />

      {isAdmin && (
        <AuthAndUsersSection
          config={settings.webAuth}
          onSave={onUpdateAuth}
          onDelete={onDeleteAuth}
          users={settings.webAuth?.users}
          onCreateUser={onCreateUser}
          onDeleteUser={onDeleteUser}
          showToast={showToast}
        />
      )}

      {settings.permissions ? (
        <Section title={t("settings.permissions")}>
          {settings.permissions.allow && settings.permissions.allow.length > 0 ? (
            <div className="settings-row">
              <span className="settings-label">{t("settings.allow")}</span>
              <div className="settings-tag-list">
                {settings.permissions.allow.map((r: string) => (
                  <span key={r} className="settings-tag">{r}</span>
                ))}
              </div>
            </div>
          ) : null}
          {settings.permissions.deny && settings.permissions.deny.length > 0 ? (
            <div className="settings-row">
              <span className="settings-label">{t("settings.deny")}</span>
              <div className="settings-tag-list">
                {settings.permissions.deny.map((r: string) => (
                  <span key={r} className="settings-tag settings-tag-deny">{r}</span>
                ))}
              </div>
            </div>
          ) : null}
          {!settings.permissions.allow?.length && !settings.permissions.deny?.length ? (
            <Row label={t("settings.rules")} value={t("settings.noneConfigured")} />
          ) : null}
        </Section>
      ) : null}
    </div>
  );
}

// --- Tab: Tools ---

function ToolsTab({
  settings,
  registeredTools,
  categoryLabel,
  t,
}: {
  settings: ServerSettings;
  registeredTools?: { name: string; description: string; category: string }[];
  categoryLabel: (cat: string) => string;
  t: (key: TKey) => string;
}) {
  return (
    <div className="settings-tab-stack">
      {registeredTools && registeredTools.length > 0 ? (
        <Section title={`${t("settings.registeredTools")} (${registeredTools.length})`}>
          {groupTools(registeredTools, categoryLabel).map(([group, tools]) => (
            <div key={group} className="settings-tool-group">
              <div className="settings-tool-group-label">{group} ({tools.length})</div>
              <div className="settings-tag-list settings-tools-list">
                {tools.map((tool) => (
                  <span key={tool.name} className="settings-tag settings-tool-tag" data-tooltip={truncateDesc(tool.description)}>
                    {tool.name}
                  </span>
                ))}
              </div>
            </div>
          ))}
        </Section>
      ) : (
        <Section title={t("settings.registeredTools")}>
          <Row label={t("settings.registeredTools")} value={t("settings.noneConfigured")} />
        </Section>
      )}

      {settings.disallowedTools && settings.disallowedTools.length > 0 ? (
        <Section title={t("settings.disallowedTools")}>
          <div className="settings-tag-list settings-tools-list">
            {settings.disallowedTools.map((tool: string) => (
              <span key={tool} className="settings-tag settings-tag-deny">{tool}</span>
            ))}
          </div>
        </Section>
      ) : null}

      {settings.mcp ? (
        <Section title={t("settings.mcpServers")}>
          <div className="settings-json">
            {JSON.stringify(settings.mcp, null, 2)}
          </div>
        </Section>
      ) : null}
    </div>
  );
}

// --- Tab: Engines ---

function EnginesTab({
  settings,
  embedBackends,
  isAdmin,
  onUpdateAigo,
  onUpdateFailover,
  onUpdateStorage,
  showToast,
  t,
}: {
  settings: ServerSettings;
  embedBackends?: EmbedBackend[];
  isAdmin: boolean;
  onUpdateAigo?: (aigo: AigoConfig) => Promise<void>;
  onUpdateFailover?: (failover: FailoverConfig) => Promise<void>;
  onUpdateStorage?: (storage: StorageConfig) => Promise<void>;
  showToast: (text: string, type: "success" | "error") => void;
  t: (key: TKey) => string;
}) {
  return (
    <div className="settings-tab-stack">
      <AigoSection config={settings.aigo} onSave={isAdmin ? onUpdateAigo : undefined} showToast={showToast} />
      <FailoverSection config={settings.failover} onSave={isAdmin ? onUpdateFailover : undefined} showToast={showToast} />
      <StorageSection config={settings.storage} onSave={isAdmin ? onUpdateStorage : undefined} showToast={showToast} />

      {embedBackends && embedBackends.length > 0 ? (
        <Section title={t("settings.embedBackends")}>
          <div className="settings-embed-grid">
            {embedBackends.map((b) => (
              <div
                key={b.name}
                className={`settings-embed-card ${b.available ? "settings-embed-card-active" : ""}`}
              >
                <span className={`settings-status-dot ${b.available ? "online" : ""}`} />
                <span className="settings-embed-card-name">{b.name}</span>
                <span className="settings-embed-card-status">
                  {b.available ? t("settings.configured") : t("settings.notConfigured")}
                </span>
                <span className="settings-embed-card-env">{b.env_key}</span>
              </div>
            ))}
          </div>
        </Section>
      ) : null}
    </div>
  );
}

// --- Tab: Environment ---

function EnvironmentTab({
  settings,
  t,
}: {
  settings: ServerSettings;
  t: (key: TKey) => string;
}) {
  const envEntries = settings.env ? Object.entries(settings.env) : [];

  return (
    <div className="settings-tab-stack">
      {envEntries.length > 0 ? (
        <Section title={t("settings.environment")}>
          <div className="settings-env-grid">
            {envEntries.map(([k, v]) => (
              <div key={k} className="settings-env-card">
                <span className="settings-env-card-key">{k}</span>
                <span className="settings-env-card-value" title={v}>{v}</span>
              </div>
            ))}
          </div>
        </Section>
      ) : (
        <Section title={t("settings.environment")}>
          <Row label={t("settings.environment")} value={t("settings.noneConfigured")} />
        </Section>
      )}
    </div>
  );
}
