package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/cinience/saker/pkg/api"
)

type e2eServer struct {
	runtime *api.Runtime
}

type runRequest struct {
	Prompt    string `json:"prompt"`
	SessionID string `json:"session_id"`
	TimeoutMs int    `json:"timeout_ms"`
}

type runResponse struct {
	SessionID  string `json:"session_id"`
	Output     string `json:"output"`
	StopReason string `json:"stop_reason"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func (s *e2eServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *e2eServer) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorResponse{"only POST supported"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{err.Error()})
		return
	}

	var req runRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{err.Error()})
		return
	}

	if req.SessionID == "" {
		req.SessionID = fmt.Sprintf("e2e-%d", time.Now().UnixNano())
	}

	timeout := 5 * time.Minute
	if req.TimeoutMs > 0 {
		timeout = time.Duration(req.TimeoutMs) * time.Millisecond
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	result, err := s.runtime.Run(ctx, api.Request{
		Prompt:    req.Prompt,
		SessionID: req.SessionID,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{err.Error()})
		return
	}

	resp := runResponse{SessionID: req.SessionID}
	if result.Result != nil {
		resp.Output = result.Result.Output
		resp.StopReason = result.Result.StopReason
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *e2eServer) handleStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorResponse{"only POST supported"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{err.Error()})
		return
	}

	var req runRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{err.Error()})
		return
	}

	if req.SessionID == "" {
		req.SessionID = fmt.Sprintf("e2e-%d", time.Now().UnixNano())
	}

	timeout := 5 * time.Minute
	if req.TimeoutMs > 0 {
		timeout = time.Duration(req.TimeoutMs) * time.Millisecond
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, errorResponse{"streaming not supported"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	events, err := s.runtime.RunStream(ctx, api.Request{
		Prompt:    req.Prompt,
		SessionID: req.SessionID,
	})
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
		flusher.Flush()
		return
	}

	for event := range events {
		data, _ := json.Marshal(event)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
		flusher.Flush()
	}

	fmt.Fprintf(w, "event: done\ndata: {}\n\n")
	flusher.Flush()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
