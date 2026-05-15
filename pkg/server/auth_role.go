package server

import "github.com/saker-ai/saker/pkg/config"

// resolveRole determines the Saker role for an authenticated user based on
// the RoleMappingConfig. It checks AdminUsers, then AdminGroups, and falls
// back to DefaultRole (default "user").
func resolveRole(result *AuthResult, mapping *config.RoleMappingConfig) string {
	if mapping == nil {
		return result.Role // no mapping configured, keep provider's decision
	}

	// Explicit admin users.
	for _, u := range mapping.AdminUsers {
		if u == result.Username {
			return "admin"
		}
	}

	// Admin groups — any intersection grants admin.
	if len(mapping.AdminGroups) > 0 && len(result.Groups) > 0 {
		adminSet := make(map[string]struct{}, len(mapping.AdminGroups))
		for _, g := range mapping.AdminGroups {
			adminSet[g] = struct{}{}
		}
		for _, g := range result.Groups {
			if _, ok := adminSet[g]; ok {
				return "admin"
			}
		}
	}

	// Default role.
	if mapping.DefaultRole != "" {
		return mapping.DefaultRole
	}
	return "user"
}
