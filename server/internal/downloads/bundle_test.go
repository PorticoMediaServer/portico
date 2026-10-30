package downloads

import (
	"context"
	"errors"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"strings"
	"testing"
)

// The bundle is everything a device needs to list and play a downloaded film
// with no server in reach. Its title facts, media facts, artwork names and the
// library file its extras belong to all come from the ready claim's item.
func TestBundleCarriesTheFilmsFacts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	h := newHarness(t)
	ready := h.ready(t, "bundle")
	bundle, e := h.service.Bundle(context.Background(), h.viewer, ready.ID)
	if e != nil {
		t.Fatal(e)
	}
	if bundle.Title.Name != "Film" || bundle.Title.Kind != "movie" || !bundle.Title.OnServer {
		t.Fatalf("title %+v", bundle.Title)
	}
	if bundle.Media.Container != "mp4" || bundle.Media.VideoCodec != "h264" {
		t.Fatalf("media %+v", bundle.Media)
	}
	paths := map[string]string{}
	for _, art := range bundle.Artwork {
		paths[art.Role] = art.Path
	}
	if paths["poster"] != "artwork/poster" || paths["backdrop"] != "artwork/backdrop" {
		t.Fatalf("artwork %+v", bundle.Artwork)
	}
	if bundle.SourceID != h.asset {
		t.Fatalf("source id %q, want the film's asset %q", bundle.SourceID, h.asset)
	}
}

// Chapters belong to the library file behind the download, so rows filed
// against the film's asset are listed in order with their titles and times.
func TestBundleListsTheFilesChapters(t *testing.T) {
	h := newHarness(t)
	ready := h.ready(t, "chapters")
	if _, e := h.db.Exec(`INSERT INTO asset_chapter_facts(asset_id,revision,size,modified_ns,status,fingerprint) VALUES(?,1,?,?,'ready','x')`, h.asset, 1000, 1); e != nil {
		t.Fatal(e)
	}
	if _, e := h.db.Exec(`INSERT INTO asset_chapters(asset_id,chapter_index,title,start_seconds,end_seconds) VALUES(?,1,'One',0,10),(?,2,'Two',10,20)`, h.asset, h.asset); e != nil {
		t.Fatal(e)
	}
	bundle, e := h.service.Bundle(context.Background(), h.viewer, ready.ID)
	if e != nil {
		t.Fatal(e)
	}
	if len(bundle.Chapters) != 2 {
		t.Fatalf("chapters %+v", bundle.Chapters)
	}
	first, second := bundle.Chapters[0], bundle.Chapters[1]
	if first.Index != 1 || first.Title != "One" || first.StartSeconds != 0 || first.EndSeconds != 10 {
		t.Fatalf("first chapter %+v", first)
	}
	if second.Index != 2 || second.Title != "Two" || second.StartSeconds != 10 || second.EndSeconds != 20 {
		t.Fatalf("second chapter %+v", second)
	}
}

// A bundle answers only for a ready claim of the viewer who holds it: a claim
// still being prepared is not ready, and another viewer's claim is not found.
func TestBundleRefusesOtherViewersAndClaimsNotReady(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	submitted, e := h.service.Submit(ctx, h.viewer, Request{OperationID: "not-ready", MediaID: h.item, Quality: QualityOriginal}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(submitted.Items) != 1 || submitted.Items[0].State == StateReady {
		t.Fatalf("fixture claim %+v", submitted)
	}
	if _, e = h.service.Bundle(ctx, h.viewer, submitted.Items[0].ID); !errors.Is(e, ErrNotReady) {
		t.Fatalf("bundle of an unfinished claim: %v", e)
	}
	ready := h.ready(t, "ready")
	other := h.viewer
	other.ProfileID = "other"
	if _, e = h.service.Bundle(ctx, other, ready.ID); !errors.Is(e, ErrNotFound) {
		t.Fatalf("bundle of another viewer's claim: %v", e)
	}
}

// The name a browser saves is built from the title, but a title is not a path:
// separators become spaces and an unusable name keeps the claim's own.
func TestReadableFileName(t *testing.T) {
	two, three, group := 2, 3, 1
	for name, tc := range map[string]struct {
		title    Title
		ext      string
		fallback string
		want     string
	}{
		"movie with year": {
			title: Title{Kind: "movie", Name: "Film", Year: 2019},
			ext:   ".mkv", fallback: "fallback.mkv", want: "Film (2019).mkv",
		},
		"episode with show and season": {
			title: Title{Kind: "episode", Name: "Pilot", ParentTitle: "Show", GroupNumber: &group, Number: &two},
			ext:   ".mkv", fallback: "fallback.mkv", want: "Show - S01E02 - Pilot.mkv",
		},
		"song with number": {
			title: Title{Kind: "song", Name: "Song", Number: &three},
			ext:   ".flac", fallback: "fallback.flac", want: "03 Song.flac",
		},
		"title with path characters": {
			title: Title{Kind: "movie", Name: `A/B:C*D?E`},
			ext:   ".mkv", fallback: "fallback.mkv", want: "A B C D E.mkv",
		},
		"empty title keeps the fallback": {
			title: Title{Kind: "movie", Name: ""},
			ext:   ".mkv", fallback: "fallback.mkv", want: "fallback.mkv",
		},
	} {
		got := readableFileName(tc.title, tc.ext, tc.fallback)
		if got != tc.want {
			t.Fatalf("%s: name %q, want %q", name, got, tc.want)
		}
		if strings.ContainsRune(got, os.PathSeparator) {
			t.Fatalf("%s: name %q contains a path separator", name, got)
		}
	}
}

// Downloads carry a bounded width for each kind of artwork, and nothing for a
// role they do not carry.
func TestArtworkWidth(t *testing.T) {
	for role, want := range map[string]int{"poster": 800, "still": 800, "cover": 800, "backdrop": 1920, "thumbnail": 0, "": 0} {
		if got := ArtworkWidth(role); got != want {
			t.Fatalf("ArtworkWidth(%q) = %d, want %d", role, got, want)
		}
	}
}

func TestChapterPolicyFiltersOfflineSuggestionsButRetainsOwnerChoice(t *testing.T) {
	h := newHarness(t)
	for _, statement := range []string{
		`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) SELECT 'chapter-object',src.id,?,src.incarnation,'Film.mp4','revision','{}',1,1 FROM library_sources src WHERE src.library_id='lib' LIMIT 1`,
	} {
		if _, err := h.db.Exec(statement, h.asset); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.db.Exec(`INSERT INTO analysis_markers(id,object_id,source_revision,source_binding,kind,start_us,end_us,confidence,provenance,result_id,approved,edited) VALUES('suggestion','chapter-object','revision','binding','intro',0,1000000,.85,'embedded_chapter_label:v1','',0,0),('owner-edit','chapter-object','revision','binding','credits',50000000,55000000,.85,'embedded_chapter_label:v1','',1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Exec(`INSERT INTO server_settings(singleton,"segments.useChapterNames") VALUES(1,0) ON CONFLICT(singleton) DO UPDATE SET "segments.useChapterNames"=0`); err != nil {
		t.Fatal(err)
	}
	snapshot, err := dbwork.BeginSnapshot(context.Background(), h.db)
	if err != nil {
		t.Fatal(err)
	}
	markers, err := readMarkers(context.Background(), snapshot.Tx(), h.asset)
	snapshot.Rollback()
	if err != nil || len(markers) != 1 || markers[0].Kind != "credits" || !markers[0].Automatic {
		t.Fatal(markers, err)
	}
}
