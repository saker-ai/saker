package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/saker-ai/saker/pkg/api"
	"github.com/gin-gonic/gin"
)

func TestHandleModels(t *testing.T) {
	g := newTestGateway(t, Options{}, nil)

	router := gin.New()
	router.GET("/v1/models", g.handleModels)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var resp ModelListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Object != "list" {
		t.Errorf("object = %q, want list", resp.Object)
	}

	wantIDs := map[string]bool{
		"saker-default":                 false,
		"saker-" + string(api.ModelTierLow):  false,
		"saker-" + string(api.ModelTierMid):  false,
		"saker-" + string(api.ModelTierHigh): false,
	}
	for _, m := range resp.Data {
		if _, ok := wantIDs[m.ID]; !ok {
			t.Errorf("unexpected model id %q", m.ID)
			continue
		}
		wantIDs[m.ID] = true
		if m.Object != "model" {
			t.Errorf("%s: object = %q, want model", m.ID, m.Object)
		}
		if m.OwnedBy != "saker" {
			t.Errorf("%s: owned_by = %q, want saker", m.ID, m.OwnedBy)
		}
		if m.Created == 0 {
			t.Errorf("%s: created not set", m.ID)
		}
	}
	for id, seen := range wantIDs {
		if !seen {
			t.Errorf("missing expected model id %q", id)
		}
	}
}
