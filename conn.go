package rtcnet

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/datachannel"
	"github.com/pion/webrtc/v3"
)

type Conn struct {
	peerConn *webrtc.PeerConnection
	dataChannel *webrtc.DataChannel
	raw datachannel.ReadWriteCloser

	// readChan chan []byte
	errorChan chan error

	closeOnce sync.Once
	closed atomic.Bool
}
func newConn(peer *webrtc.PeerConnection) *Conn {
	c := &Conn{
		peerConn: peer,
		errorChan: make(chan error, 16), //TODO! - Sizing
	}
	return c
}

// For pushing error data out of the webrtc connection into the error buffer
func (c *Conn) pushErrorData(err error) {
	if c.closed.Load() { return } // Skip if we are already closed

	c.errorChan <- err
}


func (c *Conn) Read(b []byte) (int, error) {
	select {
	case err := <-c.errorChan:
		return 0, err // There was some error
	default:
		// Just exit
	}
	return c.raw.Read(b)
}

func (c *Conn) Write(b []byte) (int, error) {
	select {
	case err := <-c.errorChan:
		return 0, err // There was some error
	default:
		// Just exit
	}
	return c.raw.Write(b)
}

func (c *Conn) Close() error {
	var closeErr error
	c.closeOnce.Do(func() {
		trace("conn: closing: ")
		c.closed.Store(true)

		err1 := c.dataChannel.Close()
		err2 := c.peerConn.Close()
		err3 := c.raw.Close()

		close(c.errorChan)

		if err1 != nil || err2 != nil || err3 != nil {
			closeErr = errors.Join(errors.New("failed to close: (datachannel, peerconn, raw)"), err1, err2, err3)
			logger.Error().
				Err(closeErr).
				Msg("Closing rtc connection")
		}
	})
	return closeErr
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
