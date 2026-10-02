package llm

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

// TestIsNetworkErrorHTTP2PeerReset reproduces the 2026-10-01 incident
// ("stream error: stream ID 9; INTERNAL_ERROR; received from peer"): a cloud
// provider resets the response stream mid-body with RST_STREAM. std net/http's
// bundled HTTP/2 implementation surfaces that as its own unexported
// http2StreamError — not a *url.Error, *net.OpError, *os.SyscallError, or bare
// syscall errno — so it escaped the adapter seam raw as ErrUnknown.
//
// The test spins up a real HTTP/2 server (raw framer, TLS ALPN "h2") that sends
// a partial SSE body and then RST_STREAM(INTERNAL_ERROR), read by a real std
// net/http client, and asserts the failure classifies as the transient
// ErrNetwork transport failure it is.
func TestIsNetworkErrorHTTP2PeerReset(t *testing.T) {
	addr := startH2RstServer(t)

	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
		ForceAttemptHTTP2: true,
	}}
	resp, err := client.Get("https://" + addr + "/")
	if err != nil {
		t.Fatalf("request failed at round trip (unexpected — reset should be mid-body): %v", err)
	}
	defer resp.Body.Close()

	if resp.ProtoMajor != 2 {
		t.Fatalf("ProtoMajor=%d, want 2 (must exercise HTTP/2)", resp.ProtoMajor)
	}
	body, err := io.ReadAll(resp.Body)
	t.Logf("body=%q err=%T %v", string(body), err, err)

	if err == nil {
		t.Fatal("expected a read error from the peer reset, got none")
	}
	const want = "stream error: stream ID 1; INTERNAL_ERROR; received from peer"
	if err.Error() != want {
		t.Fatalf("read error = %q, want %q", err.Error(), want)
	}
	if !IsNetworkError(err) {
		t.Errorf("IsNetworkError(h2 peer-reset error) = false; want true (transient transport)")
	}
}

// TestIsNetworkErrorHTTP2ErrorShapes pins the textual shapes of both HTTP/2
// implementations' peer-level transport errors. std's bundled copies are
// unexported (errors.As cannot reach them from here); x/net's exported types
// render the identical shapes, so the classifier matches on the shape.
func TestIsNetworkErrorHTTP2ErrorShapes(t *testing.T) {
	streamErr := http2.StreamError{StreamID: 9, Code: http2.ErrCodeInternal, Cause: errors.New("received from peer")}
	if got, want := streamErr.Error(), "stream error: stream ID 9; INTERNAL_ERROR; received from peer"; got != want {
		t.Fatalf("x/net StreamError shape = %q, want %q (x/net and std must agree)", got, want)
	}
	connErr := http2.ConnectionError(http2.ErrCodeInternal)
	if got, want := connErr.Error(), "connection error: INTERNAL_ERROR"; got != want {
		t.Fatalf("x/net ConnectionError shape = %q, want %q", got, want)
	}

	if !IsNetworkError(streamErr) {
		t.Errorf("IsNetworkError(StreamError) = false, want true")
	}
	if !IsNetworkError(connErr) {
		t.Errorf("IsNetworkError(ConnectionError) = false, want true")
	}
	if IsNetworkError(nil) {
		t.Errorf("IsNetworkError(nil) = true, want false")
	}
	if IsNetworkError(errors.New("some provider error")) {
		t.Errorf("IsNetworkError(plain error) = true, want false")
	}
}

func startH2RstServer(t *testing.T) string {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{selfSignedCert(t)},
		NextProtos:   []string{"h2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(10 * time.Second))
		if _, err := io.ReadFull(conn, make([]byte, len(http2.ClientPreface))); err != nil {
			return
		}
		f := http2.NewFramer(conn, conn)
		if err := f.WriteSettings(); err != nil {
			return
		}
		for {
			frame, err := f.ReadFrame()
			if err != nil {
				return
			}
			switch fr := frame.(type) {
			case *http2.SettingsFrame:
				if err := f.WriteSettingsAck(); err != nil {
					return
				}
			case *http2.WindowUpdateFrame:
			case *http2.HeadersFrame:
				streamID := fr.StreamID
				var hb bytes.Buffer
				enc := hpack.NewEncoder(&hb)
				_ = enc.WriteField(hpack.HeaderField{Name: ":status", Value: "200"})
				_ = enc.WriteField(hpack.HeaderField{Name: "content-type", Value: "text/event-stream"})
				if err := f.WriteHeaders(http2.HeadersFrameParam{StreamID: streamID, EndHeaders: true, BlockFragment: hb.Bytes()}); err != nil {
					return
				}
				if err := f.WriteData(streamID, false, []byte("data: {\"type\":\"response.created\"}\n\n")); err != nil {
					return
				}
				time.Sleep(50 * time.Millisecond)
				_ = f.WriteRSTStream(streamID, http2.ErrCodeInternal)
				return
			default:
			}
		}
	}()
	return ln.Addr().String()
}

func selfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "probe"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}
