package catalog

import (
	"context"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
	"testing"
	"time"
)

func TestFileInfoRuntimeOpeningRetainsOriginalDetailAndLiveWindowAudience(t *testing.T) {
	c := catalogtest.Open(t)
	store, err := readprep.New(readprep.Config{Workers: 1, MaxBytes: 128 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	id := FileInfoIdentity{Item: 1, Library: 1, PublicItem: [16]byte{1, 2, 3}}
	ad := FileInfoAdmission{Identity: id, ItemIncarnation: [16]byte{9, 8, 7}, Head: compactcatalog.InfoHead{Desired: 17, Applied: 17}, ConfigurationRevision: 4, Kind: "movie", Playable: true, Owner: true, DetailScope: DetailScope{"server", "library", "item", "original-detail-authority"}, Scope: DetailScope{"server", "library", "item", "separate-file-authority"}}
	calls := 0
	guard := func(_ context.Context, caller readprep.Caller, got FileInfoIdentity) (FileInfoAdmission, error) {
		calls++
		if caller != "private-family" || got != id {
			return FileInfoAdmission{}, readprep.ErrStale
		}
		return ad, nil
	}
	runtime, err := NewFileInfoRuntime(c.DB, store, CheckFileInfoAuthority)
	if err != nil {
		t.Fatal(err)
	}
	runtime.now = func() time.Time { return now }
	runtime.anchors.now = runtime.now
	ctx := WithFileInfoAuthority(context.Background(), guard)
	original := ContentRevision{Catalog: 123, Viewer: 45}
	if _, err = runtime.AcceptDetailOpening(ctx, "private-family", id, original); !errors.Is(err, readprep.ErrInvalid) || calls != 0 {
		t.Fatal("accepted outside Detail snapshot", err, calls)
	}
	var opening string
	err = dbwork.WithReadSnapshot(ctx, c.DB, func(snapshot context.Context) error {
		var e error
		opening, e = runtime.AcceptDetailOpening(snapshot, "private-family", id, original)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	proof, accepted, err := runtime.opening(ctx, "private-family", opening)
	if err != nil || proof.Revision != original || proof.InfoEpoch != 17 || !proof.Expires.Equal(now.Add(30*time.Minute)) {
		t.Fatal("opening changed capture", proof, err)
	}
	witness := fileInfoWitness(accepted)
	// No current ContentRevision is read or supplied here. Unrelated library
	// churn cannot replace or revoke this independently ready item's capture.
	if _, err = runtime.checkWindowBasis(ctx, "private-family", proof, witness); err != nil {
		t.Fatal(err)
	}
	// A separate FileInfo authority change may leave the original Detail proof
	// valid; a window still must retire rather than adopt the new audience.
	ad.Scope.ViewerFence = "fresh-file-authority"
	if _, _, err = runtime.opening(ctx, "private-family", opening); err != nil {
		t.Fatal("file audience overwrote Detail proof", err)
	}
	if _, err = runtime.checkWindowBasis(ctx, "private-family", proof, witness); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("window audience silently adopted", err)
	}
	ad.Scope = accepted.Scope
	for _, mutate := range []func(){
		func() { ad.Head.SourceBlockers = 1 },
		func() { ad.Head.Desired++ },
		func() { ad.Head.Applied++; ad.Head.Desired++ },
		func() { ad.ItemIncarnation[15]++ },
		func() { ad.ConfigurationRevision++ },
		func() { ad.DetailScope.ViewerFence = "new-detail-authority" },
	} {
		ad = accepted
		mutate()
		if _, _, err = runtime.opening(ctx, "private-family", opening); !errors.Is(err, readprep.ErrStale) {
			t.Fatal("changed accepted basis survived", ad, err)
		}
	}
	ad = accepted
	ad.Owner = false
	if _, err = runtime.checkWindowBasis(ctx, "private-family", proof, witness); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("owner rows survived authority loss", err)
	}
	ad = accepted
	if _, _, err = runtime.opening(ctx, "other-family", opening); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("foreign caller accepted", err)
	}
	now = proof.Expires
	if _, _, err = runtime.opening(ctx, "private-family", opening); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("original expiry extended", err)
	}
}

func TestFileInfoRuntimeRequiresReadinessBeforeOpening(t *testing.T) {
	c := catalogtest.Open(t)
	store, err := readprep.New(readprep.Config{Workers: 1, MaxBytes: 128 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	id := FileInfoIdentity{Item: 1, Library: 1, PublicItem: [16]byte{1}}
	ad := FileInfoAdmission{Identity: id, ItemIncarnation: [16]byte{2}, Head: compactcatalog.InfoHead{Desired: 2, Applied: 1}, Kind: "movie", DetailScope: DetailScope{"s", "l", "i", "detail"}, Scope: DetailScope{"s", "l", "i", "file"}}
	runtime, err := NewFileInfoRuntime(c.DB, store, CheckFileInfoAuthority)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithFileInfoAuthority(context.Background(), func(context.Context, readprep.Caller, FileInfoIdentity) (FileInfoAdmission, error) { return ad, nil })
	err = dbwork.WithReadSnapshot(ctx, c.DB, func(snapshot context.Context) error {
		token, e := runtime.AcceptDetailOpening(snapshot, "family", id, ContentRevision{1, 1})
		if token != "" {
			t.Fatal("pending head minted opening")
		}
		return e
	})
	if !errors.Is(err, compactcatalog.ErrInfoPreparing) {
		t.Fatal("pending was not preparing", err)
	}
	if _, err = CheckFileInfoAuthority(context.Background(), "family", id); !errors.Is(err, ErrFileInfoAuthorityUnavailable) {
		t.Fatal("missing guard admitted", err)
	}
}
