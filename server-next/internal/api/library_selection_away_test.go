package api

import (
	"os"
	"path/filepath"
	"testing"
)

// A library has two folders and the disk of the second is not connected. A selection with a
// title in each is refused before anything is deleted: "Nothing was deleted" is then true.
func TestASelectionIsRefusedWholeWhenALibraryFolderIsAway(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	here, away := filepath.Join(t.TempDir(), "here"), filepath.Join(t.TempDir(), "away")
	writeFile(t, filepath.Join(here, "Heat (1995).mkv"), 10)
	writeFile(t, filepath.Join(away, "Alien (1979).mkv"), 11)
	var lib AdminLibrary
	create := LibraryCreate{Name: "Films", Kind: "movies", Folders: []string{here, away},
		Options: &LibraryOptions{Depth: "basic", Trash: TrashRule{Mode: "manual"}, AllowDelete: true}}
	if w := call(t, s, "POST", "/v1/admin/libraries", owner, create, &lib); w.Code != 200 {
		t.Fatalf("creating the library answered %d: %s", w.Code, w.Body)
	}
	scanNow(t, s, owner, lib.ID)
	heat, alien := missingItemID(t, s, owner, lib.ID, "Heat"), missingItemID(t, s, owner, lib.ID, "Alien")

	// The second disk goes away: its folder is no longer there.
	if err := os.Rename(away, away+"-unplugged"); err != nil {
		t.Fatal(err)
	}
	w := call(t, s, "POST", "/v1/admin/items/delete?confirm=2", owner, SelectionDelete{IDs: []string{heat, alien}}, nil)
	if w.Code != 409 {
		t.Fatalf("deleting with a folder away answered %d: %s", w.Code, w.Body)
	}
	// The answer says nothing was deleted, so nothing may have been.
	if _, err := os.Stat(filepath.Join(here, "Heat (1995).mkv")); err != nil {
		t.Errorf("the selection was refused with %s, yet the first title's file is gone: %v", w.Body, err)
	}
	missingItemID(t, s, owner, lib.ID, "Heat") // and the title is still in the library
}
