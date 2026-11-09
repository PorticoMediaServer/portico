package read

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

//go:embed ia_coverage_test.go
var iaCoverageJournal string

func iaCoverageHash(t *testing.T) string {
	h := sha256.Sum256([]byte(readfixture.ForegroundHash(t) + iaCoverageJournal))
	return hex.EncodeToString(h[:])
}

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

func newIACoverageFixture(t *testing.T) (*browseFixture, *ForegroundReader) {
	t.Helper()
	f := newIACoverageSource(t)
	f = composeReadFixture(t, f, append(browseDefinitions(), IACoverageHarnessDefinitions()...), readfixture.LiveConfig(time.Now), false, iaCoverageHash(t))
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
// relations with an indexed SEARCH. The group C receipt table is not touched, so
// a Capture golden can be recorded after this commit against the embedded
// reader; none is invented here.
func TestIACoverageDirectoryPlans(t *testing.T) {
	f, _ := newIACoverageFixture(t)
	defs := IACoverageHarnessDefinitions()
	if len(defs) != 9 {
		t.Fatal("coverage read family inventory changed", len(defs))
	}
	for _, d := range defs {
		if d.Kind != stmt.Read || !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(plan.LintSQL(d.SQL)) != 0 {
			t.Fatal("coverage read statements must retain closed hot read grammar", d.ID)
		}
		for _, cohort := range d.Cohorts {
			rows, err := f.conn.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+d.SQL, cohort.Bindings...)
			if err != nil {
				t.Fatal(d.ID, cohort.Name, err)
			}
			searched := false
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					rows.Close()
					t.Fatal(err)
				}
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
		}
	}
	t.Logf("%d coverage directory statements EXPLAINed on the embedded schema; Capture golden deferred", len(defs))
}
