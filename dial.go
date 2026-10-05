package rtcnet

import (
	"context"
	"crypto/tls"
	"time"

	"github.com/pion/webrtc/v4"
)

func Dial(address string, tlsConfig *tls.Config, ordered bool, iceServers []string) (*Conn, error) {
	dialCtx, cancel := context.WithTimeout(context.Background(), 10 * time.Second) // TODO: pass in timeout
	defer cancel()

	// Note: The websocket only carries signaling, so it is closed once Dial returns
	wSock, err := dialWebsocket(address, tlsConfig, dialCtx)
	if err != nil {
		return nil, err
	}
	defer wSock.Close()

	trace("Dial: Starting WebRTC negotiation")
	api := webrtc.NewAPI(webrtc.WithSettingEngine(newSettingEngine()))
	h, err := newHandshake(api, wSock, iceServers)
	if err != nil {
		return nil, err
	}

	err = h.offer(ordered)
	if err != nil {
		h.conn.Close()
		return nil, err
	}

	return h.run(dialCtx)
}
