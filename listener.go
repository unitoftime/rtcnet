package rtcnet

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/pion/webrtc/v4"
)

const (
	defaultMaxPendingConns = 256
	handshakeTimeout = 30 * time.Second // TODO: make timeout configurable?
)

type ListenConfig struct {
	TlsConfig *tls.Config
	OriginPatterns []string
	IceServers []string
	// Max connections that are negotiating or waiting to be accepted. Beyond this, new requests are rejected with a 503. Defaults to 256
	MaxPendingConns int
	// AllowWebsocketFallback bool // TODO: Restriction?
}

type Listener struct {
	httpServer *http.Server
	addr net.Addr
	api *webrtc.API
	acceptOptions *websocket.AcceptOptions
	iceServers []string

	pendingSlots chan struct{} // Semaphore bounding the connections that haven't been accepted yet
	pendingAccepts chan net.Conn

	ctx context.Context // Canceled with the reason once the listener stops
	cancel context.CancelCauseFunc
}

func NewListener(address string, config ListenConfig) (*Listener, error) {
	// TODO - Is tcp always correct here?
	tcpListener, err := tls.Listen("tcp", address, config.TlsConfig)
	if err != nil {
		return nil, err
	}

	maxPendingConns := config.MaxPendingConns
	if maxPendingConns <= 0 {
		maxPendingConns = defaultMaxPendingConns
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	l := &Listener{
		addr: tcpListener.Addr(),
		api: newAPI(),
		acceptOptions: &websocket.AcceptOptions{
			OriginPatterns: config.OriginPatterns,
		},
		iceServers: config.IceServers,
		pendingSlots: make(chan struct{}, maxPendingConns),
		pendingAccepts: make(chan net.Conn),
		ctx: ctx,
		cancel: cancel,
	}
	l.httpServer = &http.Server{
		Handler: http.HandlerFunc(l.serveHTTP),
		TLSConfig: config.TlsConfig,
		ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		err := l.httpServer.Serve(tcpListener)
		// ErrServerClosed is returned when shutdown or close is called
		if !errors.Is(err, http.ErrServerClosed) {
			l.cancel(err) // The listen socket failed, so surface it through Accept
		}
	}()

	return l, nil
}

func (l *Listener) Accept() (net.Conn, error) {
	select{
	case conn := <-l.pendingAccepts:
		return conn, nil
	case <-l.ctx.Done():
		return nil, context.Cause(l.ctx)
	}
}
func (l *Listener) Close() error {
	l.cancel(net.ErrClosed)

	ctx, cancel := context.WithTimeout(context.Background(), 10 * time.Second)
	defer cancel()
	return l.httpServer.Shutdown(ctx)
}
func (l *Listener) Addr() net.Addr {
	return l.addr
}

// Every connection runs its full setup inside its own request handler. Failures only affect that connection, so they are logged rather than returned from Accept
func (l *Listener) serveHTTP(w http.ResponseWriter, r *http.Request) {
	select {
	case l.pendingSlots <- struct{}{}:
		defer func() { <-l.pendingSlots }()
	default:
		http.Error(w, "too many pending connections", http.StatusServiceUnavailable)
		return
	}

	wsConn, err := websocket.Accept(w, r, l.acceptOptions)
	if err != nil {
		// Note: Accept already wrote the http error response. These are routine from scanners and bad origins
		logger.Debug().
			Err(err).
			Str("remote", r.RemoteAddr).
			Msg("rtcnet: websocket upgrade failed")
		return
	}

	var conn net.Conn
	if r.URL.Path == "/wss" {
		logger.Warn().Msg("Dialer requested wss fallback socket!")
		conn = websocket.NetConn(context.Background(), wsConn, websocket.MessageBinary) // Note: This has to be background because the fallback socket outlives the request
	} else {
		conn, err = l.negotiate(wsConn)
		if err != nil {
			logger.Warn().
				Err(err).
				Str("remote", r.RemoteAddr).
				Msg("rtcnet: webrtc negotiation failed")
			return
		}
	}

	select {
	case l.pendingAccepts <- conn:
	case <-l.ctx.Done():
		conn.Close()
	}
}

func (l *Listener) negotiate(wsConn *websocket.Conn) (*Conn, error) {
	defer trace("finished negotiate")

	ctx, cancel := context.WithTimeout(l.ctx, handshakeTimeout)
	defer cancel()

	wSock := websocket.NetConn(ctx, wsConn, websocket.MessageBinary)
	defer wSock.Close()

	h, err := newHandshake(l.api, wSock, l.iceServers)
	if err != nil {
		return nil, err
	}
	h.pc.OnDataChannel(h.open)

	return h.run(ctx)
}
