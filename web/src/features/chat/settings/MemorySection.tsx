import { useState, useCallback, useEffect } from "react";
import { Brain, User, MessageSquare, FolderOpen, BookOpen, Trash2, ChevronDown, ChevronRight } from "lucide-react";
import type { MemoryEntry, MemoryListResult } from "@/features/rpc/types";
import type { RPCClient } from "@/features/rpc/client";
import { useT, type TKey } from "@/features/i18n";
import { useToast, Toast, ConfirmDialog } from "./shared";

interface Props {
  rpc: RPCClient | null;
}

const TYPE_ORDER: MemoryEntry["type"][] = ["user", "feedback", "project", "reference"];

const TYPE_ICONS: Record<MemoryEntry["type"], React.ReactNode> = {
  user: <User size={14} />,
  feedback: <MessageSquare size={14} />,
  project: <FolderOpen size={14} />,
  reference: <BookOpen size={14} />,
};

const TYPE_LABEL_KEYS: Record<MemoryEntry["type"], TKey> = {
  user: "memory.typeUser",
  feedback: "memory.typeFeedback",
  project: "memory.typeProject",
  reference: "memory.typeReference",
};

function formatRelativeTime(isoStr: string): string {
  try {
    const date = new Date(isoStr);
    const now = new Date();
    const diffMs = now.getTime() - date.getTime();
    const diffMin = Math.floor(diffMs / 60000);
    if (diffMin < 1) return "just now";
    if (diffMin < 60) return `${diffMin}m ago`;
    const diffHr = Math.floor(diffMin / 60);
    if (diffHr < 24) return `${diffHr}h ago`;
    const diffDay = Math.floor(diffHr / 24);
    if (diffDay < 30) return `${diffDay}d ago`;
    return date.toLocaleDateString();
  } catch {
    return "";
  }
}

export function MemorySection({ rpc }: Props) {
  const { t } = useT();
  const { toast, showToast } = useToast();
  const [entries, setEntries] = useState<MemoryEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [expanded, setExpanded] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!rpc) {
      setLoading(false);
      return;
    }
    try {
      const res = await rpc.request<MemoryListResult>("memory/list");
      setEntries(res.entries || []);
    } catch {
      showToast(t("settings.saveFailed"), "error");
    } finally {
      setLoading(false);
    }
  }, [rpc, showToast, t]);

  useEffect(() => { load(); }, [load]);

  const handleDelete = async (name: string) => {
    if (!rpc || saving) return;
    setSaving(true);
    try {
      await rpc.request("memory/delete", { name });
      showToast(t("settings.saved"), "success");
      if (expanded === name) setExpanded(null);
      await load();
    } catch {
      showToast(t("settings.saveFailed"), "error");
    } finally {
      setSaving(false);
      setConfirmDelete(null);
    }
  };

  if (loading) {
    return <div className="settings-empty">{t("settings.loading")}</div>;
  }

  const grouped = new Map<MemoryEntry["type"], MemoryEntry[]>();
  for (const type of TYPE_ORDER) {
    grouped.set(type, []);
  }
  for (const entry of entries) {
    const list = grouped.get(entry.type);
    if (list) list.push(entry);
  }

  const hasEntries = entries.length > 0;

  return (
    <div className="settings-tab-stack">
      <Toast msg={toast} />

      <ConfirmDialog
        open={confirmDelete !== null}
        title={t("memory.confirmDeleteTitle")}
        message={t("memory.confirmDelete")}
        confirmLabel={t("memory.confirmBtn")}
        danger
        onConfirm={() => confirmDelete && handleDelete(confirmDelete)}
        onCancel={() => setConfirmDelete(null)}
      />

      <div className="settings-card-v2">
        <div className="settings-card-v2-header">
          <Brain size={18} />
          <span>{t("memory.title")}</span>
          {hasEntries && (
            <span className="memory-count-badge">{entries.length}</span>
          )}
        </div>
        <div className="settings-card-v2-body">
          <p className="persona-subtitle">{t("memory.subtitle")}</p>
        </div>
      </div>

      {!hasEntries && (
        <div className="settings-card-v2">
          <div className="settings-card-v2-body">
            <p className="persona-subtitle">{t("memory.empty")}</p>
          </div>
        </div>
      )}

      {TYPE_ORDER.map((type) => {
        const items = grouped.get(type) || [];
        if (items.length === 0) return null;

        return (
          <div key={type} className="settings-card-v2">
            <div className="settings-card-v2-header">
              {TYPE_ICONS[type]}
              <span>{t(TYPE_LABEL_KEYS[type])}</span>
              <span className="memory-count-badge">{items.length}</span>
            </div>
            <div className="settings-card-v2-body memory-entry-list">
              {items.map((entry) => {
                const isExpanded = expanded === entry.name;
                return (
                  <div key={entry.name} className="memory-entry">
                    <div
                      className="memory-entry-header"
                      onClick={() => setExpanded(isExpanded ? null : entry.name)}
                      onKeyDown={(e) => {
                        if (e.key === "Enter" || e.key === " ") {
                          e.preventDefault();
                          setExpanded(isExpanded ? null : entry.name);
                        }
                      }}
                      role="button"
                      tabIndex={0}
                    >
                      <span className="memory-entry-toggle">
                        {isExpanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                      </span>
                      <div className="memory-entry-info">
                        <span className="memory-entry-name">{entry.name}</span>
                        {entry.description && (
                          <span className="memory-entry-desc">{entry.description}</span>
                        )}
                      </div>
                      <span className="memory-entry-time">
                        {formatRelativeTime(entry.mod_time)}
                      </span>
                      <button
                        className="persona-icon-btn danger"
                        onClick={(e) => {
                          e.stopPropagation();
                          setConfirmDelete(entry.name);
                        }}
                        aria-label={`${t("memory.confirmBtn")} ${entry.name}`}
                        disabled={saving}
                        type="button"
                      >
                        <Trash2 size={14} />
                      </button>
                    </div>
                    {isExpanded && (
                      <div className="memory-entry-content">
                        <pre>{entry.content}</pre>
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          </div>
        );
      })}
    </div>
  );
}
