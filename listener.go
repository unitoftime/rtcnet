package rtcnet

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
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
	// Note: STUN lookups run on ephemeral ports rather than the listen port, so their candidates are unreachable when only the listen port is open. Prefer PublicIP
	IceServers []string
	// The IPv4 address clients reach this listener at, advertised in place of the local interface addresses.
	// Needed behind a 1:1 NAT, such as a docker bridge network or a cloud VM without its public IP on an interface
	PublicIP string
	// Run ICE-lite: the listener only answers connectivity checks rather than also sending its own, which is less work and traffic per connection.
	// Only use it when clients can reach the listener directly at its advertised address, ie a public interface or PublicIP with the port forwarded. Lite only uses host candidates, so IceServers must be empty
	IceLite bool
	// Max connections that are negotiating or waiting to be accepted. Beyond this, new requests are rejected with a 503. Defaults to 256
	MaxPendingConns int
	// AllowWebsocketFallback bool // TODO: Restriction?
}

type Listener struct {
	httpServer *http.Server
	addr net.Addr
	api *webrtc.API
	udpMux io.Closer // Carries the webrtc traffic of every connection, on the same port number as the websocket listener
	acceptOptions *websocket.AcceptOptions
	iceServers []string

	pendingSlots chan struct{} // Semaphore bounding the connections that haven't been accepted yet
	pendingAccepts chan net.Conn

	ctx context.Context // Canceled with the reason once the listener stops
	cancel context.CancelCauseFunc
}

func NewListener(address string, config ListenConfig) (*Listener, error) {
	if config.IceLite && len(config.IceServers) > 0 {
		return nil, errors.New("rtcnet: IceLite only uses host candidates, so IceServers must be empty")
	}

	// TODO - Is tcp always correct here?
	tcpListener, err := tls.Listen("tcp", address, config.TlsConfig)
	if err != nil {
		return nil, err
	}

	api, udpMux, err := newListenerAPI(tcpListener.Addr().(*net.TCPAddr), config)
	if err != nil {
		tcpListener.Close()
		return nil, err
	}

	maxPendingConns := config.MaxPendingConns
	if maxPendingConns <= 0 {
		maxPendingConns = defaultMaxPendingConns
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	l := &Listener{
		addr: tcpListener.Addr(),
		api: api,
		udpMux: udpMux,
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
// Note: This also closes the shared UDP port, which ends every connection accepted from this listener
func (l *Listener) Close() error {
	l.cancel(net.ErrClosed)

	ctx, cancel := context.WithTimeout(context.Background(), 10 * time.Second)
	defer cancel()
	return errors.Join(l.httpServer.Shutdown(ctx), l.udpMux.Close())
}
func (l *Listener) Addr() net.Addr {
	return l.addr
}

// The peer of an http request, which the server only tells us as text
type requestAddr string
func (a requestAddr) Network() string {
	return "tcp"
}
func (a requestAddr) String() string {
	return string(a)
}

// The wss fallback socket, with the addresses of the request that opened it
type fallbackConn struct {
	net.Conn
	localAddr, remoteAddr net.Addr
}
func (c fallbackConn) LocalAddr() net.Addr {
	return c.localAddr
}
func (c fallbackConn) RemoteAddr() net.Addr {
	return c.remoteAddr
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

	// Note: A websocket's net.Conn only reports placeholder addresses, so we carry the ones from its http request
	remote := requestAddr(r.RemoteAddr)

	var conn net.Conn
	if r.URL.Path == "/wss" {
		logger.Warn().Msg("Dialer requested wss fallback socket!")
		wsock := websocket.NetConn(context.Background(), wsConn, websocket.MessageBinary) // Note: This has to be background because the fallback socket outlives the request
		conn = fallbackConn{wsock, l.addr, remote}
	} else {
		conn, err = l.negotiate(wsConn, remote)
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

func (l *Listener) negotiate(wsConn *websocket.Conn, remote net.Addr) (*Conn, error) {
	defer trace("finished negotiate")

	ctx, cancel := context.WithTimeout(l.ctx, handshakeTimeout)
	defer cancel()

	wSock := websocket.NetConn(ctx, wsConn, websocket.MessageBinary)
	defer wSock.Close()

	h, err := newHandshake(l.api, wSock, l.iceServers, l.addr, remote)
	if err != nil {
		return nil, err
	}
	h.pc.OnDataChannel(h.open)

	return h.run(ctx)
}
