package openai

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/cinience/saker/pkg/api"
	"github.com/gin-gonic/gin"
)

// stubRunner satisfies Runner without doing any real work; only used to
// give RegisterOpenAIGateway a non-nil Runtime.
type stubRunner struct{}

func (stubRunner) RunStream(context.Context, api.Request) (<-chan api.StreamEvent, error) {
	ch := make(chan api.StreamEvent)
	close(ch)
	return ch, nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRegisterOpenAIGateway_DisabledNoOp(t *testing.T) {
	t.Parallel()
	gw, err := RegisterOpenAIGateway(gin.New(), Deps{
		Runtime: stubRunner{},
		Logger:  discardLogger(),
		Options: Options{Enabled: false},
	})
	if err != nil {
		t.Fatalf("disabled register err: %v", err)
	}
	if gw != nil {
		t.Errorf("disabled register should return nil gateway, got %p", gw)
	}
}

func TestRegisterOpenAIGateway_RejectsNilEngine(t *testing.T) {
	t.Parallel()
	_, err := RegisterOpenAIGateway(nil, Deps{
		Runtime: stubRunner{},
		Logger:  discardLogger(),
		Options: Options{Enabled: true},
	})
	if err == nil || !strings.Contains(err.Error(), "gin engine") {
		t.Errorf("expected nil-engine error, got %v", err)
	}
}

func TestRegisterOpenAIGateway_RejectsNilRuntime(t *testing.T) {
	t.Parallel()
	_, err := RegisterOpenAIGateway(gin.New(), Deps{
		Runtime: nil,
		Logger:  discardLogger(),
		Options: Options{Enabled: true},
	})
	if err == nil || !strings.Contains(err.Error(), "Runtime") {
		t.Errorf("expected runtime-required error, got %v", err)
	}
}

func TestRegisterOpenAIGateway_PropagatesValidateError(t *testing.T) {
	t.Parallel()
	_, err := RegisterOpenAIGateway(gin.New(), Deps{
		Runtime: stubRunner{},
		Logger:  discardLogger(),
		Options: Options{
			Enabled:         true,
			RPSPerTenant:    -1, // invalid
			ErrorDetailMode: "dev",
		},
	})
	if err == nil {
		t.Fatal("expected Validate error to bubble up")
	}
}

func TestRegisterOpenAIGateway_HappyPath(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	eng := gin.New()
	gw, err := RegisterOpenAIGateway(eng, Deps{
		Runtime: stubRunner{},
		Logger:  discardLogger(),
		Options: Options{
			Enabled:             true,
			MaxRuns:             10,
			MaxRunsPerTenant:    2,
			RingSize:            64,
			RPSPerTenant:        5,
			MaxRequestBodyBytes: 1024,
			ErrorDetailMode:     "dev",
		},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if gw == nil {
		t.Fatal("expected non-nil gateway")
	}
	t.Cleanup(gw.Shutdown)

	// Accessors should now be populated.
	if gw.Runtime() == nil {
		t.Error("Runtime() should be non-nil after register")
	}
	if gw.Hub() == nil {
		t.Error("Hub() should be non-nil after register")
	}
	if gw.Logger() == nil {
		t.Error("Logger() should be non-nil after register (defaults to slog.Default if not set)")
	}
	if gw.ProjectStore() != nil {
		t.Error("ProjectStore() should be nil when not provided")
	}
	if gw.Options().MaxRuns != 10 {
		t.Errorf("Options().MaxRuns = %d, want 10", gw.Options().MaxRuns)
	}

	// Verify routes mounted: GET /v1/models and POST /v1/chat/completions.
	routes := eng.Routes()
	want := map[string]bool{
		"GET /v1/models":            false,
		"POST /v1/chat/completions": false,
	}
	for _, r := range routes {
		key := r.Method + " " + r.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for k, ok := range want {
		if !ok {
			t.Errorf("expected route %s to be mounted", k)
		}
	}
}

func TestRegisterOpenAIGateway_DefaultsLogger(t *testing.T) {
	t.Parallel()
	gw, err := RegisterOpenAIGateway(gin.New(), Deps{
		Runtime: stubRunner{},
		Logger:  nil, // intentionally nil — handler should default to slog.Default()
		Options: Options{
			Enabled:         true,
			ErrorDetailMode: "dev",
		},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Cleanup(gw.Shutdown)
	if gw.Logger() == nil {
		t.Error("Logger() should be non-nil even when caller passed nil")
	}
}

func TestGatewayShutdown_NilSafe(t *testing.T) {
	t.Parallel()
	// Should not panic on nil receiver.
	var gw *Gateway
	gw.Shutdown()
}

func TestGatewayShutdown_Idempotent(t *testing.T) {
	t.Parallel()
	gw, err := RegisterOpenAIGateway(gin.New(), Deps{
		Runtime: stubRunner{},
		Logger:  discardLogger(),
		Options: Options{
			Enabled:         true,
			ErrorDetailMode: "dev",
		},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	gw.Shutdown()
	// Second call must not panic.
	gw.Shutdown()
}

// Sanity: stubRunner satisfies the Runner interface at compile time.
var _ Runner = stubRunner{}
