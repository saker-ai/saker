import type {
  AppInputField,
  AppOutputField,
  RunSummary,
} from "@/features/apps/appsApi";

// ---------- Re-exported types ----------

export type { AppInputField, AppOutputField, RunSummary };

export interface PublicSchema {
  name: string;
  description?: string;
  icon?: string;
  inputs: AppInputField[];
  outputs: AppOutputField[];
}

// ---------- Error ----------

export class PublicApiError extends Error {
  status: number;
  retryAfter?: number;

  constructor(status: number, message: string, retryAfter?: number) {
    super(message);
    this.status = status;
    this.retryAfter = retryAfter;
    this.name = "PublicApiError";
  }
}

// ---------- URL helpers ----------

/**
 * Mirror resolveHttpBaseUrl() from web/src/features/rpc/httpRpc.ts.
 * In dev (port 10111) talk to the API server on port 10112;
 * in embedded / production mode use the same origin.
 */
function baseUrl(): string {
  if (typeof window === "undefined") return "http://127.0.0.1:10112";
  const { protocol, hostname, host, port } = window.location;
  if (port === "10111") return `${protocol}//${hostname}:10112`;
  return `${protocol}//${host}`;
}

/**
 * Build the public-app path.
 *
 * Single-tenant (projectId === null):  /api/apps/public/{token}/...rest
 * Multi-tenant  (projectId provided):  /api/apps/{projectId}/public/{token}/...rest
 */
function publicPath(
  token: string,
  projectId: string | null,
  ...rest: string[]
): string {
  const base = baseUrl();
  const suffix = rest.length > 0 ? `/${rest.join("/")}` : "";
  if (projectId) {
    return `${base}/api/apps/${encodeURIComponent(projectId)}/public/${encodeURIComponent(token)}${suffix}`;
  }
  return `${base}/api/apps/public/${encodeURIComponent(token)}${suffix}`;
}

// ---------- Internal fetch helper ----------

async function apiFetch(
  url: string,
  init?: RequestInit,
): Promise<Response> {
  const res = await fetch(url, { ...init });
  if (res.ok) return res;

  const retryAfterHeader = res.headers.get("Retry-After");
  const retryAfter = retryAfterHeader ? parseInt(retryAfterHeader, 10) : undefined;
  const body = await res.text().catch(() => `HTTP ${res.status}`);
  throw new PublicApiError(res.status, body || `HTTP ${res.status}`, retryAfter);
}

// ---------- Public API functions ----------

export async function fetchSchema(
  token: string,
  projectId: string | null,
): Promise<PublicSchema> {
  const url = publicPath(token, projectId);
  const res = await apiFetch(url);
  return res.json() as Promise<PublicSchema>;
}

export async function runPublic(
  token: string,
  projectId: string | null,
  inputs: Record<string, unknown>,
): Promise<{ runId: string; status: string }> {
  const url = publicPath(token, projectId, "run");
  const res = await apiFetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ inputs }),
  });
  return res.json() as Promise<{ runId: string; status: string }>;
}

export async function getRun(
  token: string,
  projectId: string | null,
  runId: string,
): Promise<RunSummary> {
  const url = publicPath(token, projectId, "runs", encodeURIComponent(runId));
  const res = await apiFetch(url);
  return res.json() as Promise<RunSummary>;
}

export async function cancelRunPublic(
  token: string,
  projectId: string | null,
  runId: string,
): Promise<void> {
  const url = publicPath(token, projectId, "runs", encodeURIComponent(runId), "cancel");
  await apiFetch(url, { method: "POST" });
}
