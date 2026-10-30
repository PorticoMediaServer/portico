package lyrics

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A rate-limit answer is a pause the provider asked for, not a verdict on the
// song: the client says so, and sends nothing more until the pause has passed.
func TestProviderRateLimitIsReportedAsAPause(t *testing.T) {
	sent := 0
	p := &LRCLIB{origin: "https://lyrics.example", client: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		sent++
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"60"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}}
	for i := 0; i < 3; i++ {
		if _, e := p.Search(context.Background(), "Alpha"); !errors.Is(e, errProviderPaused) || !errors.Is(e, ErrUnavailable) {
			t.Fatalf("search %d during a rate limit: %v", i, e)
		}
	}
	if sent != 1 {
		t.Fatalf("%d requests were sent during the provider's pause", sent)
	}
	// Any other failure is still an ordinary unavailable answer.
	p = &LRCLIB{origin: "https://lyrics.example", client: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 500, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}}
	if _, e := p.Search(context.Background(), "Alpha"); !errors.Is(e, ErrUnavailable) || errors.Is(e, errProviderPaused) {
		t.Fatalf("a server error was reported as %v", e)
	}
}

// A library run that meets a provider pause waits on the song it was about to
// ask for. Nothing is counted missing, nothing more is sent or written until
// the pause has passed, and the run finishes the library once the provider
// answers again.
func TestBulkLyricFetchWaitsOutAProviderPause(t *testing.T) {
	c, bulk, names := bulkFixture(t)
	db := c.DB
	ctx := context.Background()
	clock := time.Unix(1_800_000_000, 0)
	bulk.Now = func() time.Time { return clock }
	document, e := Parse([]byte("[00:01.00]First line\n[00:10.00]Second line"), "lrc")
	if e != nil {
		t.Fatal(e)
	}
	paused := true
	asked := 0
	bulk.provider = func(_ context.Context, query string) ([]acquired, error) {
		asked++
		if paused {
			return nil, errProviderPaused
		}
		return []acquired{{doc: document, language: "en", title: strings.Fields(query)[0], artist: "Fixture Artist", duration: 5400, provenance: Provenance{Origin: "lrclib", ProviderID: "7", Label: "Song", Rights: "LRCLIB record."}}}, nil
	}
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = bulk.QueueTx(ctx, tx, "job", "library"); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	written := func() (string, int64) {
		t.Helper()
		var phase string
		var updated int64
		if e := db.QueryRow(`SELECT phase,updated_ms FROM lyric_fetch_runs WHERE id='job'`).Scan(&phase, &updated); e != nil {
			t.Fatal(e)
		}
		return phase, updated
	}
	step := func(when string) {
		t.Helper()
		if more, e := bulk.Step(ctx); !errors.Is(e, errProviderPaused) || more {
			t.Fatalf("a step %s: more=%v %v", when, more, e)
		}
	}
	step("that meets the pause")
	phase, updated := written()
	if !strings.Contains(phase, "waiting") || asked != 1 {
		t.Fatalf("after meeting the pause: phase %q, provider asked %d times", phase, asked)
	}
	// Woken again and again while the pause lasts: nothing is asked or written.
	for i := 0; i < 4; i++ {
		clock = clock.Add(time.Second)
		step("during the pause")
	}
	if again, at := written(); asked != 1 || again != phase || at != updated {
		t.Fatalf("during the pause the provider was asked %d times and the run was rewritten (%q at %d, was %q at %d)", asked, again, at, phase, updated)
	}
	if wait := bulk.pauseLeft(); wait <= 0 || wait > lrclibMaxWait {
		t.Fatalf("the run looks again in %v", wait)
	}
	// The pause has passed but the provider still refuses: asked once more,
	// and the waiting run is not rewritten.
	clock = clock.Add(lrclibMaxWait)
	step("after the pause, refused again")
	if again, at := written(); asked != 2 || again != phase || at != updated {
		t.Fatalf("a second refusal: provider asked %d times, run %q at %d (was %q at %d)", asked, again, at, phase, updated)
	}
	run, e := bulk.Observe(ctx, "job")
	if e != nil || run.State != "running" || run.Processed != 0 || run.Missing != 0 {
		t.Fatalf("run during the pause: %+v %v", run, e)
	}
	paused = false
	clock = clock.Add(2 * lrclibMaxWait)
	more, e := bulk.Step(ctx)
	if e != nil || !more {
		t.Fatalf("the step after the provider recovered: more=%v %v", more, e)
	}
	if phase, _ = written(); strings.Contains(phase, "waiting") {
		t.Fatalf("the run still says %q while it fetches", phase)
	}
	for i := 0; i < 4; i++ {
		if _, e = bulk.Step(ctx); e != nil {
			t.Fatal(e)
		}
	}
	run, e = bulk.Observe(ctx, "job")
	if e != nil || run.State != "succeeded" || run.Published != 2 || run.Missing != 0 || run.Processed != 2 {
		t.Fatalf("run after the pause: %+v %v", run, e)
	}
	for _, name := range []string{"song-a", "song-b"} {
		var n int
		if e = db.QueryRow(`SELECT count(*) FROM lyric_resources WHERE item_id=? AND scope='library' AND deleted=0`, names[name].ID).Scan(&n); e != nil || n != 1 {
			t.Fatalf("%s has %d library lyrics after the run: %v", name, n, e)
		}
	}
}

// The supervised loop is woken by every commit to the tables it follows, its
// own included. During a provider pause it must neither ask the provider nor
// rewrite the run on those wakes, and it must pick the library up again by
// itself when the pause ends.
func TestSupervisedLyricsRunSitsOutAProviderPause(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c, bulk, _ := bulkFixture(t)
	db := c.DB
	floor := worker.MinSignalInterval
	worker.MinSignalInterval = 5 * time.Millisecond
	t.Cleanup(func() { worker.MinSignalInterval = floor })
	var requests atomic.Int64
	bulk.Service.Provider = &LRCLIB{origin: "https://lyrics.example", client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if requests.Add(1) == 1 {
			return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"3"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		// The provider answers with the recording that was asked for: a match
		// is only published when its title, artist and length are the song's.
		asked, _ := json.Marshal(strings.Fields(r.URL.Query().Get("q"))[0])
		body := `[{"id":7,"trackName":` + string(asked) + `,"artistName":"Fixture Artist","duration":5400,"syncedLyrics":"[00:01.00]First line\n[00:10.00]Second line"}]`
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	ctx, cancel := context.WithCancel(context.Background())
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = bulk.QueueTx(ctx, tx, "job", "library"); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	stopped := make(chan struct{})
	supervise.Go("test.lyrics-bulk", func() {
		defer close(stopped)
		bulk.Run(ctx)
	})
	defer func() {
		cancel()
		<-stopped
	}()
	written := func() (string, string, int64) {
		t.Helper()
		var state, phase string
		var updated int64
		if e := db.QueryRow(`SELECT state,phase,updated_ms FROM lyric_fetch_runs WHERE id='job'`).Scan(&state, &phase, &updated); e != nil {
			t.Fatal(e)
		}
		return state, phase, updated
	}
	// Other writers keep committing to a table the loop follows.
	noise := func(until time.Time) {
		for i := 0; time.Now().Before(until); i++ {
			if _, e := db.Exec(`INSERT INTO console_documents VALUES('test:noise',1,'{}',0) ON CONFLICT(scope) DO UPDATE SET revision=revision+1`); e != nil {
				t.Fatal(e)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	var phase string
	var updated int64
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, phase, updated = written(); strings.Contains(phase, "waiting") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the run never met the pause: phase %q, %d requests", phase, requests.Load())
		}
	}
	noise(time.Now().Add(1200 * time.Millisecond))
	if _, again, at := written(); requests.Load() != 1 || again != phase || at != updated {
		t.Fatalf("during the pause %d requests were sent and the run was rewritten (%q at %d, was %q at %d)", requests.Load(), again, at, phase, updated)
	}
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if state, _, _ := written(); state == "succeeded" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the run did not resume after the pause: %d requests", requests.Load())
		}
	}
	run, e := bulk.Observe(ctx, "job")
	if e != nil || run.Published != 2 || run.Missing != 0 || requests.Load() != 3 {
		t.Fatalf("after the pause: %+v, %d requests, %v", run, requests.Load(), e)
	}
}
