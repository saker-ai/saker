package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/cinience/saker/pkg/config"
	"github.com/go-ldap/ldap/v3"
)

// LDAPProvider authenticates users against an LDAP/Active Directory server.
type LDAPProvider struct {
	cfg *config.LDAPConfig
	log *slog.Logger
}

// NewLDAPProvider creates a new LDAP authentication provider.
func NewLDAPProvider(cfg *config.LDAPConfig, log *slog.Logger) *LDAPProvider {
	if log == nil {
		log = slog.Default()
	}
	return &LDAPProvider{cfg: cfg, log: log}
}

func (p *LDAPProvider) Name() string { return "ldap" }
func (p *LDAPProvider) Type() string { return "password" }

func (p *LDAPProvider) Authenticate(ctx context.Context, username, password string) (*AuthResult, error) {
	if !p.cfg.Enabled {
		return nil, ErrInvalidCredentials
	}

	conn, err := p.connect()
	if err != nil {
		return nil, fmt.Errorf("ldap connect: %w", err)
	}
	defer conn.Close()

	// Bind with service account for search (if configured).
	if p.cfg.BindDN != "" {
		bindPass := expandEnvVar(p.cfg.BindPassword)
		if err := conn.Bind(p.cfg.BindDN, bindPass); err != nil {
			return nil, fmt.Errorf("ldap service bind: %w", err)
		}
	}

	// Search for the user DN.
	userFilter := p.cfg.UserFilter
	if userFilter == "" {
		userFilter = "(uid=%s)"
	}
	filter := fmt.Sprintf(userFilter, ldap.EscapeFilter(username))

	attrs := p.attrList()
	searchReq := ldap.NewSearchRequest(
		p.cfg.BaseDN,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 1, 0, false,
		filter, attrs, nil,
	)

	sr, err := conn.Search(searchReq)
	if err != nil {
		return nil, fmt.Errorf("ldap search: %w", err)
	}
	if len(sr.Entries) == 0 {
		return nil, ErrInvalidCredentials
	}

	entry := sr.Entries[0]
	userDN := entry.DN

	// Bind as the user to verify password.
	if err := conn.Bind(userDN, password); err != nil {
		return nil, ErrInvalidCredentials
	}

	// Build AuthResult from LDAP attributes.
	result := &AuthResult{
		Username:    p.getAttr(entry, p.usernameAttr(), username),
		DisplayName: p.getAttr(entry, p.displayNameAttr(), ""),
		Email:       p.getAttr(entry, p.emailAttr(), ""),
		Groups:      entry.GetAttributeValues(p.memberOfAttr()),
		Provider:    "ldap",
		Role:        "user", // role resolved later by resolveRole
	}

	p.log.Info("ldap auth success", "username", result.Username, "groups", len(result.Groups))
	return result, nil
}

// connect establishes a connection to the LDAP server.
func (p *LDAPProvider) connect() (*ldap.Conn, error) {
	tlsConfig := &tls.Config{InsecureSkipVerify: p.cfg.InsecureSkipVerify} //nolint:gosec // user-configured

	if strings.HasPrefix(p.cfg.URL, "ldaps://") {
		return ldap.DialURL(p.cfg.URL, ldap.DialWithTLSConfig(tlsConfig))
	}

	conn, err := ldap.DialURL(p.cfg.URL)
	if err != nil {
		return nil, err
	}

	if p.cfg.StartTLS {
		if err := conn.StartTLS(tlsConfig); err != nil {
			conn.Close()
			return nil, fmt.Errorf("starttls: %w", err)
		}
	}
	return conn, nil
}

func (p *LDAPProvider) attrList() []string {
	return []string{
		p.usernameAttr(), p.emailAttr(), p.displayNameAttr(), p.memberOfAttr(),
	}
}

func (p *LDAPProvider) usernameAttr() string {
	if p.cfg.Attrs.Username != "" {
		return p.cfg.Attrs.Username
	}
	return "uid"
}

func (p *LDAPProvider) emailAttr() string {
	if p.cfg.Attrs.Email != "" {
		return p.cfg.Attrs.Email
	}
	return "mail"
}

func (p *LDAPProvider) displayNameAttr() string {
	if p.cfg.Attrs.DisplayName != "" {
		return p.cfg.Attrs.DisplayName
	}
	return "cn"
}

func (p *LDAPProvider) memberOfAttr() string {
	if p.cfg.Attrs.MemberOf != "" {
		return p.cfg.Attrs.MemberOf
	}
	return "memberOf"
}

func (p *LDAPProvider) getAttr(entry *ldap.Entry, attr, fallback string) string {
	if v := entry.GetAttributeValue(attr); v != "" {
		return v
	}
	return fallback
}

// expandEnvVar expands a single ${ENV_VAR} reference in a string.
// If the string doesn't match the ${...} pattern, it is returned as-is.
func expandEnvVar(s string) string {
	if !strings.HasPrefix(s, "${") || !strings.HasSuffix(s, "}") {
		return s
	}
	return os.Getenv(s[2 : len(s)-1])
}
