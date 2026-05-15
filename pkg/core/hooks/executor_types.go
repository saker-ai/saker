package hooks

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/saker-ai/saker/pkg/core/events"
)

// executor_types.go declares the public types and configuration knobs for the
// hook executor: Decision, HookOutput, Result, Selector, ShellHook, the
// Executor struct, and the WithXxx option helpers. Runtime behaviour
// (matching, dispatch, payload assembly) lives in sibling files.

// Default timeouts per Claude Code spec.
const (
	defaultCommandTimeout = 600 * time.Second
	defaultPromptTimeout  = 30 * time.Second
	defaultAgentTimeout   = 60 * time.Second
	defaultHookTimeout    = defaultCommandTimeout
)

// Decision captures the permission outcome encoded in the hook exit code.
// Claude Code spec: 0=success(parse JSON), 2=blocking error(stderr),
// other=non-blocking(log stderr & continue).
type Decision int

const (
	DecisionAllow         Decision = iota // exit 0: success, parse JSON stdout
	DecisionBlockingError                 // exit 2: blocking error, stderr is message
	DecisionNonBlocking                   // exit 1,3+: non-blocking, log & continue
)

func (d Decision) String() string {
	switch d {
	case DecisionAllow:
		return "allow"
	case DecisionBlockingError:
		return "blocking_error"
	default:
		return "non_blocking"
	}
}

// HookOutput is the structured JSON output from hooks on exit 0.
type HookOutput struct {
	Continue      *bool  `json:"continue,omitempty"`
	StopReason    string `json:"stopReason,omitempty"`
	Decision      string `json:"decision,omitempty"`
	Reason        string `json:"reason,omitempty"`
	SystemMessage string `json:"systemMessage,omitempty"`

	HookSpecificOutput *HookSpecificOutput `json:"hookSpecificOutput,omitempty"`
}

// HookSpecificOutput carries event-specific fields from hook JSON output.
type HookSpecificOutput struct {
	HookEventName string `json:"hookEventName,omitempty"`

	// PreToolUse specific
	PermissionDecision       string         `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string         `json:"permissionDecisionReason,omitempty"`
	UpdatedInput             map[string]any `json:"updatedInput,omitempty"`
	AdditionalContext        string         `json:"additionalContext,omitempty"`
}

// Result captures the full outcome of executing a shell hook.
type Result struct {
	Event    events.Event
	Decision Decision
	ExitCode int
	Output   *HookOutput // parsed JSON stdout on exit 0
	Stdout   string
	Stderr   string
}

// Selector filters hooks by matcher target and/or payload pattern.
type Selector struct {
	ToolName *regexp.Regexp
	Pattern  *regexp.Regexp
}

// NewSelector compiles optional regex patterns. Empty strings are treated as wildcards.
func NewSelector(toolPattern, payloadPattern string) (Selector, error) {
	sel := Selector{}
	if strings.TrimSpace(toolPattern) != "" {
		re, err := regexp.Compile(toolPattern)
		if err != nil {
			return sel, fmt.Errorf("hooks: compile tool matcher: %w", err)
		}
		sel.ToolName = re
	}
	if strings.TrimSpace(payloadPattern) != "" {
		re, err := regexp.Compile(payloadPattern)
		if err != nil {
			return sel, fmt.Errorf("hooks: compile payload matcher: %w", err)
		}
		sel.Pattern = re
	}
	return sel, nil
}

// Match returns true when the event satisfies all configured selectors.
func (s Selector) Match(evt events.Event) bool {
	if s.ToolName != nil {
		target := extractMatcherTarget(evt.Type, evt.Payload)
		if target == "" || !s.ToolName.MatchString(target) {
			return false
		}
	}
	if s.Pattern != nil {
		payload, err := json.Marshal(evt.Payload)
		if err != nil {
			return false
		}
		if !s.Pattern.Match(payload) {
			return false
		}
	}
	return true
}

// ShellHook describes a single shell command bound to an event type.
type ShellHook struct {
	Event         events.EventType
	Command       string
	Selector      Selector
	Timeout       time.Duration
	Env           map[string]string
	Name          string // optional label for debugging
	Async         bool   // fire-and-forget execution
	Once          bool   // execute only once per session
	StatusMessage string // status message shown during execution
}
