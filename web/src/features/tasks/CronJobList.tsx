"use client";

import { useState } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { 
  Plus, 
  Clock, 
  Calendar, 
  RefreshCw, 
  Play, 
  History, 
  Edit2, 
  Trash2, 
  AlertCircle,
  CheckCircle2,
  ChevronRight,
  MoreVertical
} from "lucide-react";
import type { CronJob } from "@/features/rpc/types";
import { useT } from "@/features/i18n";

interface Props {
  jobs: CronJob[];
  onAdd: () => void;
  onEdit: (job: CronJob) => void;
  onDelete: (id: string) => void;
  onToggle: (id: string, enabled: boolean) => void;
  onRun: (id: string) => void;
  onViewRuns: (id: string) => void;
}

function formatSchedule(job: CronJob): string {
  const s = job.schedule;
  switch (s.kind) {
    case "every": {
      const ms = s.every_ms || 0;
      if (ms >= 3600000) return `Every ${Math.round(ms / 3600000)}h`;
      if (ms >= 60000) return `Every ${Math.round(ms / 60000)}m`;
      return `Every ${Math.round(ms / 1000)}s`;
    }
    case "cron":
      return s.expr || "cron";
    case "once":
      return s.run_at ? new Date(s.run_at).toLocaleString() : "once";
    default:
      return s.kind;
  }
}

function formatTime(iso?: string): string {
  if (!iso) return "-";
  return new Date(iso).toLocaleString();
}

function ScheduleIcon({ kind }: { kind: string }) {
  switch (kind) {
    case "cron": return <Clock size={14} />;
    case "once": return <ChevronRight size={14} />;
    default: return <RefreshCw size={14} />;
  }
}

export function CronJobList({ jobs, onAdd, onEdit, onDelete, onToggle, onRun, onViewRuns }: Props) {
  const { t } = useT();
  const [confirmDeleteId, setConfirmDeleteId] = useState<string | null>(null);

  return (
    <div className="task-section-v2">
      <div className="task-list-header-v2">
        <div className="task-list-title-group">
          <span className="task-count-badge-v2">{jobs.length}</span>
          <h2 className="task-list-title-text-v2">{t("tasks.jobs")}</h2>
        </div>
        <button className="btn-primary-v2" onClick={onAdd}>
          <Plus size={16} />
          {t("tasks.addJob")}
        </button>
      </div>

      {jobs.length === 0 ? (
        <div className="task-empty-v2">
          <div className="task-empty-icon-v2">
            <Calendar size={48} />
          </div>
          <p>{t("tasks.noCronJobs")}</p>
          <button className="btn-outline-v2" onClick={onAdd}>
            <Plus size={16} />
            {t("tasks.addJob")}
          </button>
        </div>
      ) : (
        <div className="task-grid-v2">
          <AnimatePresence>
            {jobs.map((job) => (
              <motion.div 
                layout
                initial={{ opacity: 0, scale: 0.95 }}
                animate={{ opacity: 1, scale: 1 }}
                exit={{ opacity: 0, scale: 0.95 }}
                key={job.id} 
                className={`task-card-v2 group ${!job.enabled ? "disabled" : ""}`}
              >
                <div className="task-card-header-v2">
                  <div className="task-card-title-row-v2">
                    <h3 className="task-card-name-v2">{job.name}</h3>
                    <div className="task-card-status-v2">
                      <div className={`status-dot-v2 ${job.state.last_status || "idle"}`} />
                      <span className="status-text-v2">{job.state.last_status || "idle"}</span>
                    </div>
                  </div>
                  <label className="switch-v2" title={job.enabled ? t("tasks.disable") : t("tasks.enable")}>
                    <input
                      type="checkbox"
                      checked={job.enabled}
                      onChange={() => onToggle(job.id, !job.enabled)}
                    />
                    <span className="slider-v2" />
                  </label>
                </div>

                {job.description && (
                  <p className="task-card-desc-v2">{job.description}</p>
                )}

                <div className="task-card-metrics-v2">
                  <div className="metric-item-v2">
                    <div className="metric-label-v2">
                      <ScheduleIcon kind={job.schedule.kind} />
                      <span>{t("tasks.schedule")}</span>
                    </div>
                    <div className="metric-value-v2">{formatSchedule(job)}</div>
                  </div>
                  <div className="metric-item-v2">
                    <div className="metric-label-v2">
                      <Play size={14} />
                      <span>{t("tasks.lastRun")}</span>
                    </div>
                    <div className="metric-value-v2">{formatTime(job.state.last_run_at)}</div>
                  </div>
                </div>

                {job.state.last_error && (
                  <div className="task-card-error-v2">
                    <AlertCircle size={14} />
                    <span>{job.state.last_error}</span>
                  </div>
                )}

                <div className="task-card-actions-v2">
                  <div className="action-group-v2">
                    <button className="action-btn-v2" onClick={() => onEdit(job)} title={t("tasks.edit")}>
                      <Edit2 size={14} />
                      <span>{t("tasks.edit")}</span>
                    </button>
                    <button className="action-btn-v2 highlight" onClick={() => onRun(job.id)} title={t("tasks.runNow")}>
                      <Play size={14} fill="currentColor" />
                      <span>{t("tasks.runNow")}</span>
                    </button>
                    <button className="action-btn-v2" onClick={() => onViewRuns(job.id)} title={t("tasks.viewRuns")}>
                      <History size={14} />
                      <span>{t("tasks.viewRuns")}</span>
                    </button>
                  </div>
                  
                  {confirmDeleteId === job.id ? (
                    <div className="delete-confirm-v2">
                      <button className="confirm-btn-v2 yes" onClick={() => { onDelete(job.id); setConfirmDeleteId(null); }}>
                        {t("thread.deleteConfirm")}
                      </button>
                      <button className="confirm-btn-v2 no" onClick={() => setConfirmDeleteId(null)}>
                        <X size={14} />
                      </button>
                    </div>
                  ) : (
                    <button className="delete-btn-v2" onClick={() => setConfirmDeleteId(job.id)} title={t("tasks.delete")}>
                      <Trash2 size={14} />
                    </button>
                  )}
                </div>
              </motion.div>
            ))}
          </AnimatePresence>
        </div>
      )}
    </div>
  );
}

function X({ size }: { size: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" />
    </svg>
  );
}
