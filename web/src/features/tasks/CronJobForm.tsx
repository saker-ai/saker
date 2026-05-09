"use client";

import { useState } from "react";
import type { CronJob } from "@/features/rpc/types";
import { useT } from "@/features/i18n";

interface Props {
  job: CronJob | null;
  onSave: (data: Record<string, unknown>) => void | Promise<void>;
  onCancel: () => void;
}

type ScheduleKind = "every" | "cron" | "once";

export function CronJobForm({ job, onSave, onCancel }: Props) {
  const { t } = useT();
  const [name, setName] = useState(job?.name || "");
  const [description, setDescription] = useState(job?.description || "");
  const [prompt, setPrompt] = useState(job?.prompt || "");
  const [enabled, setEnabled] = useState(job?.enabled ?? true);
  const [timeout, setTimeout_] = useState(job?.timeout?.toString() || "");
  const [scheduleKind, setScheduleKind] = useState<ScheduleKind>(
    (job?.schedule.kind as ScheduleKind) || "every"
  );
  const [everyMinutes, setEveryMinutes] = useState(() => {
    if (job?.schedule.every_ms) return Math.round(job.schedule.every_ms / 60000).toString();
    return "30";
  });
  const [cronExpr, setCronExpr] = useState(job?.schedule.expr || "");
  const [timezone, setTimezone] = useState(job?.schedule.timezone || "");
  const [runAt, setRunAt] = useState(job?.schedule.run_at || "");
  const [saving, setSaving] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    try {
      const schedule: Record<string, unknown> = { kind: scheduleKind };
      if (scheduleKind === "every") {
        schedule.every_ms = parseInt(everyMinutes, 10) * 60000;
      } else if (scheduleKind === "cron") {
        schedule.expr = cronExpr;
        if (timezone) schedule.timezone = timezone;
      } else if (scheduleKind === "once") {
        schedule.run_at = runAt;
      }
      const data: Record<string, unknown> = { name, description, prompt, enabled, schedule };
      if (timeout) data.timeout = parseInt(timeout, 10);
      await onSave(data);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="task-form-wrapper">
      <div className="task-form-header">
        <button className="task-btn task-btn--ghost" type="button" onClick={onCancel}>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <line x1="19" y1="12" x2="5" y2="12" /><polyline points="12 19 5 12 12 5" />
          </svg>
          {t("tasks.back")}
        </button>
        <h2 className="task-form-title">
          {job ? t("tasks.editJob") : t("tasks.newJob")}
        </h2>
      </div>

      <form className="task-form" onSubmit={handleSubmit}>
        <div className="task-form__section">
          <h3 className="task-form__section-title">{t("tasks.basicInfo")}</h3>
          <label className="task-form__field">
            <span className="task-form__label">
              {t("tasks.jobName")} <span className="task-form__required" aria-label="required">*</span>
            </span>
            <input
              className="task-form__input"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Daily Code Review"
              required
              autoFocus
            />
          </label>
          <label className="task-form__field">
            <span className="task-form__label">{t("tasks.jobDesc")}</span>
            <input
              className="task-form__input"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder={t("tasks.optional")}
            />
          </label>
          <label className="task-form__field">
            <span className="task-form__label">
              {t("tasks.prompt")} <span className="task-form__required" aria-label="required">*</span>
            </span>
            <textarea
              className="task-form__textarea"
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              rows={5}
              placeholder={t("tasks.promptPlaceholder")}
              required
            />
            <span className="task-form__hint">{prompt.length} {t("tasks.chars")}</span>
          </label>
        </div>

        <div className="task-form__section">
          <h3 className="task-form__section-title">{t("tasks.schedule")}</h3>
          <div className="task-form__schedule-picker">
            {(["every", "cron", "once"] as const).map((kind) => (
              <button
                key={kind}
                type="button"
                className={`task-form__schedule-option ${scheduleKind === kind ? "active" : ""}`}
                onClick={() => setScheduleKind(kind)}
              >
                {kind === "every" ? t("tasks.interval") : kind === "cron" ? "Cron" : t("tasks.once")}
              </button>
            ))}
          </div>

          <div className="task-form__schedule-config">
            {scheduleKind === "every" && (
              <div className="task-form__inline-group">
                <span className="task-form__inline-label">{t("tasks.every")}</span>
                <input
                  className="task-form__input task-form__input--sm"
                  type="number"
                  min="1"
                  value={everyMinutes}
                  onChange={(e) => setEveryMinutes(e.target.value)}
                />
                <span className="task-form__inline-label">{t("tasks.minutes")}</span>
              </div>
            )}
            {scheduleKind === "cron" && (
              <>
                <label className="task-form__field">
                  <span className="task-form__label">
                    {t("tasks.cronExpression")} <span className="task-form__required" aria-label="required">*</span>
                  </span>
                  <input
                    className="task-form__input task-form__input--mono"
                    value={cronExpr}
                    onChange={(e) => setCronExpr(e.target.value)}
                    placeholder="0 9 * * *"
                    required
                  />
                  <span className="task-form__hint">minute hour day month weekday</span>
                </label>
                <label className="task-form__field">
                  <span className="task-form__label">{t("tasks.timezone")}</span>
                  <input
                    className="task-form__input"
                    value={timezone}
                    onChange={(e) => setTimezone(e.target.value)}
                    placeholder="Asia/Shanghai"
                  />
                </label>
              </>
            )}
            {scheduleKind === "once" && (
              <label className="task-form__field">
                <span className="task-form__label">
                  {t("tasks.runAt")} <span className="task-form__required" aria-label="required">*</span>
                </span>
                <input
                  className="task-form__input"
                  type="datetime-local"
                  value={runAt ? runAt.slice(0, 16) : ""}
                  onChange={(e) => setRunAt(new Date(e.target.value).toISOString())}
                  required
                />
              </label>
            )}
          </div>
        </div>

        <div className="task-form__section">
          <h3 className="task-form__section-title">{t("tasks.advanced")}</h3>
          <label className="task-form__field">
            <span className="task-form__label">{t("settings.timeout")} ({t("tasks.seconds")})</span>
            <input
              className="task-form__input task-form__input--sm"
              type="number"
              min="0"
              value={timeout}
              onChange={(e) => setTimeout_(e.target.value)}
              placeholder="0 = default"
            />
          </label>
          <div className="task-form__toggle-field">
            <span className="task-form__label">{t("settings.enabled")}</span>
            <label className="task-toggle">
              <input
                type="checkbox"
                checked={enabled}
                onChange={(e) => setEnabled(e.target.checked)}
              />
              <span className="task-toggle__track" />
            </label>
          </div>
        </div>

        <div className="task-form__footer">
          <button className="task-btn task-btn--ghost" type="button" onClick={onCancel}>
            {t("settings.cancel")}
          </button>
          <button className="task-btn task-btn--primary" type="submit" disabled={saving}>
            {saving ? (
              <>
                <span className="task-btn__spinner" />
                {t("settings.saving")}
              </>
            ) : (
              <>
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <polyline points="20 6 9 17 4 12" />
                </svg>
                {t("settings.save")}
              </>
            )}
          </button>
        </div>
      </form>
    </div>
  );
}
