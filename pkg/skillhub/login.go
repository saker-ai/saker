package skillhub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxResponseBody = 1 << 20 // 1MB max response body from skillhub API

// RequestDeviceCode calls POST /api/v1/auth/device/code and returns the
// pair of codes the user must enter in a browser.
func (c *Client) RequestDeviceCode(ctx context.Context) (*DeviceCode, error) {
	req, err := c.newRequest(ctx, http.MethodPost, "/api/v1/auth/device/code", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("skillhub device code: %w", err)
	}
	defer resp.Body.Close()
	var out DeviceCode
	if err := decodeResponse(resp, &out); err != nil {
		return nil, err
	}
	if out.VerificationURL == "" {
		// Fallback: construct from baseURL when server didn't return one.
		out.VerificationURL = c.baseURL + "/auth/device/verify"
	} else if strings.HasPrefix(out.VerificationURL, "/") {
		out.VerificationURL = c.baseURL + out.VerificationURL
	}
	return &out, nil
}

// PollDeviceToken polls POST /api/v1/auth/device/token until the user
// authorizes (or the code expires). Returns the bearer token on success.
func (c *Client) PollDeviceToken(ctx context.Context, deviceCode string, interval time.Duration) (string, error) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	body := map[string]string{"deviceCode": deviceCode}
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		token, status, err := c.pollOnce(ctx, body)
		if err != nil {
			return "", err
		}
		switch status {
		case "pending":
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(interval):
			}
			continue
		case "ok":
			return token, nil
		default:
			return "", fmt.Errorf("unexpected device flow status %q", status)
		}
	}
}

func (c *Client) pollOnce(ctx context.Context, body map[string]string) (string, string, error) {
	b, _ := json.Marshal(body)
	req, err := c.newRequest(ctx, http.MethodPost, "/api/v1/auth/device/token", stringsReader(b))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("skillhub poll: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return "", "", fmt.Errorf("skillhub poll: read response: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		var out struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return "", "", fmt.Errorf("decode token: %w", err)
		}
		if out.Token == "" {
			return "", "", errors.New("server returned empty token")
		}
		return out.Token, "ok", nil
	case http.StatusAccepted:
		return "", "pending", nil
	default:
		var errBody struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &errBody)
		return "", "", &APIError{Status: resp.StatusCode, Body: string(raw), Msg: errBody.Error}
	}
}

// stringsReader is a tiny helper to avoid importing bytes in this file.
func stringsReader(b []byte) io.Reader {
	return &byteReader{b: b}
}

type byteReader struct {
	b []byte
	i int
}

func (r *byteReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}
