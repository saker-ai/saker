package server

import (
	"time"

	"github.com/cinience/saker/pkg/logging"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// WebSocket keepalive settings.
const (
	// wsPingInterval is how often the server sends a ping to the client.
	wsPingInterval = 30 * time.Second
	// wsPongTimeout is how long the server waits for a pong response.
	// Must be greater than wsPingInterval.
	wsPongTimeout = 40 * time.Second
	// wsWriteTimeout is the deadline for writing a message (including pings).
	wsWriteTimeout = 10 * time.Second
)

// handleWebSocket upgrades HTTP to WebSocket and runs the JSON-RPC loop.
//
// @Summary WebSocket connection
// @Description Upgrades an HTTP connection to WebSocket for bidirectional JSON-RPC communication. Used for real-time streaming methods like turn/send, thread/subscribe, approval/respond, and question/respond.
// @Tags websocket
// @Success 101 {string} string "WebSocket upgrade successful"
// @Router /ws [get]
func (s *Server) handleWebSocket(c *gin.Context) {
	r := c.Request
	w := c.Writer
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("websocket upgrade failed", "error", err, "remote_addr", r.RemoteAddr)
		return
	}
	defer conn.Close()

	// Register client with a thread-safe send function.
	sendMu := make(chan struct{}, 1) // serialize writes
	sendFn := func(msg any) error {
		sendMu <- struct{}{}
		defer func() { <-sendMu }()
		_ = conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
		return conn.WriteJSON(msg)
	}
	clientID := s.handler.RegisterClient(sendFn)
	defer s.handler.UnregisterClient(clientID)

	ctx := logging.WithLogger(r.Context(), s.logger)

	s.logger.Info("websocket connected", "client_id", clientID, "remote_addr", r.RemoteAddr)
	defer s.logger.Info("websocket disconnected", "client_id", clientID)

	// Configure pong handler: extend read deadline on each pong received.
	_ = conn.SetReadDeadline(time.Now().Add(wsPongTimeout))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(wsPongTimeout))
		return nil
	})

	// Start ping ticker in background goroutine.
	stopPing := make(chan struct{})
	defer close(stopPing)
	go func() {
		ticker := time.NewTicker(wsPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stopPing:
				return
			case <-ticker.C:
				sendMu <- struct{}{}
				_ = conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
				err := conn.WriteMessage(websocket.PingMessage, nil)
				<-sendMu
				if err != nil {
					return
				}
			}
		}
	}()

	// Read loop.
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				s.logger.Warn("websocket read error", "client_id", clientID, "error", err)
			}
			return
		}

		// Reset read deadline on any message (not just pong).
		_ = conn.SetReadDeadline(time.Now().Add(wsPongTimeout))

		req, err := parseRequest(raw)
		if err != nil {
			s.logger.Warn("json-rpc parse error", "client_id", clientID, "error", err)
			resp := Response{
				JSONRPC: "2.0",
				Error:   &Error{Code: ErrCodeParse, Message: err.Error()},
			}
			_ = sendFn(resp)
			continue
		}

		resp := s.handler.HandleRequest(ctx, clientID, req)
		_ = sendFn(resp)
	}
}
