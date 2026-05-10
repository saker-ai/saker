"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { RPCClient } from "@/features/rpc/client";
import type {
  SkillhubConfig,
  SkillhubSearchResult,
  SkillhubListResult,
  SkillhubSkill,
  SkillhubVersion,
  SkillhubDeviceLogin,
  SkillhubLoginPollResult,
  SkillhubInstallResult,
  SkillhubSyncReport,
  SkillhubPublishResult,
  SkillhubCategoryList,
} from "@/features/rpc/types";

type RawSkillhubConfig = Omit<SkillhubConfig, "subscriptions"> & {
  subscriptions?: unknown;
};

export function normalizeSkillhubConfig(config: SkillhubConfig): SkillhubConfig {
  const raw = config as RawSkillhubConfig;
  return {
    ...config,
    subscriptions: Array.isArray(raw.subscriptions)
      ? raw.subscriptions.filter((slug): slug is string => typeof slug === "string")
      : [],
  };
}

// useSkillhubRpc wraps `skillhub/*` JSON-RPC methods with a tiny typed surface.
// `null` rpc = render-only (offline / not yet connected); calls reject.
export function useSkillhubRpc(rpc: RPCClient | null) {
  const reqRef = useRef(rpc);
  reqRef.current = rpc;

  const call = useCallback(async <T = unknown>(method: string, params?: Record<string, unknown>): Promise<T> => {
    if (!reqRef.current) throw new Error("rpc not connected");
    return reqRef.current.request<T>(method, params);
  }, []);

  return useMemo(
    () => ({
      getConfig: () => call<SkillhubConfig>("skillhub/config/get").then(normalizeSkillhubConfig),
      updateConfig: (patch: Partial<SkillhubConfig>) =>
        call<SkillhubConfig>("skillhub/config/update", patch as Record<string, unknown>)
          .then(normalizeSkillhubConfig),
      loginStart: (registry?: string) =>
        call<SkillhubDeviceLogin>("skillhub/login/start", registry ? { registry } : undefined),
      loginPoll: (sessionId: string) =>
        call<SkillhubLoginPollResult>("skillhub/login/poll", { sessionId }),
      loginCancel: (sessionId: string) =>
        call<{ ok: boolean }>("skillhub/login/cancel", { sessionId }),
      categories: () => call<SkillhubCategoryList>("skillhub/categories"),
      logout: () => call<SkillhubConfig>("skillhub/logout").then(normalizeSkillhubConfig),
      search: (q: string, limit = 20) => call<SkillhubSearchResult>("skillhub/search", { q, limit }),
      list: (params: { category?: string; sort?: string; cursor?: string; limit?: number } = {}) =>
        call<SkillhubListResult>("skillhub/list", params as Record<string, unknown>),
      get: (slug: string) => call<SkillhubSkill>("skillhub/get", { slug }),
      versions: (slug: string) =>
        call<{ versions: SkillhubVersion[] }>("skillhub/versions", { slug }),
      install: (slug: string, version?: string) =>
        call<SkillhubInstallResult>("skillhub/install", { slug, version }),
      uninstall: (slug: string) => call<{ ok: boolean }>("skillhub/uninstall", { slug }),
      sync: () => call<SkillhubSyncReport>("skillhub/sync"),
      publishLearned: (name: string) =>
        call<SkillhubPublishResult>("skillhub/publish-learned", { name }),
    }),
    [call]
  );
}

/** Compare two SkillhubConfig objects structurally. Returns true when they differ. */
function skillhubConfigChanged(prev: SkillhubConfig, next: SkillhubConfig): boolean {
  if (
    prev.registry !== next.registry ||
    prev.handle !== next.handle ||
    prev.loggedIn !== next.loggedIn ||
    prev.offline !== next.offline ||
    prev.autoSync !== next.autoSync ||
    prev.learnedAutoPublish !== next.learnedAutoPublish ||
    prev.lastSyncAt !== next.lastSyncAt ||
    prev.lastSyncStatus !== next.lastSyncStatus ||
    prev.learnedVisibility !== next.learnedVisibility ||
    prev.syncInterval !== next.syncInterval
  ) return true;
  const a = prev.subscriptions;
  const b = next.subscriptions;
  if (a.length !== b.length) return true;
  for (let i = 0; i < a.length; i++) {
    if (a[i] !== b[i]) return true;
  }
  return false;
}

// useSkillhubConfig keeps a polled snapshot of /skillhub/config so the chip
// + plaza + settings all see the same thing. Reload by calling the returned
// `refresh` function (e.g. after login / install).
export function useSkillhubConfig(rpc: RPCClient | null): {
  config: SkillhubConfig | null;
  loading: boolean;
  error: string | null;
  refresh: () => Promise<void>;
} {
  const api = useSkillhubRpc(rpc);
  const [config, setConfig] = useState<SkillhubConfig | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    if (!rpc) return;
    setLoading(true);
    setError(null);
    try {
      const cfg = normalizeSkillhubConfig(await api.getConfig());
      // Only update state when config actually changed — avoids cascading
      // re-renders in SkillsPage / SkillPlazaView when the data is identical.
      setConfig((prev) => {
        if (prev && !skillhubConfigChanged(prev, cfg)) return prev;
        return cfg;
      });
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, [rpc, api]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  return { config, loading, error, refresh };
}
