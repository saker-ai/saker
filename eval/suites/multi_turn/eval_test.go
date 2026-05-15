package multi_turn

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/saker-ai/saker/eval"
	"github.com/saker-ai/saker/pkg/api"
	"github.com/saker-ai/saker/pkg/model"
	"github.com/saker-ai/saker/pkg/testutil"
)

// echoContextModel echoes back all user messages so we can verify
// that the runtime's history store correctly accumulates messages across turns.
type echoContextModel struct {
	mu sync.Mutex
}

func (m *echoContextModel) Complete(_ context.Context, req model.Request) (*model.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var parts []string
	for _, msg := range req.Messages {
		if msg.Role == "user" {
			parts = append(parts, msg.Content)
		}
	}
	content := "Context: " + strings.Join(parts, " | ")

	return &model.Response{
		Message:    model.Message{Role: "assistant", Content: content},
		StopReason: "end_turn",
	}, nil
}

func (m *echoContextModel) CompleteStream(ctx context.Context, req model.Request, cb model.StreamHandler) error {
	resp, err := m.Complete(ctx, req)
	if err != nil {
		return err
	}
	if cb != nil {
		return cb(model.StreamResult{Final: true, Response: resp})
	}
	return nil
}

// TestEval_ContextRetentionThroughRuntime verifies that multi-turn context
// is correctly accumulated through the real api.Runtime agent loop, not just
// raw model.Request message passing.
func TestEval_ContextRetentionThroughRuntime(t *testing.T) {
	t.Parallel()
	suite := &eval.EvalSuite{Name: "context_retention_runtime"}
	cases := ContextRetentionCases()

	for _, tc := range cases {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()

			root := testutil.TempHome(t)
			mdl := &echoContextModel{}
			rt, err := api.New(context.Background(), api.Options{
				ProjectRoot:         root,
				Model:               mdl,
				EnabledBuiltinTools: []string{},
			})
			if err != nil {
				t.Fatalf("create runtime: %v", err)
			}
			defer rt.Close()

			allPass := true
			for i, turn := range tc.Turns {
				resp, err := rt.Run(context.Background(), api.Request{
					Prompt:    turn.Prompt,
					SessionID: tc.SessionID,
				})
				if err != nil {
					t.Fatalf("turn %d: %v", i, err)
				}
				if resp == nil || resp.Result == nil {
					t.Fatalf("turn %d: nil response", i)
				}
				if !strings.Contains(resp.Result.Output, turn.ExpectedOutput) {
					t.Errorf("turn %d: expected %q in response, got %q",
						i, turn.ExpectedOutput, truncate(resp.Result.Output, 200))
					allPass = false
				}
			}

			score := 1.0
			if !allPass {
				score = 0.0
			}
			suite.Add(eval.EvalResult{
				Name:  tc.Name,
				Pass:  allPass,
				Score: score,
				Details: map[string]any{
					"turns": len(tc.Turns),
				},
			})
		})
	}

	t.Cleanup(func() { t.Logf("\n%s", suite.Summary()) })
}

// TestEval_SessionIsolationThroughRuntime verifies that different session IDs
// have completely isolated message histories when going through the real runtime.
func TestEval_SessionIsolationThroughRuntime(t *testing.T) {
	t.Parallel()
	suite := &eval.EvalSuite{Name: "session_isolation_runtime"}

	for _, tc := range SessionIsolationCases() {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()

			root := testutil.TempHome(t)
			mdl := &echoContextModel{}
			rt, err := api.New(context.Background(), api.Options{
				ProjectRoot:         root,
				Model:               mdl,
				EnabledBuiltinTools: []string{},
			})
			if err != nil {
				t.Fatalf("create runtime: %v", err)
			}
			defer rt.Close()

			// Session 1
			resp1, err := rt.Run(context.Background(), api.Request{
				Prompt:    tc.Session1.Prompt,
				SessionID: tc.Session1.ID,
			})
			if err != nil {
				t.Fatalf("session1: %v", err)
			}

			// Session 2 — must NOT contain session 1 context.
			resp2, err := rt.Run(context.Background(), api.Request{
				Prompt:    tc.Session2.Prompt,
				SessionID: tc.Session2.ID,
			})
			if err != nil {
				t.Fatalf("session2: %v", err)
			}

			s1ok := resp1.Result != nil && strings.Contains(resp1.Result.Output, tc.Session1.Output)
			s2ok := resp2.Result != nil &&
				strings.Contains(resp2.Result.Output, tc.Session2.Output) &&
				!strings.Contains(resp2.Result.Output, tc.Session1.Output)

			pass := s1ok && s2ok
			score := 0.0
			if s1ok {
				score += 0.5
			}
			if s2ok {
				score += 0.5
			}

			suite.Add(eval.EvalResult{
				Name:  tc.Name,
				Pass:  pass,
				Score: score,
				Details: map[string]any{
					"session1_ok": s1ok,
					"session2_ok": s2ok,
				},
			})

			if !pass {
				t.Errorf("session isolation failed: s1_ok=%v, s2_ok=%v", s1ok, s2ok)
			}
		})
	}

	t.Cleanup(func() { t.Logf("\n%s", suite.Summary()) })
}

// TestEval_MessageOrderThroughRuntime verifies message ordering through the
// real runtime pipeline.
func TestEval_MessageOrderThroughRuntime(t *testing.T) {
	t.Parallel()
	suite := &eval.EvalSuite{Name: "message_order_runtime"}

	for _, tc := range MessageOrderCases() {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()

			root := testutil.TempHome(t)
			mdl := &echoContextModel{}
			rt, err := api.New(context.Background(), api.Options{
				ProjectRoot:         root,
				Model:               mdl,
				EnabledBuiltinTools: []string{},
			})
			if err != nil {
				t.Fatalf("create runtime: %v", err)
			}
			defer rt.Close()

			allPass := true
			for i, turn := range tc.Turns {
				resp, err := rt.Run(context.Background(), api.Request{
					Prompt:    turn.Prompt,
					SessionID: tc.SessionID,
				})
				if err != nil {
					t.Fatalf("turn %d: %v", i, err)
				}
				if resp == nil || resp.Result == nil {
					t.Fatalf("turn %d: nil response", i)
				}
				if !strings.Contains(resp.Result.Output, turn.ExpectedOutput) {
					t.Errorf("turn %d: expected %q in response", i, turn.ExpectedOutput)
					allPass = false
				}
			}

			score := 1.0
			if !allPass {
				score = 0.0
			}
			suite.Add(eval.EvalResult{
				Name:  tc.Name,
				Pass:  allPass,
				Score: score,
			})
		})
	}

	t.Cleanup(func() { t.Logf("\n%s", suite.Summary()) })
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
