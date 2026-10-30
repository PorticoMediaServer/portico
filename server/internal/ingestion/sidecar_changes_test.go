package ingestion

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// sidecarFixture is a scanned movie library whose objects are all settled, so
// a sidecar change's effect is exactly the rows it reset.
func sidecarFixture(t *testing.T, files ...string) (*loopFixture, Job) {
	t.Helper()
	f := newLoopFixture(t, "movie", files...)
	f.finish(f.queue())
	f.exec(`UPDATE library_scan_policies SET tier='basic',operations_json='["probe","local_metadata"]' WHERE library_id=?`, f.library)
	f.markObjectsSettled([]string{"probe", "local_metadata"})
	return f, f.analysisOnlyJob()
}

func (f *loopFixture) resetBySidecars() []string {
	f.t.Helper()
	rows, err := f.db.Query(`SELECT relative_path FROM inventory_objects WHERE retired=0 AND analysis_state='pending' AND NOT EXISTS(SELECT 1 FROM json_each(analysis_operations_json) WHERE value='local_metadata') ORDER BY relative_path`)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var path string
		if err = rows.Scan(&path); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, filepath.ToSlash(path))
	}
	return out
}

// TestSidecarChangeQueuesOnlyTheFolderAndItsDescendants pins the S2 range: a
// changed album/show folder resets its own files and every descendant, never a
// sibling whose name merely shares the prefix ("Album (Deluxe)", "Album2"),
// pages exactly across the folder/descendant boundary, consumes rows that are
// no longer available without queuing them, and deletes the change on its last
// page rather than paying a separate empty continuation.
func TestSidecarChangeQueuesOnlyTheFolderAndItsDescendants(t *testing.T) {
	f, job := sidecarFixture(t,
		"Album/One (2000).mkv",
		"Album/Disc 1/Two (2001).mkv",
		"Album/Disc 1/Deep/Three (2002).mkv",
		"Album/Disc 2/Four (2003).mkv",
		"Album/Disc 2/Gone (2009).mkv",
		"Album (Deluxe)/Five (2004).mkv",
		"Album2/Six (2005).mkv",
		"Other/Seven (2006).mkv",
		"Eight (2007).mkv",
	)
	f.exec(`UPDATE inventory_objects SET state='unavailable' WHERE relative_path=?`, filepath.FromSlash("Album/Disc 2/Gone (2009).mkv"))
	// A retired row in the subtree is read by the window but never reset.
	f.exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,seen_job,directory_path,retired,analysis_operations_json) SELECT 'retired-row',source_id,asset_id,root_incarnation,relative_path||'.old','r','{}',0,0,'',directory_path,1,'["local_metadata"]' FROM inventory_objects WHERE relative_path=?`, filepath.FromSlash("Album/Disc 1/Two (2001).mkv"))
	f.exec(`INSERT INTO inventory_sidecar_changes(job_id,relative_path) VALUES(?,?)`, job.ID, "Album")
	old := sidecarChangePage
	sidecarChangePage = 2
	defer func() { sidecarChangePage = old }()
	source := f.librarySource()
	pages := 0
	for {
		more, err := f.s.enqueueSidecarChanges(context.Background(), job.ID, source)
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			break
		}
		pages++
		if pages > 10 {
			t.Fatal("the sidecar change never finished")
		}
	}
	// Six rows under Album (one unavailable, one retired), two per page: the
	// fourth page comes back empty, so the change finishes in four pages.
	if pages != 4 {
		t.Fatalf("sidecar change took %d pages, want 4", pages)
	}
	if n := f.count(`SELECT count(*) FROM inventory_sidecar_changes WHERE job_id=?`, job.ID); n != 0 {
		t.Fatalf("finished sidecar change left %d rows", n)
	}
	got := f.resetBySidecars()
	want := []string{"Album/Disc 1/Deep/Three (2002).mkv", "Album/Disc 1/Two (2001).mkv", "Album/Disc 2/Four (2003).mkv", "Album/One (2000).mkv"}
	sort.Strings(want)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("reset by the Album sidecar:\n got %v\nwant %v", got, want)
	}
	if n := f.count(`SELECT count(*) FROM inventory_objects WHERE analysis_state='pending' AND state='unavailable'`); n != 0 {
		t.Fatalf("an unavailable object was queued by the sidecar change")
	}
	if n := f.count(`SELECT count(*) FROM inventory_objects WHERE id='retired-row' AND analysis_operations_json='["local_metadata"]'`); n != 1 {
		t.Fatal("a retired object was reset by the sidecar change")
	}
}

// TestSidecarChangeAtTheRootPagesTheWholeSource keeps the "." case: a root
// sidecar governs every file, read in id order over the source index.
func TestSidecarChangeAtTheRootPagesTheWholeSource(t *testing.T) {
	f, job := sidecarFixture(t, "Album/One (2000).mkv", "Other/Two (2001).mkv", "Three (2002).mkv")
	f.exec(`INSERT INTO inventory_sidecar_changes(job_id,relative_path) VALUES(?,?)`, job.ID, ".")
	old := sidecarChangePage
	sidecarChangePage = 2
	defer func() { sidecarChangePage = old }()
	source := f.librarySource()
	for pages := 0; ; pages++ {
		more, err := f.s.enqueueSidecarChanges(context.Background(), job.ID, source)
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			if pages != 2 {
				t.Fatalf("root sidecar change took %d pages, want 2", pages)
			}
			break
		}
	}
	if got := f.resetBySidecars(); len(got) != 3 {
		t.Fatalf("root sidecar reset %v, want every file", got)
	}
}

// TestSidecarChangePlansSeekTheirIndexInOrder is the S2 plan assertion: every
// window is an index search answered in order (no temporary sort, so LIMIT
// bounds the rows examined), and the descendant window is a directory_path
// range on inventory_objects_directory, not the source index.
func TestSidecarChangePlansSeekTheirIndexInOrder(t *testing.T) {
	f, _ := sidecarFixture(t, "Album/One (2000).mkv")
	for _, tc := range []struct {
		name, query, index string
		args               []any
	}{
		{"source", sidecarSourceRows, "inventory_objects_source", []any{"i", "s", "", 64}},
		{"folder", sidecarFolderRows, "inventory_objects_directory", []any{"s", "i", "Album", "", 64}},
		{"descendants", sidecarDescendantRows, "inventory_objects_directory", []any{"s", "i", "Album/", 0, "", "Album0", 64}},
	} {
		rows, err := f.db.Query(`EXPLAIN QUERY PLAN `+tc.query, tc.args...)
		if err != nil {
			t.Fatal(err)
		}
		plan := []string{}
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		text := strings.Join(plan, "\n")
		if !strings.Contains(text, "SEARCH inventory_objects USING INDEX "+tc.index) || strings.Contains(text, "TEMP B-TREE") {
			t.Fatalf("%s window plan:\n%s", tc.name, text)
		}
		if tc.name == "descendants" && !strings.Contains(text, "directory_path") {
			t.Fatalf("descendant window is not a directory_path range:\n%s", text)
		}
	}
}
