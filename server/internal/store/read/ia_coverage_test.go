package read

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// newIACoverageSource seeds one configured source plus sealed and frontier
// directories for one run. Coverage is preparation evidence only: the writer
// re-reads every row before accepting anything.
func newIACoverageSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newForegroundSource(t)
	for _, q := range []string{
		`INSERT INTO library_sources(id,library_id,kind,name,configured_root,root,classification,generation,incarnation,root_identity,health,enabled,follow_symlinks,interval_seconds,identity_confirmed) VALUES('source-1','movies','local','Movies','/media','/media','local',1,'inc-source-1','/media','ready',1,0,0,1)`,
		`INSERT INTO library_sources(id,library_id,kind,name,configured_root,root,classification,generation,incarnation,root_identity,health,enabled,follow_symlinks,interval_seconds,identity_confirmed) VALUES('foreign-1','other','local','Other','/media','/media','local',1,'inc-foreign-1','/media','ready',1,0,0,1)`,
		`INSERT INTO jobs(id,library_id,status,processed,created_at) VALUES('run-1','movies','running',0,'')`,
		`INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('run-1','.','root-identity','root-revision','1','done',1,0)`,
		`INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('run-1','Clean','clean-identity','clean-revision','7','done',1,0)`,
		`INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('run-1','Clean/Sub','sub-identity','sub-revision','3','done',1,0)`,
	} {
		f.exec(t, q)
	}
	return f
}

// The skip veto forces a test-only partial index that INT has not admitted, so
// plan.Capture cannot mint a receipt here: the index is undeclared policy. The
// functional custody is composed over the deliberately unqualified fault registry
// and the plan test below EXPLAINs every definition on the same draft. No Capture
// golden is recorded; a Capture on the adopted schema is a later gate.
func composeIACoverageFixture(t *testing.T, f *browseFixture, definitions []stmt.Definition, gc governor.Config) *browseFixture {
	t.Helper()
	registry, err := stmttest.New(t, definitions)
	if err != nil {
		t.Fatal(err)
	}
	reader := browseReaderConfig()
	connector, err := sqliteconn.Connector(f.path, reader)
	if err != nil {
		t.Fatal(err)
	}
	g, a, err := governor.New(gc)
	if err != nil {
		t.Fatal(err)
	}
	f.governor, f.authority = g, a
	f.snapshots, err = governor.NewSnapshots(connector, reader, g, a, registry)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.snapshots.Close(); err != nil {
			t.Error(err)
		}
	})
	f.reader, err = NewBrowseReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func newIACoverageFixture(t *testing.T) (*browseFixture, *ForegroundReader) {
	t.Helper()
	return newIACoverageFixtureOverlay(t, true)
}

func newIACoverageFixtureOverlay(t *testing.T, skippedIndex bool) (*browseFixture, *ForegroundReader) {
	t.Helper()
	f := newIACoverageSource(t)
	if skippedIndex {
		ddl, err := os.ReadFile("testdata/ia-coverage-skipped-index-draft.sql")
		if err != nil {
			t.Fatal(err)
		}
		f.exec(t, string(ddl))
	}
	f = composeIACoverageFixture(t, f, append(browseDefinitions(), IACoverageHarnessDefinitions()...), readfixture.LiveConfig(time.Now))
	r, e := NewForegroundReader(f.snapshots)
	if e != nil {
		t.Fatal(e)
	}
	return f, r
}

func iaCoverageRead(t *testing.T, f *browseFixture, r *ForegroundReader, in IACoverageInput) (IACoveragePage, BrowseReadResult, error) {
	t.Helper()
	q, request, output := liveRequests("ia-coverage")
	var page IACoveragePage
	var result BrowseReadResult
	_, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		var e error
		page, result, e = r.IACoverageDirectoryState(ctx, request, time.Second, out, in)
		return e
	})
	return page, result, e
}

func iaCoverageInput(f *browseFixture) IACoverageInput {
	return IACoverageInput{View: f.input, Run: "run-1", Source: "source-1", Directory: "Clean"}
}

// The point returns the exact sealed directory row and proves the subtree has no
// pending, blocked or protected frontier.
func TestIACoverageDirectoryStateSeal(t *testing.T) {
	f, r := newIACoverageFixture(t)
	page, result, e := iaCoverageRead(t, f, r, iaCoverageInput(f))
	if e != nil {
		t.Fatal(e)
	}
	want := IACoverageDirectory{Path: "Clean", Identity: "clean-identity", Revision: "clean-revision", Cursor: "7", State: "done", Pages: 1}
	if !reflect.DeepEqual(page.Directory, want) || page.Pending || page.Blocked || page.Protected || !page.Sealed {
		t.Fatalf("coverage page=%+v", page)
	}
	if page.Library != 1 || page.LibraryKey != "movies" || page.Run != "run-1" || page.Source != "source-1" {
		t.Fatalf("coverage identity=%+v", page)
	}
	if result.LookupStatements == 0 || result.PublicationStatements == 0 || result.Attempts != 1 {
		t.Fatalf("statement accounting=%+v", result)
	}
}

// A descendant pending, blocked or protected frontier is partial, unreadable or
// refused coverage and can never seal the subtree. A sibling outside the exact
// directory bound does not leak in.
func TestIACoverageDirectoryStateSubtreeRefusals(t *testing.T) {
	for name, seed := range map[string]string{
		"pending":         `INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('run-1','Clean/Partial','partial-identity','partial-revision','1','pending',1,0)`,
		"blocked":         `INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('run-1','Clean/Blocked','blocked-identity','blocked-revision','1','blocked',1,0)`,
		"protected_issue": `INSERT INTO inventory_scan_issues(job_id,relative_path,reason,line,protect,warning) VALUES('run-1','Clean/Secret','unreadable_entry',0,1,1)`,
	} {
		t.Run(name, func(t *testing.T) {
			f, r := newIACoverageFixture(t)
			f.exec(t, seed)
			page, _, e := iaCoverageRead(t, f, r, iaCoverageInput(f))
			if e != nil || page.Sealed {
				t.Fatalf("refused subtree sealed=%+v err=%v", page, e)
			}
			switch name {
			case "pending":
				if !page.Pending || page.Blocked || page.Protected {
					t.Fatalf("pending flags=%+v", page)
				}
			case "blocked":
				if page.Pending || !page.Blocked || page.Protected {
					t.Fatalf("blocked flags=%+v", page)
				}
			case "protected_issue":
				if page.Pending || page.Blocked || !page.Protected {
					t.Fatalf("protected flags=%+v", page)
				}
			}
		})
	}
	t.Run("sibling_scope", func(t *testing.T) {
		f, r := newIACoverageFixture(t)
		f.exec(t, `INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('run-1','Other','other-identity','other-revision','1','pending',1,0)`)
		f.exec(t, `INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('run-1','CleanTwo','two-identity','two-revision','1','blocked',1,0)`)
		page, _, e := iaCoverageRead(t, f, r, iaCoverageInput(f))
		if e != nil || !page.Sealed || page.Pending || page.Blocked || page.Protected {
			t.Fatalf("sibling frontier leaked=%+v err=%v", page, e)
		}
	})
}

// A done row alone never proves absence: a skip at the selected directory, the
// root or any descendant keeps the subtree unsealed, while skips outside the exact
// half-open bound never leak in.
func TestIACoverageDirectoryStateSkipVeto(t *testing.T) {
	t.Run("selected_skip", func(t *testing.T) {
		f, r := newIACoverageFixture(t)
		f.exec(t, `UPDATE inventory_directories SET skipped=1 WHERE job_id='run-1' AND relative_path='Clean'`)
		page, _, e := iaCoverageRead(t, f, r, iaCoverageInput(f))
		if e != nil || !page.Skipped || page.Sealed || page.Directory.Skipped != 1 {
			t.Fatalf("selected skip sealed=%+v err=%v", page, e)
		}
	})
	t.Run("root_skip", func(t *testing.T) {
		f, r := newIACoverageFixture(t)
		f.exec(t, `UPDATE inventory_directories SET skipped=1 WHERE job_id='run-1' AND relative_path='.'`)
		in := iaCoverageInput(f)
		in.Directory = "."
		page, _, e := iaCoverageRead(t, f, r, in)
		if e != nil || !page.Skipped || page.Sealed {
			t.Fatalf("root skip sealed=%+v err=%v", page, e)
		}
	})
	t.Run("deep_descendant_skip", func(t *testing.T) {
		f, r := newIACoverageFixture(t)
		f.exec(t, `INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('run-1','Clean/Sub/Deep','deep-identity','deep-revision','1','done',1,1)`)
		page, _, e := iaCoverageRead(t, f, r, iaCoverageInput(f))
		if e != nil || !page.Skipped || page.Sealed {
			t.Fatalf("deep skip sealed=%+v err=%v", page, e)
		}
	})
	t.Run("out_of_subtree_skip", func(t *testing.T) {
		f, r := newIACoverageFixture(t)
		f.exec(t, `INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('run-1','CleanTwo','two-identity','two-revision','1','done',1,1)`)
		f.exec(t, `INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('run-1','Other','other-identity','other-revision','1','done',1,1)`)
		page, _, e := iaCoverageRead(t, f, r, iaCoverageInput(f))
		if e != nil || page.Skipped || !page.Sealed {
			t.Fatalf("sibling skip leaked=%+v err=%v", page, e)
		}
	})
	t.Run("zero_skip_empty_subtree", func(t *testing.T) {
		f, r := newIACoverageFixture(t)
		in := iaCoverageInput(f)
		in.Directory = "Clean/Sub"
		page, _, e := iaCoverageRead(t, f, r, in)
		if e != nil || page.Skipped || !page.Sealed || len(page.Directory.Path) == 0 {
			t.Fatalf("clean empty subtree sealed=%+v err=%v", page, e)
		}
	})
}

// Without the test-only partial index the bounded skip check cannot prepare, so
// the read fails closed instead of publishing a sealed page from an unbounded or
// empty window.
func TestIACoverageDirectoryStateSkippedAccessUnavailable(t *testing.T) {
	f, r := newIACoverageFixtureOverlay(t, false)
	page, _, e := iaCoverageRead(t, f, r, iaCoverageInput(f))
	if e == nil || page.Sealed {
		t.Fatalf("skip proof without the bounded index sealed=%+v err=%v", page, e)
	}
}

// A foreign library, a foreign source and a missing directory are denied before
// any frontier is published.
func TestIACoverageDirectoryStateDenials(t *testing.T) {
	for name, mutate := range map[string]func(*browseFixture, *IACoverageInput){
		"foreign_library":   func(f *browseFixture, in *IACoverageInput) { in.View.Library = 2 },
		"foreign_source":    func(_ *browseFixture, in *IACoverageInput) { in.Source = "foreign-1" },
		"missing_directory": func(_ *browseFixture, in *IACoverageInput) { in.Directory = "Missing" },
	} {
		t.Run(name, func(t *testing.T) {
			f, r := newIACoverageFixture(t)
			in := iaCoverageInput(f)
			mutate(f, &in)
			page, _, e := iaCoverageRead(t, f, r, in)
			if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(page, IACoveragePage{}) {
				t.Fatalf("denial=%+v err=%v", page, e)
			}
		})
	}
}

// The read is harness-only and requires ConsumeForeground custody.
func TestIACoverageDirectoryStateCustodyAndInput(t *testing.T) {
	f, r := newIACoverageFixture(t)
	for _, in := range []IACoverageInput{
		{View: f.input, Run: "run-1", Source: "source-1"},
		{View: f.input, Source: "source-1", Directory: "Clean"},
		{View: f.input, Run: "run-1", Directory: "Clean"},
		{View: f.input, Run: "run-1", Source: "source-1", Directory: "../escape"},
	} {
		page, _, e := iaCoverageRead(t, f, r, in)
		if !errors.Is(e, ErrInput) || !reflect.DeepEqual(page, IACoveragePage{}) {
			t.Fatalf("input %+v=%+v err=%v", in, page, e)
		}
	}
	page, _, e := r.IACoverageDirectoryState(t.Context(), foregroundRequest(), time.Second, BrowseOutput{}, iaCoverageInput(f))
	if !errors.Is(e, ErrInput) || !reflect.DeepEqual(page, IACoveragePage{}) {
		t.Fatal("missing foreground custody", e)
	}
}

// Every preparation statement is a closed read on the embedded inventory
// relations with an indexed SEARCH. The skipped-directory veto forces the
// test-only draft index (testdata/ia-coverage-skipped-index-draft.sql), which INT
// has not admitted, so this stays EXPLAIN-only on that draft fixture: no Capture
// golden is recorded and the group C receipt table is not touched.
func TestIACoverageDirectoryPlans(t *testing.T) {
	f, _ := newIACoverageFixture(t)
	defs := IACoverageHarnessDefinitions()
	if len(defs) != 10 {
		t.Fatal("coverage read family inventory changed", len(defs))
	}
	for _, d := range defs {
		if d.Kind != stmt.Read || !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(plan.LintSQLDefinition(d)) != 0 {
			t.Fatal("coverage read statements must retain closed hot read grammar", d.ID)
		}
		for _, cohort := range d.Cohorts {
			rows, err := f.conn.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+d.SQL, cohort.Bindings...)
			if err != nil {
				t.Fatal(d.ID, cohort.Name, err)
			}
			searched := false
			var details []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				details = append(details, detail)
				if strings.Contains(detail, "TEMP B-TREE") || strings.Contains(detail, "SCAN ") && !strings.HasPrefix(detail, "SCAN CONSTANT") {
					rows.Close()
					t.Fatal(d.ID, cohort.Name, detail)
				}
				if strings.HasPrefix(detail, "SEARCH ") {
					searched = true
				}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			rows.Close()
			if !searched {
				t.Fatal(d.ID, cohort.Name, "read has no indexed SEARCH")
			}
			if d.ID == readStatementID("foreground.ia.coverage.skipped") {
				want := "SEARCH inventory_directories USING INDEX inventory_directories_skipped (job_id=? AND relative_path>? AND relative_path<?)"
				if len(details) != 1 || details[0] != want {
					t.Fatal(d.ID, cohort.Name, details, want)
				}
			}
		}
	}
	t.Logf("%d coverage directory statements EXPLAINed on the group C draft; Capture golden deferred to index adoption", len(defs))
}
