[![Go Reference](https://pkg.go.dev/badge/github.com/unitoftime/rtcnet.svg)](https://pkg.go.dev/github.com/unitoftime/rtcnet)

# WebRTC Networking
This is my attempt at building and easy to use, client-server webrtc-based net.Conn implementation. Feel free to use it and file bug reports. I am by no means a webrtc expert, so if you see any problems with my implementation, then feel free to open an issue! Happy to answer any questions as needed!

## Notes
1. This is for client-server connections only! The main use case is if you want to use webrtc sockets in browser, but don't want to deal with the entire webrtc stack
2. The connection is signaled over Websockets, so you don't need to use any ICE servers.

# Platforms
I've tested this on:
1. Browsers (Firefox, Chrome, Edge)
2. Linux (desktop app)
3. Windows (desktop app)

# Things that I still need to do, but haven't yet
 - [x] Replace logger with injectable logger interface
 - [ ] Ability for user to select the level of reliability/orderdness that they want on the data channel
 - [ ] Close websocket after webrtc negotiation has completed (currently ws stays open until net.Conn is closed)

# Usage
```
import "github.com/unitoftime/rtcnet"
```

See [Example](https://github.com/unitoftime/rtcnet/tree/master/example)

# Deploying
All WebRTC traffic shares the listener's port number over UDP, alongside the websocket signaling on TCP. So only one port needs to be reachable, in both protocols.

Behind a 1:1 NAT (eg a docker bridge network, or a cloud VM without its public IP on an interface) set `ListenConfig.PublicIP` so clients are told the address they can actually reach. For example with docker compose:
```yaml
ports:
  - "2000:2000/tcp"
  - "2000:2000/udp"
```
```go
rtcnet.NewListener(":2000", rtcnet.ListenConfig{
	TlsConfig: tlsConfig,
	PublicIP: os.Getenv("PUBLIC_IP"),
	IceLite: true, // Optional, the listener is directly reachable so it only needs to answer connectivity checks
})
```
Note: Listener `IceServers` aren't needed in this setup. Their STUN lookups run on ephemeral ports, which aren't reachable when only the listen port is open. `IceLite` requires them to be empty.

# Used By
1. I'm currently using this for an online game I'm building for browser. [You can find it here](https://www.unit.dev/mmo)

# License
1. MIT
