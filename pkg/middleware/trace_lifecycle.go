package middleware

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// trace_lifecycle.go owns the TraceMiddleware/traceSession types and
// everything needed to keep their on-disk artifacts (JSONL + HTML) up to
// date. Per-stage hooks and the record() pipeline live in trace_hooks.go;
// skill-snapshot tracing lives in trace_skills.go.

// TraceMiddleware records middleware activity per session and renders a
// lightweight HTML viewer alongside JSONL logs.
type TraceMiddleware struct {
	outputDir   string
	sessions    map[string]*traceSession
	tmpl        *template.Template
	mu          sync.Mutex
	clock       func() time.Time
	traceSkills bool
}

type traceSession struct {
	id        string
	createdAt time.Time
	updatedAt time.Time
	timestamp string
	jsonPath  string
	htmlPath  string
	jsonFile  *os.File
	events    []TraceEvent
	mu        sync.Mutex
}

// TraceContextKey identifies values stored in a context for trace middleware consumers.
type TraceContextKey string

const (
	// TraceSessionIDContextKey stores the trace-specific session identifier.
	TraceSessionIDContextKey TraceContextKey = "trace.session_id"
	// SessionIDContextKey stores the generic session identifier fallback.
	SessionIDContextKey TraceContextKey = "session_id"

	traceSkillBeforeKey = "trace.skills.before"
	traceSkillNamesKey  = "trace.skills.names"
	skillsRegistryValue = "skills.registry"
	forceSkillsValue    = "request.force_skills"
)

// TraceOption customizes optional TraceMiddleware behavior.
type TraceOption func(*TraceMiddleware)

// WithSkillTracing enables ForceSkills body-size logging.
func WithSkillTracing(enabled bool) TraceOption {
	return func(tm *TraceMiddleware) {
		tm.traceSkills = enabled
	}
}

// NewTraceMiddleware builds a TraceMiddleware that writes to outputDir
// (defaults to .trace when empty).
func NewTraceMiddleware(outputDir string, opts ...TraceOption) *TraceMiddleware {
	dir := strings.TrimSpace(outputDir)
	if dir == "" {
		dir = ".trace"
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		slog.Error("trace middleware: mkdir failed", "dir", dir, "error", err)
	}

	tmpl, err := template.New("trace-viewer").Parse(traceHTMLTemplate)
	if err != nil {
		slog.Error("trace middleware: template parse failed", "error", err)
	}

	mw := &TraceMiddleware{
		outputDir: dir,
		sessions:  map[string]*traceSession{},
		tmpl:      tmpl,
		clock:     time.Now,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(mw)
		}
	}
	return mw
}

func (m *TraceMiddleware) Name() string { return "trace" }

// Close releases all open file handles held by trace sessions.
func (m *TraceMiddleware) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, sess := range m.sessions {
		sess.mu.Lock()
		if sess.jsonFile != nil {
			sess.jsonFile.Close()
			sess.jsonFile = nil
		}
		sess.mu.Unlock()
	}
}

func (m *TraceMiddleware) sessionFor(id string) *traceSession {
	if id == "" {
		id = "session"
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if sess, ok := m.sessions[id]; ok {
		return sess
	}

	sess, err := m.newSessionLocked(id)
	if err != nil {
		m.logf("create session %s: %v", id, err)
		return nil
	}
	m.sessions[id] = sess
	return sess
}

func (m *TraceMiddleware) newSessionLocked(id string) (*traceSession, error) {
	if err := os.MkdirAll(m.outputDir, 0o750); err != nil {
		return nil, err
	}
	timestamp := m.now().UTC().Format(time.RFC3339)
	safeID := sanitizeSessionComponent(id)
	base := fmt.Sprintf("log-%s", safeID)
	jsonPath := filepath.Join(m.outputDir, base+".jsonl")
	htmlPath := filepath.Join(m.outputDir, base+".html")
	file, err := os.OpenFile(jsonPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	now := m.now()
	return &traceSession{
		id:        id,
		timestamp: timestamp,
		jsonPath:  jsonPath,
		htmlPath:  htmlPath,
		jsonFile:  file,
		createdAt: now,
		updatedAt: now,
		events:    []TraceEvent{},
	}, nil
}

func sanitizeSessionComponent(id string) string {
	const fallback = "session"
	if strings.TrimSpace(id) == "" {
		return fallback
	}
	var b strings.Builder
	b.Grow(len(id))
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	sanitized := strings.Trim(b.String(), "-")
	if sanitized == "" {
		return fallback
	}
	return sanitized
}

func (sess *traceSession) append(evt TraceEvent, owner *TraceMiddleware) {
	if sess == nil || owner == nil {
		return
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()

	sess.events = append(sess.events, evt)
	if sess.jsonFile != nil {
		if err := writeJSONLine(sess.jsonFile, evt); err != nil {
			owner.logf("write jsonl %s: %v", sess.jsonPath, err)
		}
	} else {
		owner.logf("json file handle missing for %s", sess.id)
	}

	sess.updatedAt = owner.now()
	if err := owner.renderHTML(sess); err != nil {
		owner.logf("render html %s: %v", sess.htmlPath, err)
	}
}

func writeJSONLine(f *os.File, evt TraceEvent) error {
	if f == nil {
		return nil
	}
	line, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return nil
}

func (m *TraceMiddleware) renderHTML(sess *traceSession) error {
	if sess == nil {
		return nil
	}
	data := traceTemplateData{
		SessionID:  sess.id,
		CreatedAt:  sess.createdAt.UTC().Format(time.RFC3339),
		UpdatedAt:  sess.updatedAt.UTC().Format(time.RFC3339),
		EventCount: len(sess.events),
		JSONLog:    filepath.Base(sess.jsonPath),
	}
	tokens, duration := aggregateStats(sess.events)
	data.TotalTokens = tokens
	data.TotalDuration = duration
	raw, err := json.Marshal(sess.events)
	if err != nil {
		sanitized := make([]TraceEvent, 0, len(sess.events))
		for _, evt := range sess.events {
			sanitized = append(sanitized, TraceEvent{
				Timestamp: evt.Timestamp,
				Stage:     evt.Stage,
				Iteration: evt.Iteration,
				SessionID: evt.SessionID,
			})
		}
		raw, err = json.Marshal(sanitized)
		if err != nil {
			raw = []byte("[]")
		}
	}
	// EventsJSON is generated by json.Marshal from our TraceEvent structs (or the sanitized fallback above),
	// so it never contains user input that could introduce executable content.
	// #nosec G203 -- Treating this trusted, server-generated JSON as template.JS is safe for the trace viewer.
	// Escape </script sequences to prevent breaking out of the script context.
	safeJSON := strings.ReplaceAll(string(raw), "</script", "<\\/script")
	data.EventsJSON = template.JS(safeJSON)

	var buf bytes.Buffer
	if m.tmpl != nil {
		if err := m.tmpl.Execute(&buf, data); err != nil {
			return err
		}
	} else {
		buf.WriteString("<html><body><pre>")
		template.HTMLEscape(&buf, raw)
		buf.WriteString("</pre></body></html>")
	}

	if err := writeAtomic(sess.htmlPath, buf.Bytes()); err != nil {
		return err
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "trace-*.html")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (m *TraceMiddleware) now() time.Time {
	if m == nil || m.clock == nil {
		return time.Now()
	}
	return m.clock()
}

func (m *TraceMiddleware) logf(format string, args ...any) {
	slog.Error(fmt.Sprintf("trace middleware: "+format, args...))
}
