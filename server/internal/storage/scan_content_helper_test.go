//go:build linux || darwin

package storage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A scan command runs in the real helper, which moves the admitted file to
// descriptor 3 and execs the tool. On macOS the helper's Go runtime could hold
// 3 for its kqueue, and the move broke its poller: every probe failed.
func TestScanCommandReadsTheFileThroughTheRealHelper(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := New(binary)
	root := t.TempDir()
	path := filepath.Join(root, "track.flac")
	if err = os.WriteFile(path, []byte("probe me"), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		ctx := WithScanInput(context.Background(), c, "source", InventoryRequest{Root: root})
		var out bytes.Buffer
		handled, err := RunScanCommand(ctx, "/bin/cat", []string{path}, path, &out)
		if !handled || err != nil || out.String() != "probe me" {
			t.Fatalf("run %d: handled=%v err=%v out=%q", i, handled, err, out.String())
		}
	}
}

// The helper must start with descriptor 3 already taken (see scanHelperCommand).
// The crash it prevents is a race, so this checks the precondition itself.
func TestScanHelperStartsWithDescriptorThreeReserved(t *testing.T) {
	cmd, err := scanHelperCommand("/bin/true", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cmd.ExtraFiles) != 1 || cmd.ExtraFiles[0] == nil {
		t.Fatalf("the helper's descriptor 3 is not reserved: %v", cmd.ExtraFiles)
	}
}

func TestDiscScanCommandRetainsPrefixAndActualConfinedFile(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := New(binary)
	root := t.TempDir()
	path := filepath.Join(root, "disc.iso")
	if err = os.WriteFile(path, []byte("disc input"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := WithScanInput(context.Background(), c, "source", InventoryRequest{Root: root})
	var out bytes.Buffer
	handled, err := RunDiscScanCommand(ctx, binary, []string{"--portico-disc-scan-fixture", "bluray:" + path}, path, true, &out)
	if !handled || err != nil || out.String() != "bluray:disc input" {
		t.Fatal(handled, out.String(), err)
	}
	for _, args := range [][]string{{"--portico-disc-scan-fixture", path}, {"--portico-disc-scan-fixture", "https:" + path}, {"--portico-disc-scan-fixture", "bluray:" + path, path}} {
		out.Reset()
		handled, err = RunDiscScanCommand(ctx, binary, args, path, true, &out)
		if !handled || err == nil || out.Len() != 0 {
			t.Fatal("disc invocation escaped closed input", handled, err)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.iso")
	if err = os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked.iso")
	if err = os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	handled, err = RunDiscScanCommand(ctx, binary, []string{"--portico-disc-scan-fixture", "bluray:" + link}, link, true, &out)
	if !handled || err == nil || out.Len() != 0 {
		t.Fatal("outside-root symlink admitted", handled, err)
	}
}
