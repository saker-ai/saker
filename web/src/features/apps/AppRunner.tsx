"use client";

import { useState, useCallback } from "react";
import { Play, X } from "lucide-react";
import { useT } from "@/features/i18n";
import { runApp, getRunStatus, cancelRun, type AppMeta, type AppInputField, type AppOutputField, type RunSummary, type NodeRunResult } from "./appsApi";
import { useRunPolling } from "./useRunPolling";
import { AppFormField } from "./AppFormField";

interface Props {
  app: AppMeta & { inputs?: AppInputField[]; outputs?: AppOutputField[] };
}

function OutputRenderer({ output, nodes }: { output: AppOutputField; nodes: NodeRunResult[] }) {
  // Find the result node whose nodeId matches output.sourceRef (the appOutput node id)
  // or whose resultNodeId matches it.
  const node = nodes.find(
    (n) => n.nodeId === output.sourceRef || n.resultNodeId === output.sourceRef,
  );
  const url = node?.resultUrl ?? "";

  const containerStyle: React.CSSProperties = {
    marginBottom: 16,
  };

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
        <div style={{ color: "var(--color-text-secondary, #888)", fontSize: "0.8rem" }}>—</div>
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
          style={{ maxWidth: "100%", borderRadius: 8, border: "1px solid var(--color-border, #333)" }}
        />
      )}
      {output.kind === "video" && (
        <video
          controls
          src={url}
          style={{ maxWidth: "100%", borderRadius: 8 }}
        />
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

export function AppRunner({ app }: Props) {
  const { t } = useT();
  const inputs = app.inputs ?? [];
  const outputs = app.outputs ?? [];
  const [values, setValues] = useState<Record<string, unknown>>({});
  const [running, setRunning] = useState(false);
  const [runError, setRunError] = useState<string | null>(null);
  const [summary, setSummary] = useState<RunSummary | null>(null);
  const [activeRunId, setActiveRunId] = useState<string | null>(null);

  // Adaptive polling — replaces fixed 1500ms setInterval. Hook stays inert
  // while activeRunId is null and tears itself down on terminal status.
  useRunPolling<RunSummary>({
    enabled: running && activeRunId !== null,
    fetcher: () => getRunStatus(app.id, activeRunId as string),
    onUpdate: (s) => setSummary(s),
    onTerminal: (s) => {
      setRunning(false);
      setActiveRunId(null);
      if (s.status === "error") {
        setRunError(s.error ?? t("apps.runError"));
      }
    },
    onError: (err) => {
      setRunning(false);
      setActiveRunId(null);
      setRunError(err instanceof Error ? err.message : String(err));
    },
  });

  const handleSubmit = useCallback(async () => {
    setRunning(true);
    setRunError(null);
    setSummary(null);
    setActiveRunId(null);

    try {
      const { runId } = await runApp(app.id, values);
      setActiveRunId(runId);
    } catch (err) {
      setRunning(false);
      setRunError(err instanceof Error ? err.message : String(err));
    }
  }, [app.id, values]);

  const handleCancel = useCallback(async () => {
    if (!activeRunId) return;
    try {
      await cancelRun(app.id, activeRunId);
      // Polling will pick up the cancelled status; no state change here.
    } catch (err) {
      setRunError(err instanceof Error ? err.message : String(err));
    }
  }, [app.id, activeRunId]);

  // App not published yet.
  if (!app.publishedVersion) {
    return (
      <div
        style={{
          padding: 24,
          color: "var(--color-text-secondary, #888)",
          fontSize: "0.875rem",
        }}
      >
        {t("apps.publishFirst")}
      </div>
    );
  }

  const isDone = summary?.status === "done";
  const isError = summary?.status === "error";

  return (
    <div>
      {inputs.length === 0 ? (
        <p style={{ color: "var(--color-text-secondary, #888)", fontSize: "0.875rem", marginBottom: 16 }}>
          {t("apps.noInputs")}
        </p>
      ) : (
        <div style={{ marginBottom: 16 }}>
          {inputs.map((field) => (
            <AppFormField
              key={field.nodeId}
              field={field}
              value={values[field.variable]}
              onChange={(v) => setValues((prev) => ({ ...prev, [field.variable]: v }))}
            />
          ))}
        </div>
      )}

      <div style={{ display: "flex", gap: 8, marginBottom: 20 }}>
        <button
          onClick={handleSubmit}
          disabled={running}
          style={{
            display: "flex",
            alignItems: "center",
            gap: 6,
            padding: "8px 16px",
            borderRadius: 6,
            background: "var(--color-accent, #7c6af7)",
            color: "#fff",
            border: "none",
            cursor: running ? "not-allowed" : "pointer",
            opacity: running ? 0.7 : 1,
            fontSize: "0.875rem",
            fontWeight: 500,
          }}
        >
          <Play size={15} strokeWidth={2} />
          {running ? t("apps.running") : t("apps.run")}
        </button>

        {running && activeRunId && (
          <button
            onClick={handleCancel}
            style={{
              display: "flex",
              alignItems: "center",
              gap: 6,
              padding: "8px 16px",
              borderRadius: 6,
              background: "transparent",
              color: "var(--color-text-secondary, #888)",
              border: "1px solid var(--color-border, #333)",
              cursor: "pointer",
              fontSize: "0.875rem",
              fontWeight: 500,
            }}
          >
            <X size={15} strokeWidth={2} />
            {t("apps.cancel")}
          </button>
        )}
      </div>

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
            {t("apps.outputs")}
          </div>
          {outputs.map((out) => (
            <OutputRenderer key={out.sourceRef} output={out} nodes={summary.nodes} />
          ))}
        </div>
      )}

      {isDone && outputs.length === 0 && (
        <div style={{ color: "var(--color-text-secondary, #888)", fontSize: "0.875rem" }}>
          {t("apps.runDone")}
        </div>
      )}

      {isError && !runError && (
        <div style={{ color: "var(--color-error, #e05)", fontSize: "0.875rem" }}>
          {summary?.error ?? t("apps.runError")}
        </div>
      )}
    </div>
  );
}
