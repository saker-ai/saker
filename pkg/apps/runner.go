package apps

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/saker-ai/saker/pkg/canvas"
	"github.com/google/uuid"
)

// Runner orchestrates a single app run: load the published snapshot,
// validate inputs, clone the document, inject input values into appInput
// nodes, persist the cloned document as a temp thread, and dispatch via
// the canvas Executor. Returns the runId so the REST/RPC caller can poll
// the executor's RunTracker for progress.
//
// OnTempThread (when set) is invoked after the temp thread has been
// successfully written to disk. The server uses it to register a (threadID
// → dataDir) entry so the canvas/run-finished Notify hook can drain the
// temp file once the run completes. A nil callback is a no-op (keeps
// library callers — tests, CLI — free of plumbing).
type Runner struct {
	Store        *Store
	Executor     *canvas.Executor
	DataDir      string
	OnTempThread func(threadID, dataDir string)
}

// NewRunner constructs a Runner. DataDir must equal Executor.DataDir so
// the Save→Load handoff finds the temp thread.
func NewRunner(store *Store, exec *canvas.Executor, dataDir string) *Runner {
	return &Runner{Store: store, Executor: exec, DataDir: dataDir}
}

// Run launches the app's published canvas with the supplied inputs and
// returns the executor's runID. The caller polls runID via the tracker
// (canvas.Executor.Tracker.Get).
//
// Temp-thread cleanup runs out-of-band: see OnTempThread + the server's
// canvas/run-finished Notify hook in pkg/server/apps_temp_gc.go.
func (r *Runner) Run(ctx context.Context, appID string, inputs map[string]any) (string, error) {
	if r == nil {
		return "", fmt.Errorf("apps: nil runner")
	}
	if r.Store == nil || r.Executor == nil {
		return "", fmt.Errorf("apps: runner not fully initialised")
	}
	meta, err := r.Store.Get(ctx, appID)
	if err != nil {
		return "", err
	}
	if meta.PublishedVersion == "" {
		return "", fmt.Errorf("%w: %s", ErrNotPublished, appID)
	}

	version, err := r.Store.LoadVersion(ctx, appID, meta.PublishedVersion)
	if err != nil {
		return "", err
	}

	if err := ValidateInputs(version.Inputs, inputs); err != nil {
		return "", err
	}

	cloned, err := cloneDocument(version.Document)
	if err != nil {
		return "", err
	}

	// Index schema fields by variable for O(1) lookup during injection.
	fieldByVar := make(map[string]AppInputField, len(version.Inputs))
	for _, f := range version.Inputs {
		fieldByVar[f.Variable] = f
	}

	for _, n := range cloned.Nodes {
		if n == nil || n.NodeType() != "appInput" {
			continue
		}
		variable := n.DataString("appVariable")
		if variable == "" {
			continue
		}
		val, ok := inputs[variable]
		if !ok {
			// Fall back to schema-declared default so downstream nodes still
			// get a value when the caller omits an optional field.
			if f, has := fieldByVar[variable]; has && f.Default != nil {
				val = f.Default
				ok = true
			}
		}
		if !ok {
			continue
		}
		field := fieldByVar[variable]
		coerced, err := coerceForField(field, val)
		if err != nil {
			return "", err
		}
		if n.Data == nil {
			n.Data = map[string]any{}
		}
		n.Data["value"] = coerced
		// Mirror to "prompt" so prompt-driven gen nodes downstream that
		// reference this node via context edges pick the value up.
		n.Data["prompt"] = stringifyForPrompt(coerced)
		n.Data["status"] = canvas.NodeStatusDone
	}

	tempThreadID := "app-run-" + uuid.NewString()
	if err := canvas.Save(r.DataDir, tempThreadID, cloned); err != nil {
		return "", fmt.Errorf("apps: write temp thread: %w", err)
	}
	if r.OnTempThread != nil {
		r.OnTempThread(tempThreadID, r.DataDir)
	}

	runID, err := r.Executor.RunAsync(ctx, canvas.RunOptions{ThreadID: tempThreadID})
	if err != nil {
		return "", fmt.Errorf("apps: dispatch run for %s: %w", appID, err)
	}
	return runID, nil
}

// coerceForField normalises a raw input value to the on-wire shape the
// downstream canvas nodes expect. ValidateInputs has already screened for
// required/select/coercibility — this is the authoritative type cast.
//
//   - number → float64 (accepts JSON numbers and numeric strings; rejects
//     anything else so we don't silently send a string to a node that
//     reads strconv.ParseFloat-style float64).
//   - file → string (the upload flow returns a mediaUrl string; non-string
//     payloads are a contract violation, fail fast).
//   - text/paragraph/select → unchanged (ValidateInputs ensured select is
//     one of the listed options; text fields are passed through verbatim).
func coerceForField(field AppInputField, v any) (any, error) {
	switch field.Type {
	case "number":
		f, ok := toFloat64(v)
		if !ok {
			return nil, fmt.Errorf("apps: %s expects a number, got %T", field.Variable, v)
		}
		return f, nil
	case "file":
		s, ok := v.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return nil, fmt.Errorf("apps: %s expects a non-empty mediaUrl string, got %T", field.Variable, v)
		}
		return s, nil
	default:
		return v, nil
	}
}

// toFloat64 mirrors isCoercibleToFloat (extract.go) but returns the value.
func toFloat64(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

// stringifyForPrompt formats a coerced value for injection into the
// prompt-mirror slot. Numbers use FormatFloat 'g' so 42.0 → "42" (not
// "42.000000") and very small/large values don't gain spurious zeros.
func stringifyForPrompt(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// cloneDocument deep-copies a canvas.Document via JSON round-trip. App
// snapshots are small (a few nodes/edges per published canvas) so the
// allocation cost is irrelevant compared to a downstream gen call.
func cloneDocument(in *canvas.Document) (*canvas.Document, error) {
	if in == nil {
		return nil, fmt.Errorf("apps: cloneDocument: nil")
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("apps: clone marshal: %w", err)
	}
	out := &canvas.Document{}
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("apps: clone unmarshal: %w", err)
	}
	return out, nil
}
