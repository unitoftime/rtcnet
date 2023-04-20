package rtcnet

import (
	"fmt"
	"net"
	"time"

	"github.com/pion/webrtc/v3"
)

type Conn struct {
	peerConn *webrtc.PeerConnection
	dataChannel *webrtc.DataChannel
	websocket net.Conn
	readChan chan []byte
	errorChan chan error
	closed bool // TODO - atomic?
}
func newConn(peer *webrtc.PeerConnection, websocket net.Conn) *Conn {
	return &Conn{
		peerConn: peer,
		websocket: websocket,
		readChan: make(chan []byte, 1024), //TODO! - Sizing
		errorChan: make(chan error, 16), //TODO! - Sizing
	}
}

func (c *Conn) Read(b []byte) (int, error) {
	select {
	case err := <-c.errorChan:
		return 0, err // There was some error

		case dat := <- c.readChan:
		if len(dat) > len(b) {
			return 0, fmt.Errorf("message too big") // TODO - Instead of failing, should we just return partial?
		}
		copy(b, dat)
		return len(dat), nil
	}
}

func (c *Conn) Write(b []byte) (int, error) {
	err := c.dataChannel.Send(b)
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

func (c *Conn) Close() error {
	if !c.closed {
		c.closed = true
		err1 := c.dataChannel.Close()
		err2 := c.peerConn.Close()
		err3 := c.websocket.Close()

		close(c.readChan)
		close(c.errorChan)

		if err1 != nil || err2 != nil || err3 != nil {
			return fmt.Errorf("Conn Close Error: datachannel: %s peerconn: %s websocket: %s", err1, err2, err3)
		}
	}
	return nil
}

func (c *Conn) LocalAddr() net.Addr {
	//TODO: implement
	return nil
}

func (c *Conn) RemoteAddr() net.Addr {
	//TODO: implement
	return nil
}

func (c *Conn) SetDeadline(t time.Time) error {
	//TODO: implement
	return nil
}

func (c *Conn) SetReadDeadline(t time.Time) error {
	//TODO: implement
	return nil
}

func (c *Conn) SetWriteDeadline(t time.Time) error {
	//TODO: implement
	return nil
}
