package rtcnet

import (
	"fmt"
	"net"
	"sync"
	"crypto/tls"
	"encoding/json"

	"github.com/pion/webrtc/v3"
)

func Dial(address string, tlsConfig *tls.Config) (*Conn, error) {
	wSock, err := dialWebsocket(address, tlsConfig)
	if err != nil {
		return nil, err
	}

	// Offer WebRtc Upgrade
	var candidatesMux sync.Mutex
	pendingCandidates := make([]*webrtc.ICECandidate, 0)

	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			// {
			// 	URLs: []string{"stun:stun.l.google.com:19302"},
			// },
		},
	}

	trace("Dial: Starting WebRTC negotiation")

	peerConnection, err := webrtc.NewPeerConnection(config)
	if err != nil {
		logErr("Dial: NewPeerConnection", err)
		return nil, err
	}

	conn := newConn(peerConnection, wSock)
	connFinish := make(chan bool)
	peerConnection.OnICECandidate(func(c *webrtc.ICECandidate) {
		// log.Debug().Msg("Dial: OnICECandidate")
		if c == nil {
			return
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
			err := sendMsg(conn.websocket, sigMsg)
			if err != nil {
				logErr("Dial: Receive Peer OnIceCandidate", err)
				if !conn.closed {
					conn.errorChan <- err
				}
				return
			}
		}
	})

	go func() {
		buf := make([]byte, 8 * 1024)
		for {
			n, err := conn.websocket.Read(buf)
			if err != nil {
				// TODO: Are there any cases where we might get an error here but its not fatal?
				// Assume the websocket is closed and break
				return
			}

			if n == 0 { continue }

			var msg signalMsg
			err = json.Unmarshal(buf[:n], &msg)
			if err != nil {
				// There was some problem with the unmarshal. Let's just continue looking for another valid message
				continue
			}

			if msg.SDP != nil {
				trace("Dial: RtcSdpMsg")
				sdp := webrtc.SessionDescription{}
				sdp.Type = msg.SDP.Type
				sdp.SDP = msg.SDP.SDP

				err := peerConnection.SetRemoteDescription(sdp)
				if err != nil {
					logErr("Dial: SetRemoteDescription", err)
					if !conn.closed {
						conn.errorChan <- err
					}
					return
				}

				candidatesMux.Lock()
				defer candidatesMux.Unlock()

				for _, c := range pendingCandidates {
					trace(fmt.Sprintf("Dial: %v", *c))
					sigMsg := signalMsg{
						Candidate: &candidateMsg{c.ToJSON()},
					}
					err := sendMsg(conn.websocket, sigMsg)
					if err != nil {
						logErr("Dial: Failed Websocket Send: Pending Candidate Msg", err)
						if !conn.closed {
							conn.errorChan <- err
						}
						return
					}
				}

			} else if msg.Candidate != nil {
				trace("Dial: RtcCandidateMsg")
				err := peerConnection.AddICECandidate(msg.Candidate.CandidateInit)
				if err != nil {
					logErr("Dial: AddIceCandidate", err)
					if !conn.closed {
						conn.errorChan <- err
					}
					return
				}
			} else {
				// Warning: no valid message included
				trace("Dial: ws received unknown message ", string(buf[:n]))
				continue
			}
		}
	}()

	// Create a datachannel with label 'data'
	ordered := false
	// maxRetransmits := uint16(0)
	dataChannelOptions := webrtc.DataChannelInit{
		Ordered: &ordered,
    // MaxRetransmits: &maxRetransmits,
	}
	dataChannel, err := peerConnection.CreateDataChannel("data", &dataChannelOptions)
	if err != nil {
		logErr("Dial: CreateDataChannel", err)
		return nil, err
	}


	// Set the handler for Peer connection state
	// This will notify you when the peer has connected/disconnected
	peerConnection.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		trace("Dial: Peer Connection State has changed: ", s.String())

		// if s == webrtc.PeerConnectionStateClosed {
		// 	// This means the webrtc was closed by one side. Just close it on the other side
		// 	sock.Close()
		// }

		if s == webrtc.PeerConnectionStateFailed {
			trace("Dial: PeerConnectionStateFailed", nil)

			// Wait until PeerConnection has had no network activity for 30 seconds or another failure. It may be reconnected using an ICE Restart.
			// Use webrtc.PeerConnectionStateDisconnected if you are interested in detecting faster timeout.
			// Note that the PeerConnection may come back from PeerConnectionStateDisconnected.

			conn.errorChan <- fmt.Errorf("Peer Connection has gone to failed")
		} else if s == webrtc.PeerConnectionStateDisconnected {
			trace("Dial: PeerConnectionStateDisconnected")
			// conn.errorChan <- fmt.Errorf("Peer Connection has gone to disconnected")
		}
	})

	// Register channel opening handling
	dataChannel.OnOpen(func() {
		printDataChannel(dataChannel)
		trace("Dial: Data Channel OnOpen")
		conn.dataChannel = dataChannel
		connFinish <- true
	})

	// Register text message handling
	dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
		// log.Print("Client: Received Msg from DataChannel", len(msg.Data))
		if msg.IsString {
			trace("Dial: DataChannel OnMessage: Received string message, skipping")
			return
		}
		conn.readChan <- msg.Data
	})

	// Create an offer to send to the other process
	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		logErr("Dial: CreateOffer", err)
		return nil, err
	}
	// fmt.Println("CreateOffer")

	// Sets the LocalDescription, and starts our UDP listeners
	// Note: this will start the gathering of ICE candidates
	err = peerConnection.SetLocalDescription(offer)
	if err != nil {
		logErr("Dial: SetLocalDescription", err)
		return nil, err
	}
	// fmt.Println("SetLocalDesc")

	sigMsg := signalMsg{
		SDP: &sdpMsg{ offer.Type, offer.SDP },
	}
	err = sendMsg(conn.websocket, sigMsg)
	if err != nil {
		logErr("Dial: websocket.Send RtcSdp Offer", err)
		return nil, err
	}

	// Wait until the webrtc connection is finished getting setup
	select {
	case err := <-conn.errorChan:
		logErr("Dial: error exit", err)
		return nil, err // There was an error in setup
	case <-connFinish:
		trace("Dial: normal exit")
		// Socket finished getting setup
		return conn, nil
	}
}

func sendMsg(conn net.Conn, msg signalMsg) error {
	// log.Print("sendMsg: ", msg)
	msgDat, err := json.Marshal(msg)
	if err != nil {
		logErr("sendMsg: Marshal", err)
		return err
	}

	// log.Print("sendMsg: Marshalled: ", string(msgDat))

	_, err = conn.Write(msgDat)
	if err != nil {
		logErr("sendMsg: conn write", err)
		return err
	}

	return nil
}

func printDataChannel(d *webrtc.DataChannel) {
	trace(fmt.Sprintf(" Label : %v \n ID: %v \n MaxPacketLifeTime: %v \n MaxRetransmits: %v \n Negotiated: %v \n Ordered: %v \n Protocol: %s \n ReadyState: %v",
		d.Label(), d.ID(), d.MaxPacketLifeTime(), d.MaxRetransmits(), d.Negotiated(), d.Ordered(), d.Protocol(), d.ReadyState()),
	)
	// t := d.Transport()
	// log.Print(fmt.Sprintf(" Transport: 
}
