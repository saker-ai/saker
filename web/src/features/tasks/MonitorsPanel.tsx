"use client";

import { useState, useEffect, useCallback } from "react";
import { Video, Plus, Square, AlertCircle, Radio } from "lucide-react";
import type { RPCClient } from "@/features/rpc/client";
import type { MonitorInfo } from "@/features/rpc/types";
import { useT } from "@/features/i18n";

interface Props {
  rpc: RPCClient | null;
  connected: boolean;
}

export function MonitorsPanel({ rpc, connected }: Props) {
  const { t } = useT();
  const [monitors, setMonitors] = useState<MonitorInfo[]>([]);
  const [showForm, setShowForm] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const refresh = useCallback(() => {
    if (!rpc || !connected) return;
    rpc
      .request<{ monitors: MonitorInfo[] }>("monitor/list")
      .then((r) => setMonitors(r.monitors || []))
      .catch((err) => {
        console.error("monitor/list failed:", err);
      });
  }, [rpc, connected]);

  useEffect(() => {
    refresh();
    const interval = setInterval(refresh, 5000);
    return () => clearInterval(interval);
  }, [refresh]);

  const handleStop = async (taskId: string) => {
    if (!rpc || loading) return;
    setError(null);
    setLoading(true);
    try {
      await rpc.request("monitor/stop", { task_id: taskId });
      refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  };

  const handleStart = async (form: MonitorForm) => {
    if (!rpc || loading) return;
    setError(null);
    setLoading(true);
    const sr = parseInt(form.sampleRate, 10);
    try {
      await rpc.request("monitor/start", {
        url: form.url,
        events: form.keywords,
        sample_rate: Number.isFinite(sr) && sr > 0 ? sr : 5,
        webhook_url: form.webhookUrl || undefined,
        subject: form.subject || undefined,
      });
      setShowForm(false);
      refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="monitors-panel">
      <div className="monitors-header">
        <h3 className="monitors-title">
          <Video size={18} />
          {t("monitor.title")}
        </h3>
        {!showForm && (
          <button className="btn-v2 btn-primary-v2" onClick={() => setShowForm(true)}>
            <Plus size={14} />
            {t("monitor.new")}
          </button>
        )}
      </div>

      {error && (
        <div className="monitor-error" style={{ margin: "8px 0" }}>
          <AlertCircle size={12} />
          <span>{error}</span>
        </div>
      )}

      {showForm && (
        <MonitorFormCard
          onSubmit={handleStart}
          onCancel={() => setShowForm(false)}
          loading={loading}
        />
      )}

      {monitors.length === 0 && !showForm ? (
        <div className="empty-state-v2">
          <Video size={32} className="empty-icon" />
          <p>{t("monitor.noMonitors")}</p>
        </div>
      ) : (
        <div className="monitors-list">
          {monitors.map((m) => (
            <MonitorCard
              key={m.task_id}
              monitor={m}
              onStop={() => handleStop(m.task_id)}
              disabled={loading}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function MonitorCard({
  monitor,
  onStop,
  disabled,
}: {
  monitor: MonitorInfo;
  onStop: () => void;
  disabled?: boolean;
}) {
  const { t } = useT();

  return (
    <div className={`monitor-card ${monitor.running ? "running" : "stopped"}`}>
      <div className="monitor-card-header">
        <div className="monitor-status">
          {monitor.running ? (
            <Radio size={14} className="status-running" />
          ) : (
            <Square size={14} className="status-stopped" />
          )}
          <span className={`monitor-status-text ${monitor.running ? "text-running" : "text-stopped"}`}>
            {monitor.running ? t("monitor.running") : t("monitor.stopped")}
          </span>
        </div>
        <span className="monitor-subject">{monitor.subject || t("monitor.title")}</span>
      </div>

      <div className="monitor-url">{monitor.stream_url}</div>

      <div className="monitor-stats">
        <div className="monitor-stat">
          <span className="stat-label">{t("monitor.frames")}:</span>
          <span className="stat-value">
            {monitor.processed} {t("monitor.processed")} / {monitor.skipped} {t("monitor.skipped")}
          </span>
        </div>
        <div className="monitor-stat">
          <span className="stat-label">{t("monitor.events")}:</span>
          <span className="stat-value">{monitor.events}</span>
        </div>
        <div className="monitor-stat">
          <span className="stat-label">{t("monitor.uptime")}:</span>
          <span className="stat-value">{monitor.uptime}</span>
        </div>
      </div>

      {monitor.last_error && (
        <div className="monitor-error">
          <AlertCircle size={12} />
          <span>{monitor.last_error}</span>
        </div>
      )}

      {monitor.running && (
        <div className="monitor-actions">
          <button className="btn-v2 btn-danger-v2" onClick={onStop} disabled={disabled}>
            <Square size={12} />
            {t("monitor.stop")}
          </button>
        </div>
      )}
    </div>
  );
}

interface MonitorForm {
  url: string;
  keywords: string;
  sampleRate: string;
  webhookUrl: string;
  subject: string;
}

function MonitorFormCard({
  onSubmit,
  onCancel,
  loading,
}: {
  onSubmit: (form: MonitorForm) => void;
  onCancel: () => void;
  loading?: boolean;
}) {
  const { t } = useT();
  const [form, setForm] = useState<MonitorForm>({
    url: "",
    keywords: "",
    sampleRate: "5",
    webhookUrl: "",
    subject: "",
  });

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.url.trim()) return;
    onSubmit(form);
  };

  return (
    <form className="monitor-form" onSubmit={handleSubmit}>
      <h4 className="monitor-form-title">{t("monitor.new")}</h4>

      <div className="form-group">
        <label>{t("monitor.url")} *</label>
        <input
          type="text"
          value={form.url}
          onChange={(e) => setForm({ ...form, url: e.target.value })}
          placeholder={t("monitor.urlPlaceholder")}
          required
        />
      </div>

      <div className="form-group">
        <label>{t("monitor.subject")} <span className="form-optional">({t("monitor.optional")})</span></label>
        <input
          type="text"
          value={form.subject}
          onChange={(e) => setForm({ ...form, subject: e.target.value })}
          placeholder={t("monitor.subjectPlaceholder")}
        />
      </div>

      <div className="form-group">
        <label>{t("monitor.keywords")} <span className="form-optional">({t("monitor.optional")})</span></label>
        <input
          type="text"
          value={form.keywords}
          onChange={(e) => setForm({ ...form, keywords: e.target.value })}
          placeholder={t("monitor.keywordsPlaceholder")}
        />
      </div>

      <div className="form-row">
        <div className="form-group">
          <label>{t("monitor.sampleRate")}</label>
          <input
            type="number"
            min="1"
            max="100"
            value={form.sampleRate}
            onChange={(e) => setForm({ ...form, sampleRate: e.target.value })}
          />
        </div>
      </div>

      <div className="form-group">
        <label>{t("monitor.webhookUrl")} <span className="form-optional">({t("monitor.optional")})</span></label>
        <input
          type="text"
          value={form.webhookUrl}
          onChange={(e) => setForm({ ...form, webhookUrl: e.target.value })}
          placeholder="https://..."
        />
      </div>

      <div className="monitor-form-actions">
        <button type="button" className="btn-v2" onClick={onCancel}>
          {t("monitor.cancel")}
        </button>
        <button type="submit" className="btn-v2 btn-primary-v2" disabled={!form.url.trim() || loading}>
          <Plus size={14} />
          {t("monitor.start")}
        </button>
      </div>
    </form>
  );
}
