// +build js

package rtcnet

import (
	"time"
	"context"
	"crypto/tls"

	"nhooyr.io/websocket"
)

// Note: You cant inject tlsConfig here, you are required to use the tlsConfiguration as defined by the browser.
func dialWs(ctx context.Context, url string, tlsConfig *tls.Config) (*websocket.Conn, error) {
	wsConn, _, err := websocket.Dial(ctx, url, nil)
	return wsConn, err
}
