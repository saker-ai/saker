package middleware

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/saker-ai/saker/pkg/security"
)

// SafetyMiddleware runs leak detection and prompt injection sanitization
// on tool outputs before they reach the LLM. It operates at the AfterTool stage.
//
// Since the middleware package cannot import pkg/agent (circular dependency),
// callers must provide an OutputExtractor and OutputWriter to bridge the gap.
type SafetyMiddleware struct {
	leakDetector  *security.LeakDetector
	sanitizer     *security.Sanitizer
	extractOutput OutputExtractor
	writeOutput   OutputWriter
}

// OutputExtractor reads tool name and output from State.ToolResult.
type OutputExtractor func(toolResult any) (name, output string, ok bool)

// OutputWriter writes the sanitized output and metadata back into State.ToolResult.
type OutputWriter func(st *State, sanitizedOutput string, meta map[string]any)

// NewSafetyMiddleware creates a SafetyMiddleware with default detection patterns.
// The extractor/writer functions bridge the agent.ToolResult type without importing pkg/agent.
func NewSafetyMiddleware(extract OutputExtractor, write OutputWriter) *SafetyMiddleware {
	return &SafetyMiddleware{
		leakDetector:  security.NewLeakDetector(),
		sanitizer:     security.NewSanitizer(),
		extractOutput: extract,
		writeOutput:   write,
	}
}

func (m *SafetyMiddleware) Name() string { return "safety" }

func (m *SafetyMiddleware) BeforeAgent(_ context.Context, _ *State) error { return nil }
func (m *SafetyMiddleware) BeforeModel(_ context.Context, _ *State) error { return nil }
func (m *SafetyMiddleware) AfterModel(_ context.Context, _ *State) error  { return nil }
func (m *SafetyMiddleware) BeforeTool(_ context.Context, _ *State) error  { return nil }
func (m *SafetyMiddleware) AfterAgent(_ context.Context, _ *State) error  { return nil }

// AfterTool scans tool output for secret leaks and prompt injection patterns.
func (m *SafetyMiddleware) AfterTool(_ context.Context, st *State) error {
	if st == nil || st.ToolResult == nil || m.extractOutput == nil {
		return nil
	}

	toolName, output, ok := m.extractOutput(st.ToolResult)
	if !ok || output == "" {
		return nil
	}

	// Step 1: Leak detection — block or redact secrets.
	cleaned, findings, err := m.leakDetector.ScanAndClean(output)
	if err != nil {
		return fmt.Errorf("safety: %w", err)
	}
	for _, f := range findings {
		if f.Action == security.LeakWarn {
			slog.Warn("safety: potential secret in tool output", "tool", toolName, "pattern", f.PatternName, "severity", f.Severity)
		}
	}
	output = cleaned

	// Step 2: Injection sanitization — escape critical patterns.
	result := m.sanitizer.SanitizeToolOutput(toolName, output)
	if len(result.Findings) > 0 {
		slog.Warn("safety: injection patterns detected in tool output", "tool", toolName, "count", len(result.Findings))
	}
	output = result.Output

	// Step 3: Wrap output for LLM with boundary protection.
	output = security.WrapForLLM(toolName, output)

	// Write back the sanitized output.
	meta := map[string]any{}
	if len(findings) > 0 {
		meta["safety.leak_findings"] = len(findings)
	}
	if len(result.Findings) > 0 {
		meta["safety.injection_findings"] = len(result.Findings)
	}

	if m.writeOutput != nil {
		m.writeOutput(st, output, meta)
	}
	return nil
}
