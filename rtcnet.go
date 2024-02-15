package rtcnet

import (
	"github.com/pion/webrtc/v3"
)

// Notes: https://webrtcforthecurious.com/docs/01-what-why-and-how/
// Notes: about reliability: https://stackoverflow.com/questions/54292824/webrtc-channel-reliability
// TODO - Let stun server list be injectable

// TODO - Interesting note: if you run this in a docker container with the networking set to something other than "host" (ie the default is bridge), then what happens is the docker container gets NAT'ed behind the original host which causes you to need an ICE server. So You must run this code with HOST networking!!!
// - Read more here: https://stackoverflow.com/questions/32301119/is-ice-necessary-for-client-server-webrtc-applications
// - and here: https://forums.docker.com/t/connect-container-without-nat/54783
// - TODO - There might be some way to avoid this with: https://pkg.go.dev/github.com/pion/webrtc/v3#SettingEngine.SetNAT1To1IPs
// - TODO - also this: https://pkg.go.dev/github.com/pion/webrtc/v3#SettingEngine.SetICEUDPMux

// Current settings engine settings
// Detaching the datachannel: https://github.com/pion/webrtc/tree/master/examples/data-channels-detach
func getSettingsEngineApi() *webrtc.API {
	s := webrtc.SettingEngine{}
	s.DetachDataChannels()
	return webrtc.NewAPI(webrtc.WithSettingEngine(s))
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

