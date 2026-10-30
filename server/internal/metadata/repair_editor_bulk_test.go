package metadata

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

func editorBulkPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, w, h))
	im.Set(0, 0, color.RGBA{R: 200, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func bulkFixture(t *testing.T) (*Service, []BulkTarget, catalogtest.Names) {
	t.Helper()
	s, db := editorFixture(t)
	names := editorNames(t, db)
	c := catalogtest.New(t, db)
	movies := c.Handle("movies")
	names["movie-b"] = c.Movie(movies, "/movies/movie-b.mkv", "Beta", 2021)
	names["movie-c"] = c.Movie(movies, "/movies/movie-c.mkv", "Gamma", 2022)
	keep := `["Keep"]`
	editField(t, s, editorTarget(names, "item", "movie-c"), "tags", RepairFieldEdit{Value: &keep})
	targets := []BulkTarget{}
	for _, id := range []string{"movie", "movie-b", "movie-c"} {
		state, err := s.RepairState(context.Background(), editorTarget(names, "item", id), allowAll)
		if err != nil {
			t.Fatal(err)
		}
		targets = append(targets, BulkTarget{Kind: "item", ID: names[id].Public, ExpectedRevision: state.Revision})
	}
	return s, targets, names
}

func TestEditorBulkPartialFailureAndReplay(t *testing.T) {
	s, targets, names := bulkFixture(t)
	ctx := context.Background()
	// One stale fence among three: the other two still apply, and the caller is
	// told which one did not.
	targets[1].ExpectedRevision = strings.Repeat("0", 64)
	rating := "PG-13"
	edit := BulkEdit{
		OperationID: "bulk-one",
		Targets:     targets,
		Fields:      map[string]RepairFieldEdit{"contentRating": {Value: &rating}},
		Lists:       map[string]BulkListEdit{"tags": {Add: []string{"Noir"}}},
		Genres:      &BulkListEdit{Add: []string{"Drama"}},
	}
	out, err := s.BulkEdit(ctx, edit, editorActor(), allowAll)
	if err != nil {
		t.Fatal(err)
	}
	if out.Updated != 2 || out.Failed != 1 || len(out.Results) != 3 {
		t.Fatalf("bulk outcome %+v", out)
	}
	if out.Results[1].OK || out.Results[1].Code != "metadata_conflict" {
		t.Fatalf("stale target not reported: %+v", out.Results[1])
	}
	for _, n := range []int{0, 2} {
		if !out.Results[n].OK || len(out.Results[n].Revision) != 64 {
			t.Fatalf("applied target %d: %+v", n, out.Results[n])
		}
	}
	// A replay returns the stored receipt and edits nothing a second time.
	replay, err := s.BulkEdit(ctx, edit, editorActor(), allowAll)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replay, out) {
		t.Fatalf("replay differs: %+v vs %+v", replay, out)
	}
	state, err := s.RepairState(ctx, editorTarget(names, "item", "movie-c"), allowAll)
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Snapshot.Fields["tags"].Values; !reflect.DeepEqual(got, []string{"Keep", "Noir"}) {
		t.Fatalf("bulk replaced a list instead of adding to it: %v", got)
	}
	if !state.Snapshot.Fields["contentRating"].Locked {
		t.Fatal("bulk edit did not lock the edited field")
	}
	genres := []string{}
	for _, r := range state.Snapshot.Relationships {
		if r.Kind == "genre" {
			genres = append(genres, r.Label)
		}
	}
	if !reflect.DeepEqual(genres, []string{"Drama"}) {
		t.Fatalf("bulk genres: %v", genres)
	}
	// Removing is the mirror of adding, and never touches the tags not named.
	for n := range targets {
		current, e := s.RepairState(ctx, RepairTarget{Kind: "item", ID: targets[n].ID}, allowAll)
		if e != nil {
			t.Fatal(e)
		}
		targets[n].ExpectedRevision = current.Revision
	}
	out, err = s.BulkEdit(ctx, BulkEdit{OperationID: "bulk-two", Targets: targets, Lists: map[string]BulkListEdit{"tags": {Remove: []string{"noir"}}}}, editorActor(), allowAll)
	if err != nil || out.Failed != 0 {
		t.Fatalf("remove pass %+v %v", out, err)
	}
	state, err = s.RepairState(ctx, editorTarget(names, "item", "movie-c"), allowAll)
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Snapshot.Fields["tags"].Values; !reflect.DeepEqual(got, []string{"Keep"}) {
		t.Fatalf("remove took the wrong tags: %v", got)
	}
}

func TestEditorBulkRefusesNonBulkFieldsAndOversizedBatches(t *testing.T) {
	s, targets, names := bulkFixture(t)
	ctx := context.Background()
	title := "One title for everything"
	out, err := s.BulkEdit(ctx, BulkEdit{OperationID: "bulk-title", Targets: targets, Fields: map[string]RepairFieldEdit{"title": {Value: &title}}}, editorActor(), allowAll)
	if err != nil {
		t.Fatal(err)
	}
	if out.Updated != 0 || out.Failed != len(targets) || out.Results[0].Code != "invalid_metadata_repair" {
		t.Fatalf("title accepted as a bulk field: %+v", out)
	}
	wide := []BulkTarget{}
	for n := 0; n < bulkTargetLimit+1; n++ {
		wide = append(wide, BulkTarget{Kind: "item", ID: string(rune('a'+n%26)) + strings.Repeat("x", n), ExpectedRevision: strings.Repeat("0", 64)})
	}
	if _, err = s.BulkEdit(ctx, BulkEdit{OperationID: "bulk-wide", Targets: wide, Fields: map[string]RepairFieldEdit{"contentRating": {Value: &title}}}, editorActor(), allowAll); !errors.Is(err, ErrRepairInput) {
		t.Fatal("oversized batch accepted", err)
	}
	mixed := []BulkTarget{targets[0], {Kind: "show", ID: names["show"].Public, ExpectedRevision: strings.Repeat("0", 64)}}
	if _, err = s.BulkEdit(ctx, BulkEdit{OperationID: "bulk-mixed", Targets: mixed, Fields: map[string]RepairFieldEdit{"contentRating": {Value: &title}}}, editorActor(), allowAll); !errors.Is(err, ErrRepairInput) {
		t.Fatal("mixed kinds accepted", err)
	}
}

func editorJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, w, h))
	im.Set(0, 0, color.RGBA{B: 180, A: 255})
	var b bytes.Buffer
	if err := jpeg.Encode(&b, im, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestEditorArtworkUploadAcceptRejectAndWithdraw(t *testing.T) {
	s, db := editorFixture(t)
	names := editorNames(t, db)
	ctx := context.Background()
	target := editorTarget(names, "item", "movie")
	state, err := s.RepairState(ctx, target, allowAll)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct {
		name string
		raw  []byte
	}{
		{"empty", nil},
		{"not an image", []byte("this is not an image at all")},
		{"truncated webp container", append([]byte("RIFF\x00\x00\x00\x00WEBP"), 0, 1, 2, 3)},
		{"oversized", make([]byte, UploadBytes+1)},
	} {
		if _, err = s.UploadArtwork(ctx, target, "poster", "", state.Revision, v.raw, editorActor(), allowAll); !errors.Is(err, ErrArtworkUpload) {
			t.Fatalf("%s accepted: %v", v.name, err)
		}
	}
	if _, err = s.UploadArtwork(ctx, target, "banner", "", state.Revision, editorJPEG(t, 40, 60), editorActor(), allowAll); !errors.Is(err, ErrRepairInput) {
		t.Fatal("a role this kind does not publish was accepted", err)
	}
	if _, err = s.UploadArtwork(ctx, target, "poster", "", strings.Repeat("0", 64), editorJPEG(t, 40, 60), editorActor(), allowAll); !errors.Is(err, ErrRepairConflict) {
		t.Fatal("stale upload accepted", err)
	}
	for _, raw := range [][]byte{editorJPEG(t, 40, 60), editorBulkPNG(t, 30, 30)} {
		state, err = s.RepairState(ctx, target, allowAll)
		if err != nil {
			t.Fatal(err)
		}
		state, err = s.UploadArtwork(ctx, target, "poster", "", state.Revision, raw, editorActor(), allowAll)
		if err != nil {
			t.Fatal("upload rejected", err)
		}
	}
	if len(state.Snapshot.Artwork) != 1 || !state.Snapshot.Artwork[0].Locked || state.Snapshot.Artwork[0].Role != "poster" {
		t.Fatalf("upload was not selected and locked: %+v", state.Snapshot.Artwork)
	}
	uploads := []ArtworkCandidate{}
	for _, c := range state.Artwork.Candidates {
		if c.Provider == "upload" {
			uploads = append(uploads, c)
		}
	}
	if len(uploads) != 2 {
		t.Fatalf("uploaded candidates: %+v", state.Artwork.Candidates)
	}
	for _, c := range uploads {
		if c.PreviewURL == "" || strings.Contains(c.PreviewURL, "http") {
			t.Fatalf("upload preview is not a proxied path: %q", c.PreviewURL)
		}
	}
	selected := state.Snapshot.Artwork[0].CandidateID
	other := uploads[0].ID
	if other == selected {
		other = uploads[1].ID
	}
	// Withdrawing an unselected upload leaves the selection alone.
	state, err = s.DeleteUploadedArtwork(ctx, target, "poster", other, state.Revision, editorActor(), allowAll)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Snapshot.Artwork) != 1 || state.Snapshot.Artwork[0].CandidateID != selected {
		t.Fatalf("withdrawing an unselected upload changed the selection: %+v", state.Snapshot.Artwork)
	}
	// Withdrawing the selected upload with nothing to fall back on clears the role.
	state, err = s.DeleteUploadedArtwork(ctx, target, "poster", selected, state.Revision, editorActor(), allowAll)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Snapshot.Artwork) != 0 {
		t.Fatalf("withdrawn selection retained: %+v", state.Snapshot.Artwork)
	}
	if _, err = s.DeleteUploadedArtwork(ctx, target, "poster", selected, state.Revision, editorActor(), allowAll); !errors.Is(err, ErrRepairInput) {
		t.Fatal("withdrawing an unknown candidate accepted", err)
	}
}

func TestEditorUploadSniffsContainerNotDeclaration(t *testing.T) {
	for _, v := range []struct {
		raw  []byte
		mime string
	}{
		{[]byte("\x89PNG\r\n\x1a\n rest"), "image/png"},
		{[]byte{0xff, 0xd8, 0xff, 0xe0, 0x00}, "image/jpeg"},
		{append([]byte("RIFF\x10\x00\x00\x00WEBP"), 'V', 'P', '8', ' '), "image/webp"},
		{[]byte("GIF89a"), ""},
		{[]byte("<svg xmlns=\"http://www.w3.org/2000/svg\"/>"), ""},
		{[]byte("RIFF\x10\x00\x00\x00WAVE"), ""},
	} {
		if got := sniffArtworkUpload(v.raw); got != v.mime {
			t.Fatalf("sniffed %q, want %q", got, v.mime)
		}
	}
}
