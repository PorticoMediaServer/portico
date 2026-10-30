package mounts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestCaptureServeSourcePreservesConfigurationAndExactGeneration(t *testing.T) {
	s, input := fixture(t)
	ctx := context.Background()
	row, err := s.Create(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	source, err := s.CaptureServeSource(ctx, row.ID, "1")
	if err != nil {
		t.Fatal(err)
	}
	if source.Generation != "1" || source.Remote != input.Remote || string(source.Configuration) != input.Config || source.Program.Path != input.Executable || source.Program.Included != input.UseIncluded {
		t.Fatal("configuration capture mismatch")
	}
	// A new B-42 version-only approval records no image digest.
	if source.ApprovedProgramDigest != "" {
		t.Fatal("version-only approval invented a program digest")
	}
	clear(source.Configuration)
	stored, err := os.ReadFile(filepath.Join(s.private, row.ID+".conf"))
	if err != nil || string(stored) != input.Config {
		t.Fatal("capture changed retained credentials", err)
	}
	if _, err = s.CaptureServeSource(ctx, row.ID, "2"); !errors.Is(err, storage.ErrRemoteChanged) {
		t.Fatal("replacement generation admitted", err)
	}
	if _, err = s.db.Exec(`UPDATE mount_controls SET removal_pending=1 WHERE mount_id=?`, row.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CaptureServeSource(ctx, row.ID, "1"); err == nil {
		t.Fatal("removed source configuration captured")
	}
}
