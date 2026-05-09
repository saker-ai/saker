"use client";

import { useState, useEffect, useCallback } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { Activity, Calendar, LayoutGrid, Video } from "lucide-react";
import type { RPCClient } from "@/features/rpc/client";
import type { CronJob, CronRun, ActiveTurn } from "@/features/rpc/types";
import { useT } from "@/features/i18n";
import { ActiveTurns } from "./ActiveTurns";
import { CronJobList } from "./CronJobList";
import { CronJobForm } from "./CronJobForm";
import { CronRunLog } from "./CronRunLog";
import { MonitorsPanel } from "./MonitorsPanel";

type Tab = "active" | "cron" | "monitors";

interface Props {
  rpc: RPCClient | null;
  connected: boolean;
}

export function TasksView({ rpc, connected }: Props) {
  const { t } = useT();
  const [tab, setTab] = useState<Tab>("active");
  const [activeTurns, setActiveTurns] = useState<ActiveTurn[]>([]);
  const [cronJobs, setCronJobs] = useState<CronJob[]>([]);
  const [cronRuns, setCronRuns] = useState<CronRun[]>([]);
  const [selectedJobId, setSelectedJobId] = useState<string | null>(null);
  const [showForm, setShowForm] = useState(false);
  const [editingJob, setEditingJob] = useState<CronJob | null>(null);
  const [showRuns, setShowRuns] = useState(false);

  const refreshActiveTurns = useCallback(() => {
    if (!rpc || !connected) return;
    rpc.request<{ turns: ActiveTurn[] }>("turns/active").then((r) => {
      setActiveTurns(r.turns || []);
    }).catch(() => {});
  }, [rpc, connected]);

  const refreshCronJobs = useCallback(() => {
    if (!rpc || !connected) return;
    rpc.request<{ jobs: CronJob[] }>("cron/list").then((r) => {
      setCronJobs(r.jobs || []);
    }).catch(() => {});
  }, [rpc, connected]);

  const refreshCronRuns = useCallback((jobId?: string) => {
    if (!rpc || !connected) return;
    const params: Record<string, unknown> = { limit: 50 };
    if (jobId) params.jobId = jobId;
    rpc.request<{ runs: CronRun[] }>("cron/runs", params).then((r) => {
      setCronRuns(r.runs || []);
    }).catch(() => {});
  }, [rpc, connected]);

  // Initial load & polling for active turns.
  useEffect(() => {
    refreshActiveTurns();
    refreshCronJobs();
    const interval = setInterval(refreshActiveTurns, 5000);
    return () => clearInterval(interval);
  }, [refreshActiveTurns, refreshCronJobs]);

  // Listen for cron notifications.
  useEffect(() => {
    if (!rpc) return;
    const unsub1 = rpc.on("cron/run_started", () => {
      refreshCronJobs();
      refreshActiveTurns();
      if (showRuns) refreshCronRuns(selectedJobId || undefined);
    });
    const unsub2 = rpc.on("cron/run_finished", () => {
      refreshCronJobs();
      refreshActiveTurns();
      if (showRuns) refreshCronRuns(selectedJobId || undefined);
    });
    return () => { unsub1(); unsub2(); };
  }, [rpc, refreshCronJobs, refreshActiveTurns, refreshCronRuns, showRuns, selectedJobId]);

  const handleAddJob = async (job: Partial<CronJob>) => {
    if (!rpc) return;
    await rpc.request("cron/add", job as Record<string, unknown>);
    setShowForm(false);
    setEditingJob(null);
    refreshCronJobs();
  };

  const handleUpdateJob = async (id: string, patch: Record<string, unknown>) => {
    if (!rpc) return;
    await rpc.request("cron/update", { id, ...patch });
    setShowForm(false);
    setEditingJob(null);
    refreshCronJobs();
  };

  const handleDeleteJob = async (id: string) => {
    if (!rpc) return;
    await rpc.request("cron/remove", { id });
    refreshCronJobs();
  };

  const handleToggleJob = async (id: string, enabled: boolean) => {
    if (!rpc) return;
    await rpc.request("cron/toggle", { id, enabled });
    refreshCronJobs();
  };

  const handleRunJob = async (id: string) => {
    if (!rpc) return;
    await rpc.request("cron/run", { id });
    refreshActiveTurns();
  };

  const handleViewRuns = (jobId: string) => {
    setSelectedJobId(jobId);
    setShowRuns(true);
    refreshCronRuns(jobId);
  };

  return (
    <div className="app-content">
      <div className="page-container">
        <header className="page-header-v2">
          <div className="page-header-icon">
            <LayoutGrid size={24} className="text-accent" />
          </div>
          <div>
            <h1 className="page-title-v2">{t("tasks.title")}</h1>
            <p className="page-subtitle-v2">Manage your active turns and scheduled tasks</p>
          </div>
        </header>

        <div className="task-tabs-v2" role="tablist">
          <button
            role="tab"
            aria-selected={tab === "active"}
            className={`task-tabs-item-v2 ${tab === "active" ? "active" : ""}`}
            onClick={() => setTab("active")}
          >
            <Activity size={16} />
            {t("tasks.activeTurns")}
            {activeTurns.length > 0 && (
              <span className="task-tabs-badge-v2">{activeTurns.length}</span>
            )}
            {tab === "active" && (
              <motion.div layoutId="activeTab" className="task-tabs-indicator-v2" />
            )}
          </button>
          <button
            role="tab"
            aria-selected={tab === "cron"}
            className={`task-tabs-item-v2 ${tab === "cron" ? "active" : ""}`}
            onClick={() => setTab("cron")}
          >
            <Calendar size={16} />
            {t("tasks.cronJobs")}
            {tab === "cron" && (
              <motion.div layoutId="activeTab" className="task-tabs-indicator-v2" />
            )}
          </button>
          <button
            role="tab"
            aria-selected={tab === "monitors"}
            className={`task-tabs-item-v2 ${tab === "monitors" ? "active" : ""}`}
            onClick={() => setTab("monitors")}
          >
            <Video size={16} />
            {t("tasks.monitors")}
            {tab === "monitors" && (
              <motion.div layoutId="activeTab" className="task-tabs-indicator-v2" />
            )}
          </button>
        </div>

        <main className="task-content-v2">
          <AnimatePresence mode="wait">
            <motion.div
              key={tab + (showForm ? "-form" : "") + (showRuns ? "-runs" : "")}
              initial={{ opacity: 0, y: 10 }}
              animate={{ opacity: 1, y: 0 }}
              exit={{ opacity: 0, y: -10 }}
              transition={{ duration: 0.2 }}
            >
              {tab === "monitors" ? (
                <MonitorsPanel rpc={rpc} connected={connected} />
              ) : tab === "active" ? (
                <ActiveTurns turns={activeTurns} onRefresh={refreshActiveTurns} />
              ) : showForm ? (
                <CronJobForm
                  job={editingJob}
                  onSave={editingJob
                    ? (data) => handleUpdateJob(editingJob.id, data)
                    : (data) => handleAddJob(data)
                  }
                  onCancel={() => { setShowForm(false); setEditingJob(null); }}
                />
              ) : showRuns ? (
                <CronRunLog
                  runs={cronRuns}
                  jobName={cronJobs.find(j => j.id === selectedJobId)?.name || ""}
                  onBack={() => setShowRuns(false)}
                />
              ) : (
                <CronJobList
                  jobs={cronJobs}
                  onAdd={() => { setEditingJob(null); setShowForm(true); }}
                  onEdit={(job) => { setEditingJob(job); setShowForm(true); }}
                  onDelete={handleDeleteJob}
                  onToggle={handleToggleJob}
                  onRun={handleRunJob}
                  onViewRuns={handleViewRuns}
                />
              )}
            </motion.div>
          </AnimatePresence>
        </main>
      </div>
    </div>
  );
}
