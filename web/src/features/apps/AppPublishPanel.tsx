"use client";

import { useState, useEffect } from "react";
import { useT } from "@/features/i18n";
import {
  publishApp,
  listVersions,
  setPublishedVersion,
  type AppMeta,
  type AppInputField,
  type AppOutputField,
  type AppVersionSummary,
} from "./appsApi";

interface Props {
  app: AppMeta & { inputs?: AppInputField[]; outputs?: AppOutputField[] };
  onPublished: () => void;
}

function formatDate(iso: string): string {
  try {
    return new Date(iso).toLocaleString();
  } catch {
    return iso;
  }
}

export function AppPublishPanel({ app, onPublished }: Props) {
  const { t } = useT();
  const [publishing, setPublishing] = useState(false);
  const [publishError, setPublishError] = useState<string | null>(null);
  const [versions, setVersions] = useState<AppVersionSummary[]>([]);
  const [loadingVersions, setLoadingVersions] = useState(false);
  const [confirmRollbackVersion, setConfirmRollbackVersion] = useState<string | null>(null);
  const [rollingBack, setRollingBack] = useState<string | null>(null);
  const [rollbackError, setRollbackError] = useState<string | null>(null);

  useEffect(() => {
    setLoadingVersions(true);
    listVersions(app.id)
      .then(setVersions)
      .catch(() => {})
      .finally(() => setLoadingVersions(false));
  }, [app.id]);

  const handlePublish = async () => {
    setPublishing(true);
    setPublishError(null);
    try {
      await publishApp(app.id);
      onPublished();
      // Refresh version list
      const vs = await listVersions(app.id);
      setVersions(vs);
    } catch (err) {
      setPublishError(err instanceof Error ? err.message : String(err));
    } finally {
      setPublishing(false);
    }
  };

  const handleRollback = async (version: string) => {
    if (confirmRollbackVersion !== version) {
      setConfirmRollbackVersion(version);
      setRollbackError(null);
      return;
    }
    setRollingBack(version);
    setRollbackError(null);
    try {
      await setPublishedVersion(app.id, version);
      setConfirmRollbackVersion(null);
      onPublished();
    } catch (err) {
      setRollbackError(err instanceof Error ? err.message : String(err));
    } finally {
      setRollingBack(null);
    }
  };

  const labelStyle: React.CSSProperties = {
    fontSize: "0.8125rem",
    fontWeight: 500,
    color: "var(--color-text-secondary, #888)",
    marginBottom: 4,
  };

  const valueStyle: React.CSSProperties = {
    fontSize: "0.875rem",
    marginBottom: 16,
  };

  return (
    <div>
      <div style={{ marginBottom: 20 }}>
        <div style={labelStyle}>{t("apps.publishedVersion")}</div>
        <div style={valueStyle}>
          {app.publishedVersion || (
            <span style={{ color: "var(--color-text-secondary, #888)" }}>
              {t("apps.notPublished")}
            </span>
          )}
        </div>

        {/* Preview inputs/outputs from the current state */}
        {(app.inputs?.length ?? 0) > 0 && (
          <>
            <div style={labelStyle}>{t("apps.inputs")}</div>
            <div style={{ marginBottom: 16 }}>
              {app.inputs!.map((f) => (
                <div
                  key={f.nodeId}
                  style={{
                    fontSize: "0.8125rem",
                    padding: "4px 0",
                    color: "var(--color-text, #eee)",
                  }}
                >
                  <span style={{ fontWeight: 500 }}>{f.label}</span>
                  <span style={{ color: "var(--color-text-secondary, #888)", marginLeft: 8 }}>
                    ({f.type}
                    {f.required ? ", required" : ""})
                  </span>
                </div>
              ))}
            </div>
          </>
        )}

        {(app.outputs?.length ?? 0) > 0 && (
          <>
            <div style={labelStyle}>{t("apps.outputs")}</div>
            <div style={{ marginBottom: 16 }}>
              {app.outputs!.map((o) => (
                <div
                  key={o.sourceRef}
                  style={{
                    fontSize: "0.8125rem",
                    padding: "4px 0",
                    color: "var(--color-text, #eee)",
                  }}
                >
                  <span style={{ fontWeight: 500 }}>{o.label}</span>
                  <span style={{ color: "var(--color-text-secondary, #888)", marginLeft: 8 }}>
                    ({o.kind})
                  </span>
                </div>
              ))}
            </div>
          </>
        )}
      </div>

      <button
        onClick={handlePublish}
        disabled={publishing}
        style={{
          padding: "8px 20px",
          borderRadius: 6,
          background: "var(--color-accent, #7c6af7)",
          color: "#fff",
          border: "none",
          cursor: publishing ? "not-allowed" : "pointer",
          opacity: publishing ? 0.7 : 1,
          fontSize: "0.875rem",
          fontWeight: 500,
          marginBottom: 8,
        }}
      >
        {publishing ? `${t("apps.publish")}...` : t("apps.publish")}
      </button>

      {publishError && (
        <div
          style={{
            color: "var(--color-error, #e05)",
            fontSize: "0.8125rem",
            marginTop: 8,
            marginBottom: 16,
            padding: "8px 12px",
            background: "rgba(220,0,80,0.08)",
            borderRadius: 6,
            border: "1px solid rgba(220,0,80,0.2)",
          }}
        >
          {publishError}
        </div>
      )}

      {/* Version history */}
      <div style={{ marginTop: 28 }}>
        <div
          style={{
            fontSize: "0.8125rem",
            fontWeight: 600,
            color: "var(--color-text-secondary, #888)",
            marginBottom: 10,
            textTransform: "uppercase",
            letterSpacing: "0.05em",
          }}
        >
          {t("apps.tabPublish")} History
        </div>
        {loadingVersions ? (
          <div style={{ color: "var(--color-text-secondary, #888)", fontSize: "0.8125rem" }}>
            Loading...
          </div>
        ) : versions.length === 0 ? (
          <div style={{ color: "var(--color-text-secondary, #888)", fontSize: "0.8125rem" }}>
            {t("apps.notPublished")}
          </div>
        ) : (
          <div
            style={{
              border: "1px solid var(--color-border, #333)",
              borderRadius: 8,
              overflow: "hidden",
            }}
          >
            {versions.map((v, i) => {
              const isCurrent = v.version === app.publishedVersion;
              const isConfirming = confirmRollbackVersion === v.version;
              const isWorking = rollingBack === v.version;
              return (
                <div
                  key={v.version}
                  style={{
                    padding: "10px 14px",
                    display: "flex",
                    justifyContent: "space-between",
                    alignItems: "center",
                    borderBottom:
                      i < versions.length - 1
                        ? "1px solid var(--color-border, #333)"
                        : "none",
                    background: "var(--color-surface, #111)",
                  }}
                >
                  <div style={{ minWidth: 0 }}>
                    <span
                      style={{
                        fontFamily: "monospace",
                        fontSize: "0.8125rem",
                        color: "var(--color-text, #eee)",
                      }}
                    >
                      {v.version}
                    </span>
                    <span
                      style={{
                        fontSize: "0.75rem",
                        color: "var(--color-text-secondary, #888)",
                        marginLeft: 10,
                      }}
                    >
                      {formatDate(v.publishedAt)}
                      {v.publishedBy && ` · ${v.publishedBy}`}
                    </span>
                  </div>

                  <div style={{ display: "flex", alignItems: "center", gap: 6, flexShrink: 0 }}>
                    {isCurrent ? (
                      <span
                        style={{
                          fontSize: "0.75rem",
                          padding: "2px 8px",
                          borderRadius: 4,
                          background: "var(--color-accent, #7c6af7)",
                          color: "#fff",
                        }}
                      >
                        {t("apps.rollbackCurrent")}
                      </span>
                    ) : isConfirming ? (
                      <>
                        <button
                          onClick={() => handleRollback(v.version)}
                          disabled={isWorking}
                          style={{
                            padding: "3px 10px",
                            borderRadius: 5,
                            background: "var(--color-error, #e05)",
                            color: "#fff",
                            border: "none",
                            cursor: isWorking ? "not-allowed" : "pointer",
                            fontSize: "0.75rem",
                            opacity: isWorking ? 0.7 : 1,
                          }}
                        >
                          {isWorking ? `${t("apps.rollback")}...` : t("apps.rollbackConfirm")}
                        </button>
                        <button
                          onClick={() => { setConfirmRollbackVersion(null); setRollbackError(null); }}
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
                      </>
                    ) : (
                      <button
                        onClick={() => handleRollback(v.version)}
                        style={{
                          padding: "3px 10px",
                          borderRadius: 5,
                          background: "none",
                          border: "1px solid var(--color-border, #333)",
                          color: "var(--color-text-secondary, #888)",
                          cursor: "pointer",
                          fontSize: "0.75rem",
                        }}
                      >
                        {t("apps.rollback")}
                      </button>
                    )}
                  </div>
                </div>
              );
            })}
          </div>
        )}
        {rollbackError && (
          <div
            style={{
              color: "var(--color-error, #e05)",
              fontSize: "0.8125rem",
              marginTop: 8,
              padding: "7px 10px",
              background: "rgba(220,0,80,0.08)",
              borderRadius: 6,
              border: "1px solid rgba(220,0,80,0.2)",
            }}
          >
            {rollbackError}
          </div>
        )}
      </div>
    </div>
  );
}
