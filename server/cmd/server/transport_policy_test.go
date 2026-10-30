package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func transportTestServer(t *testing.T, handler http.Handler, timeout time.Duration) *httptest.Server {
	t.Helper()
	s := httptest.NewUnstartedServer(handler)
	s.EnableHTTP2 = true
	s.Config.HTTP2 = serverHTTP2Policy()
	s.Config.HTTP2.WriteByteTimeout = timeout
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}

func TestHTTP2TransportLimitsConcurrentStreams(t *testing.T) {
	entered := make(chan struct{}, 64)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseHandlers := func() { releaseOnce.Do(func() { close(release) }) }
	s := transportTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
	}), time.Second)
	defer releaseHandlers()
	address := strings.TrimPrefix(s.URL, "https://")
	client, err := tls.Dial("tcp", address, &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2"}}) // local httptest certificate
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err = io.WriteString(client, "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	if err = writeTestHTTP2Frame(client, 4, 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	for {
		typ, flags, _, payload, err := readTestHTTP2Frame(client)
		if err != nil {
			t.Fatal(err)
		}
		if typ != 4 || flags != 0 {
			continue
		}
		settings := make(map[uint16]uint32)
		for i := 0; i+6 <= len(payload); i += 6 {
			settings[binary.BigEndian.Uint16(payload[i:])] = binary.BigEndian.Uint32(payload[i+2:])
		}
		if settings[3] != 32 || settings[4] != 64<<10 || settings[5] != 16<<10 {
			t.Fatalf("server advertised unexpected transport limits: %v", settings)
		}
		if err = writeTestHTTP2Frame(client, 4, 1, 0, nil); err != nil {
			t.Fatal(err)
		}
		break
	}
	headers := append([]byte{0x82, 0x87, 0x84, 0x01, byte(len(address))}, address...)
	for stream := uint32(1); stream <= 63; stream += 2 {
		if err = writeTestHTTP2Frame(client, 1, 5, stream, headers); err != nil {
			t.Fatal(err)
		}
	}
	for range 32 {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("configured stream capacity did not become usable")
		}
	}
	if err = writeTestHTTP2Frame(client, 1, 5, 65, headers); err != nil {
		t.Fatal(err)
	}
	for {
		typ, _, stream, payload, err := readTestHTTP2Frame(client)
		if err != nil {
			t.Fatal(err)
		}
		if typ == 3 && stream == 65 {
			if len(payload) != 4 || binary.BigEndian.Uint32(payload) != 1 {
				t.Fatalf("excess stream reset = %x", payload)
			}
			break
		}
	}
	select {
	case <-entered:
		t.Fatal("a connection exceeded its advertised 32-stream limit")
	default:
	}
	releaseHandlers()
}

func readTestHTTP2Frame(r io.Reader) (typ, flags byte, stream uint32, payload []byte, err error) {
	header := make([]byte, 9)
	if _, err = io.ReadFull(r, header); err != nil {
		return
	}
	typ, flags, stream = header[3], header[4], binary.BigEndian.Uint32(header[5:])&0x7fffffff
	size := int(header[0])<<16 | int(header[1])<<8 | int(header[2])
	if size > 1<<20 {
		return 0, 0, 0, nil, io.ErrUnexpectedEOF
	}
	payload = make([]byte, size)
	_, err = io.ReadFull(r, payload)
	return
}

func TestHTTP2ProgressingMediaOutlivesWriteByteTimeout(t *testing.T) {
	const timeout = 100 * time.Millisecond
	s := transportTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := bytes.Repeat([]byte{'x'}, 1024)
		for range 16 {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			if err := http.NewResponseController(w).Flush(); err != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}), timeout)
	start := time.Now()
	response, err := s.Client().Get(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	n, err := io.Copy(io.Discard, response.Body)
	if err != nil || n != 16*1024 || response.ProtoMajor != 2 {
		t.Fatalf("progressing media: bytes=%d protocol=%s error=%v", n, response.Proto, err)
	}
	if time.Since(start) < 2*timeout {
		t.Fatal("test did not stream beyond the rolling timeout")
	}
}

func writeTestHTTP2Frame(w io.Writer, typ, flags byte, stream uint32, payload []byte) error {
	header := make([]byte, 9)
	header[0], header[1], header[2] = byte(len(payload)>>16), byte(len(payload)>>8), byte(len(payload))
	header[3], header[4] = typ, flags
	binary.BigEndian.PutUint32(header[5:], stream)
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func TestHTTP2StalledSocketTimesOutAndServerRecovers(t *testing.T) {
	written := make(chan error, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		chunk := bytes.Repeat([]byte{'x'}, 64<<10)
		for range 512 {
			if _, err := w.Write(chunk); err != nil {
				written <- err
				return
			}
		}
		written <- nil
	})
	fixture := httptest.NewTLSServer(http.NotFoundHandler())
	certificate := fixture.TLS.Certificates[0]
	fixture.Close()
	serverSocket, clientSocket := net.Pipe()
	writeFailures := make(chan error, 4)
	listener := &pipeTransportTestListener{connections: make(chan net.Conn, 2), closed: make(chan struct{})}
	listener.connections <- &timeoutObservedConn{Conn: serverSocket, failures: writeFailures}
	server := &http.Server{Handler: handler, HTTP2: serverHTTP2Policy(), TLSConfig: &tls.Config{Certificates: []tls.Certificate{certificate}}}
	server.HTTP2.WriteByteTimeout = 100 * time.Millisecond
	defer server.Close()
	go func() { _ = server.ServeTLS(listener, "", "") }()
	address := "localhost"
	client := tls.Client(clientSocket, &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2"}}) // local httptest certificate
	defer clientSocket.Close()
	if err := client.Handshake(); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var err error
	client.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err = io.WriteString(client, "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	// Large flow-control windows make this a socket stall, rather than the
	// distinct case of a stream waiting for a WINDOW_UPDATE from its peer.
	settings := []byte{0, 4, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(settings[2:], 32<<20)
	if err = writeTestHTTP2Frame(client, 4, 0, 0, settings); err != nil {
		t.Fatal(err)
	}
	window := make([]byte, 4)
	binary.BigEndian.PutUint32(window, 32<<20)
	if err = writeTestHTTP2Frame(client, 8, 0, 0, window); err != nil {
		t.Fatal(err)
	}
	// HPACK static GET, https, /, plus an unindexed literal :authority.
	headers := append([]byte{0x82, 0x87, 0x84, 0x01, byte(len(address))}, address...)
	if err = writeTestHTTP2Frame(client, 1, 5, 1, headers); err != nil {
		t.Fatal(err)
	}
	// Deliberately never read HTTP/2 responses. Observe the actual transport
	// write timeout, then close the pipe to avoid spending five extra seconds
	// on crypto/tls's bounded close-notify grace period.
	select {
	case err := <-writeFailures:
		if timeout, ok := err.(net.Error); !ok || !timeout.Timeout() {
			t.Fatalf("stalled write error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stalled socket write did not time out")
	}
	clientSocket.Close()
	select {
	case err := <-written:
		if err == nil {
			t.Fatal("non-reading client consumed the complete response")
		}
	case <-time.After(time.Second):
		t.Fatal("stalled handler did not recover after transport closure")
	}
	// The same server still accepts and serves a fresh connection.
	recoveryServerSocket, recoveryClientSocket := net.Pipe()
	listener.connections <- recoveryServerSocket
	transport := &http.Transport{ForceAttemptHTTP2: true, TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DialContext: func(context.Context, string, string) (net.Conn, error) { return recoveryClientSocket, nil }} // local test certificate
	defer transport.CloseIdleConnections()
	recoveryClient := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	response, err := recoveryClient.Get("https://localhost/recovery")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent || response.ProtoMajor != 2 {
		t.Fatalf("recovery = %d %s", response.StatusCode, response.Proto)
	}

}

type pipeTransportTestListener struct {
	connections chan net.Conn
	closed      chan struct{}
	once        sync.Once
}

func (l *pipeTransportTestListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.connections:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}
func (l *pipeTransportTestListener) Close() error   { l.once.Do(func() { close(l.closed) }); return nil }
func (l *pipeTransportTestListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }

type timeoutObservedConn struct {
	net.Conn
	failures chan error
}

func (c *timeoutObservedConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if err != nil {
		select {
		case c.failures <- err:
		default:
		}
	}
	return n, err
}

func TestConnectionBudgetAccountsForSmallHosts(t *testing.T) {
	for _, tc := range []struct {
		memory int64
		want   int
	}{{0, 64}, {-1, 64}, {32 << 20, 16}, {256 << 20, 16}, {512 << 20, 32}, {1 << 30, 64}, {16 << 30, 1024}, {32 << 30, 2048}, {128 << 30, 2048}} {
		if got := connectionBudget(tc.memory); got != tc.want {
			t.Errorf("memory=%d ceiling=%d want=%d", tc.memory, got, tc.want)
		}
	}
}
