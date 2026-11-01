package enrichment

import (
	"bufio"
	"cmp"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// TestTitleDatasetAtScale is the B2 R7 performance run (opt-in): a dataset of
// PORTICO_DATASET_SCALE_WORKS works (10 tags and 5 similar titles each)
// published, downloaded, verified and imported into a library of
// PORTICO_DATASET_SCALE_LIBRARY films, then a weekly delta, new works arriving,
// Home for twenty profiles, and the off switch's purge. It records each
// phase's wall time and the process's peak RSS, and the database and dataset
// sizes, as JSON at PORTICO_DATASET_SCALE_REPORT (default: the test log).
//
//	PORTICO_DATASET_SCALE_WORKS=1000000 PORTICO_DATASET_SCALE_LIBRARY=100000 \
//	  go test ./internal/enrichment -run TestTitleDatasetAtScale -count=1 -v -timeout 150m
//
// It runs on a GitHub-hosted runner (COMMON-BRIEF "GitHub runners first").
func TestTitleDatasetAtScale(t *testing.T) {
	works, _ := strconv.Atoi(os.Getenv("PORTICO_DATASET_SCALE_WORKS"))
	if works <= 0 {
		t.Skip("set PORTICO_DATASET_SCALE_WORKS to the dataset's work count")
	}
	library, _ := strconv.Atoi(os.Getenv("PORTICO_DATASET_SCALE_LIBRARY"))
	if library <= 0 {
		library = 100000
	}
	library = min(library, works)
	const tagsPerWork, similarPerWork, vocabulary, profiles = 10, 5, 300, 20
	scratch := t.TempDir()
	type phase struct {
		Name        string  `json:"name"`
		Seconds     float64 `json:"seconds"`
		PeakRSSMiB  float64 `json:"peakRssMiB"`
		HeapMiB     float64 `json:"heapMiB"`
		Detail      string  `json:"detail,omitempty"`
		BytesOnDisk int64   `json:"bytesOnDisk,omitempty"`
		// Stages is the importer's own breakdown of the phase (importTrace): seconds and calls.
		Stages []stageTiming `json:"stages,omitempty"`
	}
	var stageMu sync.Mutex
	stages := map[string]*stageTiming{}
	importTrace = func(stage string, elapsed time.Duration) {
		stageMu.Lock()
		defer stageMu.Unlock()
		if stages[stage] == nil {
			stages[stage] = &stageTiming{Name: stage}
		}
		stages[stage].Seconds += elapsed.Seconds()
		stages[stage].Calls++
	}
	t.Cleanup(func() { importTrace = nil })
	takeStages := func() []stageTiming {
		stageMu.Lock()
		defer stageMu.Unlock()
		out := make([]stageTiming, 0, len(stages))
		for _, v := range stages {
			out = append(out, *v)
		}
		clear(stages)
		slices.SortFunc(out, func(a, b stageTiming) int { return cmp.Compare(b.Seconds, a.Seconds) })
		return out
	}
	report := struct {
		Works, Library, TagsPerWork, SimilarPerWork int
		GoVersion, GOOS, GOARCH                     string
		CPUs                                        int
		Phases                                      []phase
	}{Works: works, Library: library, TagsPerWork: tagsPerWork, SimilarPerWork: similarPerWork, GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, CPUs: runtime.NumCPU()}
	measure := func(name string, run func() string) {
		t.Helper()
		start := time.Now()
		detail := run()
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		p := phase{Name: name, Seconds: time.Since(start).Seconds(), PeakRSSMiB: peakRSSMiB(), HeapMiB: float64(stats.HeapAlloc) / (1 << 20), Detail: detail, Stages: takeStages()}
		report.Phases = append(report.Phases, p)
		t.Logf("%-28s %8.1fs  peak RSS %7.0f MiB  heap %6.0f MiB  %s", name, p.Seconds, p.PeakRSSMiB, p.HeapMiB, detail)
		for _, stage := range p.Stages {
			t.Logf("    %-34s %8.2fs  %9d calls", stage.Name, stage.Seconds, stage.Calls)
		}
	}
	size := func(paths ...string) int64 {
		var n int64
		for _, path := range paths {
			if info, err := os.Stat(path); err == nil {
				n += info.Size()
			}
		}
		return n
	}

	// The release, as the pipeline would build it.
	full := filepath.Join(scratch, "screen.sqlite")
	measure("build dataset", func() string {
		buildScaleDataset(t, full, "1", works, tagsPerWork, similarPerWork, vocabulary, nil)
		return fmt.Sprintf("%d MiB", size(full)>>20)
	})
	publish := newDiskPublisher(t, scratch)
	measure("gzip and hash", func() string {
		f := publish.add("screen-1", full)
		publish.domains["screen"] = ManifestDomain{DatasetVersion: "1", VocabularyVersion: 2, Full: f}
		return fmt.Sprintf("%d MiB compressed", f.Bytes>>20)
	})

	// The library: compact films with TMDB ids, every other one in the dataset.
	dbPath := filepath.Join(scratch, "server.sqlite")
	db, err := persistence.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := catalogtest.New(t, db)
	var films []int64
	measure("seed library", func() string {
		lib := c.Library("films", "Films", "movie", "/films")
		var root string
		if err := db.QueryRow(`SELECT root FROM catalog_libraries WHERE id=?`, lib).Scan(&root); err != nil {
			t.Fatal(err)
		}
		for start := 0; start < library; start += 20000 {
			c.Write(func(ctx context.Context, tx *sql.Tx) error {
				for i := start; i < min(start+20000, library); i++ {
					path := fmt.Sprintf("/films/f%08d.mkv", i)
					id, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: lib, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey(root, path, 0), Title: fmt.Sprintf("Film %d", i), Year: 1950 + i%70, Added: "2026-01-01T00:00:00Z"})
					if err != nil {
						return err
					}
					asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1000, ModifiedNS: 1, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 6000})
					if err != nil {
						return err
					}
					if err = compactcatalog.LinkAssetTx(ctx, tx, id, asset, compactcatalog.Link{}); err != nil {
						return err
					}
					// Two films in three are in the dataset (TMDB id = the work's qid);
					// the third has an id the dataset doesn't hold.
					tmdb := 1 + i%works
					if i%3 == 2 {
						tmdb = works + 1 + i
					}
					if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_external_ids VALUES(?,'tmdb','movie',?)`, id, strconv.Itoa(tmdb)); err != nil {
						return err
					}
					films = append(films, id)
				}
				return nil
			})
		}
		c.Drain()
		return fmt.Sprintf("%d films", len(films))
	})
	if _, err = db.Exec(`ANALYZE`); err != nil {
		t.Fatal(err)
	}

	s := publish.service(db, filepath.Join(scratch, "title-dataset"))
	measure("download, verify, import", func() string {
		steps := 0
		for more := true; more; steps++ {
			if more, err = s.Step(context.Background(), 500); err != nil {
				t.Fatal(err)
			}
		}
		var matched, tags int
		db.QueryRow(`SELECT count(*) FROM catalog_dataset_works`).Scan(&matched)
		db.QueryRow(`SELECT count(*) FROM catalog_dataset_tags`).Scan(&tags)
		return fmt.Sprintf("%d steps, %d works matched, %d tags", steps, matched, tags)
	})
	measure("derive facets", func() string {
		c.Drain()
		var facets int
		db.QueryRow(`SELECT count(*) FROM catalog_rec_facets WHERE facet LIKE 'x:%'`).Scan(&facets)
		return fmt.Sprintf("%d dataset facets", facets)
	})
	report.Phases[len(report.Phases)-1].BytesOnDisk = size(dbPath, dbPath+"-wal")

	// Twenty profiles with thirty films each, then Home with the dataset's rows.
	for p := 0; p < profiles; p++ {
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			for k := 0; k < 30; k++ {
				at := time.Now().UTC().Add(-time.Duration(k) * time.Hour).Format("2006-01-02T15:04:05.000Z")
				if _, err := tx.ExecContext(ctx, `INSERT INTO personal_history(id,profile_id,item_id,started_at,updated_at,sequence,position,unit,completed) VALUES(?,?,?,?,?,1,5400000,0,1)`,
					fmt.Sprintf("h%d-%d", p, k), fmt.Sprintf("p%d", p), films[(p*7919+k*104729)%len(films)], at, at); err != nil {
					return err
				}
			}
			return nil
		})
	}
	c.Drain()
	home := catalog.New(db)
	measure("Home, 20 profiles", func() string {
		var durations []time.Duration
		rows := 0
		for p := 0; p < profiles; p++ {
			profile := fmt.Sprintf("p%d", p)
			start := time.Now()
			doc, err := home.HomeRows(catalog.HomeRequest{Viewer: catalog.Viewer{Profile: profile, Fence: profile, Libraries: []string{"films"}}, ServerID: "s", Profile: profile, ViewerFence: profile, Libraries: []string{"films"}, Now: time.Now(), RowOrder: []string{"recommended", "for_you"}})
			if err != nil {
				t.Fatal(err)
			}
			durations = append(durations, time.Since(start))
			for _, row := range doc.Rows {
				if strings.HasPrefix(row.ID, "for_you:tag:") {
					rows++
				}
			}
		}
		slices.Sort(durations)
		return fmt.Sprintf("p50 %s p95 %s max %s, %d dataset rows", durations[len(durations)/2].Round(time.Millisecond), durations[len(durations)*95/100].Round(time.Millisecond), durations[len(durations)-1].Round(time.Millisecond), rows)
	})

	// A weekly delta changing 1% of works.
	measure("weekly delta (1%)", func() string {
		next := filepath.Join(scratch, "delta.sqlite")
		changed := buildScaleDataset(t, next, "2", works, tagsPerWork, similarPerWork, vocabulary, func(qid int) bool { return qid%100 == 7 })
		d := publish.add("screen-1-2", next)
		d.Version, d.BaseVersion = "2", "1"
		publish.domains["screen"] = ManifestDomain{DatasetVersion: "2", VocabularyVersion: 2, Full: publish.domains["screen"].Full, Deltas: []ManifestFile{d}}
		publish.sequence = 2
		if _, err := db.Exec(`UPDATE enrichment_domains SET checked_at='2000-01-01T00:00:00Z'`); err != nil {
			t.Fatal(err)
		}
		for more := true; more; {
			if more, err = s.Step(context.Background(), 500); err != nil {
				t.Fatal(err)
			}
		}
		c.Drain()
		var version string
		db.QueryRow(`SELECT version FROM enrichment_domains WHERE domain='screen'`).Scan(&version)
		return fmt.Sprintf("%d works in the delta, installed %s", changed, version)
	})

	// Off: the purge of everything the dataset brought.
	measure("switch off (purge)", func() string {
		if _, err := db.Exec(`INSERT INTO server_settings(singleton,updated_ms,"recommendations.titleDataset") VALUES(1,0,0) ON CONFLICT(singleton) DO UPDATE SET "recommendations.titleDataset"=0`); err != nil {
			t.Fatal(err)
		}
		steps := 0
		for more := true; more; steps++ {
			if more, err = s.Step(context.Background(), 500); err != nil {
				t.Fatal(err)
			}
		}
		c.Drain()
		var left int
		db.QueryRow(`SELECT count(*) FROM catalog_dataset_tags`).Scan(&left)
		return fmt.Sprintf("%d steps, %d tags left", steps, left)
	})
	report.Phases[len(report.Phases)-1].BytesOnDisk = size(dbPath, dbPath+"-wal")

	raw, _ := json.MarshalIndent(report, "", " ")
	if out := os.Getenv("PORTICO_DATASET_SCALE_REPORT"); out != "" {
		if err := os.WriteFile(out, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("report:\n%s", raw)
}

// buildScaleDataset writes a full dataset file (or, given changed, a delta of
// the works it selects, with new tags). Works are qid 1..n with TMDB id = qid.
func buildScaleDataset(t *testing.T, path, version string, n, tags, similar, vocabulary int, changed func(qid int) bool) int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	schema, err := os.ReadFile("testdata/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{string(schema), `PRAGMA journal_mode=OFF`, `PRAGMA synchronous=OFF`} {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	kind := "full"
	if changed != nil {
		kind = "delta"
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	mustTx := func(q string, args ...any) {
		if _, err := tx.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustTx(`INSERT INTO kinds VALUES(1,'movie'),(2,'show'),(3,'artist'),(4,'album'),(5,'book')`)
	mustTx(`INSERT INTO providers VALUES(1,'tmdb','movie'),(2,'tmdb','tv'),(3,'tvdb','series'),(4,'tvdb','movie'),(5,'anilist','anime'),(6,'imdb','title')`)
	mustTx(`INSERT INTO meta VALUES('kind',?),('dataset_version',?),('schema_version','3')`, kind, version)
	if changed != nil {
		mustTx(`INSERT INTO meta VALUES('base_version','1')`)
	}
	families := []string{"mood", "tone", "theme", "subgenre", "appeal", "setting", "pacing", "style", "narrative", "origin"}
	for v := 1; v <= vocabulary; v++ {
		family := families[v%len(families)]
		mustTx(`INSERT INTO vocabulary VALUES(?,?,?,?,'d','["movie","show"]',2,NULL,?)`, v, fmt.Sprintf("%s:t%03d", family, v), family, fmt.Sprintf("Tag %03d", v), fmt.Sprintf("tag %03d titles", v))
	}
	insWork, _ := tx.Prepare(`INSERT INTO works VALUES(?,1,0,0,1,2,?,10,?)`)
	insID, _ := tx.Prepare(`INSERT INTO work_ids VALUES(1,?,?)`)
	insTag, _ := tx.Prepare(`INSERT INTO work_tags VALUES(?,?,?)`)
	insSim, _ := tx.Prepare(`INSERT INTO work_similar VALUES(?,?,?,?)`)
	insSrc, _ := tx.Prepare(`INSERT INTO work_sources VALUES(?,3,'www',1)`)
	count := 0
	for qid := 1; qid <= n; qid++ {
		if changed != nil && !changed(qid) {
			continue
		}
		count++
		shift := 0
		if changed != nil {
			shift = 1
		}
		for _, step := range []struct {
			s    *sql.Stmt
			args []any
		}{{insWork, []any{qid, 1950 + qid%70, "Work " + strconv.Itoa(qid)}}, {insID, []any{qid, qid}}, {insSrc, []any{qid}}} {
			if _, err = step.s.Exec(step.args...); err != nil {
				t.Fatal(err)
			}
		}
		for k := 0; k < tags; k++ {
			if _, err = insTag.Exec(qid, 1+(qid*7+k*31+shift)%vocabulary, 1+(qid+k)%3); err != nil && !strings.Contains(err.Error(), "UNIQUE") {
				t.Fatal(err)
			}
		}
		for r := 1; r <= similar; r++ {
			target := 1 + (qid+r*9973)%n
			if target == qid {
				target = 1 + target%n
			}
			if _, err = insSim.Exec(qid, r, target, 1+r%5); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if changed == nil {
		if _, err = db.Exec(`VACUUM`); err != nil {
			t.Fatal(err)
		}
	}
	return count
}

// diskPublisher serves a signed manifest from memory and the (large) dataset
// files from disk, through the service's own verification path.
type diskPublisher struct {
	*publisher
	dir   string
	paths map[string]string
}

func newDiskPublisher(t *testing.T, dir string) *diskPublisher {
	return &diskPublisher{publisher: newPublisher(t), dir: dir, paths: map[string]string{}}
}

func (p *diskPublisher) add(name, source string) ManifestFile {
	p.t.Helper()
	in, err := os.Open(source)
	if err != nil {
		p.t.Fatal(err)
	}
	defer in.Close()
	target := filepath.Join(p.dir, name+".sqlite.gz")
	out, err := os.Create(target)
	if err != nil {
		p.t.Fatal(err)
	}
	hash := sha256.New()
	zw := gzip.NewWriter(io.MultiWriter(out, hash))
	n, err := io.Copy(zw, bufio.NewReaderSize(in, 1<<20))
	if err == nil {
		err = zw.Close()
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		p.t.Fatal(err)
	}
	info, _ := os.Stat(target)
	p.paths["/"+name+".sqlite.gz"] = target
	return ManifestFile{URL: name + ".sqlite.gz", SHA256: hex.EncodeToString(hash.Sum(nil)), Bytes: info.Size(), UncompressedBytes: n}
}

func (p *diskPublisher) service(db *sql.DB, dir string) *Service {
	s := publisherService(db, p.publisher, dir)
	inner := s.cfg.Client.Transport
	s.cfg.Client.Transport = datasetRoundTrip(func(request *http.Request) (*http.Response, error) {
		if path, ok := p.paths[request.URL.Path]; ok {
			f, err := os.Open(path)
			if err != nil {
				return nil, err
			}
			info, _ := f.Stat()
			return &http.Response{StatusCode: http.StatusOK, Body: f, ContentLength: info.Size(), Header: http.Header{}, Request: request}, nil
		}
		return inner.RoundTrip(request)
	})
	return s
}

// peakRSSMiB is the process's peak resident set (Linux VmHWM), or 0.
func peakRSSMiB() float64 {
	raw, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "VmHWM:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, _ := strconv.ParseFloat(fields[1], 64)
				return kb / 1024
			}
		}
	}
	return 0
}

// stageTiming is one importer stage's total time and call count within a phase.
type stageTiming struct {
	Name    string  `json:"name"`
	Seconds float64 `json:"seconds"`
	Calls   int     `json:"calls"`
}
