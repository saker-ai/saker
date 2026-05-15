package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/saker-ai/saker/pkg/api"
	"github.com/saker-ai/saker/pkg/config"
	coreevents "github.com/saker-ai/saker/pkg/core/events"
	"github.com/saker-ai/saker/pkg/model"
)

// noopModel satisfies model.Model for handler tests that never call the model.
type noopModel struct{}

func (noopModel) Complete(context.Context, model.Request) (*model.Response, error) {
	return &model.Response{}, nil
}
func (noopModel) CompleteStream(context.Context, model.Request, model.StreamHandler) error {
	return nil
}

// newTestHandler creates a Handler backed by a real Runtime with a temp project root.
func newTestHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	root := t.TempDir()
	sakerDir := filepath.Join(root, ".saker")
	if err := os.MkdirAll(sakerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sakerDir, "settings.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	rt, err := api.New(context.Background(), api.Options{
		ProjectRoot:  root,
		Model:        noopModel{},
		SystemPrompt: "test",
	})
	if err != nil {
		t.Fatalf("create test runtime: %v", err)
	}
	t.Cleanup(func() { rt.Close() })

	h := &Handler{
		runtime:     rt,
		logger:      slog.Default(),
		clients:     map[string]*wsClient{},
		subscribers: map[string]map[string]*wsClient{},
		approvals:   map[string]chan coreevents.PermissionDecisionType{},
		questions:   map[string]chan map[string]string{},
		cancels:     map[string]context.CancelFunc{},
		turnThreads: map[string]string{},
	}
	return h, root
}

func adminCtx(username string) context.Context {
	ctx := context.Background()
	ctx = context.WithValue(ctx, userContextKey, username)
	ctx = context.WithValue(ctx, roleContextKey, "admin")
	return ctx
}

func userCtx(username string) context.Context {
	ctx := context.Background()
	ctx = context.WithValue(ctx, userContextKey, username)
	ctx = context.WithValue(ctx, roleContextKey, "user")
	return ctx
}

// resultJSON re-marshals the response result to JSON and unmarshals into target.
func resultJSON(t *testing.T, resp Response, target any) {
	t.Helper()
	data, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
}

func TestHandlePersonaList_ScrubsFilePaths(t *testing.T) {
	t.Parallel()
	h, root := newTestHandler(t)

	settings := &config.Settings{
		Personas: &config.PersonasConfig{
			Default: "bot1",
			Profiles: map[string]config.PersonaProfile{
				"bot1": {
					Name:         "Bot One",
					Soul:         "I am a bot",
					SoulFile:     "/secret/path/soul.md",
					InstructFile: "/secret/path/instruct.md",
				},
			},
		},
	}
	writeSettingsJSON(t, root, settings)
	reloadSettings(t, h)

	resp := h.handlePersonaList(Request{ID: 1, Method: "persona/list"})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	var result struct {
		Default  string                           `json:"default"`
		Profiles map[string]config.PersonaProfile `json:"profiles"`
	}
	resultJSON(t, resp, &result)

	bot := result.Profiles["bot1"]
	if bot.SoulFile != "" {
		t.Errorf("SoulFile should be scrubbed, got %q", bot.SoulFile)
	}
	if bot.InstructFile != "" {
		t.Errorf("InstructFile should be scrubbed, got %q", bot.InstructFile)
	}
	if bot.Name != "Bot One" {
		t.Errorf("Name should be preserved, got %q", bot.Name)
	}
	if result.Default != "bot1" {
		t.Errorf("Default should be bot1, got %q", result.Default)
	}
}

func TestHandlePersonaList_NilPersonas(t *testing.T) {
	t.Parallel()
	h, _ := newTestHandler(t)

	resp := h.handlePersonaList(Request{ID: 1})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	var result map[string]any
	resultJSON(t, resp, &result)
	if result["default"] != "" {
		t.Errorf("expected empty default, got %v", result["default"])
	}
}

func TestHandlePersonaSave_RequiresAdmin(t *testing.T) {
	t.Parallel()
	h, _ := newTestHandler(t)

	ctx := userCtx("alice")
	resp := h.handlePersonaSave(ctx, Request{
		ID:     1,
		Params: map[string]any{"id": "test", "profile": map[string]any{"name": "Test"}},
	})
	if resp.Error == nil {
		t.Fatal("expected admin required error")
	}
	if resp.Error.Message != "admin access required" {
		t.Errorf("unexpected error: %s", resp.Error.Message)
	}
}

func TestHandlePersonaSave_MissingID(t *testing.T) {
	t.Parallel()
	h, _ := newTestHandler(t)
	ctx := adminCtx("admin")

	resp := h.handlePersonaSave(ctx, Request{
		ID:     1,
		Params: map[string]any{"profile": map[string]any{"name": "Test"}},
	})
	if resp.Error == nil || resp.Error.Message != "id is required" {
		t.Errorf("expected 'id is required', got %v", resp.Error)
	}
}

func TestHandlePersonaSave_Success(t *testing.T) {
	t.Parallel()
	h, root := newTestHandler(t)
	ctx := adminCtx("admin")

	resp := h.handlePersonaSave(ctx, Request{
		ID: 1,
		Params: map[string]any{
			"id":      "aria",
			"profile": map[string]any{"name": "Aria", "soul": "Helpful assistant"},
		},
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	// Verify persisted in settings.local.json.
	saved, err := config.LoadSettingsLocal(root)
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if saved == nil || saved.Personas == nil {
		t.Fatal("saved settings has no personas")
	}
	if saved.Personas.Profiles["aria"].Name != "Aria" {
		t.Errorf("expected name Aria, got %q", saved.Personas.Profiles["aria"].Name)
	}
}

func TestHandlePersonaDelete_ClearsDefault(t *testing.T) {
	t.Parallel()
	h, root := newTestHandler(t)
	ctx := adminCtx("admin")

	settings := &config.Settings{
		Personas: &config.PersonasConfig{
			Default:  "bot1",
			Profiles: map[string]config.PersonaProfile{"bot1": {Name: "Bot"}},
		},
	}
	writeSettingsLocal(t, root, settings)
	reloadSettings(t, h)

	resp := h.handlePersonaDelete(ctx, Request{
		ID:     1,
		Params: map[string]any{"id": "bot1"},
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	saved, err := config.LoadSettingsLocal(root)
	if err != nil || saved == nil {
		t.Fatal("load settings failed")
	}
	if saved.Personas.Default != "" {
		t.Errorf("default should be cleared, got %q", saved.Personas.Default)
	}
	if _, ok := saved.Personas.Profiles["bot1"]; ok {
		t.Error("persona should be deleted")
	}
}

func TestHandleUserPersonaSave_NotAuthenticated(t *testing.T) {
	t.Parallel()
	h, _ := newTestHandler(t)
	ctx := context.Background()

	resp := h.handleUserPersonaSave(ctx, Request{
		ID:     1,
		Params: map[string]any{"id": "test", "profile": map[string]any{"name": "T"}},
	})
	if resp.Error == nil || resp.Error.Message != "not authenticated" {
		t.Errorf("expected 'not authenticated', got %v", resp.Error)
	}
}

func TestHandleUserPersonaSave_Success(t *testing.T) {
	t.Parallel()
	h, root := newTestHandler(t)
	ctx := adminCtx("alice")

	resp := h.handleUserPersonaSave(ctx, Request{
		ID:     1,
		Params: map[string]any{"id": "mybot", "profile": map[string]any{"name": "MyBot", "soul": "test"}},
	})
	if resp.Error != nil {
		t.Fatalf("save error: %s", resp.Error.Message)
	}

	// Verify persisted.
	saved, err := config.LoadSettingsLocal(root)
	if err != nil || saved == nil {
		t.Fatal("load settings failed")
	}
	uc := saved.UserPersonas["alice"]
	if uc == nil || uc.Profiles["mybot"].Name != "MyBot" {
		t.Error("user persona not persisted")
	}
}

func TestHandleUserPersonaDelete_ClearsActive(t *testing.T) {
	t.Parallel()
	h, root := newTestHandler(t)
	ctx := adminCtx("alice")

	// Pre-create user persona with active set.
	settings := &config.Settings{
		UserPersonas: map[string]*config.UserPersonasConfig{
			"alice": {
				Active:   "mybot",
				Profiles: map[string]config.PersonaProfile{"mybot": {Name: "MyBot"}},
			},
		},
	}
	writeSettingsLocal(t, root, settings)
	reloadSettings(t, h)

	resp := h.handleUserPersonaDelete(ctx, Request{
		ID:     1,
		Params: map[string]any{"id": "mybot"},
	})
	if resp.Error != nil {
		t.Fatalf("delete error: %s", resp.Error.Message)
	}

	saved, err := config.LoadSettingsLocal(root)
	if err != nil || saved == nil {
		t.Fatal("load settings failed")
	}
	uc := saved.UserPersonas["alice"]
	if uc == nil {
		t.Fatal("user config missing")
	}
	if uc.Active != "" {
		t.Errorf("active should be cleared, got %q", uc.Active)
	}
	if _, ok := uc.Profiles["mybot"]; ok {
		t.Error("persona should be deleted")
	}
}

func TestHandleUserPersonaSetActive_ValidatesExistence(t *testing.T) {
	t.Parallel()
	h, _ := newTestHandler(t)
	ctx := adminCtx("alice")

	resp := h.handleUserPersonaSetActive(ctx, Request{
		ID:     1,
		Params: map[string]any{"id": "nonexistent"},
	})
	if resp.Error == nil {
		t.Fatal("expected error for non-existent persona")
	}
	if resp.Error.Message != "persona not found: nonexistent" {
		t.Errorf("unexpected error: %s", resp.Error.Message)
	}
}

func TestHandleUserPersonaSetActive_DeactivateAlwaysAllowed(t *testing.T) {
	t.Parallel()
	h, _ := newTestHandler(t)
	ctx := adminCtx("alice")

	resp := h.handleUserPersonaSetActive(ctx, Request{
		ID:     1,
		Params: map[string]any{"id": ""},
	})
	if resp.Error != nil {
		t.Fatalf("deactivate should succeed, got: %s", resp.Error.Message)
	}
}

func TestHandleUserPersonaSetActive_AcceptsGlobalPersona(t *testing.T) {
	t.Parallel()
	h, root := newTestHandler(t)
	ctx := adminCtx("alice")

	// Create a global persona via settings.json (merged by SettingsLoader).
	settings := &config.Settings{
		Personas: &config.PersonasConfig{
			Profiles: map[string]config.PersonaProfile{"aria": {Name: "Aria"}},
		},
	}
	writeSettingsJSON(t, root, settings)
	reloadSettings(t, h)

	resp := h.handleUserPersonaSetActive(ctx, Request{
		ID:     1,
		Params: map[string]any{"id": "aria"},
	})
	if resp.Error != nil {
		t.Fatalf("should accept global persona, got: %s", resp.Error.Message)
	}
}

func TestHandleProfileList_RequiresAdmin(t *testing.T) {
	t.Parallel()
	h, _ := newTestHandler(t)
	ctx := userCtx("alice")

	resp := h.handleProfileList(ctx, Request{ID: 1})
	if resp.Error == nil {
		t.Fatal("expected admin required error")
	}
}

func TestHandleUserPersonaList_ReturnsStructure(t *testing.T) {
	t.Parallel()
	h, root := newTestHandler(t)
	ctx := adminCtx("alice")

	// Write globals to settings.json and user data to settings.local.json.
	writeSettingsJSON(t, root, &config.Settings{
		Personas: &config.PersonasConfig{
			Default:  "aria",
			Profiles: map[string]config.PersonaProfile{"aria": {Name: "Aria"}},
		},
	})
	writeSettingsLocal(t, root, &config.Settings{
		UserPersonas: map[string]*config.UserPersonasConfig{
			"alice": {
				Active:   "aria",
				Profiles: map[string]config.PersonaProfile{"mybot": {Name: "MyBot"}},
			},
		},
	})
	reloadSettings(t, h)

	resp := h.handleUserPersonaList(ctx, Request{ID: 1})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	var result map[string]any
	resultJSON(t, resp, &result)
	if result["active"] != "aria" {
		t.Errorf("expected active=aria, got %v", result["active"])
	}
	if result["globalDefault"] != "aria" {
		t.Errorf("expected globalDefault=aria, got %v", result["globalDefault"])
	}
}

// --- helpers ---

func writeSettingsJSON(t *testing.T, root string, s *config.Settings) {
	t.Helper()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".saker", "settings.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeSettingsLocal(t *testing.T, root string, s *config.Settings) {
	t.Helper()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".saker", "settings.local.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func reloadSettings(t *testing.T, h *Handler) {
	t.Helper()
	if err := h.runtime.ReloadSettings(); err != nil {
		t.Fatalf("reload settings: %v", err)
	}
}
