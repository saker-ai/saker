package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// validator_misc.go owns the long tail of per-block validators that don't
// need the heavyweight permissions/hooks helpers: status line, personas,
// force-login, storage, aigo, failover, web auth, CORS. Permissions checks
// live in validator_permissions.go; hooks/sandbox/MCP in validator_hooks.go;
// settings root in validator.go.

func validateStatusLineConfig(cfg *StatusLineConfig) []error {
	if cfg == nil {
		return nil
	}
	var errs []error
	typ := strings.TrimSpace(cfg.Type)
	switch typ {
	case "command":
		if strings.TrimSpace(cfg.Command) == "" {
			errs = append(errs, errors.New("statusLine.command is required when type=command"))
		}
	case "template":
		if strings.TrimSpace(cfg.Template) == "" {
			errs = append(errs, errors.New("statusLine.template is required when type=template"))
		}
	case "":
		errs = append(errs, errors.New("statusLine.type is required"))
	default:
		errs = append(errs, fmt.Errorf("statusLine.type %q is not supported", cfg.Type))
	}
	if cfg.IntervalSeconds < 0 {
		errs = append(errs, errors.New("statusLine.intervalSeconds cannot be negative"))
	}
	if cfg.TimeoutSeconds < 0 {
		errs = append(errs, errors.New("statusLine.timeoutSeconds cannot be negative"))
	}
	return errs
}

func validatePersonasConfig(cfg *PersonasConfig) []error {
	if cfg == nil {
		return nil
	}
	var errs []error
	for id, p := range cfg.Profiles {
		if strings.TrimSpace(id) == "" {
			errs = append(errs, errors.New("personas: profile ID cannot be empty"))
		}
		if p.Inherit != "" {
			if _, ok := cfg.Profiles[p.Inherit]; !ok {
				errs = append(errs, fmt.Errorf("personas: profile %q inherits unknown profile %q", id, p.Inherit))
			}
		}
	}
	for i, route := range cfg.Routes {
		if strings.TrimSpace(route.Channel) == "" {
			errs = append(errs, fmt.Errorf("personas: route[%d] channel is required", i))
		}
		if strings.TrimSpace(route.Persona) == "" {
			errs = append(errs, fmt.Errorf("personas: route[%d] persona is required", i))
		}
	}
	return errs
}

func validateForceLoginConfig(method, org string) []error {
	rawOrg := org
	method = strings.TrimSpace(method)
	org = strings.TrimSpace(org)
	if method == "" {
		return nil
	}

	var errs []error
	if method != "claudeai" && method != "console" {
		errs = append(errs, fmt.Errorf("forceLoginMethod must be \"claudeai\" or \"console\", got %q", method))
	}
	if rawOrg != "" && org == "" {
		errs = append(errs, errors.New("forceLoginOrgUUID cannot be blank"))
	}
	return errs
}

func validateStorageConfig(cfg *StorageConfig) []error {
	if cfg == nil {
		return nil
	}
	var errs []error
	backend := strings.TrimSpace(cfg.Backend)
	switch backend {
	case "", "osfs", "memfs", "embedded", "s3":
	default:
		errs = append(errs, fmt.Errorf("storage.backend %q is not supported (use osfs, memfs, embedded, or s3)", backend))
	}
	if backend == "s3" {
		if cfg.S3 == nil {
			errs = append(errs, errors.New("storage.s3 is required when backend is s3"))
		} else {
			if strings.TrimSpace(cfg.S3.Bucket) == "" {
				errs = append(errs, errors.New("storage.s3.bucket is required"))
			}
			if strings.TrimSpace(cfg.S3.AccessKeyID) == "" {
				errs = append(errs, errors.New("storage.s3.accessKeyID is required"))
			}
			if strings.TrimSpace(cfg.S3.SecretAccessKey) == "" {
				errs = append(errs, errors.New("storage.s3.secretAccessKey is required"))
			}
		}
	}
	if backend == "embedded" && cfg.Embedded != nil {
		mode := strings.TrimSpace(cfg.Embedded.Mode)
		switch mode {
		case "", "external", "standalone":
		default:
			errs = append(errs, fmt.Errorf("storage.embedded.mode %q is not supported (use external or standalone)", mode))
		}
	}
	return errs
}

func validateAigoConfig(cfg *AigoConfig) []error {
	if cfg == nil {
		return nil
	}
	var errs []error
	if len(cfg.Providers) == 0 {
		errs = append(errs, errors.New("aigo.providers is required when aigo is configured"))
	}
	for alias, p := range cfg.Providers {
		if strings.TrimSpace(alias) == "" {
			errs = append(errs, errors.New("aigo.providers: alias cannot be empty"))
			continue
		}
		if strings.TrimSpace(p.Type) == "" {
			errs = append(errs, fmt.Errorf("aigo.providers[%s].type is required", alias))
		}
		if strings.TrimSpace(p.APIKey) == "" && strings.TrimSpace(p.BaseURL) == "" {
			errs = append(errs, fmt.Errorf("aigo.providers[%s]: apiKey or baseUrl is required", alias))
		}
	}
	if cfg.Timeout != "" {
		if _, err := time.ParseDuration(cfg.Timeout); err != nil {
			errs = append(errs, fmt.Errorf("aigo.timeout %q is not a valid duration: %w", cfg.Timeout, err))
		}
	}
	return errs
}

func validateFailoverConfig(cfg *FailoverConfig) []error {
	if cfg == nil {
		return nil
	}
	var errs []error
	// PrimaryKeyPool can be configured even when failover.enabled is false —
	// multi-key load balancing is independent of cross-provider failover.
	for i, k := range cfg.PrimaryKeyPool {
		if strings.TrimSpace(k.Provider) == "" {
			errs = append(errs, fmt.Errorf("failover.primaryKeyPool[%d].provider is required", i))
		}
		if strings.TrimSpace(k.APIKey) == "" {
			errs = append(errs, fmt.Errorf("failover.primaryKeyPool[%d].apiKey is required", i))
		}
		if k.Weight < 0 {
			errs = append(errs, fmt.Errorf("failover.primaryKeyPool[%d].weight must be >=0, got %v", i, k.Weight))
		}
	}
	if cfg.Enabled == nil || !*cfg.Enabled {
		return errs
	}
	if len(cfg.Models) == 0 {
		errs = append(errs, errors.New("failover.models is required when failover is enabled"))
	}
	for i, m := range cfg.Models {
		if strings.TrimSpace(m.Provider) == "" {
			errs = append(errs, fmt.Errorf("failover.models[%d].provider is required", i))
		}
		if strings.TrimSpace(m.Model) == "" {
			errs = append(errs, fmt.Errorf("failover.models[%d].model is required", i))
		}
	}
	if cfg.MaxRetries < 0 {
		errs = append(errs, fmt.Errorf("failover.maxRetries must be >=0, got %d", cfg.MaxRetries))
	}
	return errs
}

func validateBifrostConfig(cfg *BifrostConfig) []error {
	if cfg == nil {
		return nil
	}
	var errs []error
	errs = append(errs, validateSemanticCacheConfig(cfg.SemanticCache)...)
	errs = append(errs, validateTelemetryConfig(cfg.Telemetry)...)
	return errs
}

func validateSemanticCacheConfig(cfg *SemanticCacheConfig) []error {
	if cfg == nil {
		return nil
	}
	enabled := cfg.Enabled != nil && *cfg.Enabled
	var errs []error
	if cfg.Threshold < 0 || cfg.Threshold > 1 {
		errs = append(errs, fmt.Errorf("bifrost.semanticCache.threshold must be in [0,1], got %v", cfg.Threshold))
	}
	if cfg.TTLSeconds < 0 {
		errs = append(errs, fmt.Errorf("bifrost.semanticCache.ttlSeconds must be >=0, got %d", cfg.TTLSeconds))
	}
	if cfg.Dimension < 0 {
		errs = append(errs, fmt.Errorf("bifrost.semanticCache.dimension must be >=0, got %d", cfg.Dimension))
	}
	if cfg.ConvHistoryThreshold < 0 {
		errs = append(errs, fmt.Errorf("bifrost.semanticCache.convHistoryThreshold must be >=0, got %d", cfg.ConvHistoryThreshold))
	}
	if !enabled {
		return errs
	}
	if strings.TrimSpace(cfg.Provider) == "" {
		errs = append(errs, errors.New("bifrost.semanticCache.provider is required when enabled"))
	}
	if strings.TrimSpace(cfg.EmbeddingModel) == "" {
		errs = append(errs, errors.New("bifrost.semanticCache.embeddingModel is required when enabled"))
	}
	if cfg.VectorStore == nil {
		errs = append(errs, errors.New("bifrost.semanticCache.vectorStore is required when enabled"))
	} else {
		errs = append(errs, validateVectorStoreConfig(cfg.VectorStore)...)
	}
	return errs
}

func validateVectorStoreConfig(cfg *VectorStoreConfig) []error {
	if cfg == nil {
		return nil
	}
	var errs []error
	typ := strings.ToLower(strings.TrimSpace(cfg.Type))
	switch typ {
	case "redis", "qdrant", "pinecone", "weaviate":
	case "":
		errs = append(errs, errors.New("bifrost.semanticCache.vectorStore.type is required (redis|qdrant|pinecone|weaviate)"))
	default:
		errs = append(errs, fmt.Errorf("bifrost.semanticCache.vectorStore.type %q is not supported (use redis|qdrant|pinecone|weaviate)", cfg.Type))
	}
	if strings.TrimSpace(cfg.Endpoint) == "" {
		errs = append(errs, errors.New("bifrost.semanticCache.vectorStore.endpoint is required"))
	}
	return errs
}

func validateTelemetryConfig(cfg *TelemetryConfig) []error {
	if cfg == nil {
		return nil
	}
	var errs []error
	if cfg.Sampling < 0 || cfg.Sampling > 1 {
		errs = append(errs, fmt.Errorf("bifrost.telemetry.sampling must be in [0,1], got %v", cfg.Sampling))
	}
	if cfg.MetricsPushIntervalSeconds < 0 {
		errs = append(errs, fmt.Errorf("bifrost.telemetry.metricsPushIntervalSeconds must be >=0, got %d", cfg.MetricsPushIntervalSeconds))
	}
	enabled := cfg.Enabled != nil && *cfg.Enabled
	if !enabled {
		return errs
	}
	proto := strings.ToLower(strings.TrimSpace(cfg.Protocol))
	switch proto {
	case "", "grpc", "http":
	default:
		errs = append(errs, fmt.Errorf("bifrost.telemetry.protocol %q is not supported (use grpc or http)", cfg.Protocol))
	}
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		errs = append(errs, errors.New("bifrost.telemetry.endpoint is required when enabled"))
	} else if u, err := url.Parse(endpoint); err != nil || u.Host == "" {
		errs = append(errs, fmt.Errorf("bifrost.telemetry.endpoint %q is not a valid URL", endpoint))
	}
	traceType := strings.ToLower(strings.TrimSpace(cfg.TraceType))
	switch traceType {
	case "", "genai_extension", "vercel", "open_inference":
	default:
		errs = append(errs, fmt.Errorf("bifrost.telemetry.traceType %q is not supported", cfg.TraceType))
	}
	return errs
}

func validateGovernanceConfig(cfg *GovernanceConfig) []error {
	if cfg == nil {
		return nil
	}
	var errs []error
	seen := make(map[string]int, len(cfg.VirtualKeys))
	for i, vk := range cfg.VirtualKeys {
		id := strings.TrimSpace(vk.ID)
		if id == "" {
			errs = append(errs, fmt.Errorf("governance.virtualKeys[%d].id is required", i))
			continue
		}
		if prev, dup := seen[id]; dup {
			errs = append(errs, fmt.Errorf("governance.virtualKeys[%d].id %q duplicates entry [%d]", i, id, prev))
		}
		seen[id] = i
		if vk.BudgetUSD < 0 {
			errs = append(errs, fmt.Errorf("governance.virtualKeys[%d].budgetUSD must be >=0, got %v", i, vk.BudgetUSD))
		}
		if vk.RPM < 0 {
			errs = append(errs, fmt.Errorf("governance.virtualKeys[%d].rpm must be >=0, got %d", i, vk.RPM))
		}
		if vk.TPM < 0 {
			errs = append(errs, fmt.Errorf("governance.virtualKeys[%d].tpm must be >=0, got %d", i, vk.TPM))
		}
		switch strings.ToLower(strings.TrimSpace(vk.ResetCron)) {
		case "", "monthly", "weekly", "daily":
		default:
			errs = append(errs, fmt.Errorf("governance.virtualKeys[%d].resetCron %q is not supported (use monthly|weekly|daily)", i, vk.ResetCron))
		}
	}
	return errs
}

func validateWebAuthConfig(cfg *WebAuthConfig) []error {
	if cfg == nil {
		return nil
	}
	var errs []error
	for i, u := range cfg.Users {
		if strings.TrimSpace(u.Username) == "" {
			errs = append(errs, fmt.Errorf("webAuth.users[%d].username is required", i))
		}
		if strings.TrimSpace(u.Password) == "" {
			errs = append(errs, fmt.Errorf("webAuth.users[%d].password is required", i))
		}
	}
	if cfg.LDAP != nil && cfg.LDAP.Enabled {
		if strings.TrimSpace(cfg.LDAP.URL) == "" {
			errs = append(errs, errors.New("webAuth.ldap.url is required when LDAP is enabled"))
		}
		if strings.TrimSpace(cfg.LDAP.BaseDN) == "" {
			errs = append(errs, errors.New("webAuth.ldap.baseDN is required when LDAP is enabled"))
		}
	}
	if cfg.OIDC != nil && cfg.OIDC.Enabled {
		if strings.TrimSpace(cfg.OIDC.Issuer) == "" {
			errs = append(errs, errors.New("webAuth.oidc.issuer is required when OIDC is enabled"))
		}
		if strings.TrimSpace(cfg.OIDC.ClientID) == "" {
			errs = append(errs, errors.New("webAuth.oidc.clientId is required when OIDC is enabled"))
		}
	}
	if cfg.RoleMapping != nil {
		role := strings.TrimSpace(cfg.RoleMapping.DefaultRole)
		switch role {
		case "", "user", "admin":
		default:
			errs = append(errs, fmt.Errorf("webAuth.roleMapping.defaultRole %q is not supported (use user or admin)", role))
		}
	}
	return errs
}

func validateCORSConfig(cfg *CORSConfig) []error {
	if cfg == nil {
		return nil
	}
	var errs []error
	for i, origin := range cfg.AllowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			errs = append(errs, fmt.Errorf("cors.allowedOrigins[%d] is empty", i))
			continue
		}
		if u, err := url.Parse(origin); err != nil || u.Scheme == "" || u.Host == "" {
			errs = append(errs, fmt.Errorf("cors.allowedOrigins[%d] %q is not a valid URL", i, origin))
		}
	}
	return errs
}
