package rtcnet

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"

	"github.com/pion/webrtc/v4"
)

type ListenConfig struct {
	TlsConfig *tls.Config
	OriginPatterns []string
	IceServers []string
	// AllowWebsocketFallback bool // TODO: Restriction?
}

type Listener struct {
	wsListener *websocketListener
	pendingAccepts chan net.Conn // TODO - should this get buffered?
	pendingAcceptErrors chan error // TODO - should this get buffered?
	closed atomic.Bool
	iceServers []string
}

func NewListener(address string, config ListenConfig) (*Listener, error) {
	wsl, err := newWebsocketListener(address, config)
	if err != nil {
		return nil, err
	}

	rtcListener := &Listener{
		wsListener: wsl,
		pendingAccepts: make(chan net.Conn),
		pendingAcceptErrors: make(chan error),
		iceServers: config.IceServers,
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

			fallback, isFallback := wsConn.(wsFallback)
			if isFallback {
				rtcListener.pendingAccepts <- fallback.Conn
			} else {
				// Try and negotiate a webrtc connection for the websocket connection
				go rtcListener.attemptWebRtcNegotiation(wsConn)
			}
		}
	}()

	return rtcListener, nil
}

func (l *Listener) Accept() (net.Conn, error) {
	select{
	case conn := <-l.pendingAccepts:
		return conn, nil
	case err := <-l.pendingAcceptErrors:
		return nil, err
	}
}
func (l *Listener) Close() error {
	l.closed.Store(true)
	// TODO: these aren't channel safe. need to do a boolean check before writing to them.
	close(l.pendingAccepts)
	close(l.pendingAcceptErrors)

	return l.wsListener.Close()
}
func (l *Listener) Addr() net.Addr {
	return l.wsListener.Addr()
}

func (l *Listener) attemptWebRtcNegotiation(wsConn net.Conn) {
	defer trace("finished attemptWebRtcNegotiation")

	localAddr := wsConn.LocalAddr()
	remoteAddr := wsConn.RemoteAddr()

	api := getSettingsEngineApi()

	var candidatesMux sync.Mutex
	pendingCandidates := make([]*webrtc.ICECandidate, 0)
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			// {
			// 	URLs: l.iceServers,
			// },
			// {
			// 	URLs: []string{"stun:stun.l.google.com:19302"},
			// },
		},
	}
	if len(l.iceServers) > 0 {
		config.ICEServers = append(config.ICEServers,
			webrtc.ICEServer{
				URLs: l.iceServers,
			})
	}

	peerConnection, err := api.NewPeerConnection(config)
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

		// logger.Trace().
		// 	Str("Address", c.Address).
		// 	Str("Protocol", c.Protocol.String()).
		// 	Uint16("Port", c.Port).
		// 	Str("CandidateType", c.Typ.String()).
		// 	Str("RelatedAddress", c.RelatedAddress).
		// 	Uint16("RelatedPort", c.RelatedPort).
		// 	Msg("rtcnet: peerConnection.OnICECandidate")

		candidatesMux.Lock()
		defer candidatesMux.Unlock()

		desc := peerConnection.RemoteDescription()
		if desc == nil {
			pendingCandidates = append(pendingCandidates, c)
		} else {
			sigMsg := signalMsg{
				Candidate: &candidateMsg{c.ToJSON()},
			}

			err := sendMsg(wsConn, sigMsg)
			if err != nil {
				l.pendingAcceptErrors <- fmt.Errorf("OnIceCandidate Send - Possible websocket disconnect: %w", err)
				return
			}
		}
	})

	// Set the handler for Peer connection state
	// This will notify you when the peer has connected/disconnected
	peerConnection.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		trace("Listener: Peer Connection State has changed: " + s.String())

		if s == webrtc.PeerConnectionStateClosed {
			// This means the webrtc was closed by one side. Just close it on the other side
			// Note: because this is the listen side. I don't think we actually need to close this
			trace("Listener: Peer Connection has been closed!")
		}

		if s == webrtc.PeerConnectionStateFailed {
			// Wait until PeerConnection has had no network activity for 30 seconds or another failure. It may be reconnected using an ICE Restart.
			// Use webrtc.PeerConnectionStateDisconnected if you are interested in detecting faster timeout.
			// Note that the PeerConnection may come back from PeerConnectionStateDisconnected.
			trace("Listener: Peer Connection has gone to failed")

			// TODO - Do some cancellation
			err := peerConnection.Close()
			if err != nil {
				logger.Error().Err(err).Msg("PeerConnectionStateFailed")
			}
		}
	})

	// Register data channel creation handling
	peerConnection.OnDataChannel(func(d *webrtc.DataChannel) {
		conn := newConn(peerConnection, localAddr, remoteAddr)
		conn.dataChannel = d

		// Register channel opening handling
		d.OnOpen(func() {
			printDataChannel(d)
			wsConn.Close()

			var err error
			conn.raw, err = d.Detach()
			if err != nil {
				l.pendingAcceptErrors <- err
			} else {
				l.pendingAccepts <- conn
			}
		})

		// // Register channel opening handling
		// d.OnClose(func() {
		// 	trace("Listener: Data channel was closed!")
		// })

		// Note: Stopped using this now that I have detached data channels
		// // Register text message handling
		// d.OnMessage(func(msg webrtc.DataChannelMessage) {
		// 	// log.Print("Server: Received Msg from DataChannel", len(msg.Data))
		// 	if msg.IsString {
		// 		trace("Listener: DataChannel OnMessage: Received string message, skipping")
		// 		return
		// 	}
		// 	conn.pushReadData(msg.Data)
		// })
	})

	buf := make([]byte, 8 * 1024) // TODO: hardcoded to be big enough for the signalling messages
	for {
		n, err := wsConn.Read(buf)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				logger.Error().
					Err(err).
					Msg("error reading websocket")
			}
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
				logger.Error().
					Err(err).
					Msg("Listener: SetRemoteDescription")
				l.pendingAcceptErrors <- fmt.Errorf("RtcSdpMsg Recv - Failed to set remote description: %w", err)
				return
			}

			// Create an answer to send to the other process
			answer, err := peerConnection.CreateAnswer(nil)
			if err != nil {
				logger.Error().
					Err(err).
					Msg("Listener: CreateAnswer")
				l.pendingAcceptErrors <- fmt.Errorf("RtcSdpMsg Recv - Failed to create answer: %w", err)
				return
			}

			sigMsg := signalMsg{
				SDP: &sdpMsg{ answer.Type, answer.SDP },
			}
			err = sendMsg(wsConn, sigMsg)
			if err != nil {
				logger.Error().
					Err(err).
					Msg("Listener: Websocket Send Answer")
				l.pendingAcceptErrors <- fmt.Errorf("RtcSdpMsg Recv - Failed to send SDP answer: %w", err)
				return
			}

			// Sets the LocalDescription, and starts our UDP listeners
			err = peerConnection.SetLocalDescription(answer)
			if err != nil {
				logger.Error().
					Err(err).
					Msg("Listener: SetLocalDescription")
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
					logger.Error().
						Err(err).
						Msg("Listener: Websocket Send Pending Candidate Message")
					l.pendingAcceptErrors <- fmt.Errorf("RtcSdpMsg Recv - Failed to send RtcCandidate: %w", err)
					return
				}
			}
			candidatesMux.Unlock()
		} else if msg.Candidate != nil {
			// log.Debug().Msg("Listener: RtcCandidateMsg")
			err := peerConnection.AddICECandidate(msg.Candidate.CandidateInit)
			if err != nil {
				logger.Error().
					Err(err).
					Msg("Listener: AddICECandidate")
				l.pendingAcceptErrors <- fmt.Errorf("RtcCandidateMsg Recv - Failed to add candidate: %w", err)
				return
			}
		} else {
			// Warning: no valid message included
			trace("Listener: ws received unknown message: " + string(buf[:n]))
			continue
		}
	}
}

