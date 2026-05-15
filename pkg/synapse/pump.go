package synapse

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	synapsev1 "github.com/cinience/saker/proto/synapse/v1"
)

// Pump owns the bidi stream lifecycle once Hello has been ack'd.
type Pump struct {
	stream  synapsev1.SynapseHub_RegisterClient
	backend Backend
	logger  *zap.Logger

	sendCh    chan *synapsev1.SakerMessage
	heartbeat time.Duration

	mu      sync.Mutex
	cancels map[string]context.CancelFunc

	inFlight atomic.Int32

	closeOnce sync.Once
	closeErr  error
	doneCh    chan struct{}
}

// PumpOptions wires the pump.
type PumpOptions struct {
	Stream    synapsev1.SynapseHub_RegisterClient
	Backend   Backend
	Logger    *zap.Logger
	Heartbeat time.Duration
}

func NewPump(opts PumpOptions) *Pump {
	if opts.Logger == nil {
		opts.Logger = zap.NewNop()
	}
	if opts.Heartbeat <= 0 {
		opts.Heartbeat = 15 * time.Second
	}
	return &Pump{
		stream:    opts.Stream,
		backend:   opts.Backend,
		logger:    opts.Logger,
		sendCh:    make(chan *synapsev1.SakerMessage, 64),
		heartbeat: opts.Heartbeat,
		cancels:   make(map[string]context.CancelFunc),
		doneCh:    make(chan struct{}),
	}
}

// Run blocks until the stream closes or ctx fires.
func (p *Pump) Run(ctx context.Context) error {
	pumpCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	wg := sync.WaitGroup{}
	wg.Add(2)

	sendErrCh := make(chan error, 1)
	go func() {
		defer wg.Done()
		sendErrCh <- p.sendLoop(pumpCtx)
	}()

	go func() {
		defer wg.Done()
		p.heartbeatLoop(pumpCtx)
	}()

	recvErr := p.recvLoop(pumpCtx)
	p.markClosed(recvErr)
	cancel()
	wg.Wait()

	if recvErr != nil {
		return recvErr
	}
	return <-sendErrCh
}

func (p *Pump) recvLoop(ctx context.Context) error {
	for {
		msg, err := p.stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		switch payload := msg.GetPayload().(type) {
		case *synapsev1.HubMessage_Request:
			p.handleRequest(ctx, payload.Request)
		case *synapsev1.HubMessage_Cancel:
			p.handleCancel(payload.Cancel.GetRequestId())
		case *synapsev1.HubMessage_Shutdown:
			p.logger.Info("hub requested shutdown",
				zap.String("reason", payload.Shutdown.GetReason()),
				zap.Int32("grace_seconds", payload.Shutdown.GetGraceSeconds()),
			)
			return nil
		case *synapsev1.HubMessage_HelloAck:
			// Handled in dialer; tolerated for session resets.
		case *synapsev1.HubMessage_Ping:
			p.enqueue(&synapsev1.SakerMessage{Payload: &synapsev1.SakerMessage_Pong{
				Pong: &synapsev1.Pong{UnixNanos: time.Now().UnixNano()},
			}})
		default:
			p.logger.Warn("unknown hub frame", zap.String("type", fmt.Sprintf("%T", payload)))
		}
	}
}

func (p *Pump) handleRequest(ctx context.Context, req *synapsev1.ChatRequest) {
	if req.GetRequestId() == "" {
		p.logger.Warn("rejecting ChatRequest with empty request_id")
		return
	}
	reqCtx, cancel := context.WithCancel(ctx)
	p.mu.Lock()
	if _, dup := p.cancels[req.RequestId]; dup {
		p.mu.Unlock()
		cancel()
		p.logger.Warn("duplicate request_id; dropping",
			zap.String("request_id", req.RequestId))
		return
	}
	p.cancels[req.RequestId] = cancel
	p.mu.Unlock()
	p.inFlight.Add(1)

	go func() {
		defer p.inFlight.Add(-1)
		defer p.completeRequest(req.RequestId)

		out := make(chan Frame, 16)
		errCh := make(chan error, 1)
		go func() { errCh <- p.backend.Stream(reqCtx, requestFromWire(req), out) }()

		for {
			select {
			case <-reqCtx.Done():
				p.enqueue(&synapsev1.SakerMessage{Payload: &synapsev1.SakerMessage_Error{
					Error: &synapsev1.ChatError{
						RequestId: req.RequestId, Code: "cancelled",
						Message: reqCtx.Err().Error(), HttpStatus: 499,
					},
				}})
				go drainFrames(out)
				<-errCh
				return
			case frame, ok := <-out:
				if !ok {
					p.enqueue(&synapsev1.SakerMessage{Payload: &synapsev1.SakerMessage_Error{
						Error: &synapsev1.ChatError{
							RequestId: req.RequestId,
							Code:      "saker_stream_closed",
							Message:   ErrUpstreamClosed.Error(), HttpStatus: 502,
						},
					}})
					return
				}
				if frame.Chunk != nil {
					p.enqueue(&synapsev1.SakerMessage{Payload: &synapsev1.SakerMessage_Chunk{Chunk: frame.Chunk}})
				}
				if frame.Done != nil {
					p.enqueue(&synapsev1.SakerMessage{Payload: &synapsev1.SakerMessage_Done{Done: frame.Done}})
					<-errCh
					return
				}
				if frame.Error != nil {
					p.enqueue(&synapsev1.SakerMessage{Payload: &synapsev1.SakerMessage_Error{Error: frame.Error}})
					<-errCh
					return
				}
			}
		}
	}()
}

func drainFrames(out chan Frame) {
	for range out {
	}
}

func (p *Pump) handleCancel(requestID string) {
	p.mu.Lock()
	cancel, ok := p.cancels[requestID]
	p.mu.Unlock()
	if ok {
		cancel()
	}
}

func (p *Pump) completeRequest(requestID string) {
	p.mu.Lock()
	cancel, ok := p.cancels[requestID]
	if ok {
		delete(p.cancels, requestID)
	}
	p.mu.Unlock()
	if ok {
		cancel()
	}
}

func (p *Pump) enqueue(msg *synapsev1.SakerMessage) {
	select {
	case p.sendCh <- msg:
	case <-p.doneCh:
	}
}

func (p *Pump) sendLoop(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-p.doneCh:
			return nil
		case msg := <-p.sendCh:
			if err := p.stream.Send(msg); err != nil {
				return fmt.Errorf("stream send: %w", err)
			}
		}
	}
}

func (p *Pump) heartbeatLoop(ctx context.Context) {
	t := time.NewTicker(p.heartbeat)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.doneCh:
			return
		case <-t.C:
			p.enqueue(&synapsev1.SakerMessage{Payload: &synapsev1.SakerMessage_Heartbeat{
				Heartbeat: &synapsev1.Heartbeat{
					UnixNanos: time.Now().UnixNano(),
					InFlight:  p.inFlight.Load(),
				},
			}})
		}
	}
}

func (p *Pump) markClosed(err error) {
	p.closeOnce.Do(func() {
		p.closeErr = err
		close(p.doneCh)
		p.mu.Lock()
		cancels := p.cancels
		p.cancels = nil
		p.mu.Unlock()
		for _, c := range cancels {
			c()
		}
	})
}

func requestFromWire(req *synapsev1.ChatRequest) Request {
	path := pathForProtocol(req.GetProtocol())
	return Request{
		RequestID: req.GetRequestId(),
		Protocol:  req.GetProtocol(),
		Path:      path,
		Body:      req.GetPayload(),
		Headers:   req.GetHeaders(),
	}
}

func pathForProtocol(p synapsev1.Protocol) string {
	switch p {
	case synapsev1.Protocol_PROTOCOL_ANTHROPIC_MESSAGES:
		return "/v1/messages"
	default:
		return "/v1/chat/completions"
	}
}
