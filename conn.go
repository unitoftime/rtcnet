package rtcnet

import (
	"fmt"
	"net"
	"time"
	"sync"
	"sync/atomic"
	"errors"

	"github.com/pion/webrtc/v3"
)

type Conn struct {
	peerConn *webrtc.PeerConnection
	dataChannel *webrtc.DataChannel
	readChan chan []byte
	errorChan chan error

	closeOnce sync.Once
	closed atomic.Bool
}
func newConn(peer *webrtc.PeerConnection) *Conn {
	return &Conn{
		peerConn: peer,
		readChan: make(chan []byte, 1024), //TODO! - Sizing
		errorChan: make(chan error, 16), //TODO! - Sizing
	}
}

// For pushing read data out of the datachannel and into the read buffer
func (c *Conn) pushReadData(dat []byte) {
	if c.closed.Load() { return } // Skip if we are already closed

	c.readChan <- dat
}

// For pushing error data out of the webrtc connection into the error buffer
func (c *Conn) pushErrorData(err error) {
	if c.closed.Load() { return } // Skip if we are already closed

	c.errorChan <- err
}


func (c *Conn) Read(b []byte) (int, error) {
	select {
	case err, ok := <-c.errorChan:
		if !ok {
			return 0, net.ErrClosed
		}
		return 0, err // There was some error

		case dat, ok := <- c.readChan:
		if !ok {
			return 0, net.ErrClosed
		}
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
	var closeErr error
	c.closeOnce.Do(func() {
		trace("conn: closing")
		c.closed.Store(true)

		err1 := c.dataChannel.Close()
		err2 := c.peerConn.Close()

		close(c.readChan)
		close(c.errorChan)

		if err1 != nil || err2 != nil {
			closeErr = errors.Join(errors.New("failed to close: (datachannel, peerconn)"), err1, err2)
			logErr("conn: closing error: ", closeErr)
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
