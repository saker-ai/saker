"use client";

import { useState, useEffect } from "react";
import type { ActiveTurn } from "@/features/rpc/types";
import { useT } from "@/features/i18n";

interface Props {
  turns: ActiveTurn[];
  onRefresh: () => void;
}

function useElapsed(startedAt: string) {
  const [, setTick] = useState(0);
  useEffect(() => {
    const id = setInterval(() => setTick((n) => n + 1), 1000);
    return () => clearInterval(id);
  }, []);
  const ms = Date.now() - new Date(startedAt).getTime();
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  return `${m}m ${s % 60}s`;
}

function TurnCard({ turn }: { turn: ActiveTurn }) {
  const { t } = useT();
  const elapsed = useElapsed(turn.started_at);

  return (
    <div className="task-card task-card--active">
      <div className="task-card__indicator" />
      <div className="task-card__body">
        <div className="task-card__top">
          <div className="task-card__title-group">
            <h3 className="task-card__title">
              {turn.thread_title || turn.thread_id.slice(0, 8)}
            </h3>
            <span className="task-badge task-badge--running">
              <svg width="8" height="8" viewBox="0 0 8 8" aria-hidden="true">
                <circle cx="4" cy="4" r="4" fill="currentColor" />
              </svg>
              {turn.status}
            </span>
          </div>
          <span className="task-card__elapsed">{elapsed}</span>
        </div>
        <div className="task-card__meta">
          <span className="task-meta-chip">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              {turn.source === "cron" ? (
                <><rect x="3" y="4" width="18" height="18" rx="2" ry="2" /><line x1="16" y1="2" x2="16" y2="6" /><line x1="8" y1="2" x2="8" y2="6" /><line x1="3" y1="10" x2="21" y2="10" /></>
              ) : (
                <><path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2" /><circle cx="12" cy="7" r="4" /></>
              )}
            </svg>
            {turn.source}
          </span>
          {turn.tool_name && (
            <span className="task-meta-chip">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z" />
              </svg>
              {turn.tool_name}
            </span>
          )}
        </div>
        <p className="task-card__prompt">{turn.prompt}</p>
        {turn.stream_text && (
          <div className="task-card__stream">
            <pre>{turn.stream_text}</pre>
          </div>
        )}
      </div>
    </div>
  );
}

export function ActiveTurns({ turns, onRefresh }: Props) {
  const { t } = useT();

  return (
    <div className="task-section">
      <div className="task-section__header">
        <div className="task-section__count">
          {turns.length > 0 && (
            <span className="task-count-pill">{turns.length}</span>
          )}
          <span>{turns.length > 0 ? t("tasks.running") : ""}</span>
        </div>
        <button className="task-btn task-btn--ghost" onClick={onRefresh} aria-label={t("tasks.refresh")}>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <polyline points="23 4 23 10 17 10" />
            <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10" />
          </svg>
          {t("tasks.refresh")}
        </button>
      </div>

      {turns.length === 0 ? (
        <div className="task-empty">
          <div className="task-empty__icon">
            <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <circle cx="12" cy="12" r="10" />
              <polyline points="12 6 12 12 16 14" />
            </svg>
          </div>
          <p className="task-empty__text">{t("tasks.noActiveTurns")}</p>
          <p className="task-empty__hint">{t("tasks.autoRefreshHint")}</p>
        </div>
      ) : (
        <div className="task-card-list">
          {turns.map((turn) => (
            <TurnCard key={turn.turn_id} turn={turn} />
          ))}
        </div>
      )}
    </div>
  );
}
