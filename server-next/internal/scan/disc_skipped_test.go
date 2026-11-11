package scan_test

import (
	"context"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
	"testing"
)

// protected is a prober for which every file is a copy-protected disc.
type protected struct{}

func (protected) Probe(context.Context, string) (scan.Facts, error) {
	return scan.Facts{}, scan.ErrEncrypted
}

// A copy-protected disc is listed in Files skipped with a reason of its own, and its title
// stays in the library under its folder's name.
func TestACopyProtectedDiscIsSkippedByName(t *testing.T) {
	f := newFixture(t, db.LibraryMovies)
	f.write("Heat (1995)/BDMV/index.bdmv")
	f.s.Prober = protected{}
	f.scan()
	skipped, total, err := f.d.SkippedFiles(ctx, f.lib.ID, 0, 10)
	if err != nil || total != 1 || skipped[0].Problem != db.ProblemEncryptedDisc || skipped[0].Problem.String() != "encrypted_disc" {
		t.Fatalf("Files skipped: %+v, %v", skipped, err)
	}
	if _, ok := f.titles(db.KindMovie)["Heat"]; !ok {
		t.Fatal("the disc's title should stay in the library")
	}
}
