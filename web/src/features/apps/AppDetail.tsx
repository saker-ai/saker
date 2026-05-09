"use client";

import { useState, useEffect } from "react";
import { ArrowLeft, Trash2 } from "lucide-react";
import { useT } from "@/features/i18n";
import { getApp, deleteApp, type AppMeta, type AppInputField, type AppOutputField } from "./appsApi";
import { AppRunner } from "./AppRunner";
import { AppPublishPanel } from "./AppPublishPanel";
import { AppKeysPanel } from "./AppKeysPanel";
import { AppSharePanel } from "./AppSharePanel";

type AppWithSchema = AppMeta & { inputs?: AppInputField[]; outputs?: AppOutputField[] };

type Tab = "run" | "publish" | "keys" | "share";

interface Props {
  appId: string;
  onBack: () => void;
  onDeleted: () => void;
}

export function AppDetail({ appId, onBack, onDeleted }: Props) {
  const { t } = useT();
  const [app, setApp] = useState<AppWithSchema | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>("run");
  const [deleting, setDeleting] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);

  const load = () => {
    setLoading(true);
    setError(null);
    getApp(appId)
      .then(setApp)
      .catch((err) => setError(err instanceof Error ? err.message : String(err)))
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [appId]);

  const handleDelete = async () => {
    if (!confirmDelete) {
      setConfirmDelete(true);
      return;
    }
    setDeleting(true);
    try {
      await deleteApp(appId);
      onDeleted();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setDeleting(false);
      setConfirmDelete(false);
    }
  };

  const headerStyle: React.CSSProperties = {
    display: "flex",
    alignItems: "center",
    gap: 12,
    padding: "16px 24px",
    borderBottom: "1px solid var(--color-border, #333)",
    background: "var(--color-surface, #111)",
  };

  const tabBarStyle: React.CSSProperties = {
    display: "flex",
    gap: 0,
    padding: "0 24px",
    borderBottom: "1px solid var(--color-border, #333)",
    background: "var(--color-surface, #111)",
  };

  const tabStyle = (active: boolean): React.CSSProperties => ({
    padding: "10px 16px",
    border: "none",
    background: "none",
    cursor: "pointer",
    fontSize: "0.875rem",
    fontWeight: active ? 600 : 400,
    color: active ? "var(--color-accent, #7c6af7)" : "var(--color-text-secondary, #888)",
    borderBottom: active ? "2px solid var(--color-accent, #7c6af7)" : "2px solid transparent",
    marginBottom: -1,
  });

  if (loading) {
    return (
      <div className="app-content" style={{ padding: 24, color: "var(--color-text-secondary, #888)" }}>
        Loading...
      </div>
    );
  }

  if (error || !app) {
    return (
      <div className="app-content" style={{ padding: 24 }}>
        <button
          onClick={onBack}
          style={{ background: "none", border: "none", cursor: "pointer", display: "flex", alignItems: "center", gap: 6, color: "var(--color-text-secondary, #888)", marginBottom: 16 }}
        >
          <ArrowLeft size={16} />
          {t("apps.back")}
        </button>
        <div style={{ color: "var(--color-error, #e05)" }}>{error ?? "App not found"}</div>
      </div>
    );
  }

  return (
    <div className="app-content" style={{ display: "flex", flexDirection: "column", height: "100%", overflow: "hidden" }}>
      {/* Header */}
      <div style={headerStyle}>
        <button
          onClick={onBack}
          style={{
            background: "none",
            border: "none",
            cursor: "pointer",
            display: "flex",
            alignItems: "center",
            color: "var(--color-text-secondary, #888)",
            padding: 4,
            borderRadius: 4,
          }}
          aria-label={t("apps.back")}
        >
          <ArrowLeft size={18} strokeWidth={2} />
        </button>

        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ fontWeight: 600, fontSize: "1rem", color: "var(--color-text, #eee)" }}>
            {app.icon && <span style={{ marginRight: 8 }}>{app.icon}</span>}
            {app.name}
          </div>
          {app.description && (
            <div style={{ fontSize: "0.8125rem", color: "var(--color-text-secondary, #888)", marginTop: 2 }}>
              {app.description}
            </div>
          )}
        </div>

        {/* Delete button */}
        {confirmDelete ? (
          <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
            <span style={{ fontSize: "0.8125rem", color: "var(--color-error, #e05)" }}>
              {t("apps.confirmDelete")}
            </span>
            <button
              onClick={handleDelete}
              disabled={deleting}
              style={{
                padding: "4px 12px",
                borderRadius: 5,
                background: "var(--color-error, #e05)",
                color: "#fff",
                border: "none",
                cursor: "pointer",
                fontSize: "0.8125rem",
              }}
            >
              {deleting ? "..." : t("apps.delete")}
            </button>
            <button
              onClick={() => setConfirmDelete(false)}
              style={{
                padding: "4px 10px",
                borderRadius: 5,
                background: "none",
                color: "var(--color-text-secondary, #888)",
                border: "1px solid var(--color-border, #333)",
                cursor: "pointer",
                fontSize: "0.8125rem",
              }}
            >
              Cancel
            </button>
          </div>
        ) : (
          <button
            onClick={handleDelete}
            style={{
              background: "none",
              border: "none",
              cursor: "pointer",
              color: "var(--color-text-secondary, #888)",
              padding: 4,
              borderRadius: 4,
              display: "flex",
              alignItems: "center",
            }}
            aria-label={t("apps.delete")}
            title={t("apps.delete")}
          >
            <Trash2 size={16} strokeWidth={1.75} />
          </button>
        )}
      </div>

      {/* Tab bar */}
      <div style={tabBarStyle}>
        <button style={tabStyle(tab === "run")} onClick={() => setTab("run")}>
          {t("apps.tabRun")}
        </button>
        <button style={tabStyle(tab === "publish")} onClick={() => setTab("publish")}>
          {t("apps.tabPublish")}
        </button>
        <button style={tabStyle(tab === "keys")} onClick={() => setTab("keys")}>
          {t("apps.tabKeys")}
        </button>
        <button style={tabStyle(tab === "share")} onClick={() => setTab("share")}>
          {t("apps.tabShare")}
        </button>
      </div>

      {/* Tab content */}
      <div style={{ flex: 1, overflow: "auto", padding: 24 }}>
        {tab === "run" ? (
          <AppRunner app={app} />
        ) : tab === "publish" ? (
          <AppPublishPanel app={app} onPublished={load} />
        ) : tab === "keys" ? (
          <AppKeysPanel appId={app.id} inputs={app.inputs} />
        ) : (
          <AppSharePanel appId={app.id} />
        )}
      </div>
    </div>
  );
}
