"use client";

import { useState, useMemo, useEffect, useCallback, useRef } from "react";
import type { ReactNode } from "react";
import type { GenericTaskStatus, SkillContentResult, SkillImportPayload, SkillImportPreviewResult, SkillInfo, SkillStats } from "@/features/rpc/types";
import { useT, type TKey } from "@/features/i18n";
import { Search, MousePointerClick, X as XIcon } from "lucide-react";
import { SkillsImportModal } from "./SkillsImportModal";

const PAGE_SIZE = 24;

type TabKey = "defined" | "learned";

interface Props {
  skills: SkillInfo[];
  disabledSkills?: string[];
  onRemove?: (name: string) => Promise<void>;
  onPromote?: (name: string) => Promise<void>;
  onToggleSkill?: (name: string, disabled: boolean) => Promise<void>;
  onLoadContent?: (name: string) => Promise<SkillContentResult>;
  onLoadAnalytics?: () => Promise<Record<string, SkillStats> | null>;
  onSelectRelated?: (name: string) => void;
  onImport?: (payload: SkillImportPayload) => Promise<{ taskId: string }>;
  onPreviewImport?: (payload: SkillImportPayload) => Promise<SkillImportPreviewResult>;
  onTaskStatus?: (taskId: string) => Promise<GenericTaskStatus>;
  onRefreshSkills?: () => Promise<SkillInfo[]>;
  // Optional slot rendered at the very start of the toolbar, before the
  // defined/learned tabs. Used by MySkillsView to inline its scope-filter
  // pills so we don't waste a separate row above the catalog.
  toolbarLeftSlot?: ReactNode;
}

function SkillCard({
  skill,
  isActive,
  isLearned,
  isDisabled,
  onClick,
  t,
}: {
  skill: SkillInfo;
  isActive: boolean;
  isLearned: boolean;
  isDisabled: boolean;
  onClick: () => void;
  t: (key: TKey) => string;
}) {
  return (
    <button
      type="button"
      className={`skills-card ${isActive ? "active" : ""} ${isLearned ? "skills-card-learned" : ""} ${isDisabled ? "skills-card-disabled" : ""}`}
      onClick={onClick}
    >
      <div className="skills-card-name">
        <span className="skills-card-name-text">{skill.Name}</span>
        {isLearned && <span className="skills-learned-badge">{t("skills.learnedBadge")}</span>}
        {isDisabled && <span className="skills-disabled-badge">{t("skills.disabled")}</span>}
      </div>
      <p className="skills-card-desc">
        {skill.Description || t("skills.noDescription")}
      </p>
    </button>
  );
}

export function SkillsCatalog({ skills, disabledSkills = [], onRemove, onPromote, onToggleSkill, onLoadContent, onLoadAnalytics, onSelectRelated, onImport, onPreviewImport, onTaskStatus, onRefreshSkills, toolbarLeftSlot }: Props) {
  const { t } = useT();
  type SortKey = "name" | "usage" | "successRate" | "lastUsed";
  const [searchInput, setSearchInput] = useState("");
  const [search, setSearch] = useState("");
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [sortBy, setSortBy] = useState<SortKey>("name");
  const [activeTab, setActiveTab] = useState<TabKey>("defined");
  const [activeSkill, setActiveSkill] = useState<SkillInfo | null>(null);
  const [currentPage, setCurrentPage] = useState(1);
  const [actionLoading, setActionLoading] = useState(false);
  const [skillContent, setSkillContent] = useState<SkillContentResult | null>(null);
  const [contentLoading, setContentLoading] = useState(false);
  const [showContent, setShowContent] = useState(false);
  const [selectedSkills, setSelectedSkills] = useState<Set<string>>(new Set());
  const [bulkLoading, setBulkLoading] = useState(false);
  const [analyticsMap, setAnalyticsMap] = useState<Record<string, SkillStats> | null>(null);
  const [analyticsLoading, setAnalyticsLoading] = useState(false);
  const [importOpen, setImportOpen] = useState(false);

  const loadAnalytics = useCallback(async () => {
    if (!onLoadAnalytics || analyticsMap) return;
    setAnalyticsLoading(true);
    try {
      const data = await onLoadAnalytics();
      setAnalyticsMap(data ?? {});
    } finally {
      setAnalyticsLoading(false);
    }
  }, [onLoadAnalytics, analyticsMap]);

  // Load analytics on mount
  useEffect(() => { loadAnalytics(); }, [loadAnalytics]);

  const disabledSet = useMemo(() => new Set(disabledSkills.map(n => n.toLowerCase())), [disabledSkills]);

  const filtered = useMemo(() => {
    if (!search.trim()) return skills;
    const q = search.toLowerCase();
    return skills.filter(
      (s) =>
        s.Name.toLowerCase().includes(q) ||
        s.Description.toLowerCase().includes(q) ||
        s.Keywords?.some(kw => kw.toLowerCase().includes(q)) ||
        s.Scope?.toLowerCase().includes(q)
    );
  }, [skills, search]);

  const sorted = useMemo(() => {
    if (sortBy === "name") return [...filtered].sort((a, b) => a.Name.localeCompare(b.Name));
    if (!analyticsMap) return filtered;
    return [...filtered].sort((a, b) => {
      const sa = analyticsMap[a.Name];
      const sb = analyticsMap[b.Name];
      if (sortBy === "usage") return (sb?.activation_count ?? 0) - (sa?.activation_count ?? 0);
      if (sortBy === "successRate") {
        const ra = sa && sa.activation_count > 0 ? sa.success_count / sa.activation_count : 0;
        const rb = sb && sb.activation_count > 0 ? sb.success_count / sb.activation_count : 0;
        return rb - ra;
      }
      // lastUsed
      return (sb?.last_used ?? "").localeCompare(sa?.last_used ?? "");
    });
  }, [filtered, sortBy, analyticsMap]);

  const { defined, learned } = useMemo(() => {
    const learned: SkillInfo[] = [];
    const defined: SkillInfo[] = [];
    for (const s of sorted) {
      if (s.Scope === "learned") {
        learned.push(s);
      } else {
        defined.push(s);
      }
    }
    return { defined, learned };
  }, [sorted]);

  const currentList = activeTab === "learned" ? learned : defined;
  const totalPages = Math.max(1, Math.ceil(currentList.length / PAGE_SIZE));

  useEffect(() => {
    if (currentPage > totalPages && totalPages > 0) {
      setCurrentPage(totalPages);
    }
  }, [currentPage, totalPages]);

  const paginated = useMemo(() => {
    const start = (currentPage - 1) * PAGE_SIZE;
    return currentList.slice(start, start + PAGE_SIZE);
  }, [currentPage, currentList]);

  const handleSearch = (value: string) => {
    setSearchInput(value);
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => {
      setSearch(value);
      setCurrentPage(1);
    }, 300);
  };

  const handleTabChange = (tab: TabKey) => {
    setActiveTab(tab);
    setCurrentPage(1);
    setActiveSkill(null);
    setSkillContent(null);
    setShowContent(false);
  };

  const handleRemove = useCallback(async (name: string) => {
    if (!onRemove || !confirm(t("skills.confirmRemove"))) return;
    setActionLoading(true);
    try {
      await onRemove(name);
      if (activeSkill?.Name === name) setActiveSkill(null);
    } finally {
      setActionLoading(false);
    }
  }, [onRemove, activeSkill, t]);

  const handlePromote = useCallback(async (name: string) => {
    if (!onPromote) return;
    setActionLoading(true);
    try {
      await onPromote(name);
      if (activeSkill?.Name === name) setActiveSkill(null);
    } finally {
      setActionLoading(false);
    }
  }, [onPromote, activeSkill]);

  const handleLoadContent = useCallback(async (name: string) => {
    if (!onLoadContent) return;
    setContentLoading(true);
    try {
      const result = await onLoadContent(name);
      setSkillContent(result);
      setShowContent(true);
    } finally {
      setContentLoading(false);
    }
  }, [onLoadContent]);

  const handleSelectSkill = useCallback((skill: SkillInfo) => {
    setActiveSkill(skill);
    setSkillContent(null);
    setShowContent(false);
  }, []);

  const toggleSelect = useCallback((name: string) => {
    setSelectedSkills(prev => {
      const next = new Set(prev);
      if (next.has(name)) next.delete(name); else next.add(name);
      return next;
    });
  }, []);

  const selectAllOnPage = useCallback(() => {
    const names = paginated.map(s => s.Name);
    setSelectedSkills(prev => {
      const next = new Set(prev);
      const allSelected = names.every(n => next.has(n));
      if (allSelected) { names.forEach(n => next.delete(n)); } else { names.forEach(n => next.add(n)); }
      return next;
    });
  }, [paginated]);

  const bulkAction = useCallback(async (action: "enable" | "disable" | "remove") => {
    if (selectedSkills.size === 0) return;
    setBulkLoading(true);
    try {
      for (const name of selectedSkills) {
        if (action === "remove" && onRemove) await onRemove(name);
        else if (action === "enable" && onToggleSkill) await onToggleSkill(name, false);
        else if (action === "disable" && onToggleSkill) await onToggleSkill(name, true);
      }
      setSelectedSkills(new Set());
    } finally {
      setBulkLoading(false);
    }
  }, [selectedSkills, onRemove, onToggleSkill]);

  const scopeLabel = (scope?: string) => {
    if (scope === "learned") return t("skills.learnedBadge");
    if (scope === "repo") return "Repo";
    if (scope === "user") return "User";
    return scope || "-";
  };

  return (
    <>
      <div className="skills-grid">
        <section className="skills-list-section">
          {/* Combined toolbar: tabs + search/sort/import in one row. */}
          <div className="skills-list-toolbar">
            {toolbarLeftSlot}
            <div className="skills-tabs" role="tablist" aria-label={t("skills.title")}>
              <button
                type="button"
                role="tab"
                aria-selected={activeTab === "defined"}
                className={`skills-tab ${activeTab === "defined" ? "skills-tab-active" : ""}`}
                onClick={() => handleTabChange("defined")}
              >
                {t("skills.defined")}
                <span className="skills-tab-count">{defined.length}</span>
              </button>
              <button
                type="button"
                role="tab"
                aria-selected={activeTab === "learned"}
                className={`skills-tab ${activeTab === "learned" ? "skills-tab-active" : ""}`}
                onClick={() => handleTabChange("learned")}
              >
                {t("skills.learned")}
                <span className="skills-tab-count">{learned.length}</span>
              </button>
            </div>
            <div className="skills-list-toolbar-controls">
              <div className="skills-search-wrapper">
                <Search size={15} className="skills-search-icon" />
                <input
                  className="skills-search-input"
                  type="text"
                  placeholder={t("skills.search")}
                  value={searchInput}
                  onChange={(e) => handleSearch(e.target.value)}
                />
              </div>
              <select
                className="skills-sort-select"
                value={sortBy}
                onChange={(e) => { setSortBy(e.target.value as SortKey); setCurrentPage(1); }}
                aria-label={t("skills.sortBy")}
              >
                <option value="name">{t("skills.sortByName")}</option>
                <option value="usage">{t("skills.sortByUsage")}</option>
                <option value="successRate">{t("skills.sortBySuccessRate")}</option>
                <option value="lastUsed">{t("skills.sortByLastUsed")}</option>
              </select>
              {onImport && (
                <button
                  type="button"
                  className="skills-page-primary-btn"
                  onClick={() => setImportOpen(true)}
                >
                  {t("skills.import")}
                </button>
              )}
            </div>
          </div>

          {/* Tab description */}
          {activeTab === "learned" && (
            <p className="skills-tab-desc">{t("skills.learnedDesc")}</p>
          )}

          {/* Bulk toolbar */}
          {selectedSkills.size > 0 && (
            <div className="skills-bulk-toolbar">
              <span className="skills-bulk-count">{selectedSkills.size} {t("skills.selected")}</span>
              {onToggleSkill && (
                <>
                  <button className="skills-bulk-btn" onClick={() => bulkAction("enable")} disabled={bulkLoading}>{t("skills.bulkEnable")}</button>
                  <button className="skills-bulk-btn" onClick={() => bulkAction("disable")} disabled={bulkLoading}>{t("skills.bulkDisable")}</button>
                </>
              )}
              {onRemove && activeTab === "learned" && (
                <button className="skills-bulk-btn skills-bulk-btn-danger" onClick={() => bulkAction("remove")} disabled={bulkLoading}>{t("skills.bulkRemove")}</button>
              )}
              <button className="skills-bulk-btn" onClick={() => setSelectedSkills(new Set())}>&times;</button>
            </div>
          )}

          {/* Skill Cards */}
          {currentList.length === 0 ? (
            <div className="skills-empty">
              {skills.length === 0
                ? t("skills.noRegistered")
                : activeTab === "learned"
                  ? t("skills.noLearned")
                  : t("skills.noMatch")}
            </div>
          ) : (
            <>
              <div className="skills-bulk-select-all">
                <label>
                  <input type="checkbox" checked={paginated.length > 0 && paginated.every(s => selectedSkills.has(s.Name))} onChange={selectAllOnPage} />
                  {t("skills.selectAll")}
                </label>
              </div>
              <div className="skills-card-grid">
                {paginated.map((s) => (
                  <div key={s.Name} className="skills-card-wrapper">
                    <input
                      type="checkbox"
                      className="skills-card-checkbox"
                      checked={selectedSkills.has(s.Name)}
                      onChange={() => toggleSelect(s.Name)}
                      onClick={(e) => e.stopPropagation()}
                    />
                    <SkillCard
                      skill={s}
                      isActive={activeSkill?.Name === s.Name}
                      isLearned={s.Scope === "learned"}
                      isDisabled={disabledSet.has(s.Name.toLowerCase())}
                      onClick={() => handleSelectSkill(s)}
                      t={t}
                    />
                  </div>
                ))}
              </div>
            </>
          )}

          {totalPages > 1 && (
            <div className="skills-pagination">
              <span className="skills-pagination-info">
                {t("skills.page")} {currentPage} {t("skills.of")} {totalPages} ({currentList.length} {t("skills.total")})
              </span>
              <div className="skills-pagination-buttons">
                <button
                  type="button"
                  disabled={currentPage === 1}
                  onClick={() =>
                    setCurrentPage((p) => Math.max(1, p - 1))
                  }
                  className="skills-pagination-btn"
                >
                  {t("skills.previous")}
                </button>
                <button
                  type="button"
                  disabled={currentPage >= totalPages}
                  onClick={() =>
                    setCurrentPage((p) => Math.min(totalPages, p + 1))
                  }
                  className="skills-pagination-btn"
                >
                  {t("skills.next")}
                </button>
              </div>
            </div>
          )}
        </section>

        <aside className="skills-detail-panel">
          <div className="skills-detail-header">
            <h2 className="skills-detail-title">{t("skills.details")}</h2>
            {activeSkill && (
              <button
                type="button"
                className="skills-detail-close"
                onClick={() => {
                  setActiveSkill(null);
                  setSkillContent(null);
                  setShowContent(false);
                }}
                aria-label={t("common.close")}
                title={t("common.close")}
              >
                <XIcon size={14} strokeWidth={1.75} />
              </button>
            )}
          </div>
          {activeSkill ? (
            <div className="skills-detail-content">
              <div className="skills-detail-name">
                {activeSkill.Name}
                {activeSkill.Scope === "learned" && (
                  <span className="skills-learned-badge">{t("skills.learnedBadge")}</span>
                )}
              </div>
              <div className="skills-detail-meta">
                <div className="skills-detail-row">
                  <span className="skills-detail-label">{t("skills.name")}</span>
                  <span className="skills-detail-value">
                    {activeSkill.Name}
                  </span>
                </div>
                <div className="skills-detail-row">
                  <span className="skills-detail-label">{t("skills.description")}</span>
                  <span className="skills-detail-value">
                    {activeSkill.Description || "-"}
                  </span>
                </div>
                <div className="skills-detail-row">
                  <span className="skills-detail-label">{t("skills.scope")}</span>
                  <span className="skills-detail-value">
                    {scopeLabel(activeSkill.Scope)}
                  </span>
                </div>
              </div>
              {onToggleSkill && (
                <div className="skills-detail-section">
                  <button
                    type="button"
                    className={`skills-toggle-btn ${disabledSet.has(activeSkill.Name.toLowerCase()) ? "skills-toggle-disabled" : "skills-toggle-enabled"}`}
                    disabled={actionLoading}
                    onClick={async () => {
                      const isCurrentlyDisabled = disabledSet.has(activeSkill.Name.toLowerCase());
                      setActionLoading(true);
                      try {
                        await onToggleSkill(activeSkill.Name, !isCurrentlyDisabled);
                      } finally {
                        setActionLoading(false);
                      }
                    }}
                  >
                    {disabledSet.has(activeSkill.Name.toLowerCase()) ? t("skills.enable") : t("skills.disable")}
                  </button>
                </div>
              )}
              {activeSkill.Keywords && activeSkill.Keywords.length > 0 && (
                <div className="skills-detail-section">
                  <div className="skills-detail-section-title">{t("skills.keywords")}</div>
                  <div className="skills-detail-tags">
                    {activeSkill.Keywords.map((kw) => (
                      <span key={kw} className="skills-tag">{kw}</span>
                    ))}
                  </div>
                </div>
              )}
              {activeSkill.RelatedSkills && activeSkill.RelatedSkills.length > 0 && (
                <div className="skills-detail-section">
                  <div className="skills-detail-section-title">{t("skills.relatedSkills")}</div>
                  <div className="skills-detail-tags">
                    {activeSkill.RelatedSkills.map((rs) => (
                      <button
                        key={rs}
                        type="button"
                        className="skills-tag skills-tag-link"
                        onClick={() => {
                          const found = skills.find((s) => s.Name === rs);
                          if (found) {
                            // Switch tab if needed
                            if (found.Scope === "learned" && activeTab !== "learned") {
                              setActiveTab("learned");
                            } else if (found.Scope !== "learned" && activeTab !== "defined") {
                              setActiveTab("defined");
                            }
                            handleSelectSkill(found);
                          }
                          onSelectRelated?.(rs);
                        }}
                      >
                        {rs}
                      </button>
                    ))}
                  </div>
                </div>
              )}
              <div className="skills-detail-section">
                <div className="skills-detail-section-title">{t("skills.usage")}</div>
                <div className="skills-detail-usage">
                  <code>/{activeSkill.Name}</code>
                </div>
              </div>
              {onLoadContent && (
                <div className="skills-detail-section">
                  <button
                    type="button"
                    className="skills-action-btn skills-action-content"
                    disabled={contentLoading}
                    onClick={() => {
                      if (showContent) {
                        setShowContent(false);
                      } else if (skillContent) {
                        setShowContent(true);
                      } else {
                        handleLoadContent(activeSkill.Name);
                      }
                    }}
                  >
                    {contentLoading
                      ? t("skills.loadingContent")
                      : showContent
                        ? t("skills.hideContent")
                        : t("skills.viewContent")}
                  </button>
                  {showContent && skillContent && (
                    <div className="skills-content-body">
                      {skillContent.support_files && Object.keys(skillContent.support_files).length > 0 && (
                        <div className="skills-support-files">
                          <div className="skills-detail-section-title">{t("skills.supportFiles")}</div>
                          {Object.entries(skillContent.support_files).map(([dir, files]) => (
                            <div key={dir}>
                              <strong>{dir}/</strong>
                              <ul>{files.map((f) => <li key={f}>{f}</li>)}</ul>
                            </div>
                          ))}
                        </div>
                      )}
                      <pre className="skills-content-pre">{skillContent.body}</pre>
                    </div>
                  )}
                </div>
              )}
              {/* Analytics */}
              {analyticsMap && (() => {
                const stats = analyticsMap[activeSkill.Name];
                if (!stats || stats.activation_count === 0) {
                  return (
                    <div className="skills-analytics-section">
                      <div className="skills-detail-section-title">{t("skills.analytics")}</div>
                      <p className="skills-detail-empty" style={{ padding: "8px 0" }}>{t("skills.noAnalytics")}</p>
                    </div>
                  );
                }
                const successRate = stats.activation_count > 0
                  ? Math.round((stats.success_count / stats.activation_count) * 100)
                  : 0;
                const lastUsed = stats.last_used
                  ? new Date(stats.last_used).toLocaleDateString()
                  : t("skills.never");
                return (
                  <div className="skills-analytics-section">
                    <div className="skills-detail-section-title">{t("skills.analytics")}</div>
                    <div className="skills-analytics-grid">
                      <div className="skills-analytics-stat">
                        <span className="skills-analytics-stat-label">{t("skills.activationCount")}</span>
                        <span className="skills-analytics-stat-value">{stats.activation_count}</span>
                      </div>
                      <div className="skills-analytics-stat">
                        <span className="skills-analytics-stat-label">{t("skills.successRate")}</span>
                        <span className="skills-analytics-stat-value">{successRate}%</span>
                        <div className="skills-analytics-bar">
                          <div className="skills-analytics-bar-fill" style={{ width: `${successRate}%` }} />
                        </div>
                      </div>
                      <div className="skills-analytics-stat">
                        <span className="skills-analytics-stat-label">{t("skills.lastUsed")}</span>
                        <span className="skills-analytics-stat-value">{lastUsed}</span>
                      </div>
                      <div className="skills-analytics-stat">
                        <span className="skills-analytics-stat-label">{t("skills.avgDuration")}</span>
                        <span className="skills-analytics-stat-value">{Math.round(stats.avg_duration_ms)}ms</span>
                      </div>
                      <div className="skills-analytics-stat">
                        <span className="skills-analytics-stat-label">{t("skills.totalTokens")}</span>
                        <span className="skills-analytics-stat-value">{stats.total_tokens.toLocaleString()}</span>
                      </div>
                    </div>
                    {stats.by_source && Object.keys(stats.by_source).length > 0 && (
                      <div>
                        <div className="skills-analytics-stat-label" style={{ marginTop: 8 }}>{t("skills.activationSource")}</div>
                        <div className="skills-analytics-sources">
                          {Object.entries(stats.by_source).map(([src, count]) => (
                            <span key={src} className="skills-analytics-source-tag">
                              {src} <span className="skills-analytics-source-count">{count}</span>
                            </span>
                          ))}
                        </div>
                      </div>
                    )}
                  </div>
                );
              })()}
              {activeSkill.Scope === "learned" && (
                <div className="skills-detail-actions">
                  <button
                    type="button"
                    className="skills-action-btn skills-action-promote"
                    disabled={actionLoading}
                    onClick={() => handlePromote(activeSkill.Name)}
                  >
                    {t("skills.promote")}
                  </button>
                  <button
                    type="button"
                    className="skills-action-btn skills-action-remove"
                    disabled={actionLoading}
                    onClick={() => handleRemove(activeSkill.Name)}
                  >
                    {t("skills.remove")}
                  </button>
                </div>
              )}
            </div>
          ) : (
            <div className="skills-detail-empty">
              <MousePointerClick size={28} className="skills-detail-empty-icon" />
              <p>{t("skills.selectToView")}</p>
            </div>
          )}
        </aside>
      </div>

      {importOpen && onImport && onPreviewImport && onTaskStatus && (
        <SkillsImportModal
          open={importOpen}
          onClose={() => setImportOpen(false)}
          onImport={onImport}
          onPreview={onPreviewImport}
          onTaskStatus={onTaskStatus}
          onRefreshSkills={onRefreshSkills}
          onImported={(refreshed, importedNames) => {
            if (importedNames.length === 0) return;
            const nextActive = refreshed.find((skill) => skill.Name === importedNames[0]);
            if (!nextActive) return;
            setActiveTab(nextActive.Scope === "learned" ? "learned" : "defined");
            setCurrentPage(1);
            setActiveSkill(nextActive);
          }}
        />
      )}
    </>
  );
}
