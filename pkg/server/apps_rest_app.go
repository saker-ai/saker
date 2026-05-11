package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/cinience/saker/pkg/apps"
	"github.com/cinience/saker/pkg/canvas"
	"github.com/gin-gonic/gin"
)

// handleAppsList returns every app meta for the resolved scope.
//
// @Summary List apps
// @Description Returns every app meta for the current project scope.
// @Tags apps
// @Produce json
// @Success 200 {array} apps.AppMeta "List of app metadata"
// @Failure 500 {string} string "internal error"
// @Router /api/apps [get]
func (s *Server) handleAppsList(c *gin.Context) {
	r := c.Request
	w := c.Writer
	store := s.handler.appsStoreFor(r.Context())
	metas, err := store.List(r.Context())
	if err != nil {
		writeAppsError(w, err)
		return
	}
	if metas == nil {
		metas = []*apps.AppMeta{}
	}
	writeJSON(w, http.StatusOK, metas)
}

// handleAppsCreate creates a new app and returns the freshly-persisted meta.
//
// @Summary Create app
// @Description Creates a new app with name, description, icon, source thread, and visibility. Returns the persisted app metadata.
// @Tags apps
// @Accept json
// @Produce json
// @Param body body object true "App creation payload: {name, description, icon, sourceThreadId, visibility}"
// @Success 201 {object} apps.AppMeta "Created app metadata"
// @Failure 400 {string} string "invalid JSON body"
// @Router /api/apps [post]
func (s *Server) handleAppsCreate(c *gin.Context) {
	r := c.Request
	w := c.Writer
	body := struct {
		Name           string `json:"name"`
		Description    string `json:"description,omitempty"`
		Icon           string `json:"icon,omitempty"`
		SourceThreadID string `json:"sourceThreadId"`
		Visibility     string `json:"visibility,omitempty"`
	}{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	store := s.handler.appsStoreFor(r.Context())
	meta, err := store.Create(r.Context(), apps.CreateInput{
		Name:           body.Name,
		Description:    body.Description,
		Icon:           body.Icon,
		SourceThreadID: body.SourceThreadID,
		Visibility:     body.Visibility,
	})
	if err != nil {
		writeAppsError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, meta)
}

// appGetResponse augments the meta with the published version's schema so
// the frontend can render the run form in a single round trip.
type appGetResponse struct {
	*apps.AppMeta
	Inputs  []apps.AppInputField  `json:"inputs,omitempty"`
	Outputs []apps.AppOutputField `json:"outputs,omitempty"`
}

// handleAppsGet returns the meta plus (if published) the latest version's
// inputs/outputs so the UI can render the run form without a second call.
//
// @Summary Get app
// @Description Returns app metadata plus the published version's inputs and outputs schema.
// @Tags apps
// @Produce json
// @Param appId path string true "App ID"
// @Success 200 {object} appGetResponse "App metadata with inputs/outputs"
// @Failure 404 {string} string "app not found"
// @Router /api/apps/{appId} [get]
func (s *Server) handleAppsGet(c *gin.Context) {
	r := c.Request
	w := c.Writer
	appID := c.Param("appId")
	store := s.handler.appsStoreFor(r.Context())
	meta, err := store.Get(r.Context(), appID)
	if err != nil {
		writeAppsError(w, err)
		return
	}
	resp := appGetResponse{AppMeta: meta}
	if meta.PublishedVersion != "" {
		v, err := store.LoadVersion(r.Context(), appID, meta.PublishedVersion)
		if err == nil {
			resp.Inputs = v.Inputs
			resp.Outputs = v.Outputs
		}
		// LoadVersion errors here are non-fatal: the meta is still returned
		// without the schema attached so the UI can show the app row.
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleAppsUpdate applies a partial patch to meta.json.
//
// @Summary Update app
// @Description Applies a partial patch to app metadata (name, description, icon, visibility, sourceThreadId).
// @Tags apps
// @Accept json
// @Produce json
// @Param appId path string true "App ID"
// @Param body body object true "Partial update payload"
// @Success 200 {object} apps.AppMeta "Updated app metadata"
// @Failure 400 {string} string "invalid JSON body"
// @Failure 404 {string} string "app not found"
// @Router /api/apps/{appId} [put]
func (s *Server) handleAppsUpdate(c *gin.Context) {
	r := c.Request
	w := c.Writer
	appID := c.Param("appId")
	body := struct {
		Name           *string `json:"name,omitempty"`
		Description    *string `json:"description,omitempty"`
		Icon           *string `json:"icon,omitempty"`
		Visibility     *string `json:"visibility,omitempty"`
		SourceThreadID *string `json:"sourceThreadId,omitempty"`
	}{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	store := s.handler.appsStoreFor(r.Context())
	meta, err := store.Update(r.Context(), appID, apps.UpdateInput{
		Name:           body.Name,
		Description:    body.Description,
		Icon:           body.Icon,
		SourceThreadID: body.SourceThreadID,
		Visibility:     body.Visibility,
	})
	if err != nil {
		writeAppsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

// handleAppsDelete removes the app directory.
//
// @Summary Delete app
// @Description Removes an app and all its associated data.
// @Tags apps
// @Produce json
// @Param appId path string true "App ID"
// @Success 200 {object} map[string]bool "ok: true"
// @Failure 404 {string} string "app not found"
// @Router /api/apps/{appId} [delete]
func (s *Server) handleAppsDelete(c *gin.Context) {
	r := c.Request
	w := c.Writer
	appID := c.Param("appId")
	store := s.handler.appsStoreFor(r.Context())
	if err := store.Delete(r.Context(), appID); err != nil {
		writeAppsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAppsPublish loads the source canvas, snapshots it, and bumps
// meta.PublishedVersion.
//
// @Summary Publish app version
// @Description Loads the source canvas, snapshots it, and bumps the app's published version.
// @Tags apps
// @Accept json
// @Produce json
// @Param appId path string true "App ID"
// @Success 200 {object} apps.AppVersion "Published version"
// @Failure 400 {string} string "load source canvas error"
// @Failure 404 {string} string "app not found"
// @Failure 405 {string} string "POST required"
// @Router /api/apps/{appId}/publish [post]
func (s *Server) handleAppsPublish(c *gin.Context) {
	r := c.Request
	w := c.Writer
	appID := c.Param("appId")
	store := s.handler.appsStoreFor(r.Context())
	meta, err := store.Get(r.Context(), appID)
	if err != nil {
		writeAppsError(w, err)
		return
	}
	root := s.handler.pathsFor(r.Context()).Root
	doc, err := canvas.Load(root, meta.SourceThreadID)
	if err != nil {
		http.Error(w, "load source canvas: "+err.Error(), http.StatusBadRequest)
		return
	}
	publishedBy := UserFromContext(r.Context())
	version, err := store.PublishVersion(r.Context(), appID, doc, publishedBy)
	if err != nil {
		writeAppsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, version)
}

// handleAppsVersions returns the list of published version summaries.
//
// @Summary List app versions
// @Description Returns the list of published version summaries for an app.
// @Tags apps
// @Produce json
// @Param appId path string true "App ID"
// @Success 200 {array} apps.AppVersion "List of version summaries"
// @Failure 404 {string} string "app not found"
// @Failure 405 {string} string "GET required"
// @Router /api/apps/{appId}/versions [get]
func (s *Server) handleAppsVersions(c *gin.Context) {
	r := c.Request
	w := c.Writer
	appID := c.Param("appId")
	store := s.handler.appsStoreFor(r.Context())
	versions, err := store.ListVersions(r.Context(), appID)
	if err != nil {
		writeAppsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, versions)
}

// handleAppsSetPublishedVersion rolls back (or forward) the published version
// pointer without re-snapshotting from the source thread. The version must
// already exist on disk under versions/.
//
// @Summary Set published version
// @Description Rolls back or forward the published version pointer without re-snapshotting. The version must already exist.
// @Tags apps
// @Accept json
// @Produce json
// @Param appId path string true "App ID"
// @Param body body object true "{version: string}"
// @Success 200 {object} apps.AppMeta "Updated app metadata"
// @Failure 400 {string} string "version is required or invalid JSON"
// @Failure 405 {string} string "PUT required"
// @Router /api/apps/{appId}/published-version [put]
func (s *Server) handleAppsSetPublishedVersion(c *gin.Context) {
	r := c.Request
	w := c.Writer
	appID := c.Param("appId")
	body := struct {
		Version string `json:"version"`
	}{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.Version) == "" {
		http.Error(w, "version is required", http.StatusBadRequest)
		return
	}
	store := s.handler.appsStoreFor(r.Context())
	meta, err := store.SetPublishedVersion(r.Context(), appID, body.Version)
	if err != nil {
		writeAppsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meta)
}
