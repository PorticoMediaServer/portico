package storage

import (
	"os"
	"path/filepath"
	"testing"
)

// ProbeRoot's helper half answers absence instead of failing, so callers can
// tell a root that is gone from one they could not look at.
func TestProbeRootAnswersAbsence(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ path, code string }{
		{filepath.Join(dir, "missing"), RootNotFound},
		{filepath.Join(dir, "missing", "deeper"), RootNotFound},
		{file, RootNotDirectory},
		{filepath.Join(file, "below"), RootNotDirectory},
		{dir, ""},
	} {
		v, err := probeRoot(c.path)
		if err != nil {
			t.Fatalf("%s: %v", c.path, err)
		}
		if v.ErrorCode != c.code || (c.code == "" && (!v.Directory || RootIdentity(v) == "")) {
			t.Fatalf("%s: %+v, want code %q", c.path, v, c.code)
		}
	}
}
