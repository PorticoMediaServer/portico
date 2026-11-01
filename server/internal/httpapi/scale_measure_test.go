package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/httpapi/fixture"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

type scaleAttempt struct {
	Ms     float64 `json:"ms"`
	Status int     `json:"status"`
	Code   string  `json:"code,omitempty"`
}
type scaleQuantiles struct {
	N   int     `json:"n"`
	P50 float64 `json:"p50_ms"`
	P95 float64 `json:"p95_ms"`
	P99 float64 `json:"p99_ms"`
}

func scalePct(v []float64) scaleQuantiles {
	slices.Sort(v)
	q := func(p float64) float64 {
		if len(v) == 0 {
			return 0
		}
		return v[max(0, int(math.Ceil(float64(len(v))*p))-1)]
	}
	return scaleQuantiles{len(v), q(.50), q(.95), q(.99)}
}

type scaleWindow struct {
	mu       sync.Mutex
	Attempts map[string][]scaleAttempt `json:"attempts"`
}

func (w *scaleWindow) add(label string, a scaleAttempt) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.Attempts[label] = append(w.Attempts[label], a)
}
func (w *scaleWindow) summary() map[string]any {
	out := map[string]any{}
	for label, aa := range w.Attempts {
		var all, ok []float64
		codes := map[string]int{}
		for _, a := range aa {
			all = append(all, a.Ms)
			codes[fmt.Sprintf("%d/%s", a.Status, a.Code)]++
			if a.Status >= 200 && a.Status < 300 && a.Code == "" {
				ok = append(ok, a.Ms)
			}
		}
		out[label] = map[string]any{"all": scalePct(all), "success": scalePct(ok), "statuses": codes}
	}
	return out
}
func scaleRSS() float64 {
	b, _ := os.ReadFile("/proc/self/status")
	for _, line := range bytes.Split(b, []byte("\n")) {
		var kb float64
		if _, e := fmt.Sscanf(string(line), "VmHWM: %f kB", &kb); e == nil {
			return kb / 1024
		}
	}
	return 0
}
func scaleSave(t *testing.T, name string, v any) {
	t.Helper()
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	dir := os.Getenv("PORTICO_SCALE_REPORT")
	if dir == "" {
		t.Log(string(b))
		return
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, name+".json"), b, 0600); e != nil {
		t.Fatal(e)
	}
	t.Logf("report %s: %s", name, string(b))
}
func scaleCall(client *http.Client, base, token, label, method, path string, body any, headers map[string]string, w *scaleWindow) (int, []byte) {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req, _ := http.NewRequest(method, base+path, bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	at := time.Now()
	resp, e := client.Do(req)
	a := scaleAttempt{}
	if e != nil {
		a.Code = "transport"
	} else {
		a.Status = resp.StatusCode
		raw, e = io.ReadAll(resp.Body)
		resp.Body.Close()
		if e != nil {
			a.Code = "read-body"
		}
		if a.Status >= 400 {
			var envelope struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			_ = json.Unmarshal(raw, &envelope)
			a.Code = envelope.Error.Code
		}
	}
	a.Ms = float64(time.Since(at)) / float64(time.Millisecond)
	if w != nil {
		w.add(label, a)
	}
	return a.Status, raw
}

func TestServerScaleMeasurement(t *testing.T) {
	mode := os.Getenv("PORTICO_SCALE_MODE")
	if mode == "" {
		t.Skip("opt-in hosted scale diagnostic")
	}
	shape := fixture.Shape{Movies: 100000, Shows: 20, SeasonsPerShow: 1, EpisodesPerSeason: 2, Artists: 10, AlbumsPerArtist: 2, SongsPerAlbum: 10, Books: 4, FilesPerBook: 2, GenresPerItem: 1, CreditsPerItem: 3, AttributesPerItem: 2, Profiles: 20, WatchedPercent: 2, ProgressPercent: 5, Seed: 41}
	users := 200
	switch mode {
	case "smoke":
		shape.Movies = 200
		users = 4
	case "films20":
		shape.Movies = 20000
		users = 20
	case "films100":
	case "million":
		shape.Artists = 8000
		shape.AlbumsPerArtist = 10
		shape.SongsPerAlbum = 13
		shape.Shows = 20000
		shape.EpisodesPerSeason = 1
		shape.ProgressPercent = 1
		users = 20
	default:
		t.Fatal("unknown mode")
	}
	scaleSave(t, "environment", map[string]any{"mode": mode, "shape": shape, "cpus": runtime.NumCPU(), "go": runtime.Version(), "baseline": "444a7087", "cache": "schema and shape keyed fixture; OS cache warm"})
	if os.Getenv("PORTICO_REQUIRE_SCALE_SEED") == "1" {
		matches, e := filepath.Glob(filepath.Join(os.Getenv("TMPDIR"), "portico-fixture-*.sqlite"))
		if e != nil || len(matches) != 1 {
			t.Fatal("exactly one compatible completed seed required")
		}
	}
	at := time.Now()
	f := newLoadFixture(t, performanceTier{name: "F-" + mode, shape: shape, catalogItems: shape.Items(), concurrentViewers: users, capabilities: true})
	scaleSave(t, "fixture", map[string]any{"setup_seconds": time.Since(at).Seconds(), "peak_rss_mib": scaleRSS()})
	if os.Getenv("PORTICO_SCALE_SEED_ONLY") == "1" {
		return
	}
	// New HTTP profiles carry the same sparse meaningful history as a real viewer.
	c := catalogtest.New(t, f.db)
	for p, env := range f.viewers {
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			return scalePersonal(ctx, tx, identity.PersonalKey(env.Viewer), p)
		})
	}
	c.Drain()
	for _, env := range f.viewers {
		var count int
		if e := f.db.QueryRow(`SELECT count(*) FROM personal_history WHERE profile_id=?`, identity.PersonalKey(env.Viewer)).Scan(&count); e != nil || count != 30 {
			t.Fatalf("HTTP personal key history: %d (%v)", count, e)
		}
	}
	if e := persistence.RefreshPlannerStatistics(context.Background(), f.db); e != nil {
		t.Fatal(e)
	}
	// Explain before any load; save plans even when a later window is interrupted.
	plans := map[string][]string{}
	for name, q := range map[string]string{
		"recent_walk": `SELECT entity_id,recent_text FROM catalog_browse_rows INDEXED BY catalog_browse_recent_works WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id='fixture-movies') AND recent_text IS NOT NULL AND (recent_text,entity_id)<('￿',4611686018427387904) ORDER BY recent_text DESC,entity_id DESC LIMIT 128`,
		"title":       `SELECT entity_id FROM catalog_browse_rows WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id='fixture-movies') AND kind=1 ORDER BY sort_key COLLATE NOCASE,entity_id LIMIT 41`,
		"fts":         `SELECT rowid FROM catalog_search_titles WHERE catalog_search_titles MATCH 'Word001' LIMIT 40`,
	} {
		rows, e := f.db.Query("EXPLAIN QUERY PLAN " + q)
		if e != nil {
			plans[name] = []string{e.Error()}
			continue
		}
		for rows.Next() {
			var a, b, c int
			var detail string
			if e = rows.Scan(&a, &b, &c, &detail); e != nil {
				t.Fatal(e)
			}
			plans[name] = append(plans[name], detail)
		}
		rows.Close()
	}
	scaleSave(t, "plans", plans)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if e := ClosePlaybackObservations(ctx, f.handler); e != nil {
			t.Error(e)
		}
		if e := FlushCredentialUse(ctx, f.handler); e != nil {
			t.Error(e)
		}
	})
	server := httptest.NewServer(f.handler)
	defer server.Close()
	client := server.Client()
	client.Timeout = 30 * time.Second
	_, shapeBody := scaleCall(client, server.URL, f.viewers[0].AccessToken, "", "GET", "/v1/home", nil, nil, nil)
	var shapeDoc catalog.HomeDocument
	if e := json.Unmarshal(shapeBody, &shapeDoc); e != nil {
		t.Fatal(e)
	}
	shapeRows := []map[string]any{}
	for _, row := range shapeDoc.Rows {
		shapeRows = append(shapeRows, map[string]any{"id": row.ID, "deferred": row.Deferred, "endpoint": row.Endpoint, "limit": row.Limit, "entries": len(row.Entries)})
	}
	scaleSave(t, "home-shape", map[string]any{"wire": "GET /v1/home serves HomeManifest; row previews are separate HTTP reads", "rows": shapeRows})
	// Measure each endpoint in isolation before mixed load, preserving every error.
	isolated := &scaleWindow{Attempts: map[string][]scaleAttempt{}}
	endpoints := []struct {
		name, method, path string
		body               any
	}{
		{"home", "GET", "/v1/home", nil}, {"see_all", "GET", "/v1/home/rows/new_on_server?limit=40", nil},
		{"browse_films", "POST", "/v1/libraries/fixture-movies/browse", map[string]any{"pivot": "movies", "limit": 40}},
		{"browse_albums", "POST", "/v1/libraries/fixture-music/browse", map[string]any{"pivot": "albums", "limit": 40}},
		{"browse_tracks", "POST", "/v1/libraries/fixture-music/browse", map[string]any{"pivot": "songs", "limit": 40}},
		{"search_selective", "GET", "/v1/search?q=000042&limit=10", nil}, {"search_broad", "GET", "/v1/search?q=Word001&limit=10", nil},
		{"detail", "GET", "/v1/items/" + f.items[0] + "/detail", nil},
	}
	for _, kind := range []int{5, 6} {
		var id string
		if e := f.db.QueryRow(`SELECT pid(public_id) FROM catalog_entities WHERE kind=? AND retired=0 LIMIT 1`, kind).Scan(&id); e != nil {
			t.Fatal(e)
		}
		endpoints = append(endpoints, struct {
			name, method, path string
			body               any
		}{map[int]string{5: "artist", 6: "album"}[kind], "GET", "/v1/items/" + id + "/detail", nil})
	}
	ResetRouteCosts()
	dbwork.WriteGate().ResetPeak()
	for pass := 0; pass < 20; pass++ {
		env := f.viewers[pass%len(f.viewers)]
		for _, ep := range endpoints {
			label := "warm/" + ep.name
			if pass == 0 {
				label = "first/" + ep.name
			}
			scaleCall(client, server.URL, env.AccessToken, label, ep.method, ep.path, ep.body, nil, isolated)
		}
	}
	scaleSave(t, "isolated", map[string]any{"endpoints": isolated.summary(), "routes": RouteCosts(), "peak_rss_mib": scaleRSS()})
	// Service calls expose exact request VM work independent of HTTP admission.
	costs := map[string]dbwork.Cost{}
	for _, name := range []string{"home", "see_all", "browse"} {
		ctx, read := dbwork.Measure(context.Background())
		s := f.catalog.WithContext(ctx)
		profile := identity.PersonalKey(f.viewers[0].Viewer)
		v := catalog.Viewer{Profile: profile, Fence: profile, Libraries: []string{f.library}}
		r := catalog.HomeRequest{Viewer: v, ServerID: "s", Profile: profile, ViewerFence: profile, Libraries: v.Libraries, Now: time.Now()}
		var e error
		switch name {
		case "home":
			_, e = s.HomeRows(r)
		case "see_all":
			_, e = s.HomeSingleRow(r, "new_on_server", catalog.HomeRowPage{Limit: 40})
		case "browse":
			_, e = s.BrowseEntities(v, catalog.BrowseRequest{Library: f.library, Profile: profile, ViewerFence: profile, Pivot: "movies", Limit: 40})
		}
		if e != nil {
			t.Logf("isolated service %s: %v", name, e)
		}
		costs[name] = read()
	}
	scaleSave(t, "service_costs", costs)

	// Separately time actual durable observation flushes, excluding Start/Stop.
	probe := &scaleWindow{Attempts: map[string][]scaleAttempt{}}
	token := f.viewers[0].AccessToken
	status, raw := scaleCall(client, server.URL, token, "start", "POST", "/v1/playback/sessions", map[string]string{"itemId": f.playable, "startFrom": "beginning"}, map[string]string{"Idempotency-Key": "scale-observation-writer-probe-0001"}, probe)
	var probeView playbackv1.SessionView
	if status == 201 && json.Unmarshal(raw, &probeView) == nil {
		path := "/v1/playback/sessions/" + probeView.ID
		status, raw = scaleCall(client, server.URL, token, "observer", "POST", path+"/observer", map[string]any{"generation": probeView.Presentation.Generation}, nil, probe)
		var grant playbackv1.ObserverGrant
		if status == 200 && json.Unmarshal(raw, &grant) == nil {
			var flushes []float64
			dbwork.WriteGate().ResetPeak()
			before := dbwork.WriteGate().Stats()
			for seq := int64(1); seq <= 20; seq++ {
				scaleCall(client, server.URL, token, "accepted", "PUT", path+"/observation", playbackv1.Observation{Epoch: grant.Epoch, Seq: seq, Generation: probeView.Presentation.Generation, State: "playing", PositionMs: seq * 1000, MonotonicMs: seq * 1000, PlayClockMs: seq * 1000, Rate: 1}, nil, probe)
				at := time.Now()
				if e := FlushPlaybackObservations(context.Background(), f.handler); e != nil {
					t.Fatal(e)
				}
				flushes = append(flushes, float64(time.Since(at))/float64(time.Millisecond))
			}
			scaleSave(t, "progress-writer", map[string]any{"flush": scalePct(flushes), "gate_before": before, "gate_after": dbwork.WriteGate().Stats(), "http": probe.summary(), "boundary": "forced durable observation flush per accepted sample; Start/Stop excluded from gate window"})
		}
		scaleCall(client, server.URL, token, "stop", "DELETE", path, nil, nil, probe)
	}
	counts := []int{32, 50, 100, 200}
	if mode != "films100" {
		counts = []int{min(4, users)}
	}
	for _, count := range counts {
		w := &scaleWindow{Attempts: map[string][]scaleAttempt{}}
		before := dbwork.WriteGate().Stats()
		dbwork.WriteGate().ResetPeak()
		dbwork.ResetPlans()
		ResetRouteCosts()
		at = time.Now()
		var cpu *os.File
		if dir := os.Getenv("PORTICO_SCALE_REPORT"); dir != "" {
			cpu, _ = os.Create(filepath.Join(dir, fmt.Sprintf("users-%d.pprof", count)))
			if cpu != nil {
				_ = pprof.StartCPUProfile(cpu)
			}
		}
		var wg sync.WaitGroup
		for u := 0; u < count; u++ {
			wg.Add(1)
			go func(u int) {
				defer wg.Done()
				time.Sleep(time.Duration(u) * 10 * time.Millisecond)
				token := f.viewers[u].AccessToken
				rounds := 4
				if mode == "smoke" {
					rounds = 1
				}
				for round := 0; round < rounds; round++ {
					for step := 0; step < 5; step++ {
						op := (step + u + round) % 5
						switch op {
						case 0:
							status, raw := scaleCall(client, server.URL, token, "home", "GET", "/v1/home", nil, nil, w)
							if os.Getenv("PORTICO_SCALE_HOME_ROWS") == "1" && status == 200 {
								var doc catalog.HomeDocument
								if json.Unmarshal(raw, &doc) == nil {
									for _, row := range doc.Rows {
										if row.ID != "new_on_server" && row.ID != "recommended" {
											continue
										}
										if row.Endpoint != "" {
											scaleCall(client, server.URL, token, "home_row/"+row.ID, "GET", row.Endpoint+"?limit=12", nil, nil, w)
										}
									}
								}
							}
						case 1:
							scaleCall(client, server.URL, token, "browse", "POST", "/v1/libraries/fixture-movies/browse", map[string]any{"pivot": "movies", "limit": 40, "sort": []map[string]string{{"field": "title", "direction": "asc"}}}, nil, w)
						case 2:
							q := "000042"
							label := "search_selective"
							if (u+round)%2 == 0 {
								q = "Word001"
								label = "search_broad"
							}
							scaleCall(client, server.URL, token, label, "GET", "/v1/search?q="+q+"&limit=10", nil, nil, w)
						case 3:
							scaleCall(client, server.URL, token, "detail", "GET", "/v1/items/"+f.items[(u+round)%len(f.items)]+"/detail", nil, nil, w)
						case 4:
							status, b := scaleCall(client, server.URL, token, "playback_start", "POST", "/v1/playback/sessions", map[string]string{"itemId": f.playable, "startFrom": "beginning"}, map[string]string{"Idempotency-Key": fmt.Sprintf("scale-measure-F-%d-%d-%d", count, u, round)}, w)
							var view playbackv1.SessionView
							if status != 201 || json.Unmarshal(b, &view) != nil {
								break
							}
							path := "/v1/playback/sessions/" + view.ID
							status, b = scaleCall(client, server.URL, token, "observer", "POST", path+"/observer", map[string]any{"generation": view.Presentation.Generation}, nil, w)
							var grant playbackv1.ObserverGrant
							if status == 200 && json.Unmarshal(b, &grant) == nil {
								for seq := int64(1); seq <= 3; seq++ {
									scaleCall(client, server.URL, token, "progress", "PUT", path+"/observation", playbackv1.Observation{Epoch: grant.Epoch, Seq: seq, Generation: view.Presentation.Generation, State: "playing", PositionMs: seq * 1000, MonotonicMs: seq * 1000, PlayClockMs: seq * 1000, Rate: 1}, nil, w)
								}
							}
							scaleCall(client, server.URL, token, "playback_stop", "DELETE", path, nil, nil, w)
						}
						time.Sleep(250 * time.Millisecond)
					}
				}
			}(u)
		}
		wg.Wait()
		if cpu != nil {
			pprof.StopCPUProfile()
			cpu.Close()
		}
		after := dbwork.WriteGate().Stats()
		scaleSave(t, fmt.Sprintf("users-%d", count), map[string]any{"users": count, "seconds": time.Since(at).Seconds(), "endpoints": w.summary(), "gate_before": before, "gate_after": after, "admission": dbwork.WriteGate().Admission(), "write_plans": dbwork.Plans(), "routes": RouteCosts(), "peak_rss_mib": scaleRSS()})
	}
	var sessions, durable, ended int
	if e := f.db.QueryRow(`SELECT count(*),COALESCE(sum(CASE WHEN last_seq>=1 AND position_ms=3000 THEN 1 ELSE 0 END),0),COALESCE(sum(CASE WHEN ended_ms>0 AND end_reason='stopped' THEN 1 ELSE 0 END),0) FROM playback_v1_sessions WHERE start_key LIKE 'scale-measure-F-%'`).Scan(&sessions, &durable, &ended); e != nil {
		t.Logf("durable reconciliation: %v", e)
	}
	// Reconcile the user-facing resume record as well as session telemetry.
	// Only the final session owns each profile's resume row.
	resumeExpected, resumeDurable := min(users, counts[len(counts)-1]), 0
	resumeFailures := map[string]string{}
	for i := 0; i < resumeExpected; i++ {
		var position int64
		var unit int
		err := f.db.QueryRow(`SELECT p.position,p.unit FROM progress p JOIN catalog_entities e ON e.id=p.item_id WHERE p.profile_id=? AND e.public_id=pid_blob(?)`, identity.PersonalKey(f.viewers[i].Viewer), f.playable).Scan(&position, &unit)
		if err == nil && position == 3000 && unit == 0 {
			resumeDurable++
		} else {
			resumeFailures[strconv.Itoa(i)] = fmt.Sprintf("position=%d unit=%d error=%v", position, unit, err)
		}
	}
	scaleSave(t, "durability", map[string]any{"sessions": sessions, "durable_position_3000": durable, "stopped": ended, "resume_profiles_expected": resumeExpected, "resume_profiles_position_3000": resumeDurable, "resume_failures": resumeFailures})
	if mode == "smoke" {
		for label, attempts := range isolated.Attempts {
			for _, a := range attempts {
				if a.Status < 200 || a.Status >= 300 {
					t.Errorf("pilot %s: %d/%s", label, a.Status, a.Code)
				}
			}
		}
		if sessions == 0 || durable != sessions || ended != sessions {
			t.Errorf("pilot durability sessions=%d durable=%d stopped=%d", sessions, durable, ended)
		}
		if resumeDurable != resumeExpected {
			t.Errorf("pilot resume durability: %v", resumeFailures)
		}
	}
}

func scalePersonal(ctx context.Context, tx *sql.Tx, profile string, p int) error {
	at := time.Now().UTC().Add(-time.Hour).Format("2006-01-02T15:04:05.000Z")
	_, e := tx.ExecContext(ctx, `INSERT INTO personal_history(id,profile_id,item_id,started_at,updated_at,sequence,position,unit,completed) SELECT ?||'-'||id,?,id,?,?,1,5400000,0,1 FROM catalog_entities WHERE kind=1 AND retired=0 ORDER BY id LIMIT 30 OFFSET ?`, profile, profile, at, at, p*31)
	if e != nil {
		return e
	}
	for _, statement := range []struct {
		q    string
		args []any
	}{
		{`INSERT INTO personal_items(profile_id,item_id,watched,favorite,watchlisted,last_played_at,revision) SELECT ?,id,0,1,0,?,1 FROM catalog_entities WHERE kind=1 AND retired=0 ORDER BY id LIMIT 10 OFFSET ?`, []any{profile, at, p * 31}},
		{`INSERT INTO progress(profile_id,item_id,position,unit,playback_id) SELECT ?,id,120000,0,'F-fixture' FROM catalog_entities WHERE kind=1 AND retired=0 ORDER BY id LIMIT 10 OFFSET ?`, []any{profile, p * 31}},
		{`INSERT INTO progress_activity(profile_id,library_id,item_id,updated_at,state) SELECT ?,l.library_id,e.id,?,'paused' FROM catalog_entities e JOIN catalog_libraries l ON l.id=e.library_id WHERE e.kind=1 AND e.retired=0 ORDER BY e.id LIMIT 10 OFFSET ?`, []any{profile, at, p * 31}},
	} {
		if _, e = tx.ExecContext(ctx, statement.q, statement.args...); e != nil {
			return e
		}
	}
	return nil
}

// Growing publication/attribution tables surround an actual scanned EPUB and
// comic. The measured source has the same contents at every catalogue size.
func TestReadingGrowthMeasurement(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("PORTICO_GROWTH_ITEMS"))
	if n == 0 {
		t.Skip("opt-in hosted reader growth diagnostic")
	}
	for _, tc := range []struct {
		kind, format, path string
		leaf               compactcatalog.Kind
	}{{"book", "epub", "/assets/EPUB/chapter.xhtml", compactcatalog.Ebook}, {"comic", "cbz", "/pages/0", compactcatalog.Comic}, {"book", "pdf", "/source", compactcatalog.Ebook}} {
		if only := os.Getenv("PORTICO_READING_FORMAT"); only != "" && only != tc.format {
			continue
		}
		t.Run(tc.format, func(t *testing.T) {
			r := newReadingFixture(t, tc.kind, tc.format)
			var handle int64
			if e := r.f.db.QueryRow(`SELECT id FROM catalog_libraries WHERE library_id=?`, r.library).Scan(&handle); e != nil {
				t.Fatal(e)
			}
			root := filepath.Dir(r.path)
			if e := fixture.SeedReadingGrowth(context.Background(), r.f.db, handle, tc.leaf, root, tc.format, max(10, n/20)); e != nil {
				t.Fatal(e)
			}
			catalogtest.New(t, r.f.db).Drain()
			if e := persistence.RefreshPlannerStatistics(context.Background(), r.f.db); e != nil {
				t.Fatal(e)
			}
			var prepare, body []float64
			var costs []dbwork.Cost
			for pass := 0; pass < 10; pass++ {
				ctx, cost := dbwork.Measure(context.Background())
				at := time.Now()
				session, document, e := r.s.Prepare(ctx, r.p, r.item, r.source)
				prepare = append(prepare, float64(time.Since(at))/float64(time.Millisecond))
				costs = append(costs, cost())
				if e != nil {
					t.Fatal(e)
				}
				at = time.Now()
				headers := map[string]string(nil)
				want := 200
				if tc.format == "pdf" {
					headers = map[string]string{"Range": "bytes=0-4"}
					want = 206
				}
				w := r.bytesRequest(t, "GET", tc.path, session, document.Selection.Revision, r.f.owner.AccessToken, headers)
				body = append(body, float64(time.Since(at))/float64(time.Millisecond))
				if w.Code != want {
					t.Fatalf("reader bytes: %d %s", w.Code, w.Body.String())
				}
				if e = r.s.CloseSession(r.p, session.ID); e != nil {
					t.Fatal(e)
				}
			}
			scaleSave(t, "reading-"+tc.format, map[string]any{"base_items": n, "publication_count": max(10, n/20) + 1, "prepare": scalePct(prepare), "chapter_or_page_bytes": scalePct(body), "prepare_costs": costs, "peak_rss_mib": scaleRSS(), "boundary": "fixed EPUB chapter / comic page / PDF source range; growing publication and author/series facts; no PDF raster claim"})
		})
	}
}
