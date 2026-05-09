"use client";

import { useCallback, useEffect, useMemo, useRef, useState, memo } from "react";
import {
  Search,
  RefreshCw,
  Download,
  Star,
  Package,
  Trash2,
  X,
  Globe,
  Lock,
} from "lucide-react";
import { RpcError, type RPCClient } from "@/features/rpc/client";
import type {
  SkillhubConfig,
  SkillhubSearchHit,
  SkillhubSkill,
  SkillhubVersion,
} from "@/features/rpc/types";
import { useT, type TKey } from "@/features/i18n";
import { useSkillhubRpc } from "../shared/useSkillhubRpc";
import { SkillhubLoginModal } from "../shared/SkillhubLoginModal";
import { renderExternalMarkdown } from "@/features/chat/markdown";

interface Props {
  rpc: RPCClient | null;
  config: SkillhubConfig | null;
  onConfigChange?: () => void;
  onInstalled?: () => void;
  onShowToast?: (msg: string, kind: "success" | "error") => void;
}

type SortKey = "newest" | "hottest" | "downloads";

// Custom JSON-RPC error code returned by the backend when install/sync of a
// private skill needs an authenticated session. Keep in sync with
// pkg/server/skillhub_handler.go: skillhubAuthRequiredCode.
const RPC_AUTH_REQUIRED = -32010;

interface PlazaItem {
  slug: string;
  displayName?: string;
  summary?: string;
  category?: string;
  ownerHandle?: string;
  kind?: string;
  downloads?: number;
  starsCount?: number;
  visibility?: string; // "public" | "private" | undefined
}

function hitToItem(hit: SkillhubSearchHit): PlazaItem {
  return {
    slug: hit.slug,
    displayName: hit.displayName,
    summary: hit.summary,
    category: hit.category,
    ownerHandle: hit.ownerHandle,
    kind: hit.kind,
    downloads: hit.downloads,
    starsCount: hit.starsCount,
    visibility: hit.visibility,
  };
}

function skillToItem(s: SkillhubSkill): PlazaItem {
  return {
    slug: s.slug,
    displayName: s.displayName,
    summary: s.summary,
    category: s.category,
    ownerHandle: s.ownerHandle,
    kind: s.kind,
    downloads: s.downloads,
    starsCount: s.starsCount,
    visibility: s.visibility,
  };
}

function categoryLabel(id: string, t: ReturnType<typeof useT>["t"]): string {
  if (!id) return t("plaza.categoryAll");
  // Try a translation key first; fall back to title-cased id when missing.
  const key = ("plaza.cat." + id) as TKey;
  // dict has no entries for plaza.cat.* yet — fall back to title case.
  void key;
  return id.charAt(0).toUpperCase() + id.slice(1);
}

function formatRelative(iso: string | undefined, t: ReturnType<typeof useT>["t"]): string {
  if (!iso) return t("plaza.lastSyncNever");
  const ts = Date.parse(iso);
  if (Number.isNaN(ts)) return t("plaza.lastSyncNever");
  const diffMs = Date.now() - ts;
  const sec = Math.max(0, Math.floor(diffMs / 1000));
  if (sec < 60) return t("plaza.justNow");
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min} ${t("plaza.minutesAgo")}`;
  const h = Math.floor(min / 60);
  if (h < 24) return `${h} ${t("plaza.hoursAgo")}`;
  const d = Math.floor(h / 24);
  return `${d} ${t("plaza.daysAgo")}`;
}

function SkillPlazaViewInner({ rpc, config, onConfigChange, onInstalled, onShowToast }: Props) {
  const { t } = useT();
  const api = useSkillhubRpc(rpc);

  const [searchInput, setSearchInput] = useState("");
  const [appliedSearch, setAppliedSearch] = useState("");
  const [category, setCategory] = useState("");
  const [sort, setSort] = useState<SortKey>("newest");

  // Categories from server. Keep a sensible fallback so the chip row never
  // shows an empty state while RPC is loading.
  const FALLBACK_CATEGORIES = useMemo(
    () => ["general", "code", "productivity", "writing", "research", "data", "ops"],
    [],
  );
  const [categories, setCategories] = useState<string[]>(FALLBACK_CATEGORIES);

  const [items, setItems] = useState<PlazaItem[]>([]);
  const [cursor, setCursor] = useState("");
  const [hasMore, setHasMore] = useState(false);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState("");

  const [selected, setSelected] = useState<PlazaItem | null>(null);
  const [detail, setDetail] = useState<SkillhubSkill | null>(null);
  const [versions, setVersions] = useState<SkillhubVersion[]>([]);
  const [detailLoading, setDetailLoading] = useState(false);
  const [installing, setInstalling] = useState(false);
  const [syncRunning, setSyncRunning] = useState(false);
  const [pendingLogin, setPendingLogin] = useState(false);
  // Slug awaiting install once login completes — restored after device flow.
  const [pendingInstallSlug, setPendingInstallSlug] = useState<string | null>(null);

  // Optimistic install/uninstall state. When a request is in flight we treat
  // the slug as installed (or NOT installed) immediately so the UI doesn't
  // visibly lag. Rolled back if the RPC call rejects.
  const [optimisticInstalled, setOptimisticInstalled] = useState<Set<string>>(new Set());
  const [optimisticUninstalled, setOptimisticUninstalled] = useState<Set<string>>(new Set());

  const subscribed = useMemo(() => {
    const set = new Set(config?.subscriptions ?? []);
    for (const s of optimisticInstalled) set.add(s);
    for (const s of optimisticUninstalled) set.delete(s);
    return set;
  }, [config?.subscriptions, optimisticInstalled, optimisticUninstalled]);

  // Race-protect list responses: only the latest fetch's results may apply.
  const fetchSeqRef = useRef(0);

  // Listing fetch — uses search if query present, else list endpoint.
  const fetchPage = useCallback(async (reset: boolean) => {
    if (!rpc) return;
    if (config?.offline) {
      setItems([]);
      setHasMore(false);
      setLoadError(t("plaza.offlineHint"));
      return;
    }
    const mySeq = ++fetchSeqRef.current;
    setLoading(true);
    setLoadError("");
    try {
      if (appliedSearch.trim()) {
        const res = await api.search(appliedSearch.trim(), 30);
        if (mySeq !== fetchSeqRef.current) return;
        setItems(res.hits.map(hitToItem));
        setCursor("");
        setHasMore(false);
      } else {
        const res = await api.list({
          category: category || undefined,
          sort,
          cursor: reset ? undefined : cursor || undefined,
          limit: 24,
        });
        if (mySeq !== fetchSeqRef.current) return;
        const newItems = res.data.map(skillToItem);
        setItems((prev) => (reset ? newItems : [...prev, ...newItems]));
        setCursor(res.nextCursor || "");
        setHasMore(Boolean(res.nextCursor));
      }
    } catch (e) {
      if (mySeq !== fetchSeqRef.current) return;
      setLoadError(e instanceof Error ? e.message : String(e));
    } finally {
      if (mySeq === fetchSeqRef.current) setLoading(false);
    }
  }, [api, appliedSearch, category, config?.offline, cursor, rpc, sort, t]);

  // Pull live category list once we have a connected RPC.
  useEffect(() => {
    if (!rpc) return;
    let cancelled = false;
    void api.categories().then((res) => {
      if (cancelled) return;
      if (Array.isArray(res?.categories) && res.categories.length > 0) {
        // Only update state when categories actually differ — avoids
        // unnecessary re-renders when the server returns the same list.
        setCategories((prev) => {
          if (prev.length === res.categories.length && prev.every((c, i) => c === res.categories[i])) return prev;
          return res.categories;
        });
      }
    }).catch(() => { /* keep fallback list */ });
    return () => { cancelled = true; };
  }, [api, rpc]);

  // Reset list whenever search query, category, or sort changes.
  // We deliberately reset cursor here so the next fetch starts from scratch
  // — without this, switching category would tail-paginate the wrong list.
  useEffect(() => {
    setCursor("");
    void fetchPage(true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [appliedSearch, category, sort, config?.offline]);

  const handleSearch = useCallback(
    (e?: React.FormEvent) => {
      e?.preventDefault();
      setAppliedSearch(searchInput);
    },
    [searchInput]
  );

  const openDetail = useCallback(
    async (item: PlazaItem) => {
      setSelected(item);
      setDetail(null);
      setVersions([]);
      if (!rpc) return;
      setDetailLoading(true);
      try {
        const [d, v] = await Promise.all([api.get(item.slug), api.versions(item.slug)]);
        setDetail(d);
        setVersions(v.versions || []);
      } catch (e) {
        onShowToast?.(e instanceof Error ? e.message : String(e), "error");
      } finally {
        setDetailLoading(false);
      }
    },
    [api, onShowToast, rpc]
  );

  const closeDetail = useCallback(() => {
    setSelected(null);
    setDetail(null);
    setVersions([]);
  }, []);

  const performInstall = useCallback(
    async (slug: string, version?: string) => {
      setInstalling(true);
      // Optimistic: mark as installed before the RPC returns. Roll back on error.
      setOptimisticInstalled((prev) => {
        const next = new Set(prev);
        next.add(slug);
        return next;
      });
      try {
        const res = await api.install(slug, version);
        onInstalled?.();
        onConfigChange?.();
        const msg = res.notModified
          ? `${t("plaza.installed")}: ${slug}`
          : `${t("plaza.installSuccess")}: ${slug}@${res.version}`;
        onShowToast?.(msg, "success");
      } catch (err) {
        // Rollback optimistic state and re-throw so the caller can branch.
        setOptimisticInstalled((prev) => {
          const next = new Set(prev);
          next.delete(slug);
          return next;
        });
        throw err;
      } finally {
        setInstalling(false);
      }
    },
    [api, onConfigChange, onInstalled, onShowToast, t]
  );

  const handleInstall = useCallback(
    async (slug: string, version?: string) => {
      try {
        await performInstall(slug, version);
      } catch (e) {
        // Auth-required: open login modal and remember the slug so we can
        // resume install once login completes. Backend returns the stable
        // RPC code -32010 for 401/403; we no longer parse error strings.
        if (e instanceof RpcError && e.code === RPC_AUTH_REQUIRED) {
          setPendingInstallSlug(slug);
          setPendingLogin(true);
          onShowToast?.(t("plaza.authRequired"), "error");
          return;
        }
        const msg = e instanceof Error ? e.message : String(e);
        onShowToast?.(`${t("plaza.installFailed")}: ${msg}`, "error");
      }
    },
    [onShowToast, performInstall, t]
  );

  const handleUninstall = useCallback(
    async (slug: string) => {
      // Optimistic removal.
      setOptimisticUninstalled((prev) => {
        const next = new Set(prev);
        next.add(slug);
        return next;
      });
      try {
        await api.uninstall(slug);
        onConfigChange?.();
        onInstalled?.();
        onShowToast?.(`${t("plaza.uninstallSuccess")}: ${slug}`, "success");
      } catch (e) {
        // Rollback.
        setOptimisticUninstalled((prev) => {
          const next = new Set(prev);
          next.delete(slug);
          return next;
        });
        onShowToast?.(e instanceof Error ? e.message : String(e), "error");
      }
    },
    [api, onConfigChange, onInstalled, onShowToast, t]
  );

  // Reset optimistic sets when config arrives — server is the source of truth.
  useEffect(() => {
    if (!config?.subscriptions) return;
    setOptimisticInstalled(new Set());
    setOptimisticUninstalled(new Set());
  }, [config?.subscriptions]);

  const handleSync = useCallback(async () => {
    setSyncRunning(true);
    try {
      const report = await api.sync();
      const updated = report.results.filter((r) => r.status === "updated").length;
      onShowToast?.(`${t("plaza.syncDone")} (${updated})`, "success");
      onConfigChange?.();
      onInstalled?.();
    } catch (e) {
      onShowToast?.(e instanceof Error ? e.message : String(e), "error");
    } finally {
      setSyncRunning(false);
    }
  }, [api, onConfigChange, onInstalled, onShowToast, t]);

  // Render-time helpers for the detail panel README. Falls back to summary
  // when the registry doesn't expose a Readme field.
  const readmeHtml = useMemo(() => {
    if (!detail) return "";
    const text = (detail as { readme?: string }).readme || detail.summary || "";
    return renderExternalMarkdown(text);
  }, [detail]);

  const lastSyncTitle = useMemo(() => {
    if (!config?.lastSyncAt && !config?.lastSyncStatus) return t("plaza.sync");
    return `${t("plaza.lastSync")}: ${formatRelative(config?.lastSyncAt, t)}${
      config?.lastSyncStatus
        ? ` (${t(
            `plaza.lastSync${config.lastSyncStatus.charAt(0).toUpperCase() + config.lastSyncStatus.slice(1)}` as TKey,
          )})`
        : ""
    }`;
  }, [config?.lastSyncAt, config?.lastSyncStatus, t]);

  return (
    <div className="plaza-view">
      <div className="skills-list-toolbar plaza-list-toolbar">
        <form className="skills-search-wrapper plaza-search" onSubmit={handleSearch}>
          <Search size={14} className="skills-search-icon" />
          <input
            type="search"
            value={searchInput}
            onChange={(e) => setSearchInput(e.target.value)}
            placeholder={t("plaza.searchPlaceholder")}
            className="skills-search-input"
          />
        </form>
        <div className="skills-list-toolbar-controls">
          <select
            className="skills-sort-select"
            value={sort}
            onChange={(e) => setSort(e.target.value as SortKey)}
            aria-label={t("skills.sortBy")}
          >
            <option value="newest">{t("plaza.sortNewest")}</option>
            <option value="hottest">{t("plaza.sortHottest")}</option>
            <option value="downloads">{t("plaza.sortDownloads")}</option>
          </select>
          <button
            type="button"
            className="skills-page-primary-btn plaza-sync-btn"
            onClick={() => void handleSync()}
            disabled={syncRunning || !rpc}
            title={lastSyncTitle}
          >
            <RefreshCw size={14} className={syncRunning ? "spin" : ""} />
            <span>{syncRunning ? t("plaza.syncRunning") : t("plaza.sync")}</span>
            {config?.lastSyncStatus && !syncRunning && (
              <span
                className={`plaza-sync-dot plaza-sync-status-${config.lastSyncStatus}`}
                aria-hidden="true"
              />
            )}
          </button>
        </div>
      </div>

      {(categories.length > 0) && (
        <div className="plaza-categories">
          <button
            key="_all"
            type="button"
            className={`skills-tag skills-tag-link ${category === "" ? "skills-tag-active" : ""}`}
            onClick={() => setCategory("")}
          >
            {t("plaza.categoryAll")}
          </button>
          {categories.map((id) => (
            <button
              key={id}
              type="button"
              className={`skills-tag skills-tag-link ${category === id ? "skills-tag-active" : ""}`}
              onClick={() => setCategory(id)}
            >
              {categoryLabel(id, t)}
            </button>
          ))}
        </div>
      )}

      {config?.offline && (
        <div className="skills-detail-empty skills-import-error">{t("plaza.offlineHint")}</div>
      )}

      {config && !config.offline && !config.registry && (
        <div className="skills-detail-empty skills-import-error">{t("plaza.noRegistryHint")}</div>
      )}

      {loadError && !config?.offline && (
        <div className="skills-detail-empty skills-import-error">{loadError}</div>
      )}

      {/* Two-column body: cards on the left, persistent detail pane on the right.
       * Mirrors SkillsCatalog's 2fr/1fr layout so Plaza and Mine read as the
       * same shell. The detail pane stays mounted to keep widths stable. */}
      <div className="plaza-layout">
      <div className="plaza-grid">
        {items.length === 0 && !loading && !loadError && (
          <div className="skills-detail-empty">{t("plaza.empty")}</div>
        )}
        {items.map((item) => {
          const installed = subscribed.has(item.slug);
          const isPrivate = item.visibility === "private";
          return (
            <button
              key={item.slug}
              type="button"
              className={`skills-card plaza-card ${selected?.slug === item.slug ? "active" : ""}`}
              onClick={() => void openDetail(item)}
            >
              <div className="skills-card-name">
                <span className="skills-card-name-text">{item.displayName || item.slug}</span>
                {installed && (
                  <span className="skills-learned-badge">{t("plaza.installed")}</span>
                )}
                {item.kind && item.kind !== "skill" && (
                  <span className="skills-disabled-badge">{item.kind}</span>
                )}
                {item.visibility && (
                  <span
                    className={`plaza-visibility-badge plaza-visibility-${isPrivate ? "private" : "public"}`}
                    title={isPrivate ? t("plaza.privateNeedsLogin") : t("plaza.public")}
                  >
                    {isPrivate ? <Lock size={10} /> : <Globe size={10} />}
                    <span>{isPrivate ? t("plaza.private") : t("plaza.public")}</span>
                  </span>
                )}
              </div>
              <p className="skills-card-desc">{item.summary || t("skills.noDescription")}</p>
              <div className="plaza-card-meta">
                {item.ownerHandle && <span>@{item.ownerHandle}</span>}
                {typeof item.downloads === "number" && (
                  <span className="plaza-card-meta-item">
                    <Download size={11} /> {item.downloads}
                  </span>
                )}
                {typeof item.starsCount === "number" && (
                  <span className="plaza-card-meta-item">
                    <Star size={11} /> {item.starsCount}
                  </span>
                )}
                {item.category && <span className="plaza-card-meta-item"><Package size={11} />{item.category}</span>}
              </div>
            </button>
          );
        })}
      </div>

      {hasMore && (
        <div className="plaza-load-more">
          <button
            type="button"
            className="settings-btn-cancel"
            onClick={() => void fetchPage(false)}
            disabled={loading}
          >
            {loading ? t("plaza.loading") : t("plaza.loadMore")}
          </button>
        </div>
      )}
      </div>

      {/* Persistent right-rail detail pane — replaces the prior fixed overlay.
       * When nothing is selected, shows a hint so the column has visible
       * purpose. Mobile collapses this to a row beneath the grid (see CSS). */}
      <aside className="plaza-detail-pane">
        {!selected && (
          <div className="skills-detail-empty plaza-detail-placeholder">
            {t("plaza.selectHint")}
          </div>
        )}
        {selected && (
          <div className="plaza-detail">
            <header className="plaza-detail-header">
              <div>
                <div className="plaza-detail-title">
                  {detail?.displayName || selected.displayName || selected.slug}
                  {detail?.visibility && (
                    <span
                      className={`plaza-visibility-badge plaza-visibility-${detail.visibility === "private" ? "private" : "public"}`}
                    >
                      {detail.visibility === "private" ? <Lock size={10} /> : <Globe size={10} />}
                      <span>
                        {detail.visibility === "private" ? t("plaza.private") : t("plaza.public")}
                      </span>
                    </span>
                  )}
                </div>
                <div className="plaza-detail-slug">{selected.slug}</div>
              </div>
              <button
                type="button"
                className="provider-modal-close plaza-detail-close"
                onClick={closeDetail}
                aria-label={t("common.close" as TKey)}
              >
                <X size={16} />
              </button>
            </header>

            <div className="plaza-detail-body">
              {detailLoading && <div className="skills-detail-empty">{t("plaza.loading")}</div>}
              {detail && (
                <>
                  <div className="plaza-detail-meta">
                    {detail.ownerHandle && (
                      <div>
                        <span className="skills-analytics-stat-label">{t("plaza.owner")}</span>
                        <span className="skills-analytics-stat-value">@{detail.ownerHandle}</span>
                      </div>
                    )}
                    <div>
                      <span className="skills-analytics-stat-label">{t("plaza.category")}</span>
                      <span className="skills-analytics-stat-value">{detail.category || "—"}</span>
                    </div>
                    {detail.kind && (
                      <div>
                        <span className="skills-analytics-stat-label">{t("plaza.kind")}</span>
                        <span className="skills-analytics-stat-value">{detail.kind}</span>
                      </div>
                    )}
                    <div>
                      <span className="skills-analytics-stat-label">{t("plaza.downloads")}</span>
                      <span className="skills-analytics-stat-value">{detail.downloads}</span>
                    </div>
                    <div>
                      <span className="skills-analytics-stat-label">{t("plaza.stars")}</span>
                      <span className="skills-analytics-stat-value">{detail.starsCount}</span>
                    </div>
                  </div>

                  {detail.tags && detail.tags.length > 0 && (
                    <div className="skills-detail-tags">
                      {detail.tags.map((tag) => (
                        <span key={tag} className="skills-tag">
                          {tag}
                        </span>
                      ))}
                    </div>
                  )}

                  <div className="provider-modal-section">
                    <div className="provider-modal-section-title">{t("plaza.readme")}</div>
                    {readmeHtml ? (
                      <div
                        className="md-content plaza-detail-readme"
                        dangerouslySetInnerHTML={{ __html: readmeHtml }}
                      />
                    ) : (
                      <div className="skills-detail-empty">{t("plaza.noReadme")}</div>
                    )}
                  </div>

                  {versions.length > 0 && (
                    <div className="provider-modal-section">
                      <div className="provider-modal-section-title">{t("plaza.versions")}</div>
                      <div className="plaza-detail-versions">
                        {versions.slice(0, 8).map((v) => (
                          <div key={v.id} className="plaza-detail-version-row">
                            <code className="plaza-detail-version-tag">{v.version}</code>
                            <span className="plaza-detail-version-time">{new Date(v.createdAt).toLocaleDateString()}</span>
                          </div>
                        ))}
                      </div>
                    </div>
                  )}
                </>
              )}
            </div>

            <footer className="plaza-detail-footer">
              {subscribed.has(selected.slug) ? (
                <>
                  <button
                    type="button"
                    className="settings-btn-cancel"
                    disabled={installing}
                    onClick={() => void handleUninstall(selected.slug)}
                  >
                    <Trash2 size={14} />
                    <span>{t("plaza.uninstall")}</span>
                  </button>
                  <button
                    type="button"
                    className="settings-btn-save"
                    disabled={installing}
                    onClick={() => void handleInstall(selected.slug)}
                  >
                    {installing ? t("plaza.installing") : t("plaza.update")}
                  </button>
                </>
              ) : (
                <button
                  type="button"
                  className="settings-btn-save"
                  disabled={installing}
                  onClick={() => void handleInstall(selected.slug)}
                >
                  {installing ? t("plaza.installing") : t("plaza.install")}
                </button>
              )}
            </footer>
          </div>
        )}
      </aside>

      <SkillhubLoginModal
        open={pendingLogin}
        rpc={rpc}
        registry={config?.registry}
        onClose={() => {
          setPendingLogin(false);
          setPendingInstallSlug(null);
        }}
        onSuccess={() => {
          setPendingLogin(false);
          onConfigChange?.();
          // After login succeeds, resume the install attempt the user kicked off.
          const slug = pendingInstallSlug;
          setPendingInstallSlug(null);
          if (slug) {
            void handleInstall(slug);
          }
        }}
      />
    </div>
  );
}

export const SkillPlazaView = memo(SkillPlazaViewInner);
