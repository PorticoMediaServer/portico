package remotemedia

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
)

func TestServeReconnectPreservesActualAuthorityBeforeAndAfterDial(t *testing.T) {
	for _, refusedAt := range []int{2, 3} {
		t.Run(strconv.Itoa(refusedAt), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(500)
			}))
			defer server.Close()
			u, _ := url.Parse(server.URL)
			_, textPort, _ := net.SplitHostPort(u.Host)
			port, _ := strconv.Atoi(textPort)
			endpoint, err := NewServeEndpoint(port, "private", "fixture", NewOperationBudget(context.Background(), 1))
			if err != nil {
				t.Fatal(err)
			}
			defer endpoint.Close()
			refused := errors.New("actual captured authority retired")
			checks := 0
			_, err = endpoint.ReadRange(context.Background(), "episode.mkv", `"captured"`, 8, 0, 4, func(context.Context) error {
				checks++
				if checks >= refusedAt {
					return refused
				}
				return nil
			})
			if !errors.Is(err, refused) || requests.Load() != 0 {
				t.Fatal("typed authority became provider unavailability or exposed bytes", err, requests.Load())
			}
		})
	}
}
