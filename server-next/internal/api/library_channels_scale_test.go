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
	s.setClock(newRigClock(s.now()).Now)
	// Keep allocation measurements separate from unrelated background jobs. All
	// channels below are scheduled explicitly, as the background worker does.
	stopServer(s)
	ctx := context.Background()
	films, shows := libraryOfKind(t, s, owner, "movies"), libraryOfKind(t, s, owner, "shows")
	filmsOnly := members[1] // the seeder's second-to-last of three may see only the films and music libraries
	stretch := "/v1/live/guide?limit=20&from=" + s.now().UTC().Format(time.RFC3339) + "&to=" + s.now().Add(3*time.Hour).UTC().Format(time.RFC3339)
	viewers := []struct{ name, token string }{{"owner", owner}, {"films only", filmsOnly}}
	baseline := map[string]float64{}
	pageWork := func(token string) float64 {
		return testing.AllocsPerRun(3, func() { get[LiveGuide](t, s, token, stretch) })
	}
	schedule := func() {
		t.Helper()
		ids, err := s.db.LibraryChannelsToSchedule(ctx, s.now().Add(6*24*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			if err := s.channels.Schedule(ctx, id); err != nil {
				t.Fatal(err)
			}
		}
	}
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
		if n == 39 {
			schedule()
			for _, who := range viewers {
				baseline[who.name] = pageWork(who.token)
			}
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
	schedule()
	after := heap()
	t.Logf("topping up 1,000 channels took %v; the heap went from %d to %d MB", time.Since(started).Round(time.Millisecond), before>>20, after>>20)
	if after > before+32<<20 {
		t.Fatalf("after the top-up the heap kept %d MB more", (after-before)>>20)
	}
	if left, _ := s.db.LibraryChannelsToSchedule(ctx, s.now().Add(6*24*time.Hour)); len(left) != 0 {
		t.Fatalf("%d channels still need a schedule", len(left))
	}

	for _, who := range viewers {
		// The first page is the same at forty and a thousand channels. Loading
		// every builder or guide would grow work with the extra 960 channels.
		work := pageWork(who.token)
		t.Logf("%s: guide-page allocations at 40 channels %.0f; at 1,000 %.0f", who.name, baseline[who.name], work)
		if work > 2*baseline[who.name] {
			t.Fatalf("%s: page work grew with the channel list: %.0f allocations, baseline %.0f", who.name, work, baseline[who.name])
		}
		guide := get[LiveGuide](t, s, who.token, stretch)
		if len(guide.Items) != 20 || guide.NextCursor == "" {
			t.Fatalf("%s: %d channels in a page, next %q", who.name, len(guide.Items), guide.NextCursor)
		}
		for _, row := range guide.Items {
			ch, err := s.db.LibraryChannel(ctx, row.Channel.ID, false)
			if err != nil || who.token == filmsOnly && ch.Libraries[0] != films || len(row.Programmes) == 0 {
				t.Fatalf("%s: the page shows %s (%v) with %d programmes", who.name, ch.Name, ch.Libraries, len(row.Programmes))
			}
		}
	}
}
