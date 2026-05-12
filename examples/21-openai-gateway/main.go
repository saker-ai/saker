// Example 21 — OpenAI-compatible /v1/* gateway smoke client.
//
// What this shows:
//   - GET  /v1/models             — discover the saker tier ids
//   - POST /v1/chat/completions   — non-streaming Chat Completions
//   - POST /v1/chat/completions   — streaming SSE Chat Completions
//   - extra_body.human_input_mode — force the AskUserQuestion fallback path
//
// The example uses only the Go standard library so you can read the exact
// wire format on both sides. Pair it with `saker --server --openai-gw-enabled`
// (see the README in this directory).
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	addr := flag.String("addr", "http://127.0.0.1:10112", "Saker server base URL")
	apiKey := flag.String("api-key", os.Getenv("SAKER_API_KEY"), "Bearer key issued by `saker openai-key create` (or set SAKER_API_KEY)")
	model := flag.String("model", "saker-default", "Model id from /v1/models")
	prompt := flag.String("prompt", "Reply with a single short sentence: hello from saker.", "User prompt")
	demo := flag.String("demo", "all", "Which demo to run: models|sync|stream|never|reconnect|all")
	timeout := flag.Duration("timeout", 60*time.Second, "Per-request timeout")
	flag.Parse()

	if *apiKey == "" {
		fmt.Fprintln(os.Stderr, "ERROR: --api-key is required (or SAKER_API_KEY). Issue one with:")
		fmt.Fprintln(os.Stderr, "       saker openai-key create --user $USER --project new --name openai-gw-demo")
		fmt.Fprintln(os.Stderr, "If you started the server with --openai-gw-dev-bypass, pass any non-empty value.")
		os.Exit(2)
	}

	c := &gwClient{base: strings.TrimRight(*addr, "/"), key: *apiKey, http: &http.Client{Timeout: *timeout}}
	ctx := context.Background()

	switch *demo {
	case "models":
		runModels(ctx, c)
	case "sync":
		runSync(ctx, c, *model, *prompt)
	case "stream":
		runStream(ctx, c, *model, *prompt)
	case "never":
		runNeverMode(ctx, c, *model)
	case "reconnect":
		runReconnect(ctx, c, *model, *prompt)
	case "all":
		runModels(ctx, c)
		fmt.Println()
		runSync(ctx, c, *model, *prompt)
		fmt.Println()
		runStream(ctx, c, *model, *prompt)
		fmt.Println()
		runNeverMode(ctx, c, *model)
		fmt.Println()
		runReconnect(ctx, c, *model, *prompt)
	default:
		fmt.Fprintf(os.Stderr, "unknown --demo %q (use models|sync|stream|never|reconnect|all)\n", *demo)
		os.Exit(2)
	}
}

// ----------------------------------------------------------------- demos

func runModels(ctx context.Context, c *gwClient) {
	fmt.Println("== GET /v1/models ==")
	body, err := c.get(ctx, "/v1/models")
	if err != nil {
		fmt.Fprintf(os.Stderr, "models: %v\n", err)
		return
	}
	var resp struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "models decode: %v\n", err)
		return
	}
	for _, m := range resp.Data {
		fmt.Printf("  %s (owned_by=%s)\n", m.ID, m.OwnedBy)
	}
}

func runSync(ctx context.Context, c *gwClient, model, prompt string) {
	fmt.Println("== POST /v1/chat/completions (stream=false) ==")
	req := chatRequest{
		Model:    model,
		Messages: []chatMessage{{Role: "user", Content: prompt}},
	}
	resp, err := c.chatSync(ctx, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sync: %v\n", err)
		return
	}
	for _, choice := range resp.Choices {
		fmt.Printf("  finish=%s\n", choice.FinishReason)
		if choice.Message != nil {
			fmt.Printf("  content=%q\n", choice.Message.Content)
		}
	}
}

func runStream(ctx context.Context, c *gwClient, model, prompt string) {
	fmt.Println("== POST /v1/chat/completions (stream=true) ==")
	req := chatRequest{
		Model:    model,
		Stream:   true,
		Messages: []chatMessage{{Role: "user", Content: prompt}},
	}
	if err := c.chatStream(ctx, req, func(chunk chatChunk) {
		for _, choice := range chunk.Choices {
			if choice.Delta != nil && choice.Delta.Content != "" {
				fmt.Print(choice.Delta.Content)
			}
			if choice.FinishReason != "" {
				fmt.Printf("\n  [finish=%s]\n", choice.FinishReason)
			}
		}
	}); err != nil {
		fmt.Fprintf(os.Stderr, "\nstream: %v\n", err)
	}
}

// runReconnect demonstrates the persistence + Last-Event-ID resume flow.
//
// Wire shape:
//  1. POST /v1/chat/completions stream=true; capture X-Saker-Run-Id from
//     the response headers and the highest `id:` seq seen on the SSE wire,
//     then deliberately abort the request after the first ~3 chunks.
//  2. GET /v1/runs/{id}/events?last_event_id=N; the server replays every
//     event with seq > N from the persistent store (or ring), then tails
//     live events until [DONE].
//
// Server prerequisite: --openai-gw-runhub-dsn pointing at a sqlite path or
// postgres URL. With the default empty DSN (MemoryHub) the resume call
// returns 410 once the in-memory ring evicts the prefix; this demo logs
// that case as a soft skip rather than a failure.
func runReconnect(ctx context.Context, c *gwClient, model, prompt string) {
	fmt.Println("== reconnect: stream → drop → /v1/runs/{id}/events?last_event_id=N ==")
	req := chatRequest{
		Model:    model,
		Stream:   true,
		Messages: []chatMessage{{Role: "user", Content: prompt + " (please reply with three short sentences)"}},
	}
	runID, lastSeq, err := c.streamUntil(ctx, req, 3)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  initial stream: %v\n", err)
		return
	}
	if runID == "" {
		fmt.Fprintln(os.Stderr, "  no X-Saker-Run-Id header — server too old?")
		return
	}
	fmt.Printf("  dropped after seq=%d (run_id=%s); resuming...\n", lastSeq, runID)

	// Wire-protocol cursor is `<run_id>:<seq>`. The qualified format lets
	// the gateway reject reconnect cursors from other runs and gives
	// clients a self-describing token.
	resumePath := fmt.Sprintf("/v1/runs/%s/events?last_event_id=%s:%d", runID, runID, lastSeq)
	resumed, finalSeq, err := c.streamPath(ctx, resumePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  resume: %v\n", err)
		return
	}
	fmt.Printf("  resumed events: %d (final seq=%d)\n", resumed, finalSeq)
}

func runNeverMode(ctx context.Context, c *gwClient, model string) {
	fmt.Println("== POST /v1/chat/completions (human_input_mode=never) ==")
	// Ask the model to call ask_user_question; with human_input_mode=never the
	// gateway routes the call to the fallback path so the LLM is told to ask
	// in its reply text instead. The user-visible answer should NOT block.
	req := chatRequest{
		Model: model,
		Messages: []chatMessage{
			{Role: "system", Content: "If you need clarification, you may call ask_user_question. Otherwise reply directly."},
			{Role: "user", Content: "Pick any color and tell me which one. If unsure, ask me first."},
		},
		ExtraBody: map[string]any{
			"human_input_mode":     "never",
			"cancel_on_disconnect": true,
		},
	}
	resp, err := c.chatSync(ctx, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "never: %v\n", err)
		return
	}
	for _, choice := range resp.Choices {
		fmt.Printf("  finish=%s\n", choice.FinishReason)
		if choice.Message != nil {
			fmt.Printf("  content=%q\n", choice.Message.Content)
		}
	}
}

// --------------------------------------------------------------- HTTP client

type gwClient struct {
	base string
	key  string
	http *http.Client
}

func (c *gwClient) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func (c *gwClient) postJSON(ctx context.Context, path string, payload any, accept string) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(errBody))
	}
	return resp, nil
}

func (c *gwClient) chatSync(ctx context.Context, req chatRequest) (*chatResponse, error) {
	resp, err := c.postJSON(ctx, "/v1/chat/completions", req, "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// streamUntil POSTs a streaming chat completion, parses SSE up to maxChunks
// data frames, captures the X-Saker-Run-Id response header and the highest
// `id:` seq seen on the wire, then aborts the connection. Returns the run id
// and the last seq the client observed — the inputs needed to call the
// reconnect endpoint with last_event_id=<seq>.
func (c *gwClient) streamUntil(ctx context.Context, req chatRequest, maxChunks int) (string, int, error) {
	resp, err := c.postJSON(ctx, "/v1/chat/completions", req, "text/event-stream")
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	runID := resp.Header.Get("X-Saker-Run-Id")
	br := bufio.NewReader(resp.Body)
	chunks, lastSeq := 0, 0
	for {
		line, rerr := br.ReadString('\n')
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return runID, lastSeq, nil
			}
			return runID, lastSeq, rerr
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "" || strings.HasPrefix(line, ":"):
			continue
		case strings.HasPrefix(line, "id:"):
			if n, ok := parseEventIDSeq(strings.TrimPrefix(line, "id:")); ok && n > lastSeq {
				lastSeq = n
			}
		case strings.HasPrefix(line, "data:"):
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				return runID, lastSeq, nil
			}
			chunks++
			if chunks >= maxChunks {
				// Abort early so the server keeps producing events that
				// the next call will replay from the store.
				return runID, lastSeq, nil
			}
		}
	}
}

// parseEventIDSeq pulls the integer seq out of a wire `id:` value. The
// gateway emits `<run_id>:<seq>` (qualified format); we only care about
// the seq portion here because the run id is already known to the
// caller. Returns false on a malformed line.
func parseEventIDSeq(raw string) (int, bool) {
	s := strings.TrimSpace(raw)
	if i := strings.LastIndex(s, ":"); i >= 0 {
		s = s[i+1:]
	}
	var n int
	if _, perr := fmt.Sscanf(s, "%d", &n); perr != nil {
		return 0, false
	}
	return n, true
}

// streamPath GETs an SSE endpoint and counts data frames + tracks the
// highest seq seen until [DONE] or EOF. Used by the reconnect demo against
// /v1/runs/{id}/events?last_event_id=N.
func (c *gwClient) streamPath(ctx context.Context, path string) (int, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusGone {
		return 0, 0, errors.New("410 Gone: run has aged out or ring buffer evicted (use --openai-gw-runhub-dsn to persist)")
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return 0, 0, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	br := bufio.NewReader(resp.Body)
	chunks, lastSeq := 0, 0
	for {
		line, rerr := br.ReadString('\n')
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return chunks, lastSeq, nil
			}
			return chunks, lastSeq, rerr
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "" || strings.HasPrefix(line, ":"):
			continue
		case strings.HasPrefix(line, "id:"):
			if n, ok := parseEventIDSeq(strings.TrimPrefix(line, "id:")); ok && n > lastSeq {
				lastSeq = n
			}
		case strings.HasPrefix(line, "data:"):
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				return chunks, lastSeq, nil
			}
			chunks++
		}
	}
}

// chatStream parses Server-Sent Events line-by-line and forwards each
// data: chunk (until "[DONE]") to onChunk. Comment frames (": keepalive")
// are skipped per the SSE spec.
func (c *gwClient) chatStream(ctx context.Context, req chatRequest, onChunk func(chatChunk)) error {
	resp, err := c.postJSON(ctx, "/v1/chat/completions", req, "text/event-stream")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "id:") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			return nil
		}
		var chunk chatChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			// Skip malformed frames rather than aborting the whole stream;
			// keepalives and id-only lines are already handled above.
			continue
		}
		onChunk(chunk)
	}
}

// ------------------------------------------------------------- wire types

type chatRequest struct {
	Model     string         `json:"model"`
	Messages  []chatMessage  `json:"messages"`
	Stream    bool           `json:"stream,omitempty"`
	ExtraBody map[string]any `json:"extra_body,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content,omitempty"`
}

type chatResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
}

type chatChunk struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
}

type chatChoice struct {
	Index        int             `json:"index"`
	Message      *chatMessageOut `json:"message,omitempty"`
	Delta        *chatMessageOut `json:"delta,omitempty"`
	FinishReason string          `json:"finish_reason,omitempty"`
}

type chatMessageOut struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}
