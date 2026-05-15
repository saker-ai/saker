package synapse

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	synapsev1 "github.com/saker-ai/saker/proto/synapse/v1"
)

// Backend is the abstraction for forwarding chat requests to a local
// inference endpoint. HTTPBackend talks to saker's own HTTP server over
// loopback; alternative implementations can short-circuit for testing.
type Backend interface {
	Stream(ctx context.Context, req Request, out chan<- Frame) error
	Health(ctx context.Context) error
}

// Request carries one inbound ChatRequest in backend-friendly form.
type Request struct {
	RequestID string
	Protocol  synapsev1.Protocol
	Path      string // "/v1/chat/completions" or "/v1/messages"
	Body      []byte
	Headers   map[string]string
}

// Frame is the internal streaming envelope; the pump converts these into
// SakerMessage frames before sending upstream.
type Frame struct {
	Chunk *synapsev1.ChatChunk
	Done  *synapsev1.ChatDone
	Error *synapsev1.ChatError
}

// ErrUpstreamClosed is returned when the saker process closes a streaming
// HTTP body without sending a terminator.
var ErrUpstreamClosed = errors.New("saker closed stream without terminator")

// HTTPBackend talks to the local saker HTTP server over loopback.
type HTTPBackend struct {
	baseURL string
	client  *http.Client
}

func NewHTTPBackend(baseURL string) *HTTPBackend {
	return &HTTPBackend{
		baseURL: strings.TrimRight(baseURL, "/"),
		client: &http.Client{
			Transport: &http.Transport{
				MaxIdleConnsPerHost: 4,
				IdleConnTimeout:    90 * time.Second,
				DisableCompression: true,
			},
		},
	}
}

func (b *HTTPBackend) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.baseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("health status %d", resp.StatusCode)
	}
	return nil
}

func (b *HTTPBackend) Stream(ctx context.Context, sr Request, out chan<- Frame) error {
	url := b.baseURL + sr.Path
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(sr.Body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	for k, v := range sr.Headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := b.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("saker http call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
		out <- Frame{Error: &synapsev1.ChatError{
			RequestId:  sr.RequestID,
			Code:       "saker_http_error",
			Message:    fmt.Sprintf("saker returned %d: %s", resp.StatusCode, truncate(string(body), 256)),
			HttpStatus: int32(resp.StatusCode),
		}}
		return nil
	}

	ctype := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ctype, "text/event-stream") {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			out <- Frame{Error: &synapsev1.ChatError{
				RequestId: sr.RequestID, Code: "saker_read_error",
				Message: err.Error(), HttpStatus: 502,
			}}
			return nil
		}
		out <- Frame{Chunk: &synapsev1.ChatChunk{
			RequestId: sr.RequestID, Data: body,
		}}
		out <- Frame{Done: &synapsev1.ChatDone{
			RequestId: sr.RequestID, Usage: usageFromBody(body),
		}}
		return nil
	}

	return b.streamSSE(resp.Body, sr, out)
}

func (b *HTTPBackend) streamSSE(body io.Reader, sr Request, out chan<- Frame) error {
	rd := newSSEReader(body)
	var lastUsage *synapsev1.Usage
	for {
		frame, err := rd.next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				out <- Frame{Done: &synapsev1.ChatDone{
					RequestId: sr.RequestID, Usage: lastUsage,
				}}
				return nil
			}
			out <- Frame{Error: &synapsev1.ChatError{
				RequestId: sr.RequestID, Code: "saker_stream_error",
				Message: err.Error(), HttpStatus: 502,
			}}
			return nil
		}

		if frame.isDoneSentinel() {
			out <- Frame{Done: &synapsev1.ChatDone{
				RequestId: sr.RequestID, Usage: lastUsage,
			}}
			return nil
		}
		if frame.event == "error" {
			out <- Frame{Error: &synapsev1.ChatError{
				RequestId: sr.RequestID, Code: "saker_event_error",
				Message: string(frame.data), HttpStatus: 502,
			}}
			return nil
		}

		if frame.event == "message_stop" {
			out <- Frame{Chunk: &synapsev1.ChatChunk{
				RequestId: sr.RequestID, Data: frame.data,
				EventType: frame.event, SakerEventId: frame.id,
			}}
			out <- Frame{Done: &synapsev1.ChatDone{
				RequestId: sr.RequestID, Usage: lastUsage,
			}}
			return nil
		}

		if u := tryExtractUsage(frame.data); u != nil {
			lastUsage = u
		}

		out <- Frame{Chunk: &synapsev1.ChatChunk{
			RequestId:    sr.RequestID,
			Data:         frame.data,
			EventType:    frame.event,
			SakerEventId: frame.id,
		}}
	}
}

// --- SSE parser (private) ---

type sseFrame struct {
	event string
	data  []byte
	id    string
}

func (f sseFrame) isDoneSentinel() bool {
	return f.event == "" && bytes.Equal(bytes.TrimSpace(f.data), []byte("[DONE]"))
}

type sseReader struct {
	br *bufio.Reader
}

func newSSEReader(r io.Reader) *sseReader {
	return &sseReader{br: bufio.NewReaderSize(r, 64<<10)}
}

func (s *sseReader) next() (sseFrame, error) {
	var (
		event   string
		id      string
		dataBuf bytes.Buffer
		started bool
	)
	for {
		line, err := s.br.ReadBytes('\n')
		if err != nil {
			if err == io.EOF && started {
				return assembleFrame(event, id, dataBuf), nil
			}
			return sseFrame{}, err
		}
		line = bytes.TrimRight(line, "\r\n")

		if len(line) == 0 {
			if started {
				return assembleFrame(event, id, dataBuf), nil
			}
			continue
		}
		if line[0] == ':' {
			continue
		}

		started = true
		field, value := splitSSE(line)
		switch field {
		case "event":
			event = value
		case "id":
			id = value
		case "data":
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(value)
		}
	}
}

func assembleFrame(event, id string, data bytes.Buffer) sseFrame {
	return sseFrame{event: event, id: id, data: append([]byte(nil), data.Bytes()...)}
}

func splitSSE(line []byte) (field, value string) {
	idx := bytes.IndexByte(line, ':')
	if idx == -1 {
		return string(line), ""
	}
	field = string(line[:idx])
	v := line[idx+1:]
	if len(v) > 0 && v[0] == ' ' {
		v = v[1:]
	}
	return field, string(v)
}

// --- usage extraction ---

func usageFromBody(body []byte) *synapsev1.Usage {
	var doc struct {
		Usage *struct {
			PromptTokens     uint64 `json:"prompt_tokens"`
			CompletionTokens uint64 `json:"completion_tokens"`
			TotalTokens      uint64 `json:"total_tokens"`
			InputTokens      uint64 `json:"input_tokens"`
			OutputTokens     uint64 `json:"output_tokens"`
		} `json:"usage"`
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &doc); err != nil || doc.Usage == nil {
		return nil
	}
	prompt := doc.Usage.PromptTokens
	if prompt == 0 {
		prompt = doc.Usage.InputTokens
	}
	completion := doc.Usage.CompletionTokens
	if completion == 0 {
		completion = doc.Usage.OutputTokens
	}
	total := doc.Usage.TotalTokens
	if total == 0 {
		total = prompt + completion
	}
	return &synapsev1.Usage{
		PromptTokens: prompt, CompletionTokens: completion,
		TotalTokens: total, Model: doc.Model,
	}
}

func tryExtractUsage(data []byte) *synapsev1.Usage {
	if u := usageFromBody(data); u != nil {
		return u
	}
	var anth struct {
		Usage *struct {
			InputTokens  uint64 `json:"input_tokens"`
			OutputTokens uint64 `json:"output_tokens"`
		} `json:"usage"`
		Delta *struct {
			Usage *struct {
				InputTokens  uint64 `json:"input_tokens"`
				OutputTokens uint64 `json:"output_tokens"`
			} `json:"usage"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(data, &anth); err != nil {
		return nil
	}
	pick := anth.Usage
	if pick == nil && anth.Delta != nil {
		pick = anth.Delta.Usage
	}
	if pick == nil {
		return nil
	}
	return &synapsev1.Usage{
		PromptTokens: pick.InputTokens, CompletionTokens: pick.OutputTokens,
		TotalTokens: pick.InputTokens + pick.OutputTokens,
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
