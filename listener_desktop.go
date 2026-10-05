//go:build !js
// +build !js

package rtcnet

import (
	"fmt"
	"io"
	"net"

	"github.com/pion/ice/v4"
	"github.com/pion/webrtc/v4"
)

// Every peer connection shares one UDP port, the same port number as the websocket listener
func newListenerAPI(addr *net.TCPAddr, config ListenConfig) (*webrtc.API, io.Closer, error) {
	s := newSettingEngine()
	s.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	s.SetLite(config.IceLite)

	if config.PublicIP != "" {
		ip := net.ParseIP(config.PublicIP)
		if ip == nil || ip.To4() == nil {
			return nil, nil, fmt.Errorf("rtcnet: PublicIP must be an IPv4 address, got %q", config.PublicIP)
		}

		// Advertise the public address in place of every local one
		// Note: Deprecated in pion v4.2 for SetICEAddressRewriteRules, which doesn't exist in the v4.1 this module requires
		s.SetNAT1To1IPs([]string{config.PublicIP}, webrtc.ICECandidateTypeHost)
	}

	udpMux, err := listenUDPMux(addr)
	if err != nil {
		return nil, nil, err
	}
	s.SetICEUDPMux(udpMux)

	return webrtc.NewAPI(webrtc.WithSettingEngine(s)), udpMux, nil
}

// Binds one socket per matching local IPv4 address, all on the same port.
// Note: A single wildcard socket would let the kernel pick reply addresses that don't match the advertised candidates on multi-homed hosts
func listenUDPMux(addr *net.TCPAddr) (ice.UDPMux, error) {
	opts := []ice.UDPMuxFromPortOption{
		ice.UDPMuxFromPortWithNetworks(ice.NetworkTypeUDP4),
	}
	if len(addr.IP) > 0 && !addr.IP.IsUnspecified() {
		opts = append(opts, ice.UDPMuxFromPortWithIPFilter(func(ip net.IP) bool {
			return ip.Equal(addr.IP)
		}))
		if addr.IP.IsLoopback() {
			opts = append(opts, ice.UDPMuxFromPortWithLoopback())
		}
	}

	udpMux, err := ice.NewMultiUDPMuxFromPort(addr.Port, opts...)
	if err != nil {
		return nil, err
	}
	if len(udpMux.GetListenAddresses()) == 0 {
		udpMux.Close()
		return nil, fmt.Errorf("rtcnet: no local IPv4 address to listen for UDP on %v", addr)
	}
	return udpMux, nil
}
