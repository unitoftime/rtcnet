package rtcnet

import (
	"context"
	"crypto/tls"
	"net"

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
