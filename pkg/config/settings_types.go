package config

import (
	"errors"
	"strings"
)

// Settings models the full contents of .saker/settings.json.
// All optional booleans use *bool so nil means "unset" and caller defaults apply.
type Settings struct {
	APIKeyHelper         string                         `json:"apiKeyHelper,omitempty"`         // /bin/sh script that returns an API key for outbound model calls.
	CleanupPeriodDays    *int                           `json:"cleanupPeriodDays,omitempty"`    // Days to retain chat history locally (default 30). Set to 0 to disable.
	CompanyAnnouncements []string                       `json:"companyAnnouncements,omitempty"` // Startup announcements rotated randomly.
	Env                  map[string]string              `json:"env,omitempty"`                  // Environment variables applied to every session.
	IncludeCoAuthoredBy  *bool                          `json:"includeCoAuthoredBy,omitempty"`  // Whether to append "co-authored-by Claude" to commits/PRs.
	Permissions          *PermissionsConfig             `json:"permissions,omitempty"`          // Tool permission rules and defaults.
	DisallowedTools      []string                       `json:"disallowedTools,omitempty"`      // Tool blacklist; disallowed tools are not registered.
	Hooks                *HooksConfig                   `json:"hooks,omitempty"`                // Hook commands to run around tool execution.
	DisableAllHooks      *bool                          `json:"disableAllHooks,omitempty"`      // Force-disable all hooks.
	Model                string                         `json:"model,omitempty"`                // Override default model id.
	StatusLine           *StatusLineConfig              `json:"statusLine,omitempty"`           // Custom status line settings.
	OutputStyle          string                         `json:"outputStyle,omitempty"`          // Optional named output style.
	MCP                  *MCPConfig                     `json:"mcp,omitempty"`                  // MCP server definitions keyed by name.
	LegacyMCPServers     []string                       `json:"mcpServers,omitempty"`           // Deprecated list format; kept for migration errors.
	ForceLoginMethod     string                         `json:"forceLoginMethod,omitempty"`     // Restrict login to "claudeai" or "console".
	ForceLoginOrgUUID    string                         `json:"forceLoginOrgUUID,omitempty"`    // Org UUID to auto-select during login when set.
	Sandbox              *SandboxConfig                 `json:"sandbox,omitempty"`              // Bash sandbox configuration.
	BashOutput           *BashOutputConfig              `json:"bashOutput,omitempty"`           // Thresholds for spooling bash output to disk.
	ToolOutput           *ToolOutputConfig              `json:"toolOutput,omitempty"`           // Thresholds for persisting large tool outputs to disk.
	AllowedMcpServers    []MCPServerRule                `json:"allowedMcpServers,omitempty"`    // Managed allowlist of user-configurable MCP servers.
	DeniedMcpServers     []MCPServerRule                `json:"deniedMcpServers,omitempty"`     // Managed denylist of user-configurable MCP servers.
	AWSAuthRefresh       string                         `json:"awsAuthRefresh,omitempty"`       // Script to refresh AWS SSO credentials.
	AWSCredentialExport  string                         `json:"awsCredentialExport,omitempty"`  // Script that prints JSON AWS credentials.
	RespectGitignore     *bool                          `json:"respectGitignore,omitempty"`     // Whether Glob/Grep tools should respect .gitignore patterns.
	ACP                  *ACPSettings                   `json:"acp,omitempty"`                  // ACP client configuration for calling external agents.
	Aigo                 *AigoConfig                    `json:"aigo,omitempty"`                 // Aigo multimodal media generation configuration.
	Failover             *FailoverConfig                `json:"failover,omitempty"`             // Model failover configuration.
	WebAuth              *WebAuthConfig                 `json:"webAuth,omitempty"`              // Web server authentication for remote access.
	DisabledSkills       []string                       `json:"disabledSkills,omitempty"`       // Skills to exclude from activation and listing.
	Personas             *PersonasConfig                `json:"personas,omitempty"`             // Persona/Soul configuration for multi-character agents.
	UserPersonas         map[string]*UserPersonasConfig `json:"userPersonas,omitempty"`         // Per-user persona overrides keyed by username.
	Storage              *StorageConfig                 `json:"storage,omitempty"`              // Pluggable media object storage (osfs / embedded s2 / s3).
	CORS                 *CORSConfig                    `json:"cors,omitempty"`                 // CORS policy for the web server.
	Bifrost              *BifrostConfig                 `json:"bifrost,omitempty"`              // Bifrost SDK enhancements: semantic cache, telemetry/OTel.
	Governance           *GovernanceConfig              `json:"governance,omitempty"`           // Saker-side governance: virtual keys with budget / RPM / TPM caps.
}

// StorageConfig configures the media object storage backend. See
// docs/storage-backend.md for the full design and key naming convention.
//
// When unset, the runtime defaults to osfs at <dataDir>/media — equivalent
// to the legacy on-disk behavior.
type StorageConfig struct {
	Backend       string                 `json:"backend,omitempty"`       // "osfs" (default) | "memfs" | "embedded" | "s3"
	PublicBaseURL string                 `json:"publicBaseURL,omitempty"` // URL prefix exposed to clients; default "/media"
	TenantPrefix  string                 `json:"tenantPrefix,omitempty"`  // Optional key prefix when sharing one bucket across instances
	OSFS          *StorageOSFSConfig     `json:"osfs,omitempty"`          // Local-disk backend settings
	Embedded      *StorageEmbeddedConfig `json:"embedded,omitempty"`      // In-process s2 server settings
	S3            *StorageS3Config       `json:"s3,omitempty"`            // Remote S3-compatible backend settings
}

// StorageOSFSConfig configures the on-disk backend.
type StorageOSFSConfig struct {
	Root string `json:"root,omitempty"` // Absolute directory; empty → <dataDir>/media
}

// StorageEmbeddedConfig configures the in-process S3 server.
//
// Mode controls how the S3 API is exposed:
//   - "external" (default) mounts the S3 handler on the main saker listener
//     at /_s3/. No new TCP port is opened. S3 clients connect with
//     BaseEndpoint=http://<saker-host>/_s3 and UsePathStyle=true.
//   - "standalone" runs the S3 API on a dedicated http.Server bound to Addr.
//     Use this when you need a separate network interface, port, or TLS
//     setup for the S3 endpoint.
type StorageEmbeddedConfig struct {
	Mode      string `json:"mode,omitempty"`      // "external" (default) | "standalone"
	Addr      string `json:"addr,omitempty"`      // Listen address (standalone only); e.g. "127.0.0.1:9100"
	Root      string `json:"root,omitempty"`      // Backing directory; empty → <dataDir>/media
	Bucket    string `json:"bucket,omitempty"`    // Bucket name; empty → "media"
	AccessKey string `json:"accessKey,omitempty"` // S3 client AK; required for non-anonymous access
	SecretKey string `json:"secretKey,omitempty"` // S3 client SK; supports ${ENV_VAR} expansion
}

// StorageS3Config configures a remote S3-compatible backend
// (AWS S3 / Aliyun OSS / Cloudflare R2 / MinIO / etc.).
type StorageS3Config struct {
	Endpoint        string `json:"endpoint,omitempty"`        // Custom endpoint URL; empty → AWS default
	Region          string `json:"region,omitempty"`          // Region name (e.g. "cn-hangzhou", "us-east-1")
	Bucket          string `json:"bucket,omitempty"`          // Bucket name (required when backend=s3)
	AccessKeyID     string `json:"accessKeyID,omitempty"`     // Supports ${ENV_VAR} expansion
	SecretAccessKey string `json:"secretAccessKey,omitempty"` // Supports ${ENV_VAR} expansion
	UsePathStyle    bool   `json:"usePathStyle,omitempty"`    // MinIO/R2 typically need this
	PublicBaseURL   string `json:"publicBaseURL,omitempty"`   // Public bucket domain; falls back to SignedURL when empty
}

// PersonasConfig configures the multi-persona system.
type PersonasConfig struct {
	Default  string                    `json:"default,omitempty"`  // Default persona ID used when no routing matches.
	Profiles map[string]PersonaProfile `json:"profiles,omitempty"` // Persona profiles keyed by ID.
	Routes   []PersonaRoute            `json:"routes,omitempty"`   // Channel-based routing rules.
}

// PersonaProfile defines a persona's identity, soul, and overrides.
type PersonaProfile struct {
	Name            string   `json:"name,omitempty"`
	Description     string   `json:"description,omitempty"`
	Emoji           string   `json:"emoji,omitempty"`
	Soul            string   `json:"soul,omitempty"`
	SoulFile        string   `json:"soulFile,omitempty"`
	Instructions    string   `json:"instructions,omitempty"`
	InstructFile    string   `json:"instructFile,omitempty"`
	Model           string   `json:"model,omitempty"`
	Language        string   `json:"language,omitempty"`
	EnabledTools    []string `json:"enabledTools,omitempty"`
	DisallowedTools []string `json:"disallowedTools,omitempty"`
	Inherit         string   `json:"inherit,omitempty"`
}

// PersonaRoute maps a channel pattern to a persona.
type PersonaRoute struct {
	Channel  string `json:"channel"`
	Peer     string `json:"peer,omitempty"`
	Persona  string `json:"persona"`
	Priority int    `json:"priority,omitempty"`
}

// UserPersonasConfig stores per-user persona preferences in settings.local.json.
type UserPersonasConfig struct {
	Active   string                    `json:"active,omitempty"`   // Currently active persona ID.
	Profiles map[string]PersonaProfile `json:"profiles,omitempty"` // User-created persona profiles.
}

// WebAuthConfig controls username/password authentication for the web server.
// When configured, non-localhost requests require login. Localhost access is always allowed.
//
// The admin account is defined by Username/Password (single admin, defaults to "admin").
// Additional regular users can be added via the Users slice. Each user gets an
// automatically isolated profile directory under .saker/profiles/<username>/.
type WebAuthConfig struct {
	Username string     `json:"username,omitempty"` // Admin username; defaults to "admin" if empty.
	Password string     `json:"password,omitempty"` // Bcrypt-hashed admin password. Auto-generated on first run if empty.
	Users    []UserAuth `json:"users,omitempty"`    // Regular (non-admin) user accounts.

	// External auth providers.
	LDAP *LDAPConfig `json:"ldap,omitempty"` // LDAP/Active Directory authentication.
	OIDC *OIDCConfig `json:"oidc,omitempty"` // OAuth2/OIDC authentication (Google, GitHub, Keycloak, etc.).

	// Role mapping rules for external users.
	RoleMapping *RoleMappingConfig `json:"roleMapping,omitempty"`
}

// UserAuth defines a regular user account for web authentication.
type UserAuth struct {
	Username string `json:"username"`           // Unique username.
	Password string `json:"password"`           // Bcrypt-hashed password.
	Disabled bool   `json:"disabled,omitempty"` // If true, login is rejected.
}

// LDAPConfig configures LDAP/Active Directory authentication.
type LDAPConfig struct {
	Enabled            bool        `json:"enabled"`                      // Whether LDAP authentication is active.
	URL                string      `json:"url"`                          // LDAP server URL (ldap://host:389 or ldaps://host:636).
	BindDN             string      `json:"bindDN,omitempty"`             // Service account DN for search operations.
	BindPassword       string      `json:"bindPassword,omitempty"`       // Service account password; supports ${ENV_VAR} expansion.
	BaseDN             string      `json:"baseDN"`                       // Base DN for user searches.
	UserFilter         string      `json:"userFilter,omitempty"`         // User search filter; %s is replaced with username. Default: "(uid=%s)".
	GroupFilter        string      `json:"groupFilter,omitempty"`        // Group membership search filter.
	Attrs              LDAPAttrMap `json:"attrs,omitempty"`              // LDAP attribute name mapping.
	StartTLS           bool        `json:"startTLS,omitempty"`           // Upgrade plain connection to TLS via StartTLS.
	InsecureSkipVerify bool        `json:"insecureSkipVerify,omitempty"` // Skip TLS certificate verification (not recommended).
}

// LDAPAttrMap maps LDAP attribute names to Saker user fields.
type LDAPAttrMap struct {
	Username    string `json:"username,omitempty"`    // Username attribute. Default: "uid".
	Email       string `json:"email,omitempty"`       // Email attribute. Default: "mail".
	DisplayName string `json:"displayName,omitempty"` // Display name attribute. Default: "cn".
	MemberOf    string `json:"memberOf,omitempty"`    // Group membership attribute. Default: "memberOf".
	Avatar      string `json:"avatar,omitempty"`      // Avatar attribute. Default: "jpegPhoto".
}

// OIDCConfig configures OAuth2/OIDC authentication.
type OIDCConfig struct {
	Enabled      bool         `json:"enabled"`                // Whether OIDC authentication is active.
	Issuer       string       `json:"issuer"`                 // OIDC issuer URL (used for .well-known discovery).
	ClientID     string       `json:"clientId"`               // OAuth2 client ID.
	ClientSecret string       `json:"clientSecret,omitempty"` // OAuth2 client secret; supports ${ENV_VAR} expansion.
	RedirectURL  string       `json:"redirectUrl,omitempty"`  // Callback URL. Auto-constructed if empty.
	Scopes       []string     `json:"scopes,omitempty"`       // OAuth2 scopes. Default: ["openid", "profile", "email"].
	ClaimMapping OIDCClaimMap `json:"claimMapping,omitempty"` // ID token claim name mapping.
}

// OIDCClaimMap maps OIDC ID token claim names to Saker user fields.
type OIDCClaimMap struct {
	Username string `json:"username,omitempty"` // Username claim. Default: "preferred_username", fallback "sub".
	Email    string `json:"email,omitempty"`    // Email claim. Default: "email".
	Name     string `json:"name,omitempty"`     // Display name claim. Default: "name".
	Groups   string `json:"groups,omitempty"`   // Groups claim. Default: "groups".
	Avatar   string `json:"avatar,omitempty"`   // Avatar URL claim. Default: "picture".
}

// RoleMappingConfig controls how external users are mapped to Saker roles.
type RoleMappingConfig struct {
	AdminGroups []string `json:"adminGroups,omitempty"` // Group names whose members get "admin" role.
	AdminUsers  []string `json:"adminUsers,omitempty"`  // Usernames that always get "admin" role.
	DefaultRole string   `json:"defaultRole,omitempty"` // Role for users matching no rule. Default: "user".
}

// CORSConfig controls the Cross-Origin Resource Sharing policy.
// When empty, only localhost origins are allowed.
type CORSConfig struct {
	AllowedOrigins []string `json:"allowedOrigins,omitempty"` // Origins permitted for cross-origin requests.
}

// AigoConfig configures the aigo multimodal media generation SDK.
// When present, aigo tools (generate_image, generate_video, text_to_speech, etc.)
// are automatically registered as built-in tools.
type AigoConfig struct {
	Providers map[string]AigoProvider `json:"providers"`         // Named provider instances keyed by alias.
	Routing   map[string][]string     `json:"routing"`           // Maps capability ("image","video","tts") to ordered provider/model refs.
	Timeout   string                  `json:"timeout,omitempty"` // Default execution timeout (e.g. "60s", "5m"). Default: 5m.
}

// AigoProvider describes a provider instance with credentials and connection details.
// The Type field selects the engine implementation (e.g. "aliyun", "openai", "fal", "kling", etc.).
type AigoProvider struct {
	Type           string            `json:"type"`                     // Engine type identifier.
	APIKey         string            `json:"apiKey,omitempty"`         // API key; supports ${ENV_VAR} expansion.
	BaseURL        string            `json:"baseUrl,omitempty"`        // Custom API base URL.
	Metadata       map[string]string `json:"metadata,omitempty"`       // Provider-specific config (e.g. {"appId": "xxx"} for volcvoice).
	Enabled        *bool             `json:"enabled,omitempty"`        // Provider-level toggle; default true. Set false to disable all models from this provider.
	DisabledModels []string          `json:"disabledModels,omitempty"` // Individual models to skip within this provider.

	// WaitForCompletion overrides the engine's default async-task polling
	// behavior. nil = engine smart default (video/3D/image-edit/asr-filetrans
	// auto-poll, fast image/tts return immediately). Set to *true to force
	// blocking polling; set to *false to always return upstream task_id.
	WaitForCompletion *bool `json:"waitForCompletion,omitempty"`
	// PollInterval overrides the engine's default polling cadence (e.g. "2s").
	// Only effective when WaitForCompletion resolves to true.
	PollInterval string `json:"pollInterval,omitempty"`
}

// FailoverConfig configures automatic model failover on API errors.
type FailoverConfig struct {
	Enabled        *bool                `json:"enabled,omitempty"`        // Whether failover is active.
	Models         []FailoverModelEntry `json:"models,omitempty"`         // Ordered fallback model list.
	MaxRetries     int                  `json:"maxRetries,omitempty"`     // Max retries per model before failover (default 2).
	PrimaryKeyPool []ProviderKey        `json:"primaryKeyPool,omitempty"` // Optional multi-key pool for the primary provider; Bifrost weight-balances across keys.
}

// FailoverModelEntry describes a fallback model with optional credentials.
type FailoverModelEntry struct {
	Provider string `json:"provider"`          // "anthropic", "openai", "dashscope"
	Model    string `json:"model"`             // Model name (e.g. "claude-sonnet-4-5-20250929")
	APIKey   string `json:"apiKey,omitempty"`  // Optional independent API key; supports ${ENV_VAR} expansion.
	BaseURL  string `json:"baseUrl,omitempty"` // Optional custom API base URL.
}

// ProviderKey describes one entry in a multi-key pool. Bifrost balances
// requests across keys by Weight (default 1.0); Models optionally restricts
// the key to a whitelist of model names.
type ProviderKey struct {
	Provider string   `json:"provider"`         // "anthropic" / "openai" / "dashscope" / etc.
	APIKey   string   `json:"apiKey"`           // Supports ${ENV_VAR} expansion.
	Weight   float64  `json:"weight,omitempty"` // Default 1.0; relative share within the pool.
	Models   []string `json:"models,omitempty"` // Optional model whitelist (empty = all models).
	Note     string   `json:"note,omitempty"`   // Human label, displayed masked in UI.
}

// BifrostConfig groups optional Bifrost-SDK enhancements that can be enabled
// independently. Each sub-block is nil by default; presence + Enabled=true
// activates the corresponding plugin in the Bifrost engine.
type BifrostConfig struct {
	SemanticCache *SemanticCacheConfig `json:"semanticCache,omitempty"` // Vector-similarity prompt cache via Bifrost semanticcache plugin.
	Telemetry     *TelemetryConfig     `json:"telemetry,omitempty"`     // Provider-side OTLP traces/metrics via Bifrost otel plugin.
}

// SemanticCacheConfig configures the Bifrost semanticcache plugin. The plugin
// requires an external vector store (Redis Stack / Qdrant / Pinecone / Weaviate)
// — there is no in-memory mode upstream. Embedding is computed via the named
// Provider/EmbeddingModel; cache lookups are gated by Threshold cosine
// similarity.
type SemanticCacheConfig struct {
	Enabled              *bool              `json:"enabled,omitempty"`
	Provider             string             `json:"provider,omitempty"`             // Embedding provider: "openai" / "cohere" / "ollama" / etc.
	EmbeddingModel       string             `json:"embeddingModel,omitempty"`       // e.g. "text-embedding-3-small" / "embed-english-v3.0".
	Dimension            int                `json:"dimension,omitempty"`            // Embedding vector dimension (required by some stores).
	Threshold            float64            `json:"threshold,omitempty"`            // Cosine similarity gate, default 0.8.
	TTLSeconds           int                `json:"ttlSeconds,omitempty"`           // Cache entry TTL in seconds (0 = no expiry).
	Namespace            string             `json:"namespace,omitempty"`            // Vector store namespace / index name.
	CacheByModel         *bool              `json:"cacheByModel,omitempty"`         // Scope cache key by model name.
	CacheByProvider      *bool              `json:"cacheByProvider,omitempty"`      // Scope cache key by provider name.
	ConvHistoryThreshold int                `json:"convHistoryThreshold,omitempty"` // Skip cache when conversation has more than N turns.
	ExcludeSystemPrompt  *bool              `json:"excludeSystemPrompt,omitempty"`  // Hash only the user/assistant turns, not system prompt.
	VectorStore          *VectorStoreConfig `json:"vectorStore,omitempty"`          // External store connection details.
}

// VectorStoreConfig describes how to reach the external vector store backing
// the semantic cache. Only one of the credential fields will be relevant
// per Type (e.g. APIKey for Pinecone/Weaviate, Username/Password for Redis).
type VectorStoreConfig struct {
	Type     string            `json:"type,omitempty"`     // "redis" | "qdrant" | "pinecone" | "weaviate"
	Endpoint string            `json:"endpoint,omitempty"` // URL or host:port.
	APIKey   string            `json:"apiKey,omitempty"`   // Cloud-store auth; supports ${ENV_VAR} expansion.
	Username string            `json:"username,omitempty"` // Basic auth username (Redis ACL).
	Password string            `json:"password,omitempty"` // Basic auth password; supports ${ENV_VAR} expansion.
	Database string            `json:"database,omitempty"` // Collection / index / database name.
	Headers  map[string]string `json:"headers,omitempty"`  // Extra HTTP headers (e.g. cluster id for Weaviate).
}

// TelemetryConfig configures the Bifrost otel plugin to ship traces and
// optional metrics over OTLP gRPC/HTTP. Compatible with Grafana / Datadog /
// Honeycomb / Jaeger / Tempo / OpenTelemetry Collector.
type TelemetryConfig struct {
	Enabled                    *bool             `json:"enabled,omitempty"`
	ServiceName                string            `json:"serviceName,omitempty"`                // Defaults to "saker" when empty.
	Protocol                   string            `json:"protocol,omitempty"`                   // "grpc" (default) | "http"
	Endpoint                   string            `json:"endpoint,omitempty"`                   // OTLP collector URL, e.g. "https://otlp.example.com:4317".
	Headers                    map[string]string `json:"headers,omitempty"`                    // e.g. {"x-honeycomb-team": "${HONEYCOMB_TOKEN}"}.
	Insecure                   *bool             `json:"insecure,omitempty"`                   // Skip TLS verify (dev only).
	TraceType                  string            `json:"traceType,omitempty"`                  // "genai_extension" (default) | "vercel" | "open_inference"
	MetricsEnabled             *bool             `json:"metricsEnabled,omitempty"`             // Export metrics in addition to traces.
	MetricsEndpoint            string            `json:"metricsEndpoint,omitempty"`            // Separate metrics endpoint; defaults to Endpoint.
	MetricsPushIntervalSeconds int               `json:"metricsPushIntervalSeconds,omitempty"` // Metrics export cadence (default 30).
	Sampling                   float64           `json:"sampling,omitempty"`                   // 0.0~1.0 trace sampling ratio (default 1.0).
}

// GovernanceConfig configures saker's middleware-layer access control for
// model calls. Implemented entirely in saker (`pkg/middleware/governance.go`)
// — independent of the Bifrost governance plugin which is HTTP-gateway
// oriented and pulls in framework dependencies.
type GovernanceConfig struct {
	Enabled     *bool                  `json:"enabled,omitempty"`
	VirtualKeys []GovernanceVirtualKey `json:"virtualKeys,omitempty"`
}

// GovernanceVirtualKey caps cost / rate per logical caller. ID is the
// public-facing identifier callers pass via the `X-Saker-Virtual-Key`
// header (or middleware ctx value). BudgetUSD / RPM / TPM = 0 means
// unlimited for that dimension.
type GovernanceVirtualKey struct {
	ID            string   `json:"id"`                      // Public identifier; unique within the slice.
	Name          string   `json:"name,omitempty"`          // Human label.
	AllowedModels []string `json:"allowedModels,omitempty"` // Optional whitelist; empty = all models.
	BudgetUSD     float64  `json:"budgetUSD,omitempty"`     // Spend cap in USD per ResetCron window; 0 = unlimited.
	RPM           int      `json:"rpm,omitempty"`           // Requests-per-minute cap; 0 = unlimited.
	TPM           int      `json:"tpm,omitempty"`           // Tokens-per-minute cap; 0 = unlimited.
	ResetCron     string   `json:"resetCron,omitempty"`     // Budget reset schedule: "monthly" (default) / "weekly" / "daily".
}

// ACPSettings configures ACP client connections to external agents.
type ACPSettings struct {
	Agents map[string]ACPAgentEntry `json:"agents,omitempty"` // Named agent configs keyed by target name.
}

// ACPAgentEntry describes how to launch an external ACP agent process.
type ACPAgentEntry struct {
	Command string   `json:"command"`           // Binary path (e.g. "claude", "codex").
	Args    []string `json:"args,omitempty"`    // Extra CLI arguments; --acp=true is auto-appended.
	Env     []string `json:"env,omitempty"`     // Extra environment variables (KEY=VALUE).
	Timeout string   `json:"timeout,omitempty"` // Run timeout (e.g. "5m", "300s"). Default: 5m.
}

// PermissionsConfig defines per-tool permission rules.
type PermissionsConfig struct {
	Allow                        []string `json:"allow,omitempty"`                        // Rules that auto-allow tool use.
	Ask                          []string `json:"ask,omitempty"`                          // Rules that require confirmation.
	Deny                         []string `json:"deny,omitempty"`                         // Rules that block tool use.
	AdditionalDirectories        []string `json:"additionalDirectories,omitempty"`        // Extra working directories Claude may access.
	DefaultMode                  string   `json:"defaultMode,omitempty"`                  // Default permission mode when opening Claude Code.
	DisableBypassPermissionsMode string   `json:"disableBypassPermissionsMode,omitempty"` // Set to "disable" to forbid bypassPermissions mode.
}

// HookDefinition describes a single hook action bound to a matcher entry.
// Supports command (shell), prompt (LLM), and agent hook types per the Claude Code spec.
type HookDefinition struct {
	Type          string `json:"type"`                    // "command" (default), "prompt", or "agent"
	Command       string `json:"command,omitempty"`       // Shell command (type=command)
	Prompt        string `json:"prompt,omitempty"`        // LLM prompt (type=prompt)
	Model         string `json:"model,omitempty"`         // Model override (type=prompt/agent)
	Timeout       int    `json:"timeout,omitempty"`       // Per-hook timeout in seconds (0 = use default)
	Async         bool   `json:"async,omitempty"`         // Fire-and-forget execution
	Once          bool   `json:"once,omitempty"`          // Execute only once per session
	StatusMessage string `json:"statusMessage,omitempty"` // Status message shown during execution
}

// HookMatcherEntry pairs a matcher pattern with one or more hook definitions.
type HookMatcherEntry struct {
	Matcher string           `json:"matcher"`
	Hooks   []HookDefinition `json:"hooks"`
}

// HooksConfig maps event types to matcher entries. For tool-related events the
// matcher is applied to the tool name; for session events it matches source/reason;
// for notification it matches type; for subagent events it matches agent type.
//
// Supports both Claude Code official format (array of HookMatcherEntry) and
// SDK simplified format (map[string]string) via custom UnmarshalJSON.
type HooksConfig struct {
	PreToolUse         []HookMatcherEntry `json:"PreToolUse,omitempty"`
	PostToolUse        []HookMatcherEntry `json:"PostToolUse,omitempty"`
	PostToolUseFailure []HookMatcherEntry `json:"PostToolUseFailure,omitempty"`
	PermissionRequest  []HookMatcherEntry `json:"PermissionRequest,omitempty"`
	SessionStart       []HookMatcherEntry `json:"SessionStart,omitempty"`
	SessionEnd         []HookMatcherEntry `json:"SessionEnd,omitempty"`
	SubagentStart      []HookMatcherEntry `json:"SubagentStart,omitempty"`
	SubagentStop       []HookMatcherEntry `json:"SubagentStop,omitempty"`
	Stop               []HookMatcherEntry `json:"Stop,omitempty"`
	Notification       []HookMatcherEntry `json:"Notification,omitempty"`
	UserPromptSubmit   []HookMatcherEntry `json:"UserPromptSubmit,omitempty"`
	PreCompact         []HookMatcherEntry `json:"PreCompact,omitempty"`
}

// SandboxConfig controls bash sandboxing.
type SandboxConfig struct {
	Enabled                   *bool                 `json:"enabled,omitempty"`                   // Enable filesystem/network sandboxing for bash.
	AutoAllowBashIfSandboxed  *bool                 `json:"autoAllowBashIfSandboxed,omitempty"`  // Auto-approve bash commands when sandboxed.
	ExcludedCommands          []string              `json:"excludedCommands,omitempty"`          // Commands that must run outside the sandbox.
	AllowUnsandboxedCommands  *bool                 `json:"allowUnsandboxedCommands,omitempty"`  // Whether dangerouslyDisableSandbox escape hatch is allowed.
	EnableWeakerNestedSandbox *bool                 `json:"enableWeakerNestedSandbox,omitempty"` // Allow weaker sandbox for unprivileged Docker.
	Network                   *SandboxNetworkConfig `json:"network,omitempty"`                   // Network-level sandbox knobs.
}

// SandboxNetworkConfig tunes sandbox network access.
type SandboxNetworkConfig struct {
	AllowUnixSockets  []string `json:"allowUnixSockets,omitempty"`  // Unix sockets exposed inside sandbox (SSH agent, docker socket).
	AllowLocalBinding *bool    `json:"allowLocalBinding,omitempty"` // Allow binding to localhost ports (macOS).
	HTTPProxyPort     *int     `json:"httpProxyPort,omitempty"`     // Port for custom HTTP proxy if bringing your own.
	SocksProxyPort    *int     `json:"socksProxyPort,omitempty"`    // Port for custom SOCKS5 proxy if bringing your own.
}

// BashOutputConfig configures when bash output is spooled to disk.
type BashOutputConfig struct {
	SyncThresholdBytes  *int `json:"syncThresholdBytes,omitempty"`  // Spool sync output to disk after exceeding this many bytes.
	AsyncThresholdBytes *int `json:"asyncThresholdBytes,omitempty"` // Spool async output to disk after exceeding this many bytes.
}

// ToolOutputConfig configures when tool output is persisted to disk.
type ToolOutputConfig struct {
	DefaultThresholdBytes int            `json:"defaultThresholdBytes,omitempty"` // Persist output to disk after exceeding this many bytes (0 = SDK default).
	PerToolThresholdBytes map[string]int `json:"perToolThresholdBytes,omitempty"` // Optional per-tool thresholds keyed by canonical tool name.
}

// MCPConfig nests Model Context Protocol server definitions.
type MCPConfig struct {
	Servers map[string]MCPServerConfig `json:"servers,omitempty"`
}

// MCPServerConfig describes how to reach an MCP server.
type MCPServerConfig struct {
	Type               string            `json:"type"`              // stdio/http/sse
	Command            string            `json:"command,omitempty"` // for stdio
	Args               []string          `json:"args,omitempty"`
	URL                string            `json:"url,omitempty"` // for http/sse
	Env                map[string]string `json:"env,omitempty"`
	Headers            map[string]string `json:"headers,omitempty"`
	TimeoutSeconds     int               `json:"timeoutSeconds,omitempty"`     // optional connect/list timeout
	EnabledTools       []string          `json:"enabledTools,omitempty"`       // optional remote tool allowlist
	DisabledTools      []string          `json:"disabledTools,omitempty"`      // optional remote tool denylist
	ToolTimeoutSeconds int               `json:"toolTimeoutSeconds,omitempty"` // optional timeout for each MCP tool call
}

// MCPServerRule constrains which MCP servers can be enabled.
type MCPServerRule struct {
	ServerName string `json:"serverName,omitempty"` // Name of the MCP server as declared in .mcp.json.
	URL        string `json:"url,omitempty"`        // Optional URL/endpoint to further pin the server.
}

// StatusLineConfig controls contextual status line rendering.
type StatusLineConfig struct {
	Type            string `json:"type"`                      // "command" executes a script; "template" renders a string.
	Command         string `json:"command,omitempty"`         // Executable to run when Type=command.
	Template        string `json:"template,omitempty"`        // Text template when Type=template.
	IntervalSeconds int    `json:"intervalSeconds,omitempty"` // Optional refresh interval in seconds.
	TimeoutSeconds  int    `json:"timeoutSeconds,omitempty"`  // Optional timeout for the command run.
}

// GetDefaultSettings returns Anthropic's documented defaults.
func GetDefaultSettings() Settings {
	cleanupPeriodDays := 30
	syncThresholdBytes := 30_000
	asyncThresholdBytes := 1024 * 1024
	return Settings{
		CleanupPeriodDays:   intPtr(cleanupPeriodDays),
		IncludeCoAuthoredBy: boolPtr(true),
		DisableAllHooks:     boolPtr(false),
		RespectGitignore:    boolPtr(true),
		BashOutput: &BashOutputConfig{
			SyncThresholdBytes:  &syncThresholdBytes,
			AsyncThresholdBytes: &asyncThresholdBytes,
		},
		Permissions: &PermissionsConfig{
			DefaultMode: "askBeforeRunningTools",
		},
		Sandbox: &SandboxConfig{
			Enabled:                   boolPtr(false),
			AutoAllowBashIfSandboxed:  boolPtr(true),
			AllowUnsandboxedCommands:  boolPtr(true),
			EnableWeakerNestedSandbox: boolPtr(false),
			Network: &SandboxNetworkConfig{
				AllowLocalBinding: boolPtr(false),
			},
		},
	}
}

// Validate delegates to the new aggregated validator.
func (s *Settings) Validate() error { return ValidateSettings(s) }

// Validate ensures permission modes and toggles are within allowed values.
func (p *PermissionsConfig) Validate() error { return errors.Join(validatePermissionsConfig(p)...) }

// Validate ensures hook maps contain non-empty commands.
func (h *HooksConfig) Validate() error { return errors.Join(validateHooksConfig(h)...) }

// Validate checks sandbox and network constraints.
func (s *SandboxConfig) Validate() error { return errors.Join(validateSandboxConfig(s)...) }

// Validate enforces presence of a server name.
func (r MCPServerRule) Validate() error {
	if strings.TrimSpace(r.ServerName) == "" {
		return errors.New("serverName is required")
	}
	return nil
}

// Validate ensures status line config is coherent.
func (s *StatusLineConfig) Validate() error { return errors.Join(validateStatusLineConfig(s)...) }

// boolPtr helps encode optional booleans.
func boolPtr(v bool) *bool { return &v }

// intPtr helps encode optional integers.
func intPtr(v int) *int { return &v }
