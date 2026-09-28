package rtcnet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/pion/webrtc/v4"
)

// Upper bound on everything the peer can send during a handshake. The SDP is ~1KB and each candidate ~200B
const maxSignalingBytes = 64 * 1024

// handshake negotiates a single webrtc data channel over a websocket signaling connection.
// Both sides run the same exchange: the dialer sends an offer, the listener answers it, and both trickle ICE candidates until the data channel opens.
type handshake struct {
	ws   net.Conn
	pc   *webrtc.PeerConnection
	conn *Conn

	candidatesMux     sync.Mutex
	pendingCandidates []webrtc.ICECandidateInit // Held until the remote description is set, so the peer can apply them

	ready chan struct{} // Closed once the data channel is open and attached to conn
	errc  chan error    // Holds the first failure, later ones are dropped
}

func newHandshake(api *webrtc.API, ws net.Conn, iceServers []string) (*handshake, error) {
	config := webrtc.Configuration{}
	if len(iceServers) > 0 {
		config.ICEServers = []webrtc.ICEServer{{URLs: iceServers}}
	}

	pc, err := api.NewPeerConnection(config)
	if err != nil {
		return nil, err
	}

	h := &handshake{
		ws:    ws,
		pc:    pc,
		conn:  newConn(pc, ws.LocalAddr(), ws.RemoteAddr()),
		ready: make(chan struct{}),
		errc:  make(chan error, 1),
	}

	pc.OnICECandidate(h.onICECandidate)
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		trace("Peer Connection State has changed: " + s.String())

		// Note: Disconnected can recover on its own, but Failed and Closed are terminal
		if s == webrtc.PeerConnectionStateFailed || s == webrtc.PeerConnectionStateClosed {
			h.fail(fmt.Errorf("rtcnet: peer connection %s", s))
			h.conn.Close()
		}
	})

	return h, nil
}

// Creates the data channel and sends the offer that starts negotiation. Only the dialer calls this
func (h *handshake) offer(ordered bool) error {
	dataChannel, err := h.pc.CreateDataChannel("data", &webrtc.DataChannelInit{
		Ordered: &ordered,
	})
	if err != nil {
		return err
	}
	h.open(dataChannel)

	offer, err := h.pc.CreateOffer(nil)
	if err != nil {
		return err
	}

	// Note: this starts ICE gathering, candidates are held until the answer arrives
	err = h.pc.SetLocalDescription(offer)
	if err != nil {
		return err
	}

	return sendMsg(h.ws, signalMsg{
		SDP: &sdpMsg{offer.Type, offer.SDP},
	})
}

// Answers a remote offer. Only the listener calls this
func (h *handshake) answer() error {
	answer, err := h.pc.CreateAnswer(nil)
	if err != nil {
		return err
	}

	// Send before SetLocalDescription starts ICE gathering, so the answer reaches the dialer ahead of any trickled candidates
	err = sendMsg(h.ws, signalMsg{
		SDP: &sdpMsg{answer.Type, answer.SDP},
	})
	if err != nil {
		return err
	}

	return h.pc.SetLocalDescription(answer)
}

// Completes the handshake once the data channel opens
func (h *handshake) open(d *webrtc.DataChannel) {
	d.OnOpen(func() {
		printDataChannel(d)

		raw, err := d.Detach()
		if err != nil {
			h.fail(err)
			return
		}

		// Only the first data channel is used, and none are attached once the conn has been closed
		if !h.conn.attach(d, raw) {
			raw.Close()
			return
		}
		close(h.ready)
	})
}

// Waits until the data channel opens, negotiation fails, or ctx is done. On failure the peer connection is closed
func (h *handshake) run(ctx context.Context) (*Conn, error) {
	go h.readSignals()

	var err error
	select {
	case <-h.ready:
		return h.conn, nil
	case err = <-h.errc:
	case <-ctx.Done():
		err = context.Cause(ctx)
	}

	h.conn.Close()
	return nil, err
}

func (h *handshake) fail(err error) {
	select {
	case h.errc <- err:
	default:
	}
}

// Applies signaling messages from the peer until the websocket closes
func (h *handshake) readSignals() {
	// Note: the decoder reassembles messages that span multiple websocket reads. The limit keeps a peer from growing its buffer without bound
	dec := json.NewDecoder(io.LimitReader(h.ws, maxSignalingBytes))
	for {
		var msg signalMsg
		err := dec.Decode(&msg)
		if err != nil {
			h.signalingLost(err)
			return
		}

		err = h.handleSignal(msg)
		if err != nil {
			h.fail(fmt.Errorf("rtcnet: signaling: %w", err))
			return
		}
	}
}

// The peer closes signaling as soon as its end of the data channel opens, which can be before ours does.
// Once the SDP exchange is done the peer connection can finish without signaling, so losing the websocket is only fatal before that.
// After it, ICE failure or the handshake timeout bounds the negotiation
func (h *handshake) signalingLost(err error) {
	if h.pc.RemoteDescription() == nil {
		h.fail(fmt.Errorf("rtcnet: signaling: %w", err))
		return
	}
	trace("handshake: signaling closed: " + err.Error())
}

func (h *handshake) handleSignal(msg signalMsg) error {
	if msg.SDP != nil {
		trace("handshake: RtcSdpMsg")
		err := h.pc.SetRemoteDescription(webrtc.SessionDescription{
			Type: msg.SDP.Type,
			SDP:  msg.SDP.SDP,
		})
		if err != nil {
			return err
		}

		if msg.SDP.Type == webrtc.SDPTypeOffer {
			err = h.answer()
			if err != nil {
				return err
			}
		}
		h.flushCandidates()
		return nil
	}

	if msg.Candidate != nil {
		trace("handshake: RtcCandidateMsg")
		return h.pc.AddICECandidate(msg.Candidate.CandidateInit)
	}

	trace("handshake: ws received empty signaling message")
	return nil
}

func (h *handshake) onICECandidate(c *webrtc.ICECandidate) {
	if c == nil {
		return // Gathering is complete
	}

	h.candidatesMux.Lock()
	defer h.candidatesMux.Unlock()

	if h.pc.RemoteDescription() == nil {
		h.pendingCandidates = append(h.pendingCandidates, c.ToJSON())
		return
	}
	h.sendCandidate(c.ToJSON())
}

func (h *handshake) flushCandidates() {
	h.candidatesMux.Lock()
	defer h.candidatesMux.Unlock()

	for _, c := range h.pendingCandidates {
		if !h.sendCandidate(c) {
			break
		}
	}
	h.pendingCandidates = nil
}

// Candidates are best effort, a late one can be gathered after the peer has already closed signaling
func (h *handshake) sendCandidate(c webrtc.ICECandidateInit) bool {
	err := sendMsg(h.ws, signalMsg{
		Candidate: &candidateMsg{c},
	})
	if err != nil {
		h.signalingLost(err)
		return false
	}
	return true
}

func sendMsg(conn net.Conn, msg signalMsg) error {
	msgDat, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	_, err = conn.Write(msgDat)
	return err
}

func printDataChannel(d *webrtc.DataChannel) {
	trace(fmt.Sprintf(" Label : %v \n ID: %v \n MaxPacketLifeTime: %v \n MaxRetransmits: %v \n Negotiated: %v \n Ordered: %v \n Protocol: %s \n ReadyState: %v",
		d.Label(), d.ID(), d.MaxPacketLifeTime(), d.MaxRetransmits(), d.Negotiated(), d.Ordered(), d.Protocol(), d.ReadyState()),
	)
}
