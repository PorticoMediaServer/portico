package read

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

//go:embed ia_source_test.go
var iaSourceJournal string

func iaSourceHash(t *testing.T) string {
	h := sha256.Sum256([]byte(readfixture.ForegroundHash(t) + iaSourceJournal))
	return hex.EncodeToString(h[:])
}

// iaNativeRoot keeps the current native volume so the fixture is absolute on
// every host. Concatenation preserves deliberate dot segments verbatim.
func iaNativeRoot(t *testing.T, tail string) string {
	t.Helper()
	root, err := filepath.Abs(string(filepath.Separator))
	if err != nil {
		t.Fatal(err)
	}
	return root + filepath.FromSlash(tail)
}

func newIASourceSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newForegroundSource(t)
	for _, q := range []string{
		`INSERT INTO library_sources(id,library_id,kind,name,configured_root,root,classification,generation,incarnation,root_identity,health,enabled,follow_symlinks,interval_seconds,identity_confirmed) VALUES('source-1','movies','local','Movies','/media','/media','local',1,'inc-source-1','/media','ready',1,0,0,1)`,
		`INSERT INTO library_sources(id,library_id,kind,name,configured_root,root,classification,generation,incarnation,root_identity,health,enabled,follow_symlinks,interval_seconds,identity_confirmed) VALUES('foreign-1','other','local','Other','/media','/media','local',1,'inc-foreign-1','/media','ready',1,0,0,1)`,
	} {
		f.exec(t, q)
	}
	return f
}

func newIASourceFixture(t *testing.T) (*browseFixture, *ForegroundReader) {
	t.Helper()
	f := newIASourceSource(t)
	f = composeReadFixture(t, f, append(browseDefinitions(), IASourceHarnessDefinitions()...), readfixture.LiveConfig(time.Now), false, iaSourceHash(t))
	r, e := NewForegroundReader(f.snapshots)
	if e != nil {
		t.Fatal(e)
	}
	return f, r
}

func iaSourceRead(t *testing.T, f *browseFixture, r *ForegroundReader, in IASourceInput) (IASourcePage, BrowseReadResult, error) {
	t.Helper()
	q, request, output := liveRequests("ia-source")
	var page IASourcePage
	var result BrowseReadResult
	_, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		var e error
		page, result, e = r.IASourcePoint(ctx, request, time.Second, out, in)
		return e
	})
	return page, result, e
}

func TestIASourcePointVector(t *testing.T) {
	f, r := newIASourceFixture(t)
	page, result, e := iaSourceRead(t, f, r, IASourceInput{View: f.input, Source: "source-1"})
	if e != nil {
		t.Fatal(e)
	}
	want := IASourcePage{Library: 1, LibraryKey: "movies", LibraryRoot: "/media", Source: IASource{
		ID: "source-1", LibraryKey: "movies", Kind: "local", Name: "Movies",
		ConfiguredRoot: "/media", Root: "/media", Classification: "local",
		Incarnation: "inc-source-1", RootIdentity: "/media", Health: "ready",
		Generation: 1, Enabled: 1, FollowSymlinks: 0, IntervalSeconds: 0, IdentityConfirmed: 1,
	}}
	if !reflect.DeepEqual(page, want) {
		t.Fatalf("source vector=%+v", page)
	}
	// Five common fence statements plus the two exact point pairs.
	if result.LookupStatements != 9 || result.PublicationStatements != 9 || result.Attempts != 1 {
		t.Fatalf("statement accounting=%+v", result)
	}
}

func TestIASourcePointSameSnapshotOctets(t *testing.T) {
	f, r := newIASourceFixture(t)
	root := iaNativeRoot(t, "media/Média 資料")
	f.exec(t, `UPDATE library_sources SET name=?,configured_root=?,root=? WHERE id='source-1'`, "é\x00x", root, root)
	page, result, e := iaSourceRead(t, f, r, IASourceInput{View: f.input, Source: "source-1"})
	if e != nil {
		t.Fatal(e)
	}
	// Byte lengths, not character counts, must survive the preflight.
	if page.Source.Name != "é\x00x" || page.Source.ConfiguredRoot != root || page.Source.Root != root {
		t.Fatalf("multibyte/NUL source vector=%+v", page.Source)
	}
	if result.LookupStatements != 9 || result.PublicationStatements != 9 {
		t.Fatalf("same-snapshot point pairs=%+v", result)
	}
}

func TestIASourcePointRefusesUnrepresentableRoots(t *testing.T) {
	for _, tc := range []struct {
		name             string
		configured, root string
		want             error
	}{
		{"invalid_utf8", "/media\xff", "/media\xff", ErrSourceRootUnrepresentable},
		{"nul", "/media\x00child", "/media\x00child", ErrUnqualified},
		{"control", "/media\nchild", "/media\nchild", ErrUnqualified},
		{"relative", "media", "media", ErrUnqualified},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, r := newIASourceFixture(t)
			f.exec(t, `UPDATE library_sources SET configured_root=?,root=? WHERE id='source-1'`, tc.configured, tc.root)
			page, _, e := iaSourceRead(t, f, r, IASourceInput{View: f.input, Source: "source-1"})
			if !errors.Is(e, tc.want) || !reflect.DeepEqual(page, IASourcePage{}) {
				t.Fatalf("root refusal=%+v err=%v; want %v", page, e, tc.want)
			}
		})
	}
}

func TestIASourcePointNativeRootsPreserved(t *testing.T) {
	f, r := newIASourceFixture(t)
	// The deliberate dot segment and decomposed accent must not be cleaned or
	// normalized into a different persisted identity namespace.
	root := iaNativeRoot(t, "media/Cafe\u0301/../Cafe\u0301")
	f.exec(t, `UPDATE library_sources SET configured_root=?,root=? WHERE id='source-1'`, root, root)
	page, _, e := iaSourceRead(t, f, r, IASourceInput{View: f.input, Source: "source-1"})
	if e != nil || page.Source.ConfiguredRoot != root || page.Source.Root != root {
		t.Fatalf("native roots=%q/%q err=%v", page.Source.ConfiguredRoot, page.Source.Root, e)
	}
}

func TestIASourcePointForeignLibrary(t *testing.T) {
	f, r := newIASourceFixture(t)
	for _, tc := range []struct {
		library int64
		source  string
	}{{2, "source-1"}, {1, "foreign-1"}} {
		in := IASourceInput{View: f.input, Source: tc.source}
		in.View.Library = tc.library
		page, _, e := iaSourceRead(t, f, r, in)
		if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(page, IASourcePage{}) {
			t.Fatalf("foreign source %q in library %d=%+v err=%v", tc.source, tc.library, page, e)
		}
	}
}

func TestIASourcePointStaleGeneration(t *testing.T) {
	t.Run("retry", func(t *testing.T) {
		f, r := newIASourceFixture(t)
		r.snapshots = &iaSourceBoundary{Snapshots: f.snapshots, mutate: func(phase int) {
			if phase == 1 {
				f.exec(t, `UPDATE library_sources SET generation=generation+1,incarnation='inc-source-2',root_identity='/media-2' WHERE id='source-1'`)
			}
		}}
		page, result, e := iaSourceRead(t, f, r, IASourceInput{View: f.input, Source: "source-1"})
		if e != nil || result.Attempts != 2 || page.Source.Generation != 2 || page.Source.Incarnation != "inc-source-2" {
			t.Fatalf("generation retry=%+v attempts=%d err=%v", page.Source, result.Attempts, e)
		}
	})
	t.Run("drift", func(t *testing.T) {
		f, r := newIASourceFixture(t)
		r.snapshots = &iaSourceBoundary{Snapshots: f.snapshots, mutate: func(phase int) {
			if phase%2 == 1 {
				f.exec(t, `UPDATE library_sources SET generation=generation+1 WHERE id='source-1'`)
			}
		}}
		page, _, e := iaSourceRead(t, f, r, IASourceInput{View: f.input, Source: "source-1"})
		if !errors.Is(e, ErrStale) || !errors.Is(e, governor.ErrBusy) || !reflect.DeepEqual(page, IASourcePage{}) {
			t.Fatalf("persistent generation drift=%+v err=%v", page, e)
		}
	})
}

func TestIASourcePointInputAndCustody(t *testing.T) {
	f, r := newIASourceFixture(t)
	for _, source := range []string{"", string([]byte{0xff})} {
		page, _, e := iaSourceRead(t, f, r, IASourceInput{View: f.input, Source: source})
		if !errors.Is(e, ErrInput) || !reflect.DeepEqual(page, IASourcePage{}) {
			t.Fatalf("source input %q=%+v err=%v", source, page, e)
		}
	}
	in := IASourceInput{View: f.input, Source: "source-1"}
	page, _, e := r.IASourcePoint(t.Context(), foregroundRequest(), time.Second, BrowseOutput{}, in)
	if !errors.Is(e, ErrInput) || !reflect.DeepEqual(page, IASourcePage{}) {
		t.Fatal("missing foreground custody", e)
	}
}

// This wrapper keeps the native forecast path while injecting a change only
// after SQLite has joined and retired the previous physical snapshot.
type iaSourceBoundary struct {
	*governor.Snapshots
	phase  int
	mutate func(int)
}

func (b *iaSourceBoundary) ReadForecasted(ctx context.Context, r governor.Request, budget time.Duration, f governor.SnapshotForecast, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	result, e := b.Snapshots.ReadForecasted(ctx, r, budget, f, fn)
	b.phase++
	b.mutate(b.phase)
	return result, e
}
