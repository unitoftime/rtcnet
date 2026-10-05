package rtcnet

import (
	"github.com/pion/webrtc/v4"
)

// Notes: https://webrtcforthecurious.com/docs/01-what-why-and-how/
// Notes: about reliability: https://stackoverflow.com/questions/54292824/webrtc-channel-reliability
// Note: Behind a 1:1 NAT (eg a docker bridge network, or a cloud VM without its public IP on an interface) the listener must advertise its public address, see ListenConfig.PublicIP
// - Read more here: https://stackoverflow.com/questions/32301119/is-ice-necessary-for-client-server-webrtc-applications

// Settings shared by both sides
// Detaching the datachannel: https://github.com/pion/webrtc/tree/master/examples/data-channels-detach
func newSettingEngine() webrtc.SettingEngine {
	s := webrtc.SettingEngine{}
	s.DetachDataChannels()
	return s
}

// Internal messages used for webrtc negotiation/signalling
type signalMsg struct {
	SDP *sdpMsg
	Candidate *candidateMsg
}

type sdpMsg struct {
	Type webrtc.SDPType
	SDP string
}

type candidateMsg struct {
	CandidateInit webrtc.ICECandidateInit
}

