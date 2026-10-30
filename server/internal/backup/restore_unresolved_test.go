package backup

import (
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"testing"
)

// The server starts on its current state only when a restore either never began
// moving files or finished (restored or rolled back, which clear the marker).
// A marker that can't be read, or says files may have moved, stops the start.
func TestRestoreUnresolvedStopsTheStartOnlyWhenFilesMayHaveMoved(t *testing.T) {
	state := t.TempDir()
	if RestoreUnresolved(state) {
		t.Fatal("no restore at all reported as unresolved")
	}
	staged := storagepaths.Path(state, RestoreStagedDir)
	if err := os.MkdirAll(staged, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		state string
		want  bool
	}{{markerStaged, false}, {markerPreparing, true}, {markerSwitching, true}, {markerApplied, true}, {markerRollingBack, true}} {
		if err := writeMarker(staged, restoreMarker{State: c.state}); err != nil {
			t.Fatal(err)
		}
		if got := RestoreUnresolved(state); got != c.want {
			t.Fatalf("marker %s: unresolved = %v, want %v", c.state, got, c.want)
		}
		if _, err := os.Stat(filepath.Join(staged, RestoreMarkerFile+".next")); !os.IsNotExist(err) {
			t.Fatalf("marker %s: the temporary file was left behind (%v)", c.state, err)
		}
	}
	// A torn marker (a crash in the middle of an old-style write) is unresolved.
	if err := writeTestFile(filepath.Join(staged, RestoreMarkerFile), []byte(`{"state":"swi`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !RestoreUnresolved(state) {
		t.Fatal("a torn marker let the server start")
	}
}
