package db

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/titledata/titledatatest"
)

// A new title data file or tag pass that lands while a profile's mood rows are being chosen
// leaves that choice stale: it may have been made from the old tags, so Home's next read asks
// for it again instead of keeping it for an hour.
func TestAChoiceMadeAcrossASwapIsMadeAgain(t *testing.T) {
	var k moodKeeper
	v := Viewer{UserID: 1, ProfileID: 1}
	k.ask(v)
	got, marked, ok := k.next()
	if !ok || got != v {
		t.Fatal("Home's ask was not queued")
	}
	k.stale(0) // the file is swapped while the choice is made
	k.keep(v, nil, marked)
	k.ask(v)
	if _, _, ok := k.next(); !ok {
		t.Fatal("a choice made across a swap was kept as fresh")
	}
	// Without a swap, a choice is kept.
	k.keep(v, nil, k.marked)
	k.ask(v)
	if _, _, ok := k.next(); ok {
		t.Fatal("a choice made with no swap was asked for again")
	}
}

// A choice that begins while a new file is being put in use is never kept as fresh on the old
// file: it either reads the new one or is made again.
func TestAChoiceBegunDuringAFileSwapIsNeverKeptOnTheOldFile(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	path := filepath.Join(t.TempDir(), "screen.sqlite")
	titledatatest.Write(t, path, titledatatest.File{})
	if err := PrepareTitleData(ctx, path, "screen", "2026.45"); err != nil {
		t.Fatal(err)
	}
	v := Viewer{UserID: 1, ProfileID: 1}
	d.moods.ask(v)
	var marked int64
	readNew := false
	// The choice begins between the swap and the mark: it is taken now and reads the file in use now.
	betweenSwapAndMark = func() {
		_, marked, _ = d.moods.next()
		readNew = d.titleData.Load() != nil
	}
	defer func() { betweenSwapAndMark = nil }()
	if err := d.UseTitleData(path); err != nil {
		t.Fatal(err)
	}
	d.moods.keep(v, nil, marked)
	d.moods.ask(v)
	_, _, askedAgain := d.moods.next()
	if !readNew && !askedAgain {
		t.Fatal("a choice that read the old file during a swap was kept as fresh")
	}
}
