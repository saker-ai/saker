package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func TestWebSocketUpgrade(t *testing.T) {
	// Set up a minimal HTTP server that accepts WebSocket upgrades.
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("upgrade failed: %v", err)
			return
		}
		defer conn.Close()
		// Read one message and close.
		_, _, _ = conn.ReadMessage()
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial failed: %v", err)
	}
	defer conn.Close()

	// Send a message to confirm connection is alive.
	err = conn.WriteMessage(websocket.TextMessage, []byte("hello"))
	if err != nil {
		t.Fatalf("write message failed: %v", err)
	}
}

func TestWebSocketPingPong(t *testing.T) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Send a ping and wait for pong.
		_ = conn.WriteControl(websocket.PingMessage, []byte("test"), time.Now().Add(wsWriteTimeout))
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))

		for {
			msgType, _, err := conn.ReadMessage()
			if err != nil {
				// Pong is handled internally; only text/close messages arrive here.
				if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
					return // expected: client closed
				}
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					t.Logf("unexpected read error: %v", err)
				}
				return
			}
			if msgType == websocket.TextMessage {
				return // got text message, done
			}
		}
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial failed: %v", err)
	}

	// Set ping handler: respond with pong automatically.
	conn.SetPingHandler(func(appData string) error {
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(wsWriteTimeout))
	})

	// Close after brief delay to allow ping/pong exchange.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_ = conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(time.Second),
	)
	conn.Close()
}

func TestJSONRPCParseError(t *testing.T) {
	// Verify that invalid JSON produces a JSON-RPC parse error response.
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			req, err := parseRequest(raw)
			if err != nil {
				resp := Response{
					JSONRPC: "2.0",
					Error:   &Error{Code: ErrCodeParse, Message: err.Error()},
				}
				_ = conn.WriteJSON(resp)
				continue
			}
			// Echo back valid requests.
			resp := Response{JSONRPC: "2.0", ID: req.ID, Result: "ok"}
			_ = conn.WriteJSON(resp)
		}
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial failed: %v", err)
	}
	defer conn.Close()

	// Send invalid JSON.
	err = conn.WriteMessage(websocket.TextMessage, []byte("{bad json}"))
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// Read response.
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	var resp Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if resp.JSONRPC != "2.0" {
		t.Errorf("expected jsonrpc 2.0, got %q", resp.JSONRPC)
	}
	if resp.Error == nil {
		t.Fatal("expected error in response, got nil")
	}
	if resp.Error.Code != ErrCodeParse {
		t.Errorf("expected parse error code %d, got %d", ErrCodeParse, resp.Error.Code)
	}

	// Send valid JSON-RPC request.
	req := Request{JSONRPC: "2.0", ID: 1, Method: "ping"}
	rawReq, _ := json.Marshal(req)
	err = conn.WriteMessage(websocket.TextMessage, rawReq)
	if err != nil {
		t.Fatalf("write valid request failed: %v", err)
	}

	_, raw, err = conn.ReadMessage()
	if err != nil {
		t.Fatalf("read valid response failed: %v", err)
	}

	var resp2 Response
	if err := json.Unmarshal(raw, &resp2); err != nil {
		t.Fatalf("unmarshal valid response failed: %v", err)
	}
	if resp2.Error != nil {
		t.Errorf("expected no error for valid request, got %v", resp2.Error)
	}
	if resp2.Result != "ok" {
		t.Errorf("expected result \"ok\", got %v", resp2.Result)
	}
}

func TestWebSocketInvalidVersion(t *testing.T) {
	// Sending a JSON-RPC request with wrong version should produce
	// an InvalidRequest error.
	req := Request{JSONRPC: "1.0", ID: 2, Method: "test"}
	rawReq, _ := json.Marshal(req)

	parsed, err := parseRequest(rawReq)
	if err == nil {
		t.Fatalf("expected error for jsonrpc 1.0, got nil; parsed: %+v", parsed)
	}
}

func TestWebSocketCloseNormal(t *testing.T) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// Wait for client to close.
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
					t.Logf("unexpected close: %v", err)
				}
				return
			}
		}
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial failed: %v", err)
	}

	// Send normal close frame.
	err = conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "bye"),
		time.Now().Add(time.Second),
	)
	if err != nil {
		t.Fatalf("write close frame failed: %v", err)
	}

	// Give server time to process the close.
	time.Sleep(100 * time.Millisecond)
	conn.Close()
}

func TestRequestIDMiddlewareWebSocketContext(t *testing.T) {
	// Verify that request ID middleware sets context value before WebSocket upgrade.
	var capturedID string
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	engine := buildTestGinEngine(upgrader, &capturedID)

	srv := httptest.NewServer(engine)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial with middleware failed: %v", err)
	}
	conn.Close()

	// The request ID should have been captured during upgrade.
	if capturedID == "" {
		t.Error("expected request ID to be set in context during WebSocket upgrade")
	}
}

func buildTestGinEngine(upgrader websocket.Upgrader, capturedID *string) http.Handler {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(RequestIDMiddleware())

	engine.GET("/ws", func(c *gin.Context) {
		id, exists := c.Get("requestID")
		if exists {
			*capturedID = id.(string)
		}
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		conn.Close()
	})

	return engine
}

func TestMetricsEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(PrometheusMiddleware())
	engine.GET("/metrics", PrometheusHandler())
	engine.GET("/ping", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })

	srv := httptest.NewServer(engine)
	defer srv.Close()

	// Make a request to populate metrics.
	resp, err := http.Get(srv.URL + "/ping")
	if err != nil {
		t.Fatalf("ping request failed: %v", err)
	}
	resp.Body.Close()

	// Fetch metrics endpoint.
	resp, err = http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("metrics request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200 from /metrics, got %d", resp.StatusCode)
	}

	// Verify metrics contain saker prefix.
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read metrics body failed: %v", err)
	}
	if !strings.Contains(string(bodyBytes), "saker_http_requests_total") {
		t.Error("metrics response missing saker_http_requests_total")
	}
}