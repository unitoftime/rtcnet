package rtcnet

import (
	"fmt"
	"testing"
	"runtime"
	"net"
	"io"
	"crypto/tls"
	"time"
	"math/rand"
)

// Helper functions
// Check that this boolean is true
func check(t *testing.T, b bool) {
	if !b {
		_, f, l, _ := runtime.Caller(1)
		t.Errorf("%s:%d - checked boolean is false!", f, l)
	}
}

// Check two things match, if they don't, throw an error
func compare[T comparable](t *testing.T, actual, expected T) {
	if expected != actual {
		_, f, l, _ := runtime.Caller(1)
		t.Errorf("%s:%d - actual(%v) did not match expected(%v)", f, l, actual, expected)
	}
}

func tlsConfig() *tls.Config {
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
	return tlsConfig
}

func randomSlice(length int) []byte {
	buf := make([]byte, length)
	rand.Read(buf)
	return buf
}

func TestConn(t *testing.T) {
	tlsConfig := tlsConfig()
	// Start listen
	go func() {
		l, err := NewListener("localhost:2000", ListenConfig{
			TlsConfig: tlsConfig,
			OriginPatterns: []string{"localhost", "localhost:2000"},
		})
		if err != nil {
			t.Errorf("%v", err)
		}
		defer l.Close()

		for {
			// Wait for a connection.
			conn, err := l.Accept()
			if err != nil {
				t.Errorf("%v", err)
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

	// Give time for connection to establish
	time.Sleep(1 * time.Second)

	// Dial and send some things
	{
		conn, err := Dial("localhost:2000", &tls.Config{
			InsecureSkipVerify: true,
		})
		if err != nil {
			t.Errorf("%v", err)
		}
		defer conn.Close()

		successCount := 0
		numIterations := 1000
		for iter := 0; iter < numIterations; iter++ {
			dat := randomSlice(rand.Intn(4*1024) + 1) // Note: cant send empty
			// fmt.Println(dat)
			n1, err := conn.Write(dat)
			if err != nil {
				t.Errorf("%v", err)
			}


			buf := make([]byte, len(dat))
			n2, err := conn.Read(buf)
			if err != nil {
				t.Errorf("%v", err)
			}

			compare(t, n2, n1)
			for i := range buf {
				compare(t, buf[i], dat[i])
			}

			successCount++
		}

	fmt.Println("Success: ", successCount)
	}

	fmt.Println("Done")
}
