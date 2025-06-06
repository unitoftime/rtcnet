package rtcnet

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

func Dial(address string, tlsConfig *tls.Config, ordered bool, iceServers []string) (*Conn, error) {
	dialCtx, cancel := context.WithTimeout(context.Background(), 10 * time.Second) // TODO: pass in timeout
	defer cancel()

	wSock, err := dialWebsocket(address, tlsConfig, dialCtx)
	if err != nil {
		return nil, err
	}
	defer wSock.Close()

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
	if len(iceServers) > 0 {
		config.ICEServers = append(config.ICEServers,
			webrtc.ICEServer{
				URLs: iceServers,
			})
	}
	trace("Dial: Starting WebRTC negotiation")

	api := getSettingsEngineApi()

	peerConnection, err := api.NewPeerConnection(config)
	if err != nil {
		logger.Error().
			Err(err).
			Msg("Dial: NewPeerConnection")
		return nil, err
	}

	conn := newConn(peerConnection, wSock.LocalAddr(), wSock.RemoteAddr())
	connFinish := make(chan bool)
	peerConnection.OnICECandidate(func(c *webrtc.ICECandidate) {
		logger.Trace().Msg("Dial: peerConnection.OnICECandidate")
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
			err := sendMsg(wSock, sigMsg)
			if err != nil {
				logger.Error().
					Err(err).
					Msg("Dial: Receive Peer OnIceCandidate")
				conn.pushErrorData(err)
				return
			}
		}
	})

	go func() {
		buf := make([]byte, 8 * 1024)
		for {
			n, err := wSock.Read(buf)
			if err != nil {
				// TODO: Are there any cases where we might get an error here but its not fatal?
				// Assume the websocket is closed and break
				logger.Error().
					Err(err).
					Msg("Failed to read from websocket")

				// TODO: We don't want this to cause an error, if it closed for normal reasons. Else we do want it to cause an error
				// conn.pushErrorData(err)
				return
			}

			if n == 0 { continue }

			var msg signalMsg
			err = json.Unmarshal(buf[:n], &msg)
			if err != nil {
				// There was some problem with the unmarshal. Let's just continue looking for another valid message
				logger.Error().
					Err(err).
					Msg("Failed to unmarshal signalling message")
				continue
			}

			if msg.SDP != nil {
				trace("Dial: RtcSdpMsg")
				sdp := webrtc.SessionDescription{}
				sdp.Type = msg.SDP.Type
				sdp.SDP = msg.SDP.SDP

				err := peerConnection.SetRemoteDescription(sdp)
				if err != nil {
					logger.Error().
						Err(err).
						Msg("Dial: SetRemoteDescription")
					conn.pushErrorData(err)
					return
				}

				// Warning: Be very careful with canadidatesMux
				{
					candidatesMux.Lock()
					for _, c := range pendingCandidates {
						trace(fmt.Sprintf("Dial: pendingCandidates: %v", *c))
						sigMsg := signalMsg{
							Candidate: &candidateMsg{c.ToJSON()},
						}
						err := sendMsg(wSock, sigMsg)
						if err != nil {
							logger.Error().
								Err(err).
								Msg("Dial: Failed Websocket Send: Pending Candidate Msg")
							conn.pushErrorData(err)
							candidatesMux.Unlock()
							return
						}
					}
					candidatesMux.Unlock()
				}

			} else if msg.Candidate != nil {
				trace("Dial: RtcCandidateMsg")
				err := peerConnection.AddICECandidate(msg.Candidate.CandidateInit)
				if err != nil {
					logger.Error().
						Err(err).
						Msg("Dial: AddIceCandidate")
					conn.pushErrorData(err)
					return
				}
			} else {
				// Warning: no valid message included
				trace("Dial: ws received unknown message " + string(buf[:n]))
				continue
			}
		}
	}()

	// Create a datachannel with label 'data'
	// maxRetransmits := uint16(0)
	dataChannelOptions := webrtc.DataChannelInit{
		Ordered: &ordered,
    // MaxRetransmits: &maxRetransmits,
	}
	dataChannel, err := peerConnection.CreateDataChannel("data", &dataChannelOptions)
	if err != nil {
		logger.Error().
			Err(err).
			Msg("Dial: CreateDataChannel")
		return nil, err
	}


	// Set the handler for Peer connection state
	// This will notify you when the peer has connected/disconnected
	peerConnection.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		trace("Dial: Peer Connection State has changed: " + s.String())

		if s == webrtc.PeerConnectionStateClosed {
			trace("Dial: webrtc.PeerConnectionStateClosed")
			// This means the webrtc was closed by one side. Just close it on the other side
			conn.Close()
		}

		if s == webrtc.PeerConnectionStateFailed {
			trace("Dial: PeerConnectionStateFailed")

			// Wait until PeerConnection has had no network activity for 30 seconds or another failure. It may be reconnected using an ICE Restart.
			// Use webrtc.PeerConnectionStateDisconnected if you are interested in detecting faster timeout.
			// Note that the PeerConnection may come back from PeerConnectionStateDisconnected.

			// conn.pushErrorData(fmt.Errorf("Peer Connection has gone to failed"))
			conn.Close()
		} else if s == webrtc.PeerConnectionStateDisconnected {
			trace("Dial: PeerConnectionStateDisconnected")
			// conn.errorChan <- fmt.Errorf("Peer Connection has gone to disconnected")
		}
	})

	// Register channel opening handling
	dataChannel.OnOpen(func() {
		printDataChannel(dataChannel)
		trace("Dial: Data Channel OnOpen")

		detached, err := dataChannel.Detach()
		if err != nil {
			conn.pushErrorData(err)
		} else {
			conn.raw = detached
			conn.dataChannel = dataChannel
			connFinish <- true
		}
	})

	// Note: Stopped using this now that I have detached data channels
	// // Register text message handling
	// dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
	// 	// log.Print("Client: Received Msg from DataChannel", len(msg.Data))
	// 	if msg.IsString {
	// 		trace("Dial: DataChannel OnMessage: Received string message, skipping")
	// 		return
	// 	}
	// 	conn.pushReadData(msg.Data)
	// })

	// Create an offer to send to the other process
	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		logger.Error().
			Err(err).
			Msg("Dial: CreateOffer")
		return nil, err
	}
	// fmt.Println("CreateOffer")

	// Sets the LocalDescription, and starts our UDP listeners
	// Note: this will start the gathering of ICE candidates
	err = peerConnection.SetLocalDescription(offer)
	if err != nil {
		logger.Error().
			Err(err).
			Msg("Dial: SetLocalDescription")
		return nil, err
	}
	// fmt.Println("SetLocalDesc")

	sigMsg := signalMsg{
		SDP: &sdpMsg{ offer.Type, offer.SDP },
	}
	err = sendMsg(wSock, sigMsg)
	if err != nil {
		logger.Error().
			Err(err).
			Msg("Dial: websocket.Send RtcSdp Offer")
		return nil, err
	}

	// Wait until the webrtc connection is finished getting setup
	select {
	case <-dialCtx.Done():
		logger.Error().
			Err(err).
			Msg("Dial: context Done")
		return nil, dialCtx.Err()
	case err := <-conn.errorChan:
		logger.Error().
			Err(err).
			Msg("Dial: error exit")
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
		logger.Error().
			Err(err).
			Msg("sendMsg: Marshal")
		return err
	}

	// log.Print("sendMsg: Marshalled: ", string(msgDat))

	_, err = conn.Write(msgDat)
	if err != nil {
		logger.Error().
			Err(err).
			Msg("sendMsg: conn write")
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
