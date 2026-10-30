package persistence

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func candidateCopy(t *testing.T, name string) string {
	t.Helper()
	return freshDatabaseCopy(t, t.TempDir(), name)
}

func inspectPath(ctx context.Context, path string) (CandidateReport, error) {
	return InspectCandidate(ctx, filepath.Dir(path), filepath.Base(path))
}

func TestInspectCandidate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	ctx := context.Background()
	report, err := inspectPath(ctx, candidateCopy(t, "good.db"))
	if err != nil || report.SchemaVersion != SchemaVersion() || report.Integrity != "ok" || report.Recovery != nil {
		t.Fatalf("good candidate: %+v %v", report, err)
	}
	text := filepath.Join(t.TempDir(), "text.db")
	if err = os.WriteFile(text, []byte("not a database"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = inspectPath(ctx, text); !errors.Is(err, ErrCandidateNotPortico) {
		t.Fatalf("text file: %v", err)
	}
	raw, err := os.ReadFile(candidateCopy(t, "truncated.db"))
	if err != nil {
		t.Fatal(err)
	}
	truncated := filepath.Join(t.TempDir(), "truncated.db")
	if err = os.WriteFile(truncated, raw[:len(raw)/2], 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = inspectPath(ctx, truncated); !errors.Is(err, ErrCandidateIntegrity) {
		t.Fatalf("truncated copy: %v", err)
	}
	mangled := append([]byte(nil), raw...)
	for i := 100; i < 356 && i < len(mangled); i++ {
		mangled[i] ^= 0xff
	}
	flipped := filepath.Join(t.TempDir(), "flipped.db")
	if err = os.WriteFile(flipped, mangled, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = inspectPath(ctx, flipped); !errors.Is(err, ErrCandidateIntegrity) {
		t.Fatalf("flipped copy: %v", err)
	}
	newer := candidateCopy(t, "newer.db")
	handle, err := dbwork.OpenHandle(newer, dbwork.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = handle.Exec(`INSERT INTO configuration(key,value) VALUES('schema_version','999999') ON CONFLICT(key) DO UPDATE SET value='999999'`); err != nil {
		t.Fatal(err)
	}
	handle.Close()
	if _, err = inspectPath(ctx, newer); !errors.Is(err, ErrCandidatePreRelease) {
		t.Fatalf("newer schema: %v", err)
	}
}
