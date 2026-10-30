package subtitles

import (
	"context"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// A viewer's own subtitle request waits for the write gate in the class of
// its request, not behind bulk catalogue work; work that names no class (the
// scan's import) stays background. The request here is one that writes (an
// upload): listing a title's subtitles is a read and takes no gate at all,
// which the test below holds. How many times one upload takes the gate is its
// own business; in which class is the rule.
func TestSubtitleRequestsTakeTheWriteGateInTheirOwnClass(t *testing.T) {
	f := newSubtitleFixture(t)
	acquired := func(class dbwork.Class) uint64 {
		return dbwork.WriteGate().Stats().ByClass[class.String()].Acquired
	}
	for _, class := range []dbwork.Class{dbwork.ClassEstablishedPlayback, dbwork.ClassInteractive} {
		own, background := acquired(class), acquired(dbwork.ClassBackgroundMedia)
		if _, err := f.s.Mutate(dbwork.WithClass(context.Background(), class), f.p, f.item.Public, uploadMutation(fmt.Sprint(class), plainSubtitle, f.item.Token)); err != nil {
			t.Fatal(err)
		}
		if acquired(class) <= own || acquired(dbwork.ClassBackgroundMedia) != background {
			t.Fatalf("a %s request took the gate as %s %d time(s) and as background %d time(s)", class, class, acquired(class)-own, acquired(dbwork.ClassBackgroundMedia)-background)
		}
	}
	background := acquired(dbwork.ClassBackgroundMedia)
	if _, err := f.s.Mutate(context.Background(), f.p, f.item.Public, uploadMutation("background", plainSubtitle, f.item.Token)); err != nil {
		t.Fatal(err)
	}
	established, interactive := acquired(dbwork.ClassEstablishedPlayback), acquired(dbwork.ClassInteractive)
	if _, err := f.s.Mutate(context.Background(), f.p, f.item.Public, uploadMutation("background-again", plainSubtitle, f.item.Token)); err != nil {
		t.Fatal(err)
	}
	if acquired(dbwork.ClassBackgroundMedia) <= background || acquired(dbwork.ClassEstablishedPlayback) != established || acquired(dbwork.ClassInteractive) != interactive {
		t.Fatal("work that names no class no longer takes the gate as background")
	}
}

func TestSubtitleCatalogueReadsDoNotTakeTheWriteGate(t *testing.T) {
	f := newSubtitleFixture(t)
	before := dbwork.WriteGate().Stats().Acquired
	if _, err := f.s.List(context.Background(), f.p, f.item.Public); err != nil {
		t.Fatal(err)
	}
	if dbwork.WriteGate().Stats().Acquired != before {
		t.Fatal("read acquired the write gate")
	}
}
