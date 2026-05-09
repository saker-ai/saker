package skills

import (
	"strings"
)

// SubstituteArguments replaces argument placeholders in skill content.
//
// Supported placeholders:
//   - $ARGUMENTS — replaced with the full args string
//   - $1, $2, ... — replaced with positional arguments (space-split)
//   - ${name} — replaced with named argument from argNames mapping
//
// If argNames is provided, $1 maps to argNames[0], etc.
// Unmatched placeholders are left as-is.
func SubstituteArguments(content, args string, argNames []string) string {
	if content == "" {
		return content
	}

	// Replace $ARGUMENTS with the full args string.
	content = strings.ReplaceAll(content, "$ARGUMENTS", args)

	if args == "" {
		return content
	}

	// Split args into positional parts.
	parts := splitArgs(args)

	// Replace positional $1, $2, etc.
	for i, part := range parts {
		placeholder := "$" + itoa(i+1)
		content = strings.ReplaceAll(content, placeholder, part)
	}

	// Replace named ${name} placeholders using argNames mapping.
	for i, name := range argNames {
		if name == "" {
			continue
		}
		placeholder := "${" + name + "}"
		value := ""
		if i < len(parts) {
			value = parts[i]
		}
		content = strings.ReplaceAll(content, placeholder, value)
	}

	return content
}

// splitArgs splits an argument string respecting quoted segments.
// "hello world" counts as one argument, unquoted spaces separate arguments.
func splitArgs(args string) []string {
	var parts []string
	var current strings.Builder
	inQuote := false
	quoteChar := byte(0)

	for i := 0; i < len(args); i++ {
		ch := args[i]
		switch {
		case inQuote:
			if ch == quoteChar {
				inQuote = false
			} else {
				current.WriteByte(ch)
			}
		case ch == '"' || ch == '\'':
			inQuote = true
			quoteChar = ch
		case ch == ' ' || ch == '\t':
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(ch)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

// itoa converts a small int to string without importing strconv.
func itoa(n int) string {
	if n < 0 {
		return "-" + itoa(-n)
	}
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}
