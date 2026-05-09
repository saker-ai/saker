"use client";

import { useState, useEffect, useCallback } from "react";
import { Play, X } from "lucide-react";
import { I18nProvider, useT } from "@/features/i18n";
import type { AppInputField, AppOutputField } from "@/features/apps/appsApi";
import { useRunPolling } from "@/features/apps/useRunPolling";
import {
  fetchSchema,
  runPublic,
  getRun,
  cancelRunPublic,
  PublicApiError,
  type PublicSchema,
  type RunSummary,
} from "./publicApi";

// ---------- Icon renderer ----------

function AppIcon({ icon }: { icon: string }) {
  const isUrl = icon.length > 4 || icon.startsWith("http");
  if (isUrl) {
    return (
      <img
        src={icon}
        alt="App icon"
        style={{
          width: 56,
          height: 56,
          borderRadius: 12,
          objectFit: "cover",
          border: "1px solid var(--color-border, #333)",
        }}
      />
    );
  }
  return (
    <div
      style={{
        fontSize: 48,
        lineHeight: 1,
        userSelect: "none",
      }}
      aria-hidden="true"
    >
      {icon}
    </div>
  );
}

// ---------- Output renderer (mirrors AppRunner.tsx) ----------

interface OutputRendererProps {
  output: AppOutputField;
  nodes: RunSummary["nodes"];
}

function OutputRenderer({ output, nodes }: OutputRendererProps) {
  const node = nodes.find(
    (n) => n.nodeId === output.sourceRef || n.resultNodeId === output.sourceRef,
  );
  const url = node?.resultUrl ?? "";

  const containerStyle: React.CSSProperties = { marginBottom: 16 };
  const labelStyle: React.CSSProperties = {
    fontSize: "0.8125rem",
    fontWeight: 500,
    color: "var(--color-text-secondary, #888)",
    marginBottom: 6,
  };

  if (!url) {
    return (
      <div style={containerStyle}>
        <div style={labelStyle}>{output.label}</div>
        <div style={{ color: "var(--color-text-secondary, #888)", fontSize: "0.8rem" }}>
          —
        </div>
      </div>
    );
  }

  return (
    <div style={containerStyle}>
      <div style={labelStyle}>{output.label}</div>
      {output.kind === "image" && (
        <img
          src={url}
          alt={output.label}
          style={{
            maxWidth: "100%",
            borderRadius: 8,
            border: "1px solid var(--color-border, #333)",
          }}
        />
      )}
      {output.kind === "video" && (
        <video controls src={url} style={{ maxWidth: "100%", borderRadius: 8 }} />
      )}
      {output.kind === "audio" && (
        <audio controls src={url} style={{ width: "100%" }} />
      )}
      {output.kind === "text" && (
        <pre
          style={{
            background: "var(--color-surface, #111)",
            border: "1px solid var(--color-border, #333)",
            borderRadius: 6,
            padding: 12,
            fontSize: "0.8125rem",
            whiteSpace: "pre-wrap",
            wordBreak: "break-word",
            margin: 0,
          }}
        >
          {url}
        </pre>
      )}
    </div>
  );
}

// ---------- Public-safe form field (no auth, file type disabled) ----------

interface FieldProps {
  field: AppInputField;
  value: unknown;
  onChange: (v: unknown) => void;
  fileDisabledLabel: string;
}

function PublicFormField({ field, value, onChange, fileDisabledLabel }: FieldProps) {
  const labelEl = (
    <label
      style={{
        display: "block",
        fontSize: "0.8125rem",
        fontWeight: 500,
        marginBottom: 4,
        color: "var(--color-text-secondary, #888)",
      }}
    >
      {field.label}
      {field.required && (
        <span style={{ color: "var(--color-error, #e05)", marginLeft: 4 }}>*</span>
      )}
    </label>
  );

  const inputStyle: React.CSSProperties = {
    width: "100%",
    padding: "6px 10px",
    borderRadius: 6,
    border: "1px solid var(--color-border, #333)",
    background: "var(--color-input-bg, #1a1a1a)",
    color: "var(--color-text, #eee)",
    fontSize: "0.875rem",
    boxSizing: "border-box",
  };

  switch (field.type) {
    case "text":
      return (
        <div style={{ marginBottom: 12 }}>
          {labelEl}
          <input
            type="text"
            style={inputStyle}
            value={typeof value === "string" ? value : (field.default as string) ?? ""}
            onChange={(e) => onChange(e.target.value)}
            placeholder={field.label}
          />
        </div>
      );

    case "paragraph":
      return (
        <div style={{ marginBottom: 12 }}>
          {labelEl}
          <textarea
            style={{ ...inputStyle, minHeight: 80, resize: "vertical" }}
            value={typeof value === "string" ? value : (field.default as string) ?? ""}
            onChange={(e) => onChange(e.target.value)}
            placeholder={field.label}
          />
        </div>
      );

    case "number":
      return (
        <div style={{ marginBottom: 12 }}>
          {labelEl}
          <input
            type="number"
            style={inputStyle}
            value={typeof value === "number" ? value : (field.default as number) ?? ""}
            min={field.min}
            max={field.max}
            onChange={(e) => onChange(e.target.valueAsNumber)}
          />
        </div>
      );

    case "select":
      return (
        <div style={{ marginBottom: 12 }}>
          {labelEl}
          <select
            style={inputStyle}
            value={typeof value === "string" ? value : (field.default as string) ?? ""}
            onChange={(e) => onChange(e.target.value)}
          >
            {!field.required && <option value="">—</option>}
            {(field.options ?? []).map((opt) => (
              <option key={opt} value={opt}>
                {opt}
              </option>
            ))}
          </select>
        </div>
      );

    case "file":
      return (
        <div style={{ marginBottom: 12 }}>
          {labelEl}
          <input
            type="text"
            disabled
            style={{ ...inputStyle, opacity: 0.5, cursor: "not-allowed" }}
            placeholder={fileDisabledLabel}
          />
        </div>
      );

    default:
      return null;
  }
}

// ---------- Rate-limit countdown ----------

function RateLimitBanner({
  retryAfter,
  label,
}: {
  retryAfter: number;
  label: string;
}) {
  const [remaining, setRemaining] = useState(retryAfter);

  useEffect(() => {
    if (remaining <= 0) return;
    const id = setInterval(() => {
      setRemaining((prev) => {
        if (prev <= 1) {
          clearInterval(id);
          return 0;
        }
        return prev - 1;
      });
    }, 1000);
    return () => clearInterval(id);
  }, [remaining]);

  const text = label.replace("{n}", String(remaining));

  return (
    <div
      style={{
        color: "var(--color-error, #e05)",
        fontSize: "0.8125rem",
        marginBottom: 16,
        padding: "8px 12px",
        background: "rgba(220,0,80,0.08)",
        borderRadius: 6,
        border: "1px solid rgba(220,0,80,0.2)",
      }}
    >
      {text}
    </div>
  );
}

// ---------- Spinner ----------

function Spinner() {
  return (
    <span
      style={{
        display: "inline-block",
        width: 14,
        height: 14,
        border: "2px solid rgba(255,255,255,0.3)",
        borderTopColor: "#fff",
        borderRadius: "50%",
        animation: "share-spin 0.7s linear infinite",
      }}
    />
  );
}

// ---------- Shared layout constants ----------

const centreWrap: React.CSSProperties = {
  minHeight: "100vh",
  display: "flex",
  flexDirection: "column",
  alignItems: "center",
  justifyContent: "flex-start",
  padding: "48px 16px 32px",
  background: "var(--color-bg, #0d0d0d)",
  color: "var(--color-text, #eee)",
  fontFamily: "var(--font-sans, system-ui, sans-serif)",
};

const card: React.CSSProperties = {
  width: "100%",
  maxWidth: 600,
  background: "var(--color-surface, #111)",
  border: "1px solid var(--color-border, #222)",
  borderRadius: 12,
  padding: "28px 24px",
};

// ---------- Spinner keyframe injection ----------
if (typeof document !== "undefined") {
  const styleId = "share-spin-style";
  if (!document.getElementById(styleId)) {
    const style = document.createElement("style");
    style.id = styleId;
    style.textContent = `@keyframes share-spin { to { transform: rotate(360deg); } }`;
    document.head.appendChild(style);
  }
}

// ---------- Footer ----------

function Footer({ label }: { label: string }) {
  return (
    <div
      style={{
        marginTop: 24,
        textAlign: "center",
        fontSize: "0.75rem",
        color: "var(--color-text-secondary, #666)",
      }}
    >
      {label}
    </div>
  );
}

// ---------- Main inner content ----------

interface SharePageContentProps {
  token: string;
  projectId: string | null;
}

function SharePageContent({ token, projectId }: SharePageContentProps) {
  const { t } = useT();

  const [schema, setSchema] = useState<PublicSchema | null>(null);
  const [loadError, setLoadError] = useState<"notFound" | "rateLimited" | "error" | null>(null);
  const [loadRetryAfter, setLoadRetryAfter] = useState(0);

  const [values, setValues] = useState<Record<string, unknown>>({});
  const [running, setRunning] = useState(false);
  const [runError, setRunError] = useState<string | null>(null);
  const [runRetryAfter, setRunRetryAfter] = useState(0);
  const [summary, setSummary] = useState<RunSummary | null>(null);
  const [activeRunId, setActiveRunId] = useState<string | null>(null);

  // Adaptive polling — replaces fixed 1500ms setInterval. Hook stays inert
  // while activeRunId is null; auto-stops on terminal status.
  useRunPolling<RunSummary>({
    enabled: running && activeRunId !== null,
    fetcher: () => getRun(token, projectId, activeRunId as string),
    onUpdate: (s) => setSummary(s),
    onTerminal: (s) => {
      setRunning(false);
      setActiveRunId(null);
      if (s.status === "error") {
        setRunError(s.error ?? t("share.error"));
      }
    },
    onError: (err) => {
      setRunning(false);
      setActiveRunId(null);
      if (err instanceof PublicApiError && err.status === 429) {
        setRunRetryAfter(err.retryAfter ?? 60);
        setRunError(null);
      } else {
        setRunError(err instanceof Error ? err.message : String(err));
      }
    },
  });

  useEffect(() => {
    fetchSchema(token, projectId)
      .then((s) => setSchema(s))
      .catch((err) => {
        if (err instanceof PublicApiError) {
          if (err.status === 404) {
            setLoadError("notFound");
          } else if (err.status === 429) {
            setLoadError("rateLimited");
            setLoadRetryAfter(err.retryAfter ?? 60);
          } else {
            setLoadError("error");
          }
        } else {
          setLoadError("error");
        }
      });
  }, [token, projectId]);

  const handleSubmit = useCallback(async () => {
    if (!schema) return;
    setRunning(true);
    setRunError(null);
    setRunRetryAfter(0);
    setSummary(null);
    setActiveRunId(null);

    try {
      const { runId } = await runPublic(token, projectId, values);
      setActiveRunId(runId);
    } catch (err) {
      setRunning(false);
      if (err instanceof PublicApiError && err.status === 429) {
        setRunRetryAfter(err.retryAfter ?? 60);
      } else if (err instanceof PublicApiError && err.status === 422) {
        setRunError(err.message);
      } else {
        setRunError(err instanceof Error ? err.message : String(err));
      }
    }
  }, [schema, token, projectId, values]);

  const handleCancel = useCallback(async () => {
    if (!activeRunId) return;
    try {
      await cancelRunPublic(token, projectId, activeRunId);
      // Polling picks up the cancelled status; no state change here.
    } catch (err) {
      setRunError(err instanceof Error ? err.message : String(err));
    }
  }, [token, projectId, activeRunId]);

  // ---------- Error states ----------

  if (loadError === "notFound") {
    return (
      <div style={centreWrap}>
        <div style={card}>
          <p style={{ color: "var(--color-text-secondary, #888)", fontSize: "0.9375rem", margin: 0 }}>
            {t("share.notFound")}
          </p>
        </div>
        <Footer label={t("share.poweredBy")} />
      </div>
    );
  }

  if (loadError === "rateLimited") {
    return (
      <div style={centreWrap}>
        <div style={card}>
          <RateLimitBanner retryAfter={loadRetryAfter} label={t("share.rateLimited")} />
        </div>
        <Footer label={t("share.poweredBy")} />
      </div>
    );
  }

  if (loadError === "error") {
    return (
      <div style={centreWrap}>
        <div style={card}>
          <p style={{ color: "var(--color-error, #e05)", fontSize: "0.9375rem", margin: 0 }}>
            {t("share.error")}
          </p>
        </div>
        <Footer label={t("share.poweredBy")} />
      </div>
    );
  }

  if (!schema) {
    return (
      <div style={centreWrap}>
        <div style={{ ...card, display: "flex", justifyContent: "center" }}>
          <Spinner />
        </div>
      </div>
    );
  }

  const inputs = schema.inputs ?? [];
  const outputs = schema.outputs ?? [];
  const isDone = summary?.status === "done";
  const isError = summary?.status === "error";

  return (
    <div style={centreWrap}>
      <div style={card}>
        {/* Header */}
        <div style={{ display: "flex", alignItems: "center", gap: 14, marginBottom: 20 }}>
          {schema.icon && <AppIcon icon={schema.icon} />}
          <div>
            <h1 style={{ margin: 0, fontSize: "1.25rem", fontWeight: 700, lineHeight: 1.2 }}>
              {schema.name}
            </h1>
            {schema.description && (
              <p
                style={{
                  margin: "4px 0 0",
                  fontSize: "0.875rem",
                  color: "var(--color-text-secondary, #888)",
                  lineHeight: 1.4,
                }}
              >
                {schema.description}
              </p>
            )}
          </div>
        </div>

        {/* Form */}
        {inputs.length === 0 ? (
          <p style={{ color: "var(--color-text-secondary, #888)", fontSize: "0.875rem", marginBottom: 16 }}>
            {t("share.noInputs")}
          </p>
        ) : (
          <div style={{ marginBottom: 16 }}>
            {inputs.map((field) => (
              <PublicFormField
                key={field.nodeId}
                field={field}
                value={values[field.variable]}
                onChange={(v) => setValues((prev) => ({ ...prev, [field.variable]: v }))}
                fileDisabledLabel={t("share.fileUnsupported")}
              />
            ))}
          </div>
        )}

        {/* Submit + Cancel */}
        <div style={{ display: "flex", gap: 8, marginBottom: 20 }}>
          <button
            onClick={handleSubmit}
            disabled={running}
            style={{
              display: "flex",
              alignItems: "center",
              gap: 6,
              padding: "10px 20px",
              borderRadius: 8,
              background: "var(--color-accent, #7c6af7)",
              color: "#fff",
              border: "none",
              cursor: running ? "not-allowed" : "pointer",
              opacity: running ? 0.7 : 1,
              fontSize: "0.9375rem",
              fontWeight: 600,
              minHeight: 44,
            }}
          >
            {running ? <Spinner /> : <Play size={15} strokeWidth={2} />}
            {running ? t("share.running") : t("share.submit")}
          </button>

          {running && activeRunId && (
            <button
              onClick={handleCancel}
              style={{
                display: "flex",
                alignItems: "center",
                gap: 6,
                padding: "10px 20px",
                borderRadius: 8,
                background: "transparent",
                color: "var(--color-text-secondary, #888)",
                border: "1px solid var(--color-border, #333)",
                cursor: "pointer",
                fontSize: "0.9375rem",
                fontWeight: 600,
                minHeight: 44,
              }}
            >
              <X size={15} strokeWidth={2} />
              {t("share.cancel")}
            </button>
          )}
        </div>

        {runRetryAfter > 0 && (
          <RateLimitBanner retryAfter={runRetryAfter} label={t("share.rateLimited")} />
        )}

        {runError && (
          <div
            style={{
              color: "var(--color-error, #e05)",
              fontSize: "0.8125rem",
              marginBottom: 16,
              padding: "8px 12px",
              background: "rgba(220,0,80,0.08)",
              borderRadius: 6,
              border: "1px solid rgba(220,0,80,0.2)",
            }}
          >
            {runError}
          </div>
        )}

        {isDone && outputs.length > 0 && summary && (
          <div>
            <div
              style={{
                fontSize: "0.8125rem",
                fontWeight: 600,
                color: "var(--color-text-secondary, #888)",
                marginBottom: 12,
                textTransform: "uppercase",
                letterSpacing: "0.05em",
              }}
            >
              {t("share.outputs")}
            </div>
            {outputs.map((out) => (
              <OutputRenderer key={out.sourceRef} output={out} nodes={summary.nodes} />
            ))}
          </div>
        )}

        {isDone && outputs.length === 0 && (
          <div style={{ color: "var(--color-text-secondary, #888)", fontSize: "0.875rem" }}>
            {t("share.done")}
          </div>
        )}

        {isError && !runError && (
          <div style={{ color: "var(--color-error, #e05)", fontSize: "0.875rem" }}>
            {summary?.error ?? t("share.error")}
          </div>
        )}
      </div>

      <Footer label={t("share.poweredBy")} />
    </div>
  );
}

// ---------- Route param reader ----------
// With output: "export", dynamic [token] routes cannot be pre-rendered.
// We read the token from window.location at runtime.

function useRouteParams(): { token: string; projectId: string | null } {
  const [token, setToken] = useState("");
  const [projectId, setProjectId] = useState<string | null>(null);

  useEffect(() => {
    // Path shape: /share/{token}/  (trailingSlash: true in next.config)
    const parts = window.location.pathname.replace(/\/$/, "").split("/");
    setToken(parts[parts.length - 1] ?? "");
    setProjectId(new URLSearchParams(window.location.search).get("p"));
  }, []);

  return { token, projectId };
}

function SharePageRoot() {
  const { token, projectId } = useRouteParams();

  if (!token) {
    return (
      <div style={centreWrap}>
        <div style={{ ...card, display: "flex", justifyContent: "center" }}>
          <Spinner />
        </div>
      </div>
    );
  }

  return <SharePageContent token={token} projectId={projectId} />;
}

export function ShareApp() {
  return (
    <I18nProvider>
      <SharePageRoot />
    </I18nProvider>
  );
}
