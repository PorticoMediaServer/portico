package api

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/live"
)

// gatedHLS stands in for the media engine. Its first StartLive takes as long as the test
// lets it, as measuring a channel does (up to 8 seconds), and it records the order of events.
type gatedHLS struct {
	mu     sync.Mutex
	events []string
	first  chan struct{} // closed when the first StartLive has begun
	gate   chan struct{} // the first StartLive returns when this is closed
	once   sync.Once
}

func (f *gatedHLS) note(event string) {
	f.mu.Lock()
	f.events = append(f.events, event)
	f.mu.Unlock()
}

func (f *gatedHLS) StartLive(_ context.Context, r live.Conversion) (*live.Play, error) {
	wait := false
	f.once.Do(func() { wait = true })
	if wait {
		close(f.first)
		<-f.gate
	}
	f.note("made:" + r.SessionID) // the conversion now exists in the engine
	return &live.Play{Route: "converted", Engine: "native", Container: "mpegts_hls"}, nil
}
func (f *gatedHLS) ServeLive(http.ResponseWriter, *http.Request, string, string) {}
func (f *gatedHLS) StopLive(sessionID string)                                    { f.note("stop:" + sessionID) }
func (f *gatedHLS) LiveFailed(string) bool                                       { return false }

// Review desk A, #2915. A person changes channel while the first channel is still being
// measured for their device. The first viewing is ended by the second start, before its
// conversion exists; when the conversion is then made, something must stop it. Otherwise it
// is never stopped: it keeps its place against the owner's limit on conversions, and its
// folder, until the server restarts.
func TestReviewAAConversionMadeForAViewingThatHasEndedIsStopped(t *testing.T) {
	s, owner, members := seededWith(t, 50, 1)
	fake := fakeProvider(t, s.now())
	t.Cleanup(s.tuners.Close)
	hls := &gatedHLS{first: make(chan struct{}), gate: make(chan struct{})}
	s.LiveHLSWith(hls)
	addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("Antenna"), TunerCount: new(2), Config: &live.LiveSourceConfig{PlaylistURL: fake.URL + "/tuned.m3u"}})
	channels := get[Page[LiveChannel]](t, s, members[0], "/v1/live/channels").Items
	if len(channels) != 2 {
		t.Fatalf("channels: %+v", channels)
	}
	var first PlaybackSession
	done := make(chan int, 1)
	go func() {
		done <- call(t, s, "POST", "/v1/playback/sessions", members[0], PlaybackStart{Channel: channels[0].ID}, &first).Code
	}()
	select {
	case <-hls.first:
	case <-time.After(10 * time.Second):
		t.Fatal("the first start never reached the engine")
	}
	// The same device changes channel while the first is being measured.
	var second PlaybackSession
	if w := call(t, s, "POST", "/v1/playback/sessions", members[0], PlaybackStart{Channel: channels[1].ID}, &second); w.Code != 201 {
		t.Fatalf("changing channel answered %d: %s", w.Code, w.Body)
	}
	close(hls.gate)
	code := <-done
	hls.mu.Lock()
	events := slices.Clone(hls.events)
	hls.mu.Unlock()
	t.Logf("the first start answered %d (session %s); the engine saw %v", code, first.ID, events)
	made := slices.Index(events, "made:"+first.ID)
	if first.ID == "" {
		// The first start was refused: then its conversion must have been stopped too.
		for i, e := range events {
			if len(e) > 5 && e[:5] == "made:" && e != "made:"+second.ID {
				made, first.ID = i, e[5:]
			}
		}
	}
	if made < 0 {
		t.Fatalf("no conversion was made for the first viewing: %v", events)
	}
	if !slices.Contains(events[made+1:], "stop:"+first.ID) {
		t.Errorf("the conversion made for the ended viewing %s was never stopped: %v", first.ID, events)
	}
}
