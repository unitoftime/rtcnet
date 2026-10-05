//go:build js
// +build js

package rtcnet

import (
	"errors"
	"io"
	"net"

	"github.com/pion/webrtc/v4"
)

func newListenerAPI(addr *net.TCPAddr, config ListenConfig) (*webrtc.API, io.Closer, error) {
	return nil, nil, errors.New("rtcnet: listening is not supported in the browser")
}
