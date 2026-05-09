package middleware

import (
	"context"
	"strings"
)

// ErrorCategory classifies an error into a high-level category.
type ErrorCategory string

const (
	ErrorCategoryAuth       ErrorCategory = "authentication"
	ErrorCategoryRateLimit  ErrorCategory = "rate_limit"
	ErrorCategoryNetwork    ErrorCategory = "network"
	ErrorCategoryTimeout    ErrorCategory = "timeout"
	ErrorCategoryPermission ErrorCategory = "permission"
	ErrorCategoryNotFound   ErrorCategory = "not_found"
	ErrorCategoryValidation ErrorCategory = "validation"
	ErrorCategorySandbox    ErrorCategory = "sandbox"
	ErrorCategoryModel      ErrorCategory = "model"
	ErrorCategoryTool       ErrorCategory = "tool"
	ErrorCategoryInternal   ErrorCategory = "internal"
	ErrorCategoryUnknown    ErrorCategory = "unknown"
)

// ErrorSeverity indicates the severity level.
type ErrorSeverity string

const (
	SeverityLow      ErrorSeverity = "low"
	SeverityMedium   ErrorSeverity = "medium"
	SeverityHigh     ErrorSeverity = "high"
	SeverityCritical ErrorSeverity = "critical"
)

// ClassifiedError holds structured information about an error.
type ClassifiedError struct {
	Category  ErrorCategory `json:"category"`
	Severity  ErrorSeverity `json:"severity"`
	Retryable bool          `json:"retryable"`
	Recovery  string        `json:"recovery"`
	Original  string        `json:"original"`
}

// classificationRule maps error patterns to their classification.
type classificationRule struct {
	patterns  []string
	category  ErrorCategory
	severity  ErrorSeverity
	retryable bool
	recovery  string
}

var classificationRules = []classificationRule{
	// Authentication errors.
	{
		patterns:  []string{"api key", "apikey", "unauthorized", "401", "authentication", "invalid x-api-key", "auth token"},
		category:  ErrorCategoryAuth,
		severity:  SeverityCritical,
		retryable: false,
		recovery:  "Check your API key is valid and not expired. Verify ANTHROPIC_API_KEY environment variable.",
	},
	// Rate limit errors.
	{
		patterns:  []string{"rate limit", "rate_limit", "429", "too many requests", "throttl", "overloaded"},
		category:  ErrorCategoryRateLimit,
		severity:  SeverityMedium,
		retryable: true,
		recovery:  "Wait and retry with exponential backoff. Consider reducing request frequency.",
	},
	// Timeout errors.
	{
		patterns:  []string{"timeout", "deadline exceeded", "context deadline", "timed out"},
		category:  ErrorCategoryTimeout,
		severity:  SeverityMedium,
		retryable: true,
		recovery:  "Increase timeout or simplify the request. Check network connectivity.",
	},
	// Network errors.
	{
		patterns:  []string{"connection refused", "no such host", "dns", "network unreachable", "connection reset", "eof", "broken pipe", "tls handshake"},
		category:  ErrorCategoryNetwork,
		severity:  SeverityHigh,
		retryable: true,
		recovery:  "Check network connectivity and DNS resolution. Verify the API endpoint URL.",
	},
	// Sandbox errors (check before permission — "blocked by sandbox" must not match "permission").
	{
		patterns:  []string{"sandbox", "sandboxed", "path not allowed", "outside allowed", "blocked by sandbox"},
		category:  ErrorCategorySandbox,
		severity:  SeverityMedium,
		retryable: false,
		recovery:  "Add the path to permissions.additionalDirectories in settings.json, or disable sandbox.",
	},
	// Permission errors.
	{
		patterns:  []string{"permission denied", "access denied", "forbidden", "403", "not permitted"},
		category:  ErrorCategoryPermission,
		severity:  SeverityHigh,
		retryable: false,
		recovery:  "Check file permissions and sandbox settings in .saker/settings.json.",
	},
	// Not found errors.
	{
		patterns:  []string{"not found", "no such file", "does not exist", "404", "file not found"},
		category:  ErrorCategoryNotFound,
		severity:  SeverityLow,
		retryable: false,
		recovery:  "Verify the file path or resource exists. Use glob/grep to search for the correct path.",
	},
	// Validation errors.
	{
		patterns:  []string{"invalid", "validation", "malformed", "bad request", "400", "schema", "required field", "parse error"},
		category:  ErrorCategoryValidation,
		severity:  SeverityLow,
		retryable: false,
		recovery:  "Check input parameters match the expected schema. Review tool documentation.",
	},
	// Model errors.
	{
		patterns:  []string{"model", "context window", "max tokens", "content filter", "safety", "output limit", "context length"},
		category:  ErrorCategoryModel,
		severity:  SeverityMedium,
		retryable: false,
		recovery:  "Reduce prompt size, switch to a model with larger context, or adjust max_tokens.",
	},
	// Tool errors.
	{
		patterns:  []string{"tool", "command failed", "exit code", "execution error", "non-zero exit"},
		category:  ErrorCategoryTool,
		severity:  SeverityLow,
		retryable: false,
		recovery:  "Review the tool output for details. Try running the command manually.",
	},
}

// ClassifyError categorises an error string into a structured classification.
func ClassifyError(err error) *ClassifiedError {
	if err == nil {
		return nil
	}
	return ClassifyErrorString(err.Error())
}

// ClassifyErrorString categorises an error message string.
func ClassifyErrorString(msg string) *ClassifiedError {
	if msg == "" {
		return nil
	}
	lower := strings.ToLower(msg)

	for _, rule := range classificationRules {
		for _, pattern := range rule.patterns {
			if strings.Contains(lower, pattern) {
				return &ClassifiedError{
					Category:  rule.category,
					Severity:  rule.severity,
					Retryable: rule.retryable,
					Recovery:  rule.recovery,
					Original:  msg,
				}
			}
		}
	}

	// Check for context cancellation.
	if strings.Contains(lower, "context canceled") || strings.Contains(lower, "context cancelled") {
		return &ClassifiedError{
			Category:  ErrorCategoryTimeout,
			Severity:  SeverityLow,
			Retryable: false,
			Recovery:  "The operation was cancelled. This is usually intentional (user interrupt or timeout).",
			Original:  msg,
		}
	}

	return &ClassifiedError{
		Category:  ErrorCategoryUnknown,
		Severity:  SeverityMedium,
		Retryable: false,
		Recovery:  "Inspect the error details and check logs for more context.",
		Original:  msg,
	}
}

// ErrorClassifierMiddleware is an AfterModel/AfterTool middleware that enriches
// error responses with classification metadata for downstream consumers.
type ErrorClassifierMiddleware struct{}

// NewErrorClassifier creates a new error classifier middleware.
func NewErrorClassifier() *ErrorClassifierMiddleware {
	return &ErrorClassifierMiddleware{}
}

func (m *ErrorClassifierMiddleware) Name() string { return "error_classifier" }

func (m *ErrorClassifierMiddleware) BeforeAgent(_ context.Context, _ *State) error { return nil }
func (m *ErrorClassifierMiddleware) AfterAgent(_ context.Context, _ *State) error  { return nil }
func (m *ErrorClassifierMiddleware) BeforeModel(_ context.Context, _ *State) error { return nil }
func (m *ErrorClassifierMiddleware) BeforeTool(_ context.Context, _ *State) error  { return nil }

// AfterModel checks ModelOutput for error indicators and classifies them.
func (m *ErrorClassifierMiddleware) AfterModel(_ context.Context, st *State) error {
	if st.ModelOutput == nil {
		return nil
	}
	// Check if ModelOutput carries an error string (provider-specific).
	if errStr, ok := extractErrorString(st.ModelOutput); ok && errStr != "" {
		classified := ClassifyErrorString(errStr)
		if classified != nil {
			st.Values["error_classification"] = classified
		}
	}
	return nil
}

// AfterTool checks ToolResult for errors and classifies them.
func (m *ErrorClassifierMiddleware) AfterTool(_ context.Context, st *State) error {
	if st.ToolResult == nil {
		return nil
	}
	if errStr, ok := extractErrorString(st.ToolResult); ok && errStr != "" {
		classified := ClassifyErrorString(errStr)
		if classified != nil {
			st.Values["error_classification"] = classified
		}
	}
	return nil
}

// extractErrorString attempts to extract an error string from a generic value.
// It supports structs with an Output/Error field or fmt.Stringer.
func extractErrorString(v any) (string, bool) {
	// Check for a struct with an Error() method.
	if e, ok := v.(interface{ Error() string }); ok {
		return e.Error(), true
	}
	// Check for map with "error" key.
	if m, ok := v.(map[string]any); ok {
		if errVal, exists := m["error"]; exists {
			if s, ok := errVal.(string); ok {
				return s, true
			}
		}
	}
	return "", false
}
