package linearinput

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
)

func TestProgressingManifestCanOutlastThirtySeconds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "#EXTM3U\n")
		w.(http.Flusher).Flush()
		for i := 0; i < 32; i++ {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(time.Second):
			}
			fmt.Fprintln(w, "# progress")
			w.(http.Flusher).Flush()
		}
		io.WriteString(w, "#EXT-X-TARGETDURATION:4\n#EXTINF:4,\nsegment.ts\n")
	}))
	defer source.Close()
	approval, e := (remotemedia.Policy{}).Approve(context.Background(), source.URL+"/")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	g, e := Open(ctx, livechannels.Input{Locator: source.URL + "/manifest", SourceID: source.URL, ChannelID: "progress", Policy: remotemedia.Policy{Approvals: []remotemedia.Approval{approval}}})
	if e != nil {
		t.Fatalf("progressing bounded manifest failed: %v", e)
	}
	defer g.Close()
	if !g.HLS() {
		t.Fatal("manifest not recognized")
	}
}

func TestAbandonedSharedAcquisitionRetiresDuringProgress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	requested, closed := make(chan struct{}), make(chan struct{})
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(closed)
		close(requested)
		io.WriteString(w, "#EXTM3U\n")
		w.(http.Flusher).Flush()
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-tick.C:
				io.WriteString(w, "# progress\n")
				w.(http.Flusher).Flush()
			}
		}
	}))
	defer source.Close()
	approval, e := (remotemedia.Policy{}).Approve(context.Background(), source.URL+"/")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		g, e := Open(ctx, livechannels.Input{SourceID: source.URL, ChannelID: "abandoned", Locator: source.URL, Policy: remotemedia.Policy{Approvals: []remotemedia.Approval{approval}}})
		if g != nil {
			g.Close()
		}
		result <- e
	}()
	<-requested
	cancel()
	select {
	case e := <-result:
		if e != context.Canceled {
			t.Fatal(e)
		}
	case <-time.After(inputGrace + 2*time.Second):
		t.Fatal("abandoned acquisition retained reader")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("abandoned acquisition retained upstream")
	}
	inputs.Lock()
	_, present := inputs.entries[source.URL+"\nabandoned"]
	inputs.Unlock()
	if present {
		t.Fatal("abandoned channel remained in pool")
	}
}
