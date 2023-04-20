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

# Used By
1. I'm currently using this for an online game I'm building for browser. [You can find it here](www.unit.dev/mmo)

# License
1. MIT
