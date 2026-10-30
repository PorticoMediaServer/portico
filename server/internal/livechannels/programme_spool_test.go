package livechannels

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// The live-guide spool is a private, disposable file. Its writes must never
// wait on the catalogue's process-wide writer gate: a guide refresh that spills
// would otherwise queue every sign-in and progress write behind its own
// transactions, for a database no catalogue reader can see. This holds the gate
// and runs the whole spool lifecycle in another goroutine; a spool statement
// that still asks for the gate blocks, and the deadline reports it.
func TestGuideSpoolWritesDoNotQueueOnTheCatalogueGate(t *testing.T) {
	ctx := context.Background()
	catalogue, e := persistence.Open(filepath.Join(t.TempDir(), "catalogue.sqlite"))
	if e != nil {
		t.Fatalf("open catalogue: %v", e)
	}
	t.Cleanup(func() { catalogue.Close() })
	var live int
	if e = catalogue.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE name='live_programmes'`).Scan(&live); e != nil {
		t.Fatalf("read catalogue schema: %v", e)
	}
	if live != 1 {
		t.Fatalf("catalogue is not the persistent schema: live_programmes=%d", live)
	}
	held, e := dbwork.Begin(ctx, catalogue, dbwork.ClassInteractive)
	if e != nil {
		t.Fatalf("hold the catalogue write gate: %v", e)
	}
	if !dbwork.WriteGate().Stats().Active {
		held.Rollback()
		t.Fatal("the catalogue write gate is not held, so independence is not being tested")
	}
	acquired := dbwork.WriteGate().Stats().Acquired
	publications := dbwork.Publications()
	authority := dbwork.AuthorityGeneration()

	finished := make(chan spoolRun, 1)
	go func() {
		run, err := exercisePrivateSpool()
		run.err = err
		finished <- run
	}()

	watchdog := time.NewTimer(30 * time.Second)
	defer watchdog.Stop()
	var run spoolRun
	select {
	case run = <-finished:
	case <-watchdog.C:
		held.Rollback()
		t.Fatal("a private guide spool waited on the catalogue write gate")
	}
	held.Rollback()
	if run.err != nil {
		t.Fatalf("private spool lifecycle: %v", run.err)
	}
	if run.count != 260 || run.read != 260 {
		t.Fatalf("spool kept %d and read %d programmes, want 260/260", run.count, run.read)
	}
	if run.skipped != 1 || len(run.examples) != 1 || run.examples[0] != "live.import.programmeEndMissing" {
		t.Fatalf("spool diagnostics: skipped=%d examples=%v", run.skipped, run.examples)
	}
	if !reflect.DeepEqual(run.ranges, run.wantRanges) {
		t.Fatalf("spool ranges: got %v want %v", run.ranges, run.wantRanges)
	}
	if len(run.databases) != 1 || filepath.Clean(run.databases[0]) != filepath.Clean(run.path) {
		t.Fatalf("the spool handle sees more than one database file: %v (spool %s)", run.databases, run.path)
	}
	if !run.filesGone {
		t.Fatalf("closing the spool left %s or its journal behind", run.path)
	}
	if !run.rollbackGone {
		t.Fatal("closing a spool with an open transaction left its files behind")
	}
	if got := dbwork.WriteGate().Stats().Acquired; got != acquired {
		t.Fatalf("private spool acquired the catalogue write gate %d time(s)", got-acquired)
	}
	if got := dbwork.Publications(); got != publications {
		t.Fatalf("private spool commits moved the catalogue publication counter by %d", got-publications)
	}
	if got := dbwork.AuthorityGeneration(); got != authority {
		t.Fatalf("private spool commits moved the authority generation by %d", got-authority)
	}
	var leaked int
	if e = catalogue.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE name='programmes'`).Scan(&leaked); e != nil {
		t.Fatalf("read catalogue schema after the spool ran: %v", e)
	}
	if leaked != 0 {
		t.Fatal("the private spool table appeared in the persistent catalogue")
	}
}

// A spool export must match the in-memory path for the same guide: the same
// rows, in the same order, with the same diagnostics for a missing end, an
// overlap and the day cut. The reference below is the real parser on a guide
// small enough to stay in memory.
func TestGuideSpoolNormalisationMatchesTheMemoryPath(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Hour)
	type row struct {
		channel, title string
		start, end     time.Time
	}
	rows := []row{
		{"a", "A one", base.Add(1 * time.Hour), base.Add(2 * time.Hour)},
		{"a", "A two", base.Add(2 * time.Hour), base.Add(3 * time.Hour)},
		{"gap", "No end and no successor", base.Add(10 * time.Hour), time.Time{}},
		{"close", "No end but a successor", base.Add(11 * time.Hour), time.Time{}},
		{"close", "The successor", base.Add(12 * time.Hour), base.Add(13 * time.Hour)},
		{"overlap", "Long slot", base.Add(13 * time.Hour), base.Add(15 * time.Hour)},
		{"overlap", "Shorter slot inside it", base.Add(14 * time.Hour), base.Add(15 * time.Hour)},
		{"cut", "Ended yesterday", base.Add(-30 * time.Hour), base.Add(-29 * time.Hour)},
		{"cut", "Inside the window", base.Add(2 * time.Hour), base.Add(3 * time.Hour)},
		{"cut", "Past the window", base.Add(30 * time.Hour), base.Add(31 * time.Hour)},
	}
	var playlist, guide strings.Builder
	playlist.WriteString("#EXTM3U\n")
	guide.WriteString(`<?xml version="1.0" encoding="UTF-8"?><tv>`)
	for _, channel := range []string{"a", "gap", "close", "overlap", "cut"} {
		fmt.Fprintf(&playlist, "#EXTINF:-1 tvg-id=%q,Channel %s\nhttps://streams.example/%s\n", channel, channel, channel)
		fmt.Fprintf(&guide, `<channel id=%q><display-name>Channel %s</display-name></channel>`, channel, channel)
	}
	for _, r := range rows {
		stop := ""
		if !r.end.IsZero() {
			stop = ` stop="` + r.end.Format("20060102150405 -0700") + `"`
		}
		fmt.Fprintf(&guide, `<programme channel=%q start=%q%s><title>%s</title></programme>`, r.channel, r.start.Format("20060102150405 -0700"), stop, r.title)
	}
	guide.WriteString(`</tv>`)
	settings := &LiveSourceSettings{}
	reference, e := parse(SourceInput{Name: "Spool parity", Playlist: playlist.String(), Guide: guide.String(), TunerCount: 1, configuration: settings, guideDays: 1})
	if e != nil {
		t.Fatalf("parse the in-memory reference: %v", e)
	}
	defer reference.close()

	facts := xmltvFacts(nil, nil, nil, "", nil, nil, false, false)
	source := parsedSource{diagnostics: ImportDiagnostics{Examples: []string{}}}
	spool, e := newProgrammeSpool()
	if e != nil {
		t.Fatalf("open the spool: %v", e)
	}
	source.spool = spool
	defer source.close()
	for _, r := range rows {
		p := parsedProgramme{key: r.channel + "\x00" + r.start.Format(time.RFC3339), channel: r.channel, title: r.title, lineage: "interval-only", newEvidence: "unknown", facts: facts, start: r.start.UTC()}
		if !r.end.IsZero() {
			p.end = r.end.UTC()
		}
		kept, e := spool.add(p)
		if e != nil {
			t.Fatalf("add %q: %v", r.title, e)
		}
		if !kept {
			source.diagnostics.SkippedProgrammes++
		}
	}
	if e = source.normaliseSpool(1); e != nil {
		t.Fatalf("normalise the spool: %v", e)
	}
	var got []parsedProgramme
	if e = source.batches(context.Background(), 3, func(batch []parsedProgramme) error {
		got = append(got, batch...)
		return nil
	}); e != nil {
		t.Fatalf("read the spool: %v", e)
	}
	if source.programmeCount() != 7 || len(got) != 7 {
		t.Fatalf("spool kept %d counted and %d read programmes, want 7", source.programmeCount(), len(got))
	}
	if !reflect.DeepEqual(got, reference.programmes) {
		t.Fatalf("spool rows differ from the memory path:\nspool: %+v\nmemory: %+v", got, reference.programmes)
	}
	if !reflect.DeepEqual(source.diagnostics, reference.diagnostics) {
		t.Fatalf("spool diagnostics %+v differ from the memory path %+v", source.diagnostics, reference.diagnostics)
	}
	if source.diagnostics.AdjustedProgrammes != 2 || source.diagnostics.SkippedProgrammes != 1 {
		t.Fatalf("spool diagnostics: %+v", source.diagnostics)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].channel == got[i].channel && got[i-1].end.After(got[i].start) {
			t.Fatalf("%q overlaps %q after normalisation", got[i-1].title, got[i].title)
		}
	}
}

// A repeated slot is kept once, and the caller is told it was skipped, exactly
// as the in-memory path counts a repeat.
func TestGuideSpoolDuplicateProgrammeIsCountedOnce(t *testing.T) {
	spool, e := newProgrammeSpool()
	if e != nil {
		t.Fatalf("open the spool: %v", e)
	}
	source := parsedSource{spool: spool, diagnostics: ImportDiagnostics{Examples: []string{}}}
	defer source.close()
	base := time.Now().UTC().Truncate(time.Hour)
	first := parsedProgramme{key: "one\x00" + base.Format(time.RFC3339), channel: "one", title: "First", lineage: "interval-only", newEvidence: "unknown", facts: "{}", start: base, end: base.Add(time.Hour)}
	second := first
	second.key, second.title = "one\x00second", "Second"
	if kept, e := spool.add(first); e != nil || !kept {
		t.Fatalf("first insert: kept=%v err=%v", kept, e)
	}
	if kept, e := spool.add(second); e != nil || kept {
		t.Fatalf("duplicate insert: kept=%v err=%v", kept, e)
	}
	source.diagnostics.SkippedProgrammes++
	if e = source.normaliseSpool(0); e != nil {
		t.Fatalf("normalise the spool: %v", e)
	}
	if spool.count != 1 || source.programmeCount() != 1 {
		t.Fatalf("duplicate stored: count=%d programmeCount=%d", spool.count, source.programmeCount())
	}
	var title string
	if e = spool.db.QueryRow(`SELECT title FROM programmes`).Scan(&title); e != nil {
		t.Fatalf("read the stored row: %v", e)
	}
	if title != "First" {
		t.Fatalf("the duplicate replaced the first row: %q", title)
	}
	if source.diagnostics.SkippedProgrammes != 1 || source.diagnostics.AdjustedProgrammes != 0 {
		t.Fatalf("duplicate diagnostics: %+v", source.diagnostics)
	}
}

type spoolRun struct {
	count, read  int
	skipped      int
	examples     []string
	ranges       map[string][2]string
	wantRanges   map[string][2]string
	path         string
	databases    []string
	filesGone    bool
	rollbackGone bool
	err          error
}

// exercisePrivateSpool runs the whole spool lifecycle the guide importer runs:
// create, add in 128-row batches, flush, normalise (which adjusts, deletes and
// cuts), read back in batches and close. It never touches the catalogue.
func exercisePrivateSpool() (spoolRun, error) {
	out := spoolRun{}
	spool, e := newProgrammeSpool()
	if e != nil {
		return out, e
	}
	out.path = spool.path
	source := parsedSource{spool: spool, diagnostics: ImportDiagnostics{Examples: []string{}}}
	run := func() error {
		base := time.Now().UTC().Truncate(time.Hour).Add(-2 * time.Hour)
		out.wantRanges = map[string][2]string{}
		for i := 0; i < 260; i++ {
			channel := "one"
			if i%2 == 1 {
				channel = "two"
			}
			p := parsedProgramme{key: fmt.Sprintf("%s\x00%d", channel, i), channel: channel, title: fmt.Sprintf("Programme %d", i), lineage: "interval-only", newEvidence: "unknown", facts: "{}", start: base.Add(time.Duration(i) * 5 * time.Minute)}
			p.end = p.start.Add(5 * time.Minute)
			if _, e := spool.add(p); e != nil {
				return e
			}
			from, to := p.start.Format(time.RFC3339), p.end.Format(time.RFC3339)
			v := out.wantRanges[channel]
			if v[0] == "" || from < v[0] {
				v[0] = from
			}
			if to > v[1] {
				v[1] = to
			}
			out.wantRanges[channel] = v
		}
		orphan := parsedProgramme{key: "three\x00orphan", channel: "three", title: "No end", lineage: "interval-only", newEvidence: "unknown", facts: "{}", start: base}
		if _, e := spool.add(orphan); e != nil {
			return e
		}
		if e := source.normaliseSpool(7); e != nil {
			return e
		}
		out.count = source.programmeCount()
		out.skipped = source.diagnostics.SkippedProgrammes
		out.examples = append(out.examples, source.diagnostics.Examples...)
		var ranges map[string][2]string
		if ranges, e = source.ranges(); e != nil {
			return e
		}
		out.ranges = ranges
		if e = source.batches(context.Background(), 7, func(batch []parsedProgramme) error {
			out.read += len(batch)
			return nil
		}); e != nil {
			return e
		}
		rows, e := spool.db.Query(`PRAGMA database_list`)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var sequence int
			var name, file string
			if e = rows.Scan(&sequence, &name, &file); e != nil {
				return e
			}
			if name == "main" {
				out.databases = append(out.databases, file)
			}
		}
		return rows.Err()
	}()
	source.close()
	out.filesGone = !spoolPathExists(out.path) && !spoolPathExists(out.path+"-journal")

	abandoned, e := newProgrammeSpool()
	if e != nil {
		return out, e
	}
	path := abandoned.path
	if _, e = abandoned.add(parsedProgramme{key: "one\x00open", channel: "one", title: "Open", lineage: "interval-only", newEvidence: "unknown", facts: "{}", start: time.Now().UTC()}); e != nil {
		abandoned.close()
		return out, e
	}
	abandoned.close()
	out.rollbackGone = !spoolPathExists(path) && !spoolPathExists(path+"-journal")
	return out, run
}

func spoolPathExists(path string) bool {
	_, e := os.Stat(path)
	return e == nil
}
