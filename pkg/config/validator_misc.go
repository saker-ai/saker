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
	if cfg.Enabled == nil || !*cfg.Enabled {
		return nil
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
