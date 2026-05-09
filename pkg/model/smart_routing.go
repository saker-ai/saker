package model

import (
	"net/url"
	"strings"
)

// PromptComplexity indicates how complex a prompt is.
type PromptComplexity string

const (
	ComplexityStrong   PromptComplexity = "strong"
	ComplexityStandard PromptComplexity = "standard"
)

// strongKeywords are terms that indicate a prompt needs the full-strength model.
var strongKeywords = []string{
	// Analysis & architecture
	"architect", "architecture", "analyze", "analysis", "design pattern",
	"trade-off", "tradeoff", "compare and contrast",
	// Implementation
	"refactor", "implement", "rewrite", "migrate", "optimize", "optimise",
	"debug", "diagnose", "troubleshoot", "root cause",
	// Code review & security
	"review", "audit", "security", "vulnerability", "injection",
	"race condition", "deadlock", "memory leak",
	// Planning
	"plan", "strategy", "roadmap", "proposal", "rfc",
	"breaking change", "backward compat",
	// Complex reasoning
	"explain why", "what are the implications", "pros and cons",
	"complex", "nuanced", "subtle",
	// Multi-step
	"step by step", "multi-file", "across the codebase",
	"end to end", "full stack",
	// Delegation markers
	"delegate", "subagent", "spawn",
}

// ClassifyPromptComplexity returns whether a prompt needs a strong model
// or can be handled by a standard (cheaper) model.
func ClassifyPromptComplexity(prompt string) PromptComplexity {
	lower := strings.ToLower(prompt)

	// URLs suggest research tasks that benefit from stronger models.
	if containsURL(lower) {
		return ComplexityStrong
	}

	// Long prompts (>500 chars) tend to be complex.
	if len(prompt) > 500 {
		return ComplexityStrong
	}

	// Check for strong keywords.
	for _, kw := range strongKeywords {
		if strings.Contains(lower, kw) {
			return ComplexityStrong
		}
	}

	return ComplexityStandard
}

// containsURL checks if text contains an HTTP(S) URL.
func containsURL(text string) bool {
	for _, word := range strings.Fields(text) {
		if strings.HasPrefix(word, "http://") || strings.HasPrefix(word, "https://") {
			if _, err := url.Parse(word); err == nil {
				return true
			}
		}
	}
	return false
}
