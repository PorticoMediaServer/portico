package scan_test

import (
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// While a scan runs, its progress names the file it is on, as a path within the library's
// folder, for the owner's activity panel.
func TestAScansProgressNamesTheFileItIsOn(t *testing.T) {
	f := newFixture(t, db.LibraryMovies)
	names := []string{"Heat (1995).mkv", "Ronin (1998)/Ronin (1998).mkv", "Thief (1981).mkv"}
	for _, name := range names {
		f.write(name)
	}
	f.s.BatchFiles = 1
	var seen []string
	f.s.Progress = func(stats scan.Stats) {
		if stats.Current != "" {
			seen = append(seen, stats.Current)
		}
	}
	f.scan()
	if len(seen) == 0 {
		t.Fatal("no progress named a file")
	}
	for _, current := range seen {
		if !slices.Contains(names, current) {
			t.Errorf("a progress named %q, not a file of the library as its folder has it %v", current, names)
		}
	}
}
