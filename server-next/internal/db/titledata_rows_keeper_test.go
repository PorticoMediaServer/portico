package db

import "testing"

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
