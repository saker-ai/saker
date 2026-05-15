package synapse

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	synapsev1 "github.com/saker-ai/saker/proto/synapse/v1"
)

// DialOptions describes how to reach the synapse hub.
type DialOptions struct {
	Addr             string
	TLSConfig        *tls.Config
	KeepaliveTime    time.Duration
	KeepaliveTimeout time.Duration
	MaxRecvMessageMB int
	ExtraDialOpts    []grpc.DialOption
}

// HelloOptions populates the Hello frame on Connect.
type HelloOptions struct {
	InstanceID      string
	SandboxID       string
	Version         string
	Models          []string
	MaxConcurrent   int32
	AuthToken       string
	Labels          map[string]string
	PrimaryProtocol synapsev1.Protocol
}

// Dialer encapsulates the connect-and-handshake flow plus exponential
// backoff for reconnects.
type Dialer struct {
	opts   DialOptions
	hello  HelloOptions
	logger *zap.Logger
	rand   *rand.Rand
}

func NewDialer(opts DialOptions, hello HelloOptions, logger *zap.Logger) *Dialer {
	if logger == nil {
		logger = zap.NewNop()
	}
	if opts.KeepaliveTime <= 0 {
		opts.KeepaliveTime = 30 * time.Second
	}
	if opts.KeepaliveTimeout <= 0 {
		opts.KeepaliveTimeout = 10 * time.Second
	}
	if opts.MaxRecvMessageMB <= 0 {
		opts.MaxRecvMessageMB = 16
	}
	return &Dialer{
		opts:   opts,
		hello:  hello,
		logger: logger,
		rand:   rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Session is one logical saker↔hub connection.
type Session struct {
	conn   *grpc.ClientConn
	stream synapsev1.SynapseHub_RegisterClient
	ack    *synapsev1.HelloAck
}

func (s *Session) Stream() synapsev1.SynapseHub_RegisterClient { return s.stream }
func (s *Session) HelloAck() *synapsev1.HelloAck               { return s.ack }
func (s *Session) Close() error {
	if s.stream != nil {
		_ = s.stream.CloseSend()
	}
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

// Connect dials the hub once, opens a Register stream, sends Hello, and
// blocks until HelloAck arrives.
func (d *Dialer) Connect(ctx context.Context) (*Session, error) {
	creds := credentials.NewTLS(d.opts.TLSConfig)
	if d.opts.TLSConfig == nil {
		creds = insecure.NewCredentials()
	}
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                d.opts.KeepaliveTime,
			Timeout:             d.opts.KeepaliveTimeout,
			PermitWithoutStream: true,
		}),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(d.opts.MaxRecvMessageMB * 1024 * 1024),
		),
	}
	dialOpts = append(dialOpts, d.opts.ExtraDialOpts...)

	cc, err := grpc.NewClient(d.opts.Addr, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", d.opts.Addr, err)
	}

	client := synapsev1.NewSynapseHubClient(cc)
	stream, err := client.Register(ctx)
	if err != nil {
		_ = cc.Close()
		return nil, fmt.Errorf("open Register stream: %w", err)
	}

	if err := stream.Send(&synapsev1.SakerMessage{
		Payload: &synapsev1.SakerMessage_Hello{Hello: &synapsev1.Hello{
			InstanceId:      d.hello.InstanceID,
			SandboxId:       d.hello.SandboxID,
			Version:         d.hello.Version,
			BridgeVersion:   d.hello.Version,
			Models:          d.hello.Models,
			MaxConcurrent:   d.hello.MaxConcurrent,
			AuthToken:       d.hello.AuthToken,
			Labels:          d.hello.Labels,
			PrimaryProtocol: d.hello.PrimaryProtocol,
		}},
	}); err != nil {
		_ = stream.CloseSend()
		_ = cc.Close()
		return nil, fmt.Errorf("send Hello: %w", err)
	}

	ackMsg, err := stream.Recv()
	if err != nil {
		_ = stream.CloseSend()
		_ = cc.Close()
		return nil, fmt.Errorf("recv HelloAck: %w", err)
	}
	ack := ackMsg.GetHelloAck()
	if ack == nil {
		_ = stream.CloseSend()
		_ = cc.Close()
		return nil, errors.New("first hub frame was not HelloAck")
	}
	if !ack.GetAccepted() {
		_ = stream.CloseSend()
		_ = cc.Close()
		return nil, fmt.Errorf("hub rejected hello: %s", ack.GetReason())
	}
	d.logger.Info("registered with synapse hub",
		zap.String("addr", d.opts.Addr),
		zap.String("instance", d.hello.InstanceID),
		zap.String("assigned_node", ack.GetAssignedNodeId()),
	)
	return &Session{conn: cc, stream: stream, ack: ack}, nil
}

// ConnectWithBackoff loops Connect with full-jitter exponential backoff
// (250ms → 30s) until ctx fires or a session is established.
func (d *Dialer) ConnectWithBackoff(ctx context.Context) (*Session, error) {
	delay := 250 * time.Millisecond
	const maxDelay = 30 * time.Second
	for {
		s, err := d.Connect(ctx)
		if err == nil {
			return s, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		d.logger.Warn("hub connect failed; will retry",
			zap.String("addr", d.opts.Addr),
			zap.Duration("backoff", delay),
			zap.String("err", trimErr(err)),
		)
		jitter := time.Duration(d.rand.Int63n(int64(delay)))
		select {
		case <-time.After(delay + jitter):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}
}

func trimErr(err error) string {
	s := err.Error()
	if i := strings.Index(s, "; transport:"); i > 0 {
		return s[:i]
	}
	return s
}
