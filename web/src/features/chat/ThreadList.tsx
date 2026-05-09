import { useState, useRef, useEffect } from "react";
import type { Thread } from "@/features/rpc/types";

interface Props {
  threads: Thread[];
  activeThreadId: string;
  onSelect: (id: string) => void;
  onCreate: () => void;
  onDelete: (id: string) => void;
  connected: boolean;
}

export function ThreadList({
  threads,
  activeThreadId,
  onSelect,
  onCreate,
  onDelete,
  connected,
}: Props) {
  const [confirmId, setConfirmId] = useState<string | null>(null);
  const confirmRef = useRef<HTMLDivElement>(null);

  // Click outside to cancel confirm
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

  return (
    <div className="sidebar">
      <div className="sidebar-header">
        <h2>Saker</h2>
        <button onClick={onCreate} disabled={!connected}>
          + New
        </button>
      </div>
      <div className="thread-list">
        {threads.length === 0 && (
          <div className="thread-empty">No conversations yet</div>
        )}
        {threads.map((t) => (
          <div
            key={t.id}
            className={`thread-item ${t.id === activeThreadId ? "active" : ""}`}
            onClick={() => {
              if (confirmId !== t.id) onSelect(t.id);
            }}
            title={t.title}
          >
            <div className="thread-item-content">
              <div className="thread-title">{t.title}</div>
              <div className="thread-time">{formatRelative(t.updated_at)}</div>
            </div>
            {confirmId === t.id ? (
              <div className="thread-delete-confirm" ref={confirmRef}>
                <button
                  className="thread-delete-yes"
                  onClick={(e) => {
                    e.stopPropagation();
                    setConfirmId(null);
                    onDelete(t.id);
                  }}
                >
                  确认
                </button>
                <button
                  className="thread-delete-no"
                  onClick={(e) => {
                    e.stopPropagation();
                    setConfirmId(null);
                  }}
                >
                  取消
                </button>
              </div>
            ) : (
              <button
                className="thread-delete-btn"
                onClick={(e) => {
                  e.stopPropagation();
                  setConfirmId(t.id);
                }}
                title="删除对话"
              >
                ×
              </button>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

function formatRelative(iso: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  const now = new Date();
  const diffMs = now.getTime() - d.getTime();
  const diffMin = Math.floor(diffMs / 60000);
  if (diffMin < 1) return "just now";
  if (diffMin < 60) return `${diffMin}m ago`;
  const diffHr = Math.floor(diffMin / 60);
  if (diffHr < 24) return `${diffHr}h ago`;
  const diffDay = Math.floor(diffHr / 24);
  if (diffDay < 7) return `${diffDay}d ago`;
  return d.toLocaleDateString();
}
