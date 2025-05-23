package rtcnet

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// Returns a connected socket or fails with an error
func dialWebsocket(address string, tlsConfig *tls.Config, ctx context.Context) (net.Conn, error) {
	// ctx, _ := context.WithTimeout(context.Background(), 10 * time.Second)

	url := "wss://" + address
	wsConn, err := dialWs(ctx, url, tlsConfig)
	if err != nil {
		return nil, err
	}

	// Note: The entire websocket net.Conn lifetime is managed by the context too
	// ctx, cancel := context.WithCancel(context.Background())
	conn := websocket.NetConn(ctx, wsConn, websocket.MessageBinary)

	return conn, nil
}

// --------------------------------------------------------------------------------
// - Listener
// --------------------------------------------------------------------------------
type websocketListener struct {
	httpServer *http.Server
	originPatterns []string
	addr net.Addr
	// encoder Serdes
	// decoder Serdes
	closed atomic.Bool
	pendingAccepts chan net.Conn // TODO - should this get buffered?
	pendingAcceptErrors chan error // TODO - should this get buffered?
}

func newWebsocketListener(address string, config ListenConfig) (*websocketListener, error) {
	// TODO - Is tcp always correct here?
	listener, err := tls.Listen("tcp", address, config.TlsConfig)
	if err != nil {
		return nil, err
	}

	wsl := &websocketListener{
		addr: listener.Addr(),
		pendingAccepts: make(chan net.Conn),
		pendingAcceptErrors: make(chan error),
		originPatterns: config.OriginPatterns,
		httpServer: &http.Server{
			TLSConfig: config.TlsConfig,
			ReadTimeout: 10 * time.Second,
			WriteTimeout: 10 * time.Second,
		},
	}

	// httpServer := c.HttpServer
	wsl.httpServer.Handler = wsl

	go func() {
		for {
			err := wsl.httpServer.Serve(listener)
			// ErrServerClosed is returned when shutdown or close is called
			if errors.Is(err, http.ErrServerClosed) {
				return // Just close if the server is shutdown or closed
			} else if wsl.closed.Load() {
				return // Else if closed then just exit
			}

			// TODO - Passing serve errors back through the accept channel. This might be a slightly leaky abstraction. Because these are server errors not really accept errors.
			wsl.pendingAcceptErrors <- err

			time.Sleep(1 * time.Second)
		}
	}()

	return wsl, nil
}

type wsFallback struct {
	net.Conn
}

func (l *websocketListener) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	wsConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: l.originPatterns,
	})
	if err != nil {
		// Return as an accept error
		l.pendingAcceptErrors <- err
		return
	}

	fallback := false
	if r.URL != nil {
		if r.URL.Path == "/wss" {
			logger.Warn().Msg("Dialer requested wss fallback socket!")
			fallback = true
		}
	}

	// Build the net.Conn and push to the channel
	if fallback {
		ctx := context.Background() // Note: This has to be background if it is a fallback path
		conn := websocket.NetConn(ctx, wsConn, websocket.MessageBinary)
		conn = wsFallback{conn}
		l.pendingAccepts <- conn
	} else {
		// TODO: make timeout configurable?
		ctx, _ := context.WithTimeout(context.Background(), 30 * time.Second)
		conn := websocket.NetConn(ctx, wsConn, websocket.MessageBinary)
		l.pendingAccepts <- conn
	}
}

func (l *websocketListener) Accept() (net.Conn, error) {
	select{
	case sock := <-l.pendingAccepts:
		return sock, nil
	case err := <-l.pendingAcceptErrors:
		return nil, err
	}
}
func (l *websocketListener) Close() error {
	l.closed.Store(true)
	close(l.pendingAccepts)
	close(l.pendingAcceptErrors)

	ctx, cancel := context.WithTimeout(context.Background(), 10 * time.Second)
	defer cancel()
	return l.httpServer.Shutdown(ctx)
}
func (l *websocketListener) Addr() net.Addr {
	return l.addr
}
