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

//go:embed ia_reconcile_source_test.go
var iaReconcileSourceJournal string

func iaReconcileSourceHash(t *testing.T) string {
	h := sha256.Sum256([]byte(readfixture.ForegroundHash(t) + iaReconcileSourceJournal))
	return hex.EncodeToString(h[:])
}

// newIAReconcileSourceSource seeds two configured sources in one catalogue
// library plus a foreign library and retained placements: an exact pair, a
// same-native-id duplicate, a retired duplicate and a different identity.
func newIAReconcileSourceSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newForegroundSource(t)
	for _, q := range []string{
		`INSERT INTO library_sources(id,library_id,kind,name,configured_root,root,classification,generation,incarnation,root_identity,health,enabled,follow_symlinks,interval_seconds,identity_confirmed) VALUES('source-1','movies','local','Movies','/media','/media','local',1,'inc-source-1','/media','ready',1,0,0,1)`,
		`INSERT INTO library_sources(id,library_id,kind,name,configured_root,root,classification,generation,incarnation,root_identity,health,enabled,follow_symlinks,interval_seconds,identity_confirmed) VALUES('source-2','movies','local','Movies two','/media/two','/media/two','local',1,'inc-source-2','/media-two','ready',1,0,0,1)`,
		`INSERT INTO library_sources(id,library_id,kind,name,configured_root,root,classification,generation,incarnation,root_identity,health,enabled,follow_symlinks,interval_seconds,identity_confirmed) VALUES('foreign-1','other','local','Other','/media','/media','local',1,'inc-foreign-1','/media','ready',1,0,0,1)`,
		`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(20,'reconcile-a1','/media/Reconcile/Movie.mkv',42,7,'','','',0,0,0,1),(21,'reconcile-a2','/media/Reconcile/Link.mkv',42,7,'','','',0,0,0,1),(22,'reconcile-a3','/media/Reconcile/Other.mkv',42,7,'','','',0,0,0,1),(23,'reconcile-a4','/media/Reconcile/Retired.mkv',42,7,'','','',0,0,0,1),(24,'reconcile-a5','/media/Reconcile/Changed.mkv',43,8,'','','',0,0,0,1),(25,'reconcile-a6','/media/Reconcile/Absent.mkv',42,7,'','','',0,0,0,1)`,
		`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state,retired) VALUES('object-1','source-1','reconcile-a1','inc-source-1','Reconcile/Movie.mkv','ino:41','object-revision:1','{}',42,7,'run','Reconcile','available',0)`,
		`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state,retired) VALUES('object-2','source-1','reconcile-a2','inc-source-1','Reconcile/Link.mkv','ino:41','object-revision:1','{}',42,7,'run','Reconcile','available',0)`,
		`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state,retired) VALUES('object-3','source-2','reconcile-a3','inc-source-2','Reconcile/Other.mkv','ino:41','object-revision:1','{}',42,7,'run','Reconcile','available',0)`,
		`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state,retired) VALUES('object-4','source-1','reconcile-a4','inc-source-1','Reconcile/Retired.mkv','ino:41','object-revision:1','{}',42,7,'run','Reconcile','available',1)`,
		`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state,retired) VALUES('object-5','source-1','reconcile-a5','inc-source-1','Reconcile/Changed.mkv','ino:99','object-revision:2','{}',43,8,'run','Reconcile','available',0)`,
		`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state,retired) VALUES('object-6','source-1','reconcile-a6','inc-source-1','Reconcile/Absent.mkv','','object-revision:1','{}',42,7,'run','Reconcile','available',0)`,
	} {
		f.exec(t, q)
	}
	return f
}

func newIAReconcileSourceFixture(t *testing.T) (*browseFixture, *ForegroundReader) {
	t.Helper()
	f := newIAReconcileSourceSource(t)
	f = composeReadFixture(t, f, append(browseDefinitions(), IAReconcileSourceHarnessDefinitions()...), readfixture.LiveConfig(time.Now), false, iaReconcileSourceHash(t))
	r, e := NewForegroundReader(f.snapshots)
	if e != nil {
		t.Fatal(e)
	}
	return f, r
}

func iaReconcileSourceRead(t *testing.T, f *browseFixture, r *ForegroundReader, in IAReconcileSourceInput) (IAReconcileSourcePage, BrowseReadResult, error) {
	t.Helper()
	q, request, output := liveRequests("ia-reconcile")
	var page IAReconcileSourcePage
	var result BrowseReadResult
	_, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		var e error
		page, result, e = r.IAReconcileSourceObject(ctx, request, time.Second, out, in)
		return e
	})
	return page, result, e
}

func iaReconcileSourceInput(f *browseFixture) IAReconcileSourceInput {
	return IAReconcileSourceInput{View: f.input, Source: "source-1", Object: "object-1"}
}

// The point returns the exact retained vector and the bounded same-source
// identity window. A retired placement and another source's placement never
// become candidates.
func TestIAReconcileSourceObjectVector(t *testing.T) {
	f, r := newIAReconcileSourceFixture(t)
	page, result, e := iaReconcileSourceRead(t, f, r, iaReconcileSourceInput(f))
	if e != nil {
		t.Fatal(e)
	}
	want := IAReconcileSource{ObjectID: "object-1", SourceID: "source-1", RelativePath: "Reconcile/Movie.mkv",
		ObjectIdentity: "ino:41", Revision: "object-revision:1", DirectoryPath: "Reconcile", State: "available", Size: 42, ModifiedNS: 7}
	if !reflect.DeepEqual(page.Accepted, want) || page.Library != 1 || page.LibraryKey != "movies" || page.Source != "source-1" {
		t.Fatalf("accepted vector=%+v", page)
	}
	if len(page.Candidates) != 2 || page.Candidates[0].ObjectID != "object-1" || page.Candidates[1].ObjectID != "object-2" || !page.WindowComplete || page.RawRows != 2 {
		t.Fatalf("candidate window=%+v", page)
	}
	if result.LookupStatements == 0 || result.PublicationStatements == 0 || result.Attempts != 1 {
		t.Fatalf("statement accounting=%+v", result)
	}
}

// A zero-identity adapter observation has no move window at all; an empty window
// is complete, never an implicit absence.
func TestIAReconcileSourceObjectWithoutNativeIdentity(t *testing.T) {
	f, r := newIAReconcileSourceFixture(t)
	in := iaReconcileSourceInput(f)
	in.Object = "object-6"
	page, _, e := iaReconcileSourceRead(t, f, r, in)
	if e != nil || page.Accepted.ObjectIdentity != "" || len(page.Candidates) != 0 || !page.WindowComplete || page.RawRows != 0 {
		t.Fatalf("identityless object=%+v err=%v", page, e)
	}
}

// A wider raw window is reported incomplete rather than truncated into a
// false unique placement.
func TestIAReconcileSourceObjectBoundedWindow(t *testing.T) {
	f, r := newIAReconcileSourceFixture(t)
	f.exec(t, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(30,'reconcile-b1','/media/Reconcile/B1.mkv',42,7,'','','',0,0,0,1),(31,'reconcile-b2','/media/Reconcile/B2.mkv',42,7,'','','',0,0,0,1)`)
	f.exec(t, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path,state) VALUES('object-7','source-1','reconcile-b1','inc-source-1','Reconcile/B1.mkv','ino:41','object-revision:1','{}',42,7,'run','Reconcile','available'),('object-8','source-1','reconcile-b2','inc-source-1','Reconcile/B2.mkv','ino:41','object-revision:1','{}',42,7,'run','Reconcile','available')`)
	page, _, e := iaReconcileSourceRead(t, f, r, iaReconcileSourceInput(f))
	if e != nil || page.WindowComplete || page.RawRows != maxIAReconcileCandidates || len(page.Candidates) != maxIAReconcileCandidates {
		t.Fatalf("bounded window=%+v err=%v", page, e)
	}
}

// A foreign library, a retired object and a missing object are denied before any
// candidate window is published.
func TestIAReconcileSourceObjectDenials(t *testing.T) {
	for name, mutate := range map[string]func(*browseFixture, *IAReconcileSourceInput){
		"foreign_library": func(f *browseFixture, in *IAReconcileSourceInput) { in.View.Library = 2 },
		"foreign_source":  func(_ *browseFixture, in *IAReconcileSourceInput) { in.Source = "foreign-1" },
		"retired_object":  func(_ *browseFixture, in *IAReconcileSourceInput) { in.Object = "object-4" },
		"missing_object":  func(_ *browseFixture, in *IAReconcileSourceInput) { in.Object = "missing" },
	} {
		t.Run(name, func(t *testing.T) {
			f, r := newIAReconcileSourceFixture(t)
			in := iaReconcileSourceInput(f)
			mutate(f, &in)
			page, _, e := iaReconcileSourceRead(t, f, r, in)
			if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(page, IAReconcileSourcePage{}) {
				t.Fatalf("denial=%+v err=%v", page, e)
			}
		})
	}
}

// The read is harness-only and requires ConsumeForeground custody.
func TestIAReconcileSourceObjectCustodyAndInput(t *testing.T) {
	f, r := newIAReconcileSourceFixture(t)
	for _, in := range []IAReconcileSourceInput{
		{View: f.input},
		{View: f.input, Source: "", Object: "object-1"},
		{View: f.input, Source: "source-1", Object: ""},
	} {
		page, _, e := iaReconcileSourceRead(t, f, r, in)
		if !errors.Is(e, ErrInput) || !reflect.DeepEqual(page, IAReconcileSourcePage{}) {
			t.Fatalf("input %+v=%+v err=%v", in, page, e)
		}
	}
	page, _, e := r.IAReconcileSourceObject(t.Context(), foregroundRequest(), time.Second, BrowseOutput{}, iaReconcileSourceInput(f))
	if !errors.Is(e, ErrInput) || !reflect.DeepEqual(page, IAReconcileSourcePage{}) {
		t.Fatal("missing foreground custody", e)
	}
}

// Every preparation statement is a closed read on the embedded inventory
// relations with an indexed SEARCH. The group C receipt table is not touched, so
// a Capture golden can be recorded after this commit against the embedded
// reader; none is invented here.
func TestIAReconcileSourcePlans(t *testing.T) {
	f, _ := newIAReconcileSourceFixture(t)
	defs := IAReconcileSourceHarnessDefinitions()
	if len(defs) != 7 {
		t.Fatal("reconcile source family inventory changed", len(defs))
	}
	for _, d := range defs {
		if d.Kind != stmt.Read || !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(plan.LintSQL(d.SQL)) != 0 {
			t.Fatal("reconcile source statements must retain closed hot read grammar", d.ID)
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
	t.Logf("%d reconcile source statements EXPLAINed on the embedded schema; Capture golden deferred", len(defs))
}
