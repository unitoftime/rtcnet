package rtcnet

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"crypto/tls"
	"encoding/json"

	"github.com/pion/webrtc/v3"
)

type ListenConfig struct {
	TlsConfig *tls.Config
	OriginPatterns []string
}

type Listener struct {
	wsListener *websocketListener
	pendingAccepts chan *Conn // TODO - should this get buffered?
	pendingAcceptErrors chan error // TODO - should this get buffered?
	closed atomic.Bool
}

func NewListener(address string, config ListenConfig) (*Listener, error) {
	wsl, err := newWebsocketListener(address, config)
	if err != nil {
		return nil, err
	}

	rtcListener := &Listener{
		wsListener: wsl,
		pendingAccepts: make(chan *Conn),
		pendingAcceptErrors: make(chan error),
	}

	go func() {
		for {
			wsConn, err := rtcListener.wsListener.Accept()
			if err != nil {
				rtcListener.pendingAcceptErrors <- err
				continue
			}

			if rtcListener.closed.Load() {
				return // If closed then just exit
			}

			// Try and negotiate a webrtc connection for the websocket connection
			go rtcListener.attemptWebRtcNegotiation(wsConn)
		}
	}()

	return rtcListener, nil
}

func (l *Listener) Accept() (*Conn, error) {
	select{
	case conn := <-l.pendingAccepts:
		return conn, nil
	case err := <-l.pendingAcceptErrors:
		return nil, err
	}
}
func (l *Listener) Close() error {
	l.closed.Store(true)
	close(l.pendingAccepts)
	close(l.pendingAcceptErrors)

	return l.wsListener.Close()
}
func (l *Listener) Addr() net.Addr {
	return l.wsListener.Addr()
}

func (l *Listener) attemptWebRtcNegotiation(wsConn net.Conn) {
	var candidatesMux sync.Mutex
	pendingCandidates := make([]*webrtc.ICECandidate, 0)
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			// {
			// 	URLs: []string{"stun:stun.l.google.com:19302"},
			// },
		},
	}

	peerConnection, err := webrtc.NewPeerConnection(config)
	if err != nil {
		l.pendingAcceptErrors <- err
		return
	}

	// When an ICE candidate is available send to the other Pion instance
	// the other Pion instance will add this candidate by calling AddICECandidate
	peerConnection.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return // Do nothing because the ice candidate was nil for some reason
		}

		candidatesMux.Lock()
		defer candidatesMux.Unlock()

		desc := peerConnection.RemoteDescription()
		if desc == nil {
			pendingCandidates = append(pendingCandidates, c)
		} else {
			sigMsg := signalMsg{
				Candidate: &candidateMsg{c.ToJSON()},
			}

			sendMsg(wsConn, sigMsg)
			if err != nil {
				l.pendingAcceptErrors <- fmt.Errorf("OnIceCandidate Send - Possible websocket disconnect: %w", err)
				return
			}
		}
	})

	// Set the handler for Peer connection state
	// This will notify you when the peer has connected/disconnected
	peerConnection.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		trace("Listener: Peer Connection State has changed: ", s.String())

		// if s == webrtc.PeerConnectionStateClosed {
		// 	// This means the webrtc was closed by one side. Just close it on the other side
		// 	// Note: because this is the listen side. I don't think we actually need to close this
		// }

		if s == webrtc.PeerConnectionStateFailed {
			// Wait until PeerConnection has had no network activity for 30 seconds or another failure. It may be reconnected using an ICE Restart.
			// Use webrtc.PeerConnectionStateDisconnected if you are interested in detecting faster timeout.
			// Note that the PeerConnection may come back from PeerConnectionStateDisconnected.
			trace("Listener: Peer Connection has gone to failed")

			// TODO - Do some cancellation
		}
	})

	// Register data channel creation handling
	peerConnection.OnDataChannel(func(d *webrtc.DataChannel) {
		conn := newConn(peerConnection, wsConn)
		conn.dataChannel = d

		// Register channel opening handling
		d.OnOpen(func() {
			printDataChannel(d)

			l.pendingAccepts <- conn
		})

		// Register channel opening handling
		d.OnClose(func() {
			trace("Listener: Data channel was closed!!")
		})

		// Register text message handling
		d.OnMessage(func(msg webrtc.DataChannelMessage) {
			// log.Print("Server: Received Msg from DataChannel", len(msg.Data))
			if msg.IsString {
				trace("Listener: DataChannel OnMessage: Received string message, skipping")
				return
			}
			conn.readChan <- msg.Data
		})
	})

	buf := make([]byte, 8 * 1024)
	for {
		n, err := wsConn.Read(buf)
		if err != nil {
			// TODO: Are there any cases where we might get an error here but its not fatal?
			// Assume the websocket is closed and break
			break
		}

		if n == 0 { continue }

		var msg signalMsg
		err = json.Unmarshal(buf[:n], &msg)
		if err != nil {
			// There was some problem with the unmarshal. Let's just continue looking for another valid message
			continue
		}

		if msg.SDP != nil {
			trace("Listener: RtcSdpMsg")
			sdp := webrtc.SessionDescription{}
			sdp.Type = msg.SDP.Type
			sdp.SDP = msg.SDP.SDP

			err := peerConnection.SetRemoteDescription(sdp)
			if err != nil {
				logErr("Listener: SetRemoteDescription", err)
				l.pendingAcceptErrors <- fmt.Errorf("RtcSdpMsg Recv - Failed to set remote description: %w", err)
				return
			}

			// Create an answer to send to the other process
			answer, err := peerConnection.CreateAnswer(nil)
			if err != nil {
				logErr("Listener: CreateAnswer", err)
				l.pendingAcceptErrors <- fmt.Errorf("RtcSdpMsg Recv - Failed to create answer: %w", err)
				return
			}

			sigMsg := signalMsg{
				SDP: &sdpMsg{ answer.Type, answer.SDP },
			}
			err = sendMsg(wsConn, sigMsg)
			if err != nil {
				logErr("Listener: Websocket Send Answer", err)
				l.pendingAcceptErrors <- fmt.Errorf("RtcSdpMsg Recv - Failed to send SDP answer: %w", err)
				return
			}

			// Sets the LocalDescription, and starts our UDP listeners
			err = peerConnection.SetLocalDescription(answer)
			if err != nil {
				logErr("Listener: SetLocalDescription", err)
				l.pendingAcceptErrors <- fmt.Errorf("RtcSdpMsg Recv - Failed to set local SDP: %w", err)
				return
			}

			candidatesMux.Lock()
			for _, c := range pendingCandidates {
				trace(fmt.Sprintf("Listener: %v", *c))
				sigMsg := signalMsg{
					Candidate: &candidateMsg{c.ToJSON()},
				}
				err := sendMsg(wsConn, sigMsg)
				if err != nil {
					logErr("Listener: Websocket Send Pending Candidate Message", err)
					l.pendingAcceptErrors <- fmt.Errorf("RtcSdpMsg Recv - Failed to send RtcCandidate: %w", err)
					return
				}
			}
			candidatesMux.Unlock()
		} else if msg.Candidate != nil {
			// log.Debug().Msg("Listener: RtcCandidateMsg")
			err := peerConnection.AddICECandidate(msg.Candidate.CandidateInit)
			if err != nil {
				logErr("Listener: AddICECandidate", err)
				l.pendingAcceptErrors <- fmt.Errorf("RtcCandidateMsg Recv - Failed to add candidate: %w", err)
				return
			}
		} else {
			// Warning: no valid message included
			trace("Listener: ws received unknown message: ", string(buf[:n]))
			continue
		}
	}
}

