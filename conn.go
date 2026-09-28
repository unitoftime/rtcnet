package rtcnet

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/datachannel"
	"github.com/pion/webrtc/v4"
)

type Conn struct {
	peerConn *webrtc.PeerConnection
	dataChannel *webrtc.DataChannel
	raw datachannel.ReadWriteCloser

	attachMux sync.Mutex // Guards attaching the data channel against a concurrent Close
	closed atomic.Bool

	localAddr, remoteAddr net.Addr
}
func newConn(peer *webrtc.PeerConnection, localAddr, remoteAddr net.Addr) *Conn {
	c := &Conn{
		peerConn: peer,

		localAddr: localAddr,
		remoteAddr: remoteAddr,
	}
	return c
}

// Sets the detached data channel. Returns false if the conn is closed or already has one
func (c *Conn) attach(d *webrtc.DataChannel, raw datachannel.ReadWriteCloser) bool {
	c.attachMux.Lock()
	defer c.attachMux.Unlock()

	if c.closed.Load() || c.raw != nil {
		return false
	}
	c.dataChannel = d
	c.raw = raw
	return true
}

func (c *Conn) Read(b []byte) (int, error) {
	if c.closed.Load() {
		return 0, net.ErrClosed
	}
	return c.raw.Read(b)
}

func (c *Conn) Write(b []byte) (int, error) {
	if c.closed.Load() {
		return 0, net.ErrClosed
	}
	return c.raw.Write(b)
}

func (c *Conn) Close() error {
	c.attachMux.Lock()
	alreadyClosed := c.closed.Swap(true)
	c.attachMux.Unlock()
	if alreadyClosed {
		return nil
	}
	trace("conn: closing: ")

	var err1, err2, err3 error
	if c.dataChannel != nil {
		err1 = c.dataChannel.Close()
	}
	if c.peerConn != nil {
		err2 = c.peerConn.Close()
	}
	if c.raw != nil {
		err3 = c.raw.Close()
	}

	if err1 != nil || err2 != nil || err3 != nil {
		closeErr := errors.Join(errors.New("failed to close: (datachannel, peerconn, raw)"), err1, err2, err3)
		logger.Error().
			Err(closeErr).
			Msg("Closing rtc connection")
		return closeErr
	}
	return nil
}

func (c *Conn) LocalAddr() net.Addr {
	return c.localAddr
}

func (c *Conn) RemoteAddr() net.Addr {
	return c.remoteAddr
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
