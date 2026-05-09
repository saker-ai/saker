"use client";

import type { CronRun } from "@/features/rpc/types";
import { useT } from "@/features/i18n";

interface Props {
  runs: CronRun[];
  jobName: string;
  onBack: () => void;
}

function formatDuration(ms?: number): string {
  if (ms == null) return "-";
  if (ms === 0) return "0ms";
  if (ms < 1000) return `${ms}ms`;
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  return `${Math.floor(s / 60)}m ${s % 60}s`;
}

function formatTime(iso?: string): string {
  if (!iso) return "-";
  return new Date(iso).toLocaleString();
}

export function CronRunLog({ runs, jobName, onBack }: Props) {
  const { t } = useT();

  return (
    <div className="task-section">
      <div className="task-section__header">
        <div className="task-section__count">
          <button className="task-btn task-btn--ghost" onClick={onBack} aria-label={t("tasks.back")}>
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <line x1="19" y1="12" x2="5" y2="12" /><polyline points="12 19 5 12 12 5" />
            </svg>
            {t("tasks.back")}
          </button>
          <span className="task-section__breadcrumb">{jobName}</span>
          <span className="task-section__breadcrumb-sep">/</span>
          <span>{t("tasks.runHistory")}</span>
        </div>
      </div>

      {runs.length === 0 ? (
        <div className="task-empty">
          <div className="task-empty__icon">
            <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <polyline points="22 12 18 12 15 21 9 3 6 12 2 12" />
            </svg>
          </div>
          <p className="task-empty__text">{t("tasks.noRuns")}</p>
        </div>
      ) : (
        <div className="task-run-list">
          {runs.map((run) => (
            <div key={run.id} className="task-run-item">
              <div className="task-run-item__status">
                <span className={`task-badge task-badge--${run.status}`}>
                  {run.status === "running" && (
                    <svg width="8" height="8" viewBox="0 0 8 8" aria-hidden="true">
                      <circle cx="4" cy="4" r="4" fill="currentColor" />
                    </svg>
                  )}
                  {run.status === "ok" && (
                    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                      <polyline points="20 6 9 17 4 12" />
                    </svg>
                  )}
                  {run.status === "error" && (
                    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                      <line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" />
                    </svg>
                  )}
                  {run.status}
                </span>
              </div>
              <div className="task-run-item__details">
                <div className="task-run-item__time">{formatTime(run.started_at)}</div>
                <div className="task-run-item__duration">{formatDuration(run.duration_ms)}</div>
              </div>
              {(run.summary || run.error) && (
                <div className={`task-run-item__message ${run.error ? "task-run-item__message--error" : ""}`}>
                  {run.error || run.summary}
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
