package rtcnet

import (
	"github.com/pion/webrtc/v3"

	"log"
)

// Notes: https://webrtcforthecurious.com/docs/01-what-why-and-how/
// Notes: about reliability: https://stackoverflow.com/questions/54292824/webrtc-channel-reliability
// TODO - Investigate Detaching the datachannel: https://github.com/pion/webrtc/tree/master/examples/data-channels-detach
// TODO - Let stun server list be injectable

// TODO - Interesting note: if you run this in a docker container with the networking set to something other than "host" (ie the default is bridge), then what happens is the docker container gets NAT'ed behind the original host which causes you to need an ICE server. So You must run this code with HOST networking!!!
// - Read more here: https://stackoverflow.com/questions/32301119/is-ice-necessary-for-client-server-webrtc-applications
// - and here: https://forums.docker.com/t/connect-container-without-nat/54783
// - TODO - There might be some way to avoid this with: https://pkg.go.dev/github.com/pion/webrtc/v3#SettingEngine.SetNAT1To1IPs
// - TODO - also this: https://pkg.go.dev/github.com/pion/webrtc/v3#SettingEngine.SetICEUDPMux

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

// Logger
type Logger interface {
	Log(...any)
	LogErr(string, error)
}

var DefaultLogger Logger

// func init() {
// 	UseDefaultLogger()
// }

// A basic default logger implementation
type dLog struct {
}
func UseDefaultLogger() {
	DefaultLogger = dLog{}
}
func (l dLog) Log(msg ...any) {
	log.Println(msg...)
}

func (l dLog) LogErr(msg string, err error) {
	log.Printf("%s: %w", msg, err)
}

// Actual log functions
func trace(msg ...any) {
	if DefaultLogger != nil {
		DefaultLogger.Log(msg...)
	}
}

func logErr(msg string, err error) {
	if DefaultLogger != nil {
		DefaultLogger.LogErr(msg, err)
	}
}
