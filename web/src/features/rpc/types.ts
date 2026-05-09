// JSON-RPC 2.0 types matching pkg/server/types.go

export interface RPCRequest {
  jsonrpc: "2.0";
  id: number;
  method: string;
  params?: Record<string, unknown>;
}

export interface RPCResponse {
  jsonrpc: "2.0";
  id?: number;
  result?: unknown;
  error?: RPCError;
}

export interface RPCNotification {
  jsonrpc: "2.0";
  method: string;
  params?: unknown;
}

export interface RPCError {
  code: number;
  message: string;
  data?: unknown;
}

// Business types

export interface Thread {
  id: string;
  title: string;
  created_at: string;
  updated_at: string;
}

export interface ThreadItemArtifact {
  type: string;  // "image", "video", "audio"
  url: string;   // /api/files/... path
  name?: string; // tool name
}

export interface ThreadItem {
  id: string;
  thread_id: string;
  turn_id: string;
  role: "user" | "assistant" | "system" | "tool";
  tool_name?: string;
  content: string;
  artifacts?: ThreadItemArtifact[];
  created_at: string;
}

export interface ApprovalRequest {
  id: string;
  thread_id: string;
  turn_id: string;
  tool_name: string;
  tool_params?: Record<string, unknown>;
  reason?: string;
}

// Interactive questions (AskUserQuestion)
export interface QuestionOption {
  label: string;
  description: string;
}

export interface QuestionItem {
  question: string;
  header: string;
  options: QuestionOption[];
  multiSelect: boolean;
}

export interface QuestionRequest {
  id: string;
  thread_id: string;
  turn_id: string;
  questions: QuestionItem[];
}

// Skills
// Scope values produced by pkg/runtime/skills:
//   "repo" | "user" | "learned" | "subscribed" | "custom"
export interface SkillInfo {
  Name: string;
  Description: string;
  Scope?: string;
  RelatedSkills?: string[];
  Keywords?: string[];
}

export interface SkillContentResult {
  name: string;
  description: string;
  scope?: string;
  body: string;
  support_files?: Record<string, string[]>;
}

export interface SkillImportPayload {
  source_type?: "path" | "git" | "archive";
  source_path?: string;
  source_paths?: string[];
  repo_url?: string;
  archive_url?: string;
  target_scope: "local" | "global";
  conflict_strategy?: "overwrite" | "skip" | "error";
}

export interface SkillImportItemResult {
  skill_id: string;
  source_path?: string;
  path: string;
  status: "ready" | "conflict" | "imported" | "skipped";
  message?: string;
}

export interface SkillImportPreviewResult {
  items: SkillImportItemResult[];
  targetScope?: "local" | "global";
  conflictStrategy?: "overwrite" | "skip" | "error";
  readySkills?: string[];
  conflictingSkills?: string[];
}

export interface GenericTaskStatus {
  id: string;
  status: "running" | "done" | "error";
  progress?: number;
  message?: string;
  logs?: string[];
  result?: Record<string, unknown> & {
    items?: SkillImportItemResult[];
    importedSkills?: string[];
    skippedSkills?: string[];
    targetScope?: "local" | "global";
    conflictStrategy?: "overwrite" | "skip" | "error";
  };
  error?: string;
}

// Skill Analytics
export interface SkillStats {
  name: string;
  scope: string;
  activation_count: number;
  success_count: number;
  fail_count: number;
  last_used: string;
  avg_duration_ms: number;
  total_tokens: number;
  by_source: Record<string, number>;
}

export interface SkillActivationRecord {
  skill: string;
  scope: string;
  source: string;
  score: number;
  session_id: string;
  success: boolean;
  error?: string;
  tool_calls: number;
  duration_ms: number;
  token_usage: number;
  timestamp: string;
}

// Aigo multimodal media generation config
export interface AigoProvider {
  type: string;
  apiKey?: string;
  baseUrl?: string;
  metadata?: Record<string, string>;
  enabled?: boolean;
  disabledModels?: string[];
}

export interface AigoConfig {
  providers: Record<string, AigoProvider>;
  routing?: Record<string, string[]>;
  timeout?: string;
}

// Aigo provider config schema (from aigo/providers RPC)
export interface AigoConfigField {
  key: string;
  label: string;
  type: "string" | "secret" | "url";
  required: boolean;
  envVar?: string;
  description?: string;
  default?: string;
}

export interface AigoProviderInfo {
  name: string;
  displayName?: { en: string; zh: string };
  fields: AigoConfigField[];
  models: Record<string, string[]>;
}

// Provider connectivity status
export interface ProviderStatus {
  name: string;
  reachable: boolean;
  baseUrl?: string;
  checkedAt: string;
}

// Failover configuration
export interface FailoverModelEntry {
  provider: string;
  model: string;
  apiKey?: string;
  baseUrl?: string;
}

export interface FailoverConfig {
  enabled?: boolean;
  models?: FailoverModelEntry[];
  maxRetries?: number;
}

// Settings (mirrors config.Settings)
export interface SandboxConfig {
  enabled?: boolean;
  autoAllowBashIfSandboxed?: boolean;
  allowUnsandboxedCommands?: boolean;
}

export interface WebAuthConfig {
  username?: string;
  users?: UserAuth[];
}

export interface AuthProviderInfo {
  name: string;
  type: "password" | "redirect";
}

export interface UserAuth {
  username: string;
  disabled?: boolean;
}

export interface PersonaProfile {
  name?: string;
  description?: string;
  emoji?: string;
  soul?: string;
  soulFile?: string;
  instructions?: string;
  instructFile?: string;
  model?: string;
  language?: string;
  enabledTools?: string[];
  disallowedTools?: string[];
  inherit?: string;
}

export interface PersonaRoute {
  channel: string;
  peer?: string;
  persona: string;
  priority?: number;
}

export interface PersonasConfig {
  default?: string;
  profiles: Record<string, PersonaProfile>;
  routes?: PersonaRoute[];
}

// User-level persona config
export interface UserPersonaConfig {
  active?: string;
  profiles: Record<string, PersonaProfile>;
}

export interface UserPersonaListResult {
  globalProfiles: Record<string, PersonaProfile>;
  globalDefault: string;
  userProfiles: Record<string, PersonaProfile>;
  active: string;
}

// IM Channel types
export interface ChannelField {
  key: string;
  label?: string;
  secret?: boolean;
}

export interface ChannelPlatformMeta {
  name: string;
  icon: string;
  fields: ChannelField[];
}

export interface ChannelInfo {
  platform: string;
  name: string;
  icon: string;
  enabled: boolean;
  configured: boolean;
  fields: ChannelField[];
  values: Record<string, string>;
  route?: string;
}

export interface ChannelsListResult {
  channels: ChannelInfo[];
  platforms: Record<string, ChannelPlatformMeta>;
}

export interface MemoryEntry {
  name: string;
  description: string;
  type: "user" | "feedback" | "project" | "reference";
  content: string;
  filepath: string;
  mod_time: string;
}

export interface MemoryListResult {
  entries: MemoryEntry[];
}

// Storage backend configuration (mirrors config.StorageConfig).
// Backend selects which sub-block is honored at runtime; sub-blocks are
// kept independently so a user can switch backends without re-entering
// the previous backend's credentials.
export type StorageBackend = "" | "osfs" | "memfs" | "embedded" | "s3";
export type StorageEmbeddedMode = "" | "external" | "standalone";

export interface StorageOSFSConfig {
  root?: string;
}

export interface StorageEmbeddedConfig {
  mode?: StorageEmbeddedMode;
  addr?: string;
  root?: string;
  bucket?: string;
  accessKey?: string;
  secretKey?: string;
}

export interface StorageS3Config {
  endpoint?: string;
  region?: string;
  bucket?: string;
  accessKeyID?: string;
  secretAccessKey?: string;
  usePathStyle?: boolean;
  publicBaseURL?: string;
}

export interface StorageConfig {
  backend?: StorageBackend;
  publicBaseURL?: string;
  tenantPrefix?: string;
  osfs?: StorageOSFSConfig;
  embedded?: StorageEmbeddedConfig;
  s3?: StorageS3Config;
}

export interface ServerSettings {
  model?: string;
  permissions?: { allow?: string[]; deny?: string[] };
  mcp?: unknown;
  sandbox?: SandboxConfig;
  env?: Record<string, string>;
  hooks?: unknown;
  disallowedTools?: string[];
  disabledSkills?: string[];
  aigo?: AigoConfig;
  failover?: FailoverConfig;
  webAuth?: WebAuthConfig;
  personas?: PersonasConfig;
  storage?: StorageConfig;
}

// Cron types

export interface CronSchedule {
  kind: "every" | "cron" | "once";
  expr?: string;
  every_ms?: number;
  timezone?: string;
  run_at?: string;
}

export interface CronJobState {
  next_run_at?: string;
  last_run_at?: string;
  last_status?: string;
  last_error?: string;
  run_count: number;
}

export interface CronJob {
  id: string;
  name: string;
  description?: string;
  enabled: boolean;
  schedule: CronSchedule;
  prompt: string;
  session_id: string;
  timeout?: number;
  created_at: string;
  updated_at: string;
  state: CronJobState;
}

export interface CronRun {
  id: string;
  job_id: string;
  job_name: string;
  status: string;
  started_at: string;
  finished_at?: string;
  duration_ms?: number;
  summary?: string;
  error?: string;
  session_id: string;
}

export interface CronStatus {
  enabled: boolean;
  total_jobs: number;
  active_jobs: number;
  next_wake_at?: string;
}

// Active turns

export interface ActiveTurn {
  turn_id: string;
  thread_id: string;
  thread_title: string;
  prompt: string;
  status: string;
  started_at: string;
  source: string;
  cron_job_id?: string;
  stream_text?: string;
  tool_name?: string;
}

// StreamEvent — mirrors pkg/api/stream.go, consumed as-is from the server.
export interface StreamEvent {
  type: string;
  message?: { id?: string; type?: string; role?: string; model?: string };
  index?: number;
  content_block?: { type?: string; text?: string; id?: string; name?: string; input?: unknown };
  delta?: { type?: string; text?: string; partial_json?: unknown; stop_reason?: string };
  usage?: { input_tokens?: number; output_tokens?: number };
  tool_use_id?: string;
  name?: string;
  input?: Record<string, unknown>;
  output?: unknown;
  is_stderr?: boolean;
  is_error?: boolean;
  session_id?: string;
  iteration?: number;
}

export interface MonitorInfo {
  task_id: string;
  subject: string;
  stream_url: string;
  running: boolean;
  processed: number;
  skipped: number;
  events: number;
  uptime: string;
  last_error?: string;
}

// --- Skillhub DTOs (mirrors pkg/skillhub) ----------------------------------

// Public-safe skillhub config — server strips token before sending.
export interface SkillhubConfig {
  registry: string;
  handle: string;
  loggedIn: boolean;
  offline: boolean;
  autoSync: boolean;
  syncInterval?: string;
  learnedAutoPublish: boolean;
  learnedVisibility?: string;
  subscriptions: string[];
  /** RFC3339 timestamp of the last successful sync, or undefined if never. */
  lastSyncAt?: string;
  /** "ok" | "partial" | "error" — set after each sync. */
  lastSyncStatus?: string;
}

export interface SkillhubUser {
  id: string;
  handle: string;
  role: string;
  email?: string;
}

// Mirrors skillhub.Skill (loose — only fields we render).
export interface SkillhubSkill {
  id: string;
  slug: string;
  displayName?: string;
  summary?: string;
  category: string;
  kind?: string;
  visibility: string;
  moderationStatus: string;
  tags?: string[];
  downloads: number;
  starsCount: number;
  latestVersionId?: string;
  ownerHandle?: string;
  createdAt: string;
  updatedAt: string;
}

export interface SkillhubVersion {
  id: string;
  skillId: string;
  version: string;
  fingerprint: string;
  changelog?: string;
  createdAt: string;
}

export interface SkillhubSearchHit {
  slug: string;
  displayName?: string;
  summary?: string;
  category?: string;
  ownerHandle?: string;
  /** "public" | "private" — empty when registry doesn't index it. */
  visibility?: string;
  kind?: string;
  downloads?: number;
  starsCount?: number;
  score?: number;
}

export interface SkillhubSearchResult {
  hits: SkillhubSearchHit[];
  estimatedTotalHits: number;
}

export interface SkillhubListResult {
  data: SkillhubSkill[];
  nextCursor: string;
}

// Device-flow login challenge — `deviceCode` is intentionally not present.
export interface SkillhubDeviceLogin {
  sessionId: string;
  userCode: string;
  verificationUrl: string;
  expiresIn: number;
  interval: number;
  registry: string;
}

export type SkillhubLoginPollStatus = "pending" | "ok" | "error";

export interface SkillhubLoginPollResult {
  status: SkillhubLoginPollStatus;
  error?: string;
  handle?: string;
  registry?: string;
  user?: SkillhubUser;
}

export interface SkillhubInstallResult {
  slug: string;
  version: string;
  dir: string;
  filesCount: number;
  notModified: boolean;
}

export type SkillhubSyncStatus = "up-to-date" | "updated" | "error";

export interface SkillhubSyncEntry {
  slug: string;
  status: SkillhubSyncStatus;
  version?: string;
  filesCount?: number;
  error?: string;
}

export interface SkillhubSyncReport {
  results: SkillhubSyncEntry[];
  lastSyncAt?: string;
  lastSyncStatus?: string;
}

export interface SkillhubCategoryList {
  categories: string[];
}

export interface SkillhubPublishResult {
  slug: string;
  version: string;
  fingerprint: string;
}
