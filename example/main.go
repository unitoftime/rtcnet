package main

import (
	"fmt"
	"log"
	"net"
	"io"
	"crypto/tls"

	"github.com/unitoftime/rtcnet"
)

func main() {
	// Start listen
	go func() {
		// Note: I copied these from the crpto/tls example for simplicity. You shouldn't use these!
		certPem := []byte(`-----BEGIN CERTIFICATE-----
MIIBhTCCASugAwIBAgIQIRi6zePL6mKjOipn+dNuaTAKBggqhkjOPQQDAjASMRAw
DgYDVQQKEwdBY21lIENvMB4XDTE3MTAyMDE5NDMwNloXDTE4MTAyMDE5NDMwNlow
EjEQMA4GA1UEChMHQWNtZSBDbzBZMBMGByqGSM49AgEGCCqGSM49AwEHA0IABD0d
7VNhbWvZLWPuj/RtHFjvtJBEwOkhbN/BnnE8rnZR8+sbwnc/KhCk3FhnpHZnQz7B
5aETbbIgmuvewdjvSBSjYzBhMA4GA1UdDwEB/wQEAwICpDATBgNVHSUEDDAKBggr
BgEFBQcDATAPBgNVHRMBAf8EBTADAQH/MCkGA1UdEQQiMCCCDmxvY2FsaG9zdDo1
NDUzgg4xMjcuMC4wLjE6NTQ1MzAKBggqhkjOPQQDAgNIADBFAiEA2zpJEPQyz6/l
Wf86aX6PepsntZv2GYlA5UpabfT2EZICICpJ5h/iI+i341gBmLiAFQOyTDT+/wQc
6MF9+Yw1Yy0t
-----END CERTIFICATE-----`)
		keyPem := []byte(`-----BEGIN EC PRIVATE KEY-----
MHcCAQEEIIrYSSNQFaA2Hwf1duRSxKtLYX5CB04fSeQ6tF1aY/PuoAoGCCqGSM49
AwEHoUQDQgAEPR3tU2Fta9ktY+6P9G0cWO+0kETA6SFs38GecTyudlHz6xvCdz8q
EKTcWGekdmdDPsHloRNtsiCa697B2O9IFA==
-----END EC PRIVATE KEY-----`)
		cert, err := tls.X509KeyPair(certPem, keyPem)
		if err != nil {
			panic(err)
		}
		tlsConfig := &tls.Config{Certificates: []tls.Certificate{cert}}

		l, err := rtcnet.NewListener("localhost:2000", rtcnet.ListenConfig{
			TlsConfig: tlsConfig,
			OriginPatterns: []string{"localhost", "localhost:2000"},
		})
		if err != nil {
			panic(err)
		}
		defer l.Close()

		for {
			// Wait for a connection.
			conn, err := l.Accept()
			if err != nil {
				log.Fatal(err)
			}
			// Handle the connection in a new goroutine.
			// The loop then returns to accepting, so that
			// multiple connections may be served concurrently.
			go func(c net.Conn) {
				// Echo all incoming data.
				io.Copy(c, c)
				// Shut down the connection.
				c.Close()
			}(conn)
		}
	}()


	// Dial and send some things
	{
		conn, err := rtcnet.Dial("localhost:2000", &tls.Config{
			// Note: This is not safe, you shouldn't do this in production. I'm just doing it because this is a simple example. If you run this example with the client in webassembly, then the browser won't let you do this, so you must configure your browser with a self-signed cert, or you must use a CA
			InsecureSkipVerify: true,
		})
		if err != nil {
			panic(err)
		}
		defer conn.Close()

		n, err := conn.Write([]byte("Hello, World!"))
		if err != nil {
			panic(err)
		}

		fmt.Println("Sent Bytes: ", n)

		buf := make([]byte, 4 * 1024)
		n, err = conn.Read(buf)
		if err != nil {
			panic(err)
		}
		fmt.Println("Dialer Received: ", string(buf[:n]))
	}
}
