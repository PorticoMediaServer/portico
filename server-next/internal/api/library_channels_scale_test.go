package api

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/librarychannels"
)

// At the limits the owner may reach, 1,000 channels of 50 blocks each, topping every schedule
// up holds no channel in memory once done, and a page of the guide and a restricted profile's
// channel list cost a page, not the number of channels. It takes about a minute, so -short
// leaves it out.
func TestAThousandChannelsOfFiftyBlocksStayBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("a thousand channels take about a minute")
	}
	s, owner, members := seededWith(t, 600, 3)
	ctx := context.Background()
	films, shows := libraryOfKind(t, s, owner, "movies"), libraryOfKind(t, s, owner, "shows")
	filmsOnly := members[1] // the seeder's second-to-last of three may see only the films and music libraries
	days := []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
	for n := range 1000 {
		library, kind := films, "movie"
		if n%2 == 1 {
			library, kind = shows, "show"
		}
		b := librarychannels.LibraryChannelBuilder{LibraryChannelSelection: librarychannels.LibraryChannelSelection{Libraries: []string{library}, Kinds: []string{kind}}}
		for k := range 50 {
			day, slot := k%7, k/7 // seven two-hour blocks a day, and one more on Sunday
			b.Blocks = append(b.Blocks, librarychannels.LibraryChannelBlock{ID: fmt.Sprint("b", k), Days: []string{days[day]},
				From: fmt.Sprintf("%02d:00", slot*2+1), To: fmt.Sprintf("%02d:00", slot*2+2), Overrun: "cut"})
		}
		var ch LibraryChannel
		if w := call(t, s, "POST", "/v1/admin/library-channels", owner, LibraryChannelSave{Name: new(fmt.Sprint("Channel ", n)), Builder: &b}, &ch); w.Code != 201 {
			t.Fatalf("channel %d answered %d: %s", n, w.Code, w.Body)
		}
	}
	if code := status(t, s, "POST", "/v1/admin/library-channels", owner, LibraryChannelSave{Name: new("One more"),
		Builder: &librarychannels.LibraryChannelBuilder{LibraryChannelSelection: librarychannels.LibraryChannelSelection{Libraries: []string{films}, Kinds: []string{"movie"}}}}); code != 409 {
		t.Fatalf("a 1,001st channel answered %d", code)
	}

	heap := func() uint64 {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}
	before, started := heap(), time.Now()
	// The background job has begun on them already; this does the rest, as it would.
	ids, err := s.db.LibraryChannelsToSchedule(ctx, s.now().Add(6*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if err := s.channels.Schedule(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	after := heap()
	t.Logf("topping up 1,000 channels took %v; the heap went from %d to %d MB", time.Since(started).Round(time.Millisecond), before>>20, after>>20)
	if after > before+32<<20 {
		t.Fatalf("after the top-up the heap kept %d MB more", (after-before)>>20)
	}
	if left, _ := s.db.LibraryChannelsToSchedule(ctx, s.now().Add(6*24*time.Hour)); len(left) != 0 {
		t.Fatalf("%d channels still need a schedule", len(left))
	}

	stretch := "/v1/live/guide?limit=20&from=" + s.now().UTC().Format(time.RFC3339) + "&to=" + s.now().Add(3*time.Hour).UTC().Format(time.RFC3339)
	for _, who := range []struct{ name, token string }{{"owner", owner}, {"films only", filmsOnly}} {
		var slowest time.Duration
		var guide LiveGuide
		for range 5 {
			began := time.Now()
			guide = get[LiveGuide](t, s, who.token, stretch)
			slowest = max(slowest, time.Since(began))
		}
		t.Logf("%s: a page of the guide took at most %v", who.name, slowest.Round(time.Microsecond))
		if len(guide.Items) != 20 || guide.NextCursor == "" || slowest > 250*time.Millisecond {
			t.Fatalf("%s: %d channels in a page, next %q, %v", who.name, len(guide.Items), guide.NextCursor, slowest)
		}
		for _, row := range guide.Items {
			ch, err := s.db.LibraryChannel(ctx, row.Channel.ID, false)
			if err != nil || who.token == filmsOnly && ch.Libraries[0] != films || len(row.Programmes) == 0 {
				t.Fatalf("%s: the page shows %s (%v) with %d programmes", who.name, ch.Name, ch.Libraries, len(row.Programmes))
			}
		}
	}
}
