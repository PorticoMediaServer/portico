//go:build !windows

package subtitlevideo

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"testing"
)

func orphanFixture(t *testing.T) (*Runtime, string) {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	locks, e := livechannels.NewPhysicalLocks(filepath.Join(root, "custody"))
	if e != nil {
		t.Fatal(e)
	}
	return &Runtime{directory: root, custody: locks}, root
}
func markedActor(t *testing.T, root, id, marker string) string {
	t.Helper()
	dir := filepath.Join(root, "subtitle-"+id)
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if marker != "" {
		if e := os.WriteFile(filepath.Join(dir, "owner"), []byte(marker), 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.WriteFile(filepath.Join(dir, "private-track.ass"), []byte("private fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	return dir
}
func TestOrphanSweepOnlyRemovesMarkedUnownedActors(t *testing.T) {
	r, root := orphanFixture(t)
	orphan := markedActor(t, root, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	unknown := markedActor(t, root, "cccccccccccccccccccccccccccccccccccccccccccccccc", "")
	mismatch := markedActor(t, root, "dddddddddddddddddddddddddddddddddddddddddddddddd", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee")
	outside := t.TempDir()
	if e := os.WriteFile(filepath.Join(outside, "owner"), []byte("ffffffffffffffffffffffffffffffffffffffffffffffff"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(root, "subtitle-ffffffffffffffffffffffffffffffffffffffffffffffff")); e != nil {
		t.Fatal(e)
	}
	if e := r.reconcileOrphans(); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(orphan); !os.IsNotExist(e) {
		t.Fatalf("dead marked actor retained: %v", e)
	}
	for _, path := range []string{unknown, mismatch, outside} {
		if _, e := os.Stat(path); e != nil {
			t.Fatalf("unproven actor removed: %s: %v", path, e)
		}
	}
}
func TestOrphanInheritedCustodyChild(t *testing.T) {
	if os.Getenv("PORTICO_SUBTITLE_CUSTODY_CHILD") != "1" {
		return
	}
	lock := os.NewFile(3, "inherited-custody")
	if _, e := lock.Stat(); e != nil {
		os.Exit(2)
	}
	if _, e := os.Stdout.WriteString("ready\n"); e != nil {
		os.Exit(3)
	}
	var b [1]byte
	_, _ = os.Stdin.Read(b[:])
	lock.Close()
	os.Exit(0)
}
func TestOrphanSweepRetainsSurvivingDecoderInheritedCustody(t *testing.T) {
	r, root := orphanFixture(t)
	id := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	dir := markedActor(t, root, id, id)
	lock, e := r.custody.Lock(livechannels.Allocation{ID: id, Generation: 1})
	if e != nil {
		t.Fatal(e)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestOrphanInheritedCustodyChild$")
	child.Env = append(os.Environ(), "PORTICO_SUBTITLE_CUSTODY_CHILD=1")
	child.ExtraFiles = []*os.File{lock}
	input, e := child.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	output, e := child.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = child.Start(); e != nil {
		t.Fatal(e)
	}
	finished := false
	t.Cleanup(func() {
		input.Close()
		lock.Close()
		if !finished {
			child.Process.Kill()
			child.Wait()
		}
	})
	if line, e := bufio.NewReader(output).ReadString('\n'); e != nil || line != "ready\n" {
		t.Fatalf("child custody did not initialize: %q %v", line, e)
	}
	// Closing the parent's descriptor models its loss/death. The decoder's inherited
	// open-file description must keep custody independently of parent bookkeeping.
	if e = lock.Close(); e != nil {
		t.Fatal(e)
	}
	if e = r.reconcileOrphans(); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(dir, "private-track.ass")); e != nil {
		t.Fatalf("live inherited actor was reclaimed: %v", e)
	}
	input.Close()
	io.Copy(io.Discard, output)
	if e = child.Wait(); e != nil {
		t.Fatal(e)
	}
	finished = true
	if e = r.reconcileOrphans(); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(dir); !os.IsNotExist(e) {
		t.Fatalf("retired actor was not reclaimed: %v", e)
	}
}
