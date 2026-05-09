import { resolveHttpBaseUrl } from "@/features/rpc/httpRpc";
import { useProjectStore } from "@/features/project/projectStore";

// ---------- Types ----------

export interface AppMeta {
  id: string;
  name: string;
  description?: string;
  icon?: string;
  sourceThreadId: string;
  publishedVersion: string;
  visibility: "private" | "public";
  createdAt: string;
  updatedAt: string;
}

export interface AppInputField {
  nodeId: string;
  variable: string;
  label: string;
  type: "text" | "paragraph" | "number" | "select" | "file";
  required?: boolean;
  default?: unknown;
  options?: string[];
  min?: number;
  max?: number;
}

export interface AppOutputField {
  nodeId: string;
  label: string;
  sourceRef: string;
  kind: "image" | "video" | "audio" | "text";
}

export interface AppVersion {
  version: string;
  publishedAt: string;
  publishedBy: string;
  inputs: AppInputField[];
  outputs: AppOutputField[];
  document?: unknown;
}

export interface AppVersionSummary {
  version: string;
  publishedAt: string;
  publishedBy: string;
}

export interface RunSummary {
  runId: string;
  threadId: string;
  startedAt: string;
  finishedAt?: string;
  status: "running" | "done" | "error" | "cancelled";
  total: number;
  succeeded: number;
  failed: number;
  skipped: number;
  nodes: NodeRunResult[];
  error?: string;
}

export interface NodeRunResult {
  nodeId: string;
  nodeType: string;
  tool?: string;
  status: string;
  durationMs: number;
  error?: string;
  resultUrl?: string;
  resultNodeId?: string;
}

// ---------- URL builder ----------

function appsBase(): string {
  const base = resolveHttpBaseUrl();
  const projectId = useProjectStore.getState().currentProjectId;
  if (projectId) {
    return `${base}/api/apps/${projectId}`;
  }
  return `${base}/api/apps`;
}

function appUrl(appId: string): string {
  return `${appsBase()}/${appId}`;
}

// ---------- Error helper ----------

class ApiError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

async function throwIfNotOk(res: Response): Promise<void> {
  if (!res.ok) {
    const text = await res.text().catch(() => `HTTP ${res.status}`);
    throw new ApiError(text || `HTTP ${res.status}`, res.status);
  }
}

// ---------- API functions ----------

export async function listApps(): Promise<AppMeta[]> {
  const res = await fetch(appsBase(), {
    credentials: "include",
  });
  await throwIfNotOk(res);
  return res.json();
}

export async function createApp(input: {
  name: string;
  description?: string;
  icon?: string;
  sourceThreadId: string;
  visibility?: "private" | "public";
}): Promise<AppMeta> {
  const res = await fetch(appsBase(), {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
  await throwIfNotOk(res);
  return res.json();
}

export async function getApp(
  appId: string,
): Promise<AppMeta & { inputs?: AppInputField[]; outputs?: AppOutputField[] }> {
  const res = await fetch(appUrl(appId), {
    credentials: "include",
  });
  await throwIfNotOk(res);
  return res.json();
}

export async function updateApp(
  appId: string,
  patch: {
    name?: string;
    description?: string;
    icon?: string;
    visibility?: "private" | "public";
    sourceThreadId?: string;
  },
): Promise<AppMeta> {
  const res = await fetch(appUrl(appId), {
    method: "PUT",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(patch),
  });
  await throwIfNotOk(res);
  return res.json();
}

export async function deleteApp(appId: string): Promise<void> {
  const res = await fetch(appUrl(appId), {
    method: "DELETE",
    credentials: "include",
  });
  await throwIfNotOk(res);
}

export async function publishApp(appId: string): Promise<AppVersion> {
  const res = await fetch(`${appUrl(appId)}/publish`, {
    method: "POST",
    credentials: "include",
  });
  await throwIfNotOk(res);
  return res.json();
}

export async function setPublishedVersion(appId: string, version: string): Promise<AppMeta> {
  const res = await fetch(`${appUrl(appId)}/published-version`, {
    method: "PUT",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ version }),
  });
  await throwIfNotOk(res);
  return res.json();
}

export async function listVersions(appId: string): Promise<AppVersionSummary[]> {
  const res = await fetch(`${appUrl(appId)}/versions`, {
    credentials: "include",
  });
  await throwIfNotOk(res);
  return res.json();
}

export async function runApp(
  appId: string,
  inputs: Record<string, unknown>,
): Promise<{ runId: string; status: string }> {
  const res = await fetch(`${appUrl(appId)}/run`, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ inputs }),
  });
  await throwIfNotOk(res);
  return res.json();
}

export async function getRunStatus(
  appId: string,
  runId: string,
): Promise<RunSummary> {
  const res = await fetch(`${appUrl(appId)}/runs/${runId}`, {
    credentials: "include",
  });
  await throwIfNotOk(res);
  return res.json();
}

// ---------- API Keys ----------

export interface ApiKeySummary {
  id: string;
  name: string;
  prefix: string;
  createdAt: string;
  expiresAt?: string;
  lastUsedAt?: string;
}

export interface ApiKeyCreated extends ApiKeySummary {
  apiKey: string; // plaintext, shown once
}

export async function listKeys(appId: string): Promise<ApiKeySummary[]> {
  const res = await fetch(`${appUrl(appId)}/keys`, {
    credentials: "include",
  });
  await throwIfNotOk(res);
  return res.json();
}

export async function createKey(
  appId: string,
  name: string,
  opts?: { expiresInDays?: number },
): Promise<ApiKeyCreated> {
  const body: { name: string; expiresInDays?: number } = { name };
  if (opts?.expiresInDays && opts.expiresInDays > 0) {
    body.expiresInDays = opts.expiresInDays;
  }
  const res = await fetch(`${appUrl(appId)}/keys`, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  await throwIfNotOk(res);
  return res.json();
}

export async function rotateKey(
  appId: string,
  keyId: string,
  opts?: { name?: string; expiresInDays?: number },
): Promise<ApiKeyCreated> {
  const body: { name?: string; expiresInDays?: number } = {};
  if (opts?.name) body.name = opts.name;
  if (opts?.expiresInDays !== undefined) body.expiresInDays = opts.expiresInDays;
  const res = await fetch(`${appUrl(appId)}/keys/${keyId}/rotate`, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  await throwIfNotOk(res);
  return res.json();
}

export async function deleteKey(appId: string, keyId: string): Promise<void> {
  const res = await fetch(`${appUrl(appId)}/keys/${keyId}`, {
    method: "DELETE",
    credentials: "include",
  });
  await throwIfNotOk(res);
}

export async function cancelRun(appId: string, runId: string): Promise<void> {
  const res = await fetch(`${appUrl(appId)}/runs/${runId}/cancel`, {
    method: "POST",
    credentials: "include",
  });
  await throwIfNotOk(res);
}

// ---------- Share Tokens ----------

export interface ShareTokenSummary {
  tokenPreview: string;
  createdAt: string;
  expiresAt?: string;
  rateLimit?: number;
}

export interface ShareTokenCreated {
  token: string; // full plaintext token
  createdAt: string;
  expiresAt?: string;
  rateLimit?: number;
}

export async function listShareTokens(
  appId: string,
): Promise<ShareTokenSummary[]> {
  const res = await fetch(`${appUrl(appId)}/share`, {
    credentials: "include",
  });
  await throwIfNotOk(res);
  return res.json();
}

export async function createShareToken(
  appId: string,
  opts: { expiresInDays?: number; rateLimit?: number },
): Promise<ShareTokenCreated> {
  const res = await fetch(`${appUrl(appId)}/share`, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(opts),
  });
  await throwIfNotOk(res);
  return res.json();
}

export async function deleteShareToken(
  appId: string,
  token: string,
): Promise<void> {
  const res = await fetch(`${appUrl(appId)}/share/${token}`, {
    method: "DELETE",
    credentials: "include",
  });
  await throwIfNotOk(res);
}
