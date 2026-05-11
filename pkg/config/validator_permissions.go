package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// validator_permissions.go owns the permissions-block validation plus the
// shared helpers (tool-name/regex pattern enforcement). Settings root and
// remaining validators live in validator.go and sibling files.

var (
	toolNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
)

func validatePermissionsConfig(p *PermissionsConfig) []error {
	if p == nil {
		return nil
	}
	var errs []error

	mode := strings.TrimSpace(p.DefaultMode)
	switch mode {
	case "askBeforeRunningTools", "acceptReadOnly", "acceptEdits", "bypassPermissions":
	case "":
		errs = append(errs, errors.New("permissions.defaultMode is required"))
	default:
		errs = append(errs, fmt.Errorf("permissions.defaultMode %q is not supported", mode))
	}

	if p.DisableBypassPermissionsMode != "" && p.DisableBypassPermissionsMode != "disable" {
		errs = append(errs, fmt.Errorf("permissions.disableBypassPermissionsMode must be \"disable\", got %q", p.DisableBypassPermissionsMode))
	}

	errs = append(errs, validateRuleSlice("permissions.allow", p.Allow)...)
	errs = append(errs, validateRuleSlice("permissions.ask", p.Ask)...)
	errs = append(errs, validateRuleSlice("permissions.deny", p.Deny)...)

	for i, dir := range p.AdditionalDirectories {
		if strings.TrimSpace(dir) == "" {
			errs = append(errs, fmt.Errorf("permissions.additionalDirectories[%d] is empty", i))
		}
	}

	return errs
}

func validateRuleSlice(label string, rules []string) []error {
	var errs []error
	for i, rule := range rules {
		if err := validatePermissionRule(rule); err != nil {
			errs = append(errs, fmt.Errorf("%s[%d]: %w", label, i, err))
		}
	}
	return errs
}

// validatePermissionRule enforces the Tool(target) pattern used by allow/ask/deny.
func validatePermissionRule(rule string) error {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return errors.New("permission rule is empty")
	}

	if !strings.Contains(rule, "(") {
		return nil
	}

	if !strings.HasSuffix(rule, ")") {
		return fmt.Errorf("permission rule %q must end with )", rule)
	}
	if strings.Count(rule, "(") != 1 || strings.Count(rule, ")") != 1 {
		return fmt.Errorf("permission rule %q must look like Tool(pattern)", rule)
	}
	open := strings.IndexRune(rule, '(')
	tool := rule[:open]
	target := rule[open+1 : len(rule)-1]
	if err := validateToolName(tool); err != nil {
		return fmt.Errorf("invalid tool name: %w", err)
	}
	if strings.TrimSpace(target) == "" {
		return fmt.Errorf("permission rule %q target is empty", rule)
	}
	return nil
}

// validateToolName ensures hooks and permission prefixes use a predictable charset.
func validateToolName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("tool name is empty")
	}
	if !toolNamePattern.MatchString(name) {
		return fmt.Errorf("tool name %q must match %s", name, toolNamePattern.String())
	}
	return nil
}

// validateToolPattern accepts literal tool names, wildcard "*", and arbitrary regex patterns.
// Selector in pkg/core/hooks compiles the provided string, so we enforce regex validity here
// while still allowing the catch-all wildcard used in configs.
func validateToolPattern(pattern string) error {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return errors.New("tool pattern is empty")
	}
	if pattern == "*" {
		return nil
	}
	if _, err := regexp.Compile(pattern); err != nil {
		return fmt.Errorf("tool pattern %q is not a valid regexp: %w", pattern, err)
	}
	return nil
}
