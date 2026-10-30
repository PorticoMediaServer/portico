package catalog

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// Source semantics are now read through the bounded public runtime, while Get
// keeps its constant-size summary. These fixtures contain at most two sources.
func fileInfoSourcesForTest(t *testing.T, s *Service, profile, public string) []FileInfoSource {
	t.Helper()
	library, err := s.LibraryForItem(public)
	if err != nil {
		t.Fatal(err)
	}
	viewer := Viewer{Profile: profile, Fence: "source-fixture", Libraries: []string{library}}
	guard := func(ctx context.Context, caller readprep.Caller, id FileInfoIdentity) (FileInfoAdmission, error) {
		ad, err := s.CaptureFileInfoAdmission(ctx, viewer, "server", public, true, "", false)
		if err == nil && (caller != "family" || ad.Identity != id) {
			return ad, readprep.ErrStale
		}
		return ad, err
	}
	store, err := readprep.New(readprep.Config{Workers: 1, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	r, err := NewFileInfoRuntime(s.db, store, guard)
	if err != nil {
		t.Fatal(err)
	}
	var opening string
	err = dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		ad, e := s.CaptureFileInfoAdmission(ctx, viewer, "server", public, true, "", false)
		if e != nil {
			return e
		}
		opening, e = r.AcceptDetailOpening(ctx, "family", ad.Identity, ContentRevision{Catalog: 1, Viewer: 1})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Read(context.Background(), "family", FileInfoReadRequest{Kind: "sources", Opening: opening})
	out = fileInfoAwait(t, r, out, err)
	defer out.Release()
	if out.Sources == nil || out.Sources.Sources == nil || out.Sources.Total != int64(len(out.Sources.Sources)) || out.Sources.Window.NextCursor != "" {
		t.Fatalf("small source fixture was not complete: %+v", out.Sources)
	}
	return append([]FileInfoSource(nil), out.Sources.Sources...)
}
