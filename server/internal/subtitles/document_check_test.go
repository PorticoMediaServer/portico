package subtitles

import (
	"context"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// A subtitle body is re-authorized around every chunk it sends. Those checks
// only read, so they must not queue for the write gate: they answer while
// another writer holds it, and they never stand in front of a scan's writes.
func TestDocumentChecksDoNotWaitForOrTakeTheWriteGate(t *testing.T) {
	f := newSubtitleFixture(t)
	ctx := context.Background()
	first, e := f.s.Mutate(ctx, f.p, f.item.Public, uploadMutation("upload", plainSubtitle, f.item.Token))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.Select(ctx, f.p, f.item.Public, "session", SelectRequest{OperationID: "select", Generation: 1, ExpectedRevision: 1, Mode: "track", ResourceID: first.ResourceID, ResourceRevision: 1, OffsetUS: "0"}); e != nil {
		t.Fatal(e)
	}
	pin, e := dbwork.Begin(ctx, f.db, dbwork.ClassEstablishedPlayback)
	if e != nil {
		t.Fatal(e)
	}
	if e = PinManifestTextTx(ctx, pin.Tx(), "session", f.item.Public, f.item.Token, ViewerKey("local", "account", "profile", "server")); e != nil {
		t.Fatal(e)
	}
	if e = pin.Commit(); e != nil {
		t.Fatal(e)
	}
	checks := map[string]func(context.Context) error{
		"the selected document": func(ctx context.Context) error {
			return f.s.CheckDocument(ctx, f.p, f.item.Public, "session", first.ResourceID, 1)
		},
		"a manifest document": func(ctx context.Context) error {
			return f.s.CheckManifestDocument(ctx, f.p, f.item.Public, "session", first.ResourceID, 1)
		},
	}
	playback := dbwork.WithClass(ctx, dbwork.ClassEstablishedPlayback)
	acquired := func() uint64 {
		var total uint64
		for _, class := range dbwork.WriteGate().Stats().ByClass {
			total += class.Acquired
		}
		return total
	}
	// A scan is in the middle of a write.
	scan, e := dbwork.Begin(ctx, f.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		t.Fatal(e)
	}
	before := acquired()
	for name, check := range checks {
		for i := 0; i < 50; i++ {
			bounded, cancel := context.WithTimeout(playback, 2*time.Second)
			e = check(bounded)
			cancel()
			if e != nil {
				scan.Rollback()
				t.Fatalf("a check of %s while a writer holds the gate: %v", name, e)
			}
		}
	}
	if taken := acquired() - before; taken != 0 {
		scan.Rollback()
		t.Fatalf("a hundred document checks took the write gate %d times", taken)
	}
	if e = scan.Commit(); e != nil {
		t.Fatal(e)
	}
	// They still refuse a viewer whose access has gone.
	f.allowed = false
	for name, check := range checks {
		if e = check(playback); e != identity.ErrUnauthorized {
			t.Fatalf("a check of %s after access was withdrawn: %v", name, e)
		}
	}
}
