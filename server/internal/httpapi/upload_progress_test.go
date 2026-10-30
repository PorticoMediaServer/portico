package httpapi

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestUploadDeadlineFollowsBytesAndStopsStalls(t *testing.T) {
	result := make(chan error, 2)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := &progressUploadBody{ReadCloser: r.Body, controller: http.NewResponseController(w), idle: 100 * time.Millisecond}
		defer body.clearDeadline()
		_, err := io.Copy(io.Discard, body)
		result <- err
		w.WriteHeader(200)
	}))
	server.Config.ReadTimeout = 40 * time.Millisecond
	server.Start()
	defer server.Close()
	for _, stall := range []bool{false, true} {
		conn, err := net.Dial("tcp", server.Listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(conn, "POST / HTTP/1.1\r\nHost: test\r\nContent-Length: 6\r\nConnection: close\r\n\r\n")
		if stall {
			_, _ = io.WriteString(conn, "x")
		} else {
			for i := 0; i < 6; i++ {
				_, _ = io.WriteString(conn, "x")
				time.Sleep(30 * time.Millisecond)
			}
		}
		select {
		case err := <-result:
			if stall {
				var timeout net.Error
				if err == nil || !errors.As(err, &timeout) || !timeout.Timeout() {
					t.Fatalf("stalled upload=%v", err)
				}
			} else if err != nil {
				t.Fatal("progressing upload failed", err)
			}
		case <-time.After(time.Second):
			t.Fatal("upload deadline did not end stalled sender")
		}
		conn.Close()
	}
	if laneSpecs[laneUpload].budget != 0 || !laneSpecs[laneUpload].endsWithClient {
		t.Fatal("upload still has a wall-clock request budget")
	}
}
