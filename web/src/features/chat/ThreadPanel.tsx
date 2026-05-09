import { useState, useRef, useEffect, useMemo } from "react";
import { Plus, Trash2, Check } from "lucide-react";
import type { Thread } from "@/features/rpc/types";
import { useT } from "@/features/i18n";
import { usePermissions } from "@/features/project/usePermissions";

interface Props {
  threads: Thread[];
  activeThreadId: string;
  onSelectThread: (id: string) => void;
  onCreateThread: () => void;
  onDeleteThread: (id: string) => void;
  collapsed: boolean;
  connected: boolean;
  mobileDrawer?: boolean;
  mobileOpen?: boolean;
}

export function ThreadPanel({
  threads,
  activeThreadId,
  onSelectThread,
  onCreateThread,
  onDeleteThread,
  collapsed,
  connected,
  mobileDrawer,
  mobileOpen,
}: Props) {
  const { t } = useT();
  const perms = usePermissions();
  const [confirmId, setConfirmId] = useState<string | null>(null);
  const confirmRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!confirmId) return;
    const handler = (e: MouseEvent) => {
      if (confirmRef.current && !confirmRef.current.contains(e.target as Node)) {
        setConfirmId(null);
      }
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [confirmId]);

  // Group threads by date — must be before the early return to satisfy hooks rules.
  const groupedThreads = useMemo(() => {
    const groups: { label: string; items: Thread[] }[] = [];
    const today: Thread[] = [];
    const yesterday: Thread[] = [];
    const last7Days: Thread[] = [];
    const older: Thread[] = [];

    const now = new Date();
    const todayStart = new Date(now.getFullYear(), now.getMonth(), now.getDate());
    const yesterdayStart = new Date(todayStart);
    yesterdayStart.setDate(yesterdayStart.getDate() - 1);
    const last7DaysStart = new Date(todayStart);
    last7DaysStart.setDate(last7DaysStart.getDate() - 7);

    threads.forEach((th) => {
      const d = new Date(th.updated_at);
      if (d >= todayStart) today.push(th);
      else if (d >= yesterdayStart) yesterday.push(th);
      else if (d >= last7DaysStart) last7Days.push(th);
      else older.push(th);
    });

    if (today.length > 0) groups.push({ label: t("thread.today"), items: today });
    if (yesterday.length > 0) groups.push({ label: t("thread.yesterday"), items: yesterday });
    if (last7Days.length > 0) groups.push({ label: t("thread.previous7Days"), items: last7Days });
    if (older.length > 0) groups.push({ label: t("thread.older"), items: older });

    return groups;
  }, [threads, t]);

  if (!mobileDrawer && collapsed) return null;

  const mobileClass = mobileDrawer && !mobileOpen ? " mobile-hidden" : "";

  return (
    <div id="thread-panel" className={`thread-panel${mobileClass}`} role="navigation" aria-label={t("nav.chats")}>
      {perms.canEdit && (
        <div className="new-chat-btn-wrapper">
          <button
            onClick={onCreateThread}
            disabled={!connected}
            className="gemini-new-chat-btn"
          >
            <Plus size={20} />
            <span>{t("thread.newChat")}</span>
          </button>
        </div>
      )}

      <div className="thread-list">
        {threads.length === 0 ? (
          <div className="thread-empty">{t("thread.empty")}</div>
        ) : (
          groupedThreads.map((group) => (
            <div key={group.label} className="thread-group">
              <div className="thread-group-label">{group.label}</div>
              {group.items.map((th) => (
                <div
                  key={th.id}
                  className={`thread-item ${th.id === activeThreadId ? "active" : ""}`}
                  onClick={() => {
                    if (confirmId !== th.id) onSelectThread(th.id);
                  }}
                  onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); onSelectThread(th.id); } }}
                  title={th.title}
                  role="button"
                  tabIndex={0}
                  aria-current={th.id === activeThreadId ? "true" : undefined}
                >
                  <div className="thread-title">{th.title}</div>
                  
                  {perms.canEdit && confirmId === th.id ? (
                    <div className="thread-delete-confirm" ref={confirmRef}>
                      <button
                        className="thread-delete-yes"
                        onClick={(e) => {
                          e.stopPropagation();
                          setConfirmId(null);
                          onDeleteThread(th.id);
                        }}
                      >
                        <Check size={14} />
                      </button>
                    </div>
                  ) : perms.canEdit && th.id === activeThreadId ? (
                    <button
                      className="thread-delete-btn"
                      onClick={(e) => {
                        e.stopPropagation();
                        setConfirmId(th.id);
                      }}
                    >
                      <Trash2 size={14} />
                    </button>
                  ) : null}
                </div>
              ))}
            </div>
          ))
        )}
      </div>
    </div>
  );
}

function formatRelative(iso: string, t: (key: import("@/features/i18n").TKey) => string): string {
  if (!iso) return "";
  const d = new Date(iso);
  const now = new Date();
  const diffMs = now.getTime() - d.getTime();
  const diffMin = Math.floor(diffMs / 60000);
  if (diffMin < 1) return t("time.justNow");
  if (diffMin < 60) return `${diffMin}${t("time.mAgo")}`;
  const diffHr = Math.floor(diffMin / 60);
  if (diffHr < 24) return `${diffHr}${t("time.hAgo")}`;
  const diffDay = Math.floor(diffHr / 24);
  if (diffDay < 7) return `${diffDay}${t("time.dAgo")}`;
  return d.toLocaleDateString();
}
