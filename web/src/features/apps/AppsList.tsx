"use client";

import { useState, useEffect } from "react";
import { Plus, Trash2 } from "lucide-react";
import { useT } from "@/features/i18n";
import { useAppsStore } from "./appsStore";
import { createApp } from "./appsApi";
import { httpRequest } from "@/features/rpc/httpRpc";
import type { Thread } from "@/features/rpc/types";

interface Props {
  onOpen: (appId: string) => void;
}

interface CreateForm {
  name: string;
  description: string;
  sourceThreadId: string;
}

export function AppsList({ onOpen }: Props) {
  const { t } = useT();
  const { apps, loading, error, refresh, remove } = useAppsStore();

  const [showCreate, setShowCreate] = useState(false);
  const [form, setForm] = useState<CreateForm>({ name: "", description: "", sourceThreadId: "" });
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  const [confirmDeleteId, setConfirmDeleteId] = useState<string | null>(null);

  // Thread picker state
  const [threads, setThreads] = useState<Thread[]>([]);
  const [threadsLoading, setThreadsLoading] = useState(false);

  // Fetch threads when modal opens
  useEffect(() => {
    if (!showCreate) return;
    setThreadsLoading(true);
    httpRequest<{ threads: Thread[] }>("thread/list")
      .then((res) => {
        const sorted = [...(res.threads ?? [])].sort(
          (a, b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime(),
        );
        setThreads(sorted);
        // Default-select the first thread
        if (sorted.length > 0) {
          setForm((f) => ({ ...f, sourceThreadId: sorted[0].id }));
        }
      })
      .catch(() => {
        setThreads([]);
      })
      .finally(() => setThreadsLoading(false));
  }, [showCreate]);

  const handleCreate = async () => {
    if (!form.name.trim() || !form.sourceThreadId.trim()) return;
    setCreating(true);
    setCreateError(null);
    try {
      const app = await createApp({
        name: form.name.trim(),
        description: form.description.trim() || undefined,
        sourceThreadId: form.sourceThreadId.trim(),
      });
      await refresh();
      setShowCreate(false);
      setForm({ name: "", description: "", sourceThreadId: "" });
      onOpen(app.id);
    } catch (err) {
      setCreateError(err instanceof Error ? err.message : String(err));
    } finally {
      setCreating(false);
    }
  };

  const handleDelete = async (appId: string) => {
    if (confirmDeleteId !== appId) {
      setConfirmDeleteId(appId);
      return;
    }
    try {
      await remove(appId);
    } catch {
      // error is surfaced in store
    }
    setConfirmDeleteId(null);
  };

  const inputStyle: React.CSSProperties = {
    width: "100%",
    padding: "7px 10px",
    borderRadius: 6,
    border: "1px solid var(--color-border, #333)",
    background: "var(--color-input-bg, #1a1a1a)",
    color: "var(--color-text, #eee)",
    fontSize: "0.875rem",
    boxSizing: "border-box",
    marginTop: 4,
  };

  const fieldLabel: React.CSSProperties = {
    display: "block",
    fontSize: "0.8125rem",
    fontWeight: 500,
    color: "var(--color-text-secondary, #888)",
    marginBottom: 12,
  };

  return (
    <div>
      {/* Header */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          marginBottom: 24,
        }}
      >
        <h1
          style={{
            margin: 0,
            fontSize: "1.25rem",
            fontWeight: 600,
            color: "var(--color-text, #eee)",
          }}
        >
          {t("apps.title")}
        </h1>
        <button
          onClick={() => { setShowCreate(true); setCreateError(null); }}
          style={{
            display: "flex",
            alignItems: "center",
            gap: 6,
            padding: "7px 14px",
            borderRadius: 6,
            background: "var(--color-accent, #7c6af7)",
            color: "#fff",
            border: "none",
            cursor: "pointer",
            fontSize: "0.875rem",
            fontWeight: 500,
          }}
        >
          <Plus size={16} strokeWidth={2} />
          {t("apps.create")}
        </button>
      </div>

      {/* Create modal */}
      {showCreate && (
        <div
          style={{
            position: "fixed",
            inset: 0,
            background: "rgba(0,0,0,0.6)",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            zIndex: 1000,
          }}
          onClick={(e) => { if (e.target === e.currentTarget) setShowCreate(false); }}
        >
          <div
            style={{
              background: "var(--color-bg, #0d0d0d)",
              border: "1px solid var(--color-border, #333)",
              borderRadius: 12,
              padding: 28,
              width: 420,
              maxWidth: "90vw",
            }}
          >
            <h2
              style={{
                margin: "0 0 20px",
                fontSize: "1rem",
                fontWeight: 600,
                color: "var(--color-text, #eee)",
              }}
            >
              {t("apps.create")}
            </h2>

            <label style={fieldLabel}>
              {t("apps.name")} <span style={{ color: "var(--color-error, #e05)" }}>*</span>
              <input
                type="text"
                style={inputStyle}
                value={form.name}
                onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
                placeholder={t("apps.name")}
                autoFocus
              />
            </label>

            <label style={fieldLabel}>
              {t("apps.description")}
              <input
                type="text"
                style={inputStyle}
                value={form.description}
                onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
                placeholder={t("apps.description")}
              />
            </label>

            <label style={fieldLabel}>
              {t("apps.threadPicker")} <span style={{ color: "var(--color-error, #e05)" }}>*</span>
              {threadsLoading ? (
                <div
                  style={{
                    marginTop: 4,
                    fontSize: "0.8125rem",
                    color: "var(--color-text-secondary, #888)",
                    padding: "7px 0",
                  }}
                >
                  {t("apps.threadsLoading")}
                </div>
              ) : threads.length === 0 ? (
                <div
                  style={{
                    marginTop: 4,
                    fontSize: "0.8125rem",
                    color: "var(--color-text-secondary, #888)",
                    padding: "7px 0",
                  }}
                >
                  {t("apps.threadsEmpty")}
                </div>
              ) : (
                <select
                  style={{ ...inputStyle, cursor: "pointer" }}
                  value={form.sourceThreadId}
                  onChange={(e) => setForm((f) => ({ ...f, sourceThreadId: e.target.value }))}
                >
                  {threads.map((th) => (
                    <option key={th.id} value={th.id}>
                      {th.title || th.id}
                    </option>
                  ))}
                </select>
              )}
            </label>

            {createError && (
              <div
                style={{
                  color: "var(--color-error, #e05)",
                  fontSize: "0.8125rem",
                  marginBottom: 12,
                  padding: "7px 10px",
                  background: "rgba(220,0,80,0.08)",
                  borderRadius: 6,
                  border: "1px solid rgba(220,0,80,0.2)",
                }}
              >
                {createError}
              </div>
            )}

            <div style={{ display: "flex", gap: 10, justifyContent: "flex-end", marginTop: 4 }}>
              <button
                onClick={() => setShowCreate(false)}
                style={{
                  padding: "7px 16px",
                  borderRadius: 6,
                  border: "1px solid var(--color-border, #333)",
                  background: "none",
                  color: "var(--color-text-secondary, #888)",
                  cursor: "pointer",
                  fontSize: "0.875rem",
                }}
              >
                Cancel
              </button>
              <button
                onClick={handleCreate}
                disabled={creating || !form.name.trim() || !form.sourceThreadId.trim()}
                style={{
                  padding: "7px 16px",
                  borderRadius: 6,
                  background: "var(--color-accent, #7c6af7)",
                  color: "#fff",
                  border: "none",
                  cursor: creating ? "not-allowed" : "pointer",
                  opacity: creating || !form.name.trim() || !form.sourceThreadId.trim() ? 0.6 : 1,
                  fontSize: "0.875rem",
                  fontWeight: 500,
                }}
              >
                {creating ? `${t("apps.create")}...` : t("apps.create")}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Error from store */}
      {error && (
        <div
          style={{
            color: "var(--color-error, #e05)",
            fontSize: "0.8125rem",
            marginBottom: 16,
            padding: "8px 12px",
            background: "rgba(220,0,80,0.08)",
            borderRadius: 6,
          }}
        >
          {error}
        </div>
      )}

      {/* Loading */}
      {loading && (
        <div style={{ color: "var(--color-text-secondary, #888)", fontSize: "0.875rem" }}>
          Loading...
        </div>
      )}

      {/* Empty state */}
      {!loading && apps.length === 0 && (
        <div
          style={{
            textAlign: "center",
            padding: "64px 24px",
            color: "var(--color-text-secondary, #888)",
            fontSize: "0.875rem",
          }}
        >
          {t("apps.empty")}
        </div>
      )}

      {/* App cards grid */}
      {!loading && apps.length > 0 && (
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "repeat(auto-fill, minmax(260px, 1fr))",
            gap: 16,
          }}
        >
          {apps.map((app) => (
            <div
              key={app.id}
              style={{
                border: "1px solid var(--color-border, #333)",
                borderRadius: 10,
                padding: "16px 18px",
                background: "var(--color-surface, #111)",
                display: "flex",
                flexDirection: "column",
                gap: 8,
                position: "relative",
              }}
            >
              <div style={{ display: "flex", alignItems: "flex-start", justifyContent: "space-between", gap: 8 }}>
                <div style={{ minWidth: 0 }}>
                  <div style={{ fontWeight: 600, fontSize: "0.9375rem", color: "var(--color-text, #eee)" }}>
                    {app.icon && <span style={{ marginRight: 6 }}>{app.icon}</span>}
                    {app.name}
                  </div>
                  {app.description && (
                    <div
                      style={{
                        fontSize: "0.8125rem",
                        color: "var(--color-text-secondary, #888)",
                        marginTop: 4,
                        overflow: "hidden",
                        textOverflow: "ellipsis",
                        display: "-webkit-box",
                        WebkitLineClamp: 2,
                        WebkitBoxOrient: "vertical",
                      }}
                    >
                      {app.description}
                    </div>
                  )}
                </div>

                {confirmDeleteId === app.id ? (
                  <div style={{ display: "flex", gap: 6, flexShrink: 0 }}>
                    <button
                      onClick={() => handleDelete(app.id)}
                      style={{
                        padding: "3px 10px",
                        borderRadius: 5,
                        background: "var(--color-error, #e05)",
                        color: "#fff",
                        border: "none",
                        cursor: "pointer",
                        fontSize: "0.75rem",
                      }}
                    >
                      {t("apps.delete")}
                    </button>
                    <button
                      onClick={() => setConfirmDeleteId(null)}
                      style={{
                        padding: "3px 8px",
                        borderRadius: 5,
                        background: "none",
                        border: "1px solid var(--color-border, #333)",
                        color: "var(--color-text-secondary, #888)",
                        cursor: "pointer",
                        fontSize: "0.75rem",
                      }}
                    >
                      ✕
                    </button>
                  </div>
                ) : (
                  <button
                    onClick={() => handleDelete(app.id)}
                    style={{
                      background: "none",
                      border: "none",
                      cursor: "pointer",
                      color: "var(--color-text-secondary, #888)",
                      padding: 4,
                      borderRadius: 4,
                      flexShrink: 0,
                    }}
                    aria-label={t("apps.delete")}
                    title={t("apps.confirmDelete")}
                  >
                    <Trash2 size={15} strokeWidth={1.75} />
                  </button>
                )}
              </div>

              <div style={{ fontSize: "0.75rem", color: "var(--color-text-secondary, #888)" }}>
                {app.publishedVersion
                  ? `v${app.publishedVersion}`
                  : t("apps.notPublished")}
              </div>

              <button
                onClick={() => onOpen(app.id)}
                style={{
                  marginTop: 4,
                  padding: "6px 14px",
                  borderRadius: 6,
                  background: "var(--color-accent, #7c6af7)",
                  color: "#fff",
                  border: "none",
                  cursor: "pointer",
                  fontSize: "0.8125rem",
                  fontWeight: 500,
                  alignSelf: "flex-start",
                }}
              >
                Open
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
