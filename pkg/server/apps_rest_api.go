package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/apps"
	"github.com/google/uuid"
)

// appsAuthBearer validates the Authorization: Bearer ak_... header against
// the app's KeysFile. Returns true when auth succeeds (handler may proceed),
// false when it wrote a 401 and the caller must return immediately.
func (s *Server) appsAuthBearer(w http.ResponseWriter, r *http.Request, appID string) bool {
	authHdr := r.Header.Get("Authorization")
	if authHdr == "" {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return false
	}
	store := s.handler.appsStoreFor(r.Context())
	keys, err := store.LoadKeys(r.Context(), appID)
	if err != nil {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return false
	}
	ak, ok := apps.ValidateAPIKey(keys, authHdr)
	if !ok {
		http.Error(w, "invalid API key", http.StatusUnauthorized)
		return false
	}
	// Persist the updated LastUsedAt. Best-effort: a failure here does not
	// break the run.
	_ = store.SaveKeys(r.Context(), appID, keys)
	_ = ak // already mutated in-place by ValidateAPIKey
	return true
}

// ── API Key management ────────────────────────────────────────────────────────

// handleAppsKeysCollection handles GET and POST on /api/apps/{appId}/keys.
//
// @Summary List API keys
// @Description Returns every API key registered for an app (without the secret hash).
// @Tags apps-keys
// @Produce json
// @Param appId path string true "App ID"
// @Success 200 {array} apps.ApiKey "API keys (hash field is always empty)"
// @Failure 404 {string} string "app not found"
// @Router /api/apps/{appId}/keys [get]
//
// @Summary Create API key
// @Description Generates a new API key. The plaintext apiKey is returned ONCE in the response and never again — store it immediately.
// @Tags apps-keys
// @Accept json
// @Produce json
// @Param appId path string true "App ID"
// @Param body body object true "Key creation payload: {name, expiresInDays?}"
// @Success 201 {object} map[string]any "Created key: {id, name, prefix, apiKey, createdAt, expiresAt}"
// @Failure 400 {string} string "invalid body or missing name"
// @Failure 500 {string} string "key generation failed"
// @Router /api/apps/{appId}/keys [post]
func (s *Server) handleAppsKeysCollection(w http.ResponseWriter, r *http.Request, appID string) {
	switch r.Method {
	case http.MethodGet:
		store := s.handler.appsStoreFor(r.Context())
		keys, err := store.LoadKeys(r.Context(), appID)
		if err != nil {
			writeAppsError(w, err)
			return
		}
		// Never expose hashes.
		out := make([]apps.ApiKey, len(keys.ApiKeys))
		for i, k := range keys.ApiKeys {
			out[i] = k
			out[i].Hash = ""
		}
		writeJSON(w, http.StatusOK, out)

	case http.MethodPost:
		body := struct {
			Name          string `json:"name"`
			ExpiresInDays *int   `json:"expiresInDays,omitempty"`
		}{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(body.Name) == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}
		plaintext, hash, prefix, err := apps.GenerateAPIKey()
		if err != nil {
			http.Error(w, "generate key: "+err.Error(), http.StatusInternalServerError)
			return
		}
		now := time.Now().UTC()
		ak := apps.ApiKey{
			ID:        uuid.NewString(),
			Hash:      hash,
			Prefix:    prefix,
			Name:      body.Name,
			CreatedAt: now,
		}
		if body.ExpiresInDays != nil && *body.ExpiresInDays > 0 {
			exp := now.Add(time.Duration(*body.ExpiresInDays) * 24 * time.Hour)
			ak.ExpiresAt = &exp
		}
		store := s.handler.appsStoreFor(r.Context())
		kf, err := store.LoadKeys(r.Context(), appID)
		if err != nil {
			writeAppsError(w, err)
			return
		}
		kf.ApiKeys = append(kf.ApiKeys, ak)
		if err := store.SaveKeys(r.Context(), appID, kf); err != nil {
			http.Error(w, "save keys: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// Return the plaintext only once.
		writeJSON(w, http.StatusCreated, map[string]any{
			"id":        ak.ID,
			"name":      ak.Name,
			"prefix":    ak.Prefix,
			"apiKey":    plaintext,
			"createdAt": ak.CreatedAt,
			"expiresAt": ak.ExpiresAt,
		})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleAppsKeysItem handles DELETE on /api/apps/{appId}/keys/{keyId}.
//
// @Summary Delete API key
// @Description Permanently removes the API key. The key stops authenticating immediately.
// @Tags apps-keys
// @Produce json
// @Param appId path string true "App ID"
// @Param keyId path string true "API key ID"
// @Success 200 {object} map[string]bool "{ok: true}"
// @Failure 404 {string} string "key not found"
// @Router /api/apps/{appId}/keys/{keyId} [delete]
func (s *Server) handleAppsKeysItem(w http.ResponseWriter, r *http.Request, appID, keyID string) {
	if r.Method != http.MethodDelete {
		http.Error(w, "DELETE required", http.StatusMethodNotAllowed)
		return
	}
	store := s.handler.appsStoreFor(r.Context())
	kf, err := store.LoadKeys(r.Context(), appID)
	if err != nil {
		writeAppsError(w, err)
		return
	}
	found := false
	filtered := kf.ApiKeys[:0]
	for _, k := range kf.ApiKeys {
		if k.ID == keyID {
			found = true
			continue
		}
		filtered = append(filtered, k)
	}
	if !found {
		http.Error(w, "key not found", http.StatusNotFound)
		return
	}
	kf.ApiKeys = filtered
	if err := store.SaveKeys(r.Context(), appID, kf); err != nil {
		http.Error(w, "save keys: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAppsKeysRotate handles POST /api/apps/{appId}/keys/{keyId}/rotate.
// Generates a new plaintext + bcrypt hash, optionally updates the name and
// expiry, resets LastUsedAt + CreatedAt. Returns the new plaintext (one-shot)
// so the caller can swap it into their secret store before the old key
// stops authenticating — the old hash is overwritten in the same write.
//
// @Summary Rotate API key
// @Description Generates a new secret for an existing key, optionally updating its name and expiry. Returns the new plaintext apiKey ONCE — the previous secret stops authenticating immediately.
// @Tags apps-keys
// @Accept json
// @Produce json
// @Param appId path string true "App ID"
// @Param keyId path string true "API key ID"
// @Param body body object false "Optional rotation payload: {name?, expiresInDays?}"
// @Success 200 {object} map[string]any "Rotated key: {id, name, prefix, apiKey, createdAt, expiresAt}"
// @Failure 400 {string} string "invalid body"
// @Failure 404 {string} string "key not found"
// @Failure 500 {string} string "key generation failed"
// @Router /api/apps/{appId}/keys/{keyId}/rotate [post]
func (s *Server) handleAppsKeysRotate(w http.ResponseWriter, r *http.Request, appID, keyID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	body := struct {
		Name          *string `json:"name,omitempty"`
		ExpiresInDays *int    `json:"expiresInDays,omitempty"`
	}{}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	store := s.handler.appsStoreFor(r.Context())
	kf, err := store.LoadKeys(r.Context(), appID)
	if err != nil {
		writeAppsError(w, err)
		return
	}
	idx := -1
	for i, k := range kf.ApiKeys {
		if k.ID == keyID {
			idx = i
			break
		}
	}
	if idx == -1 {
		http.Error(w, "key not found", http.StatusNotFound)
		return
	}
	plaintext, hash, prefix, err := apps.GenerateAPIKey()
	if err != nil {
		http.Error(w, "generate key: "+err.Error(), http.StatusInternalServerError)
		return
	}
	now := time.Now().UTC()
	ak := kf.ApiKeys[idx]
	ak.Hash = hash
	ak.Prefix = prefix
	ak.CreatedAt = now
	ak.LastUsedAt = nil
	if body.Name != nil && strings.TrimSpace(*body.Name) != "" {
		ak.Name = *body.Name
	}
	if body.ExpiresInDays != nil {
		if *body.ExpiresInDays > 0 {
			exp := now.Add(time.Duration(*body.ExpiresInDays) * 24 * time.Hour)
			ak.ExpiresAt = &exp
		} else {
			// 0 or negative explicitly clears the expiry — caller is opting
			// the key back into "never expires".
			ak.ExpiresAt = nil
		}
	}
	kf.ApiKeys[idx] = ak
	if err := store.SaveKeys(r.Context(), appID, kf); err != nil {
		http.Error(w, "save keys: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":        ak.ID,
		"name":      ak.Name,
		"prefix":    ak.Prefix,
		"apiKey":    plaintext,
		"createdAt": ak.CreatedAt,
		"expiresAt": ak.ExpiresAt,
	})
}