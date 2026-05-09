"use client";

import { useState, useEffect, useCallback, useRef } from "react";
import {
  BarChart, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer,
  PieChart, Pie, Cell, Legend,
  LineChart, Line, CartesianGrid,
} from "recharts";
import type { SkillStats, SkillActivationRecord } from "@/features/rpc/types";
import type { RPCClient } from "@/features/rpc/client";
import { useT } from "@/features/i18n";

const COLORS = ["#8ab4f8", "#c084fc", "#81c995", "#fdd663", "#f28b82", "#a8c7fa", "#f0b8b8", "#b4ddb4"];

interface Props {
  rpc: RPCClient | null;
}

export function SkillsAnalyticsSection({ rpc }: Props) {
  const { t } = useT();
  const [stats, setStats] = useState<Record<string, SkillStats> | null>(null);
  const [history, setHistory] = useState<SkillActivationRecord[]>([]);
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [refreshInterval] = useState(30);
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const fetchData = useCallback(async () => {
    if (!rpc) return;
    try {
      const [statsRes, historyRes] = await Promise.all([
        rpc.request<Record<string, SkillStats>>("skill/analytics"),
        rpc.request<{ history: SkillActivationRecord[] }>("skill/analytics/history", { limit: 200 }),
      ]);
      setStats(statsRes ?? {});
      setHistory(historyRes?.history ?? []);
    } catch {
      // ignore fetch errors
    }
  }, [rpc]);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  useEffect(() => {
    if (timerRef.current) clearInterval(timerRef.current);
    if (autoRefresh && rpc) {
      timerRef.current = setInterval(fetchData, refreshInterval * 1000);
    }
    return () => {
      if (timerRef.current) clearInterval(timerRef.current);
    };
  }, [autoRefresh, refreshInterval, fetchData, rpc]);

  if (!stats) {
    return (
      <div className="skills-analytics-dashboard">
        <div className="settings-card-v2">
          <div className="settings-card-v2-title">{t("settings.skillsAnalytics")}</div>
          <div className="skills-analytics-loading">
            <div className="loading-spinner" />
            <p>{t("settings.analyticsLoading")}</p>
          </div>
        </div>
      </div>
    );
  }

  const skillEntries = Object.values(stats);
  const isEmpty = skillEntries.length === 0 || skillEntries.every(e => e.activation_count === 0);

  if (isEmpty) {
    return (
      <div className="skills-analytics-dashboard">
        <div className="settings-card-v2">
          <div className="settings-card-v2-title">{t("settings.skillsAnalytics")}</div>
          <div className="skills-analytics-empty">
            <BarChart width={48} height={48} data={[{v:3},{v:7},{v:5},{v:9},{v:4}]}>
              <Bar dataKey="v" fill="var(--border)" radius={[2,2,0,0]} />
            </BarChart>
            <p>{t("settings.analyticsEmpty")}</p>
          </div>
        </div>
      </div>
    );
  }

  // Summary
  const totalActivations = skillEntries.reduce((s, e) => s + e.activation_count, 0);
  const totalSuccess = skillEntries.reduce((s, e) => s + e.success_count, 0);
  const successRate = totalActivations > 0 ? Math.round((totalSuccess / totalActivations) * 100) : 0;
  const activeSkillCount = skillEntries.filter(e => e.activation_count > 0).length;

  // Top skills bar chart
  const topSkills = [...skillEntries]
    .sort((a, b) => b.activation_count - a.activation_count)
    .slice(0, 10)
    .map(s => ({ name: s.name, count: s.activation_count, success: s.success_count, fail: s.fail_count }));

  // Source distribution pie
  const sourceAgg: Record<string, number> = {};
  for (const s of skillEntries) {
    if (s.by_source) {
      for (const [src, cnt] of Object.entries(s.by_source)) {
        sourceAgg[src] = (sourceAgg[src] || 0) + cnt;
      }
    }
  }
  const sourceData = Object.entries(sourceAgg).map(([name, value]) => ({ name, value }));

  // Success trend line (group history by day)
  const dayMap: Record<string, { total: number; success: number }> = {};
  for (const rec of history) {
    const day = rec.timestamp?.slice(0, 10) || "unknown";
    if (!dayMap[day]) dayMap[day] = { total: 0, success: 0 };
    dayMap[day].total++;
    if (rec.success) dayMap[day].success++;
  }
  const trendData = Object.entries(dayMap)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([day, { total, success }]) => ({
      day: day.slice(5), // MM-DD
      rate: total > 0 ? Math.round((success / total) * 100) : 0,
      count: total,
    }));

  const exportJSON = () => {
    const blob = new Blob([JSON.stringify({ stats, history }, null, 2)], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url; a.download = `skill-analytics-${new Date().toISOString().slice(0, 10)}.json`;
    a.click(); URL.revokeObjectURL(url);
  };

  const exportCSV = () => {
    const rows = [["name", "scope", "activations", "success", "fail", "last_used", "avg_duration_ms", "total_tokens"]];
    for (const s of skillEntries) {
      rows.push([s.name, s.scope, String(s.activation_count), String(s.success_count), String(s.fail_count), s.last_used || "", String(s.avg_duration_ms), String(s.total_tokens)]);
    }
    const csv = rows.map(r => r.map(c => `"${c.replace(/"/g, '""')}"`).join(",")).join("\n");
    const blob = new Blob([csv], { type: "text/csv" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url; a.download = `skill-analytics-${new Date().toISOString().slice(0, 10)}.csv`;
    a.click(); URL.revokeObjectURL(url);
  };

  return (
    <div className="skills-analytics-dashboard">
      {/* Controls */}
      <div className="skills-analytics-controls">
        <label className="skills-analytics-auto-refresh">
          <input type="checkbox" checked={autoRefresh} onChange={(e) => setAutoRefresh(e.target.checked)} />
          {t("settings.autoRefresh")} ({refreshInterval}s)
        </label>
        <button type="button" className="skills-analytics-refresh-btn" onClick={fetchData}>
          {t("settings.refreshInterval")}
        </button>
        <button type="button" className="skills-analytics-refresh-btn" onClick={exportCSV}>
          {t("settings.exportCSV")}
        </button>
        <button type="button" className="skills-analytics-refresh-btn" onClick={exportJSON}>
          {t("settings.exportJSON")}
        </button>
      </div>

      {/* Summary cards */}
      <div className="skills-analytics-summary-cards">
        <div className="skills-analytics-summary-card">
          <div className="skills-analytics-summary-value">{totalActivations}</div>
          <div className="skills-analytics-summary-label">{t("settings.totalActivations")}</div>
        </div>
        <div className="skills-analytics-summary-card">
          <div className="skills-analytics-summary-value">{successRate}%</div>
          <div className="skills-analytics-summary-label">{t("settings.successRate")}</div>
        </div>
        <div className="skills-analytics-summary-card">
          <div className="skills-analytics-summary-value">{activeSkillCount}</div>
          <div className="skills-analytics-summary-label">{t("settings.activeSkills")}</div>
        </div>
      </div>

      {/* Charts */}
      <div className="skills-analytics-charts">
        {/* Top Skills Bar Chart */}
        <div className="skills-analytics-chart-card">
          <div className="skills-analytics-chart-title">{t("settings.topSkills")}</div>
          {topSkills.length > 0 ? (
            <ResponsiveContainer width="100%" height={240}>
              <BarChart data={topSkills} layout="vertical" margin={{ left: 80, right: 16, top: 8, bottom: 8 }}>
                <XAxis type="number" stroke="var(--text-muted)" fontSize={11} />
                <YAxis type="category" dataKey="name" stroke="var(--text-muted)" fontSize={11} width={76} />
                <Tooltip
                  contentStyle={{ background: "var(--bg-secondary)", border: "1px solid var(--border)", borderRadius: 8, fontSize: 12 }}
                  labelStyle={{ color: "var(--text)" }}
                />
                <Bar dataKey="success" stackId="a" fill="#81c995" name="Success" />
                <Bar dataKey="fail" stackId="a" fill="#f28b82" name="Fail" radius={[0, 4, 4, 0]} />
              </BarChart>
            </ResponsiveContainer>
          ) : (
            <p className="skills-analytics-no-data">{t("settings.noData")}</p>
          )}
        </div>

        {/* Source Distribution Pie */}
        <div className="skills-analytics-chart-card">
          <div className="skills-analytics-chart-title">{t("settings.sourceDistribution")}</div>
          {sourceData.length > 0 ? (
            <ResponsiveContainer width="100%" height={240}>
              <PieChart>
                <Pie data={sourceData} cx="50%" cy="50%" outerRadius={80} dataKey="value" label={({ name, percent }) => `${name} ${((percent ?? 0) * 100).toFixed(0)}%`} fontSize={11}>
                  {sourceData.map((_, i) => (
                    <Cell key={i} fill={COLORS[i % COLORS.length]} />
                  ))}
                </Pie>
                <Tooltip contentStyle={{ background: "var(--bg-secondary)", border: "1px solid var(--border)", borderRadius: 8, fontSize: 12 }} />
                <Legend wrapperStyle={{ fontSize: 11 }} />
              </PieChart>
            </ResponsiveContainer>
          ) : (
            <p className="skills-analytics-no-data">{t("settings.noData")}</p>
          )}
        </div>

        {/* Success Trend Line */}
        <div className="skills-analytics-chart-card skills-analytics-chart-wide">
          <div className="skills-analytics-chart-title">{t("settings.successTrend")}</div>
          {trendData.length > 0 ? (
            <ResponsiveContainer width="100%" height={200}>
              <LineChart data={trendData} margin={{ left: 8, right: 16, top: 8, bottom: 8 }}>
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                <XAxis dataKey="day" stroke="var(--text-muted)" fontSize={11} />
                <YAxis stroke="var(--text-muted)" fontSize={11} domain={[0, 100]} unit="%" />
                <Tooltip contentStyle={{ background: "var(--bg-secondary)", border: "1px solid var(--border)", borderRadius: 8, fontSize: 12 }} />
                <Line type="monotone" dataKey="rate" stroke="#8ab4f8" strokeWidth={2} dot={{ r: 3 }} name="Success %" />
              </LineChart>
            </ResponsiveContainer>
          ) : (
            <p className="skills-analytics-no-data">{t("settings.noData")}</p>
          )}
        </div>
      </div>
    </div>
  );
}
