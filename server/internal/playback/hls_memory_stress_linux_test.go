//go:build linux

package playback

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Exercise actual kernel write admission with the sampler deliberately stopped.
// The mount exists only inside an unprivileged child user/mount namespace; the
// application never mounts filesystems. Hosts without user namespaces skip this
// check, while the deterministic cap/floor tests still run everywhere.
func TestMemoryKernelBoundStress(t *testing.T) {
	if os.Getenv("PORTICO_MEMORY_STRESS_CHILD") != "1" {
		binary, err := exec.LookPath("unshare")
		if err != nil {
			t.Skip("unshare unavailable: run the bounded tmpfs batch check")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "--user", "--map-root-user", "--mount", "--propagation", "private", "--", os.Args[0], "-test.run=^TestMemoryKernelBoundStress$", "-test.v")
		cmd.Env = append(os.Environ(), "PORTICO_MEMORY_STRESS_CHILD=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			if strings.Contains(string(out), "unshare:") && strings.Contains(string(out), "Operation not permitted") {
				t.Skip("unprivileged namespaces unavailable: run the bounded tmpfs batch check")
			}
			t.Fatalf("isolated tmpfs stress: %v\n%s", err, out)
		}
		t.Log(string(out))
		return
	}

	const ceiling = 320 << 20
	memory := t.TempDir()
	if err := syscall.Mount("tmpfs", memory, "tmpfs", syscall.MS_NOSUID|syscall.MS_NODEV|syscall.MS_NOEXEC, "size=320m,nr_inodes=128,mode=0700"); err != nil {
		t.Fatal(err)
	}
	defer syscall.Unmount(memory, syscall.MNT_DETACH)
	memory = filepath.Join(memory, "portico-hls-test")
	h := newSweepFixture(t.TempDir())
	h.ctx = context.Background()
	cfg := DefaultDeliveryConfiguration()
	cfg.MemoryCap = "2gb"
	h.ConfigureSettings(fixedSettings{cfg: cfg})
	if err := h.ConfigureMemory(memory); err != nil {
		t.Fatal(err)
	}
	defer h.memoryOutputRoot.Close()
	if !MemoryOutputAvailable(memory, cfg.MemoryCap) {
		facts, err := readMemoryFacts(memory)
		t.Skipf("insufficient host/cgroup headroom for the 320 MiB stress volume: facts=%+v error=%v", facts, err)
	}
	baseline, err := readMemoryFacts(memory)
	if err != nil {
		t.Fatal(err)
	}
	// One placement creates all producers' files, as video/audio/subtitle
	// producers do in one session. No directory measurements or sweeps run.
	dir, err := h.createOutput("stress", true)
	if err != nil || filepath.Dir(dir) != memory {
		t.Fatal("bounded tmpfs not admitted", dir, err)
	}
	if err = os.WriteFile(filepath.Join(dir, "generation"), []byte("3"), 0600); err != nil {
		t.Fatal(err)
	}
	const producers = 8
	errorsSeen := make(chan error, producers)
	var wg sync.WaitGroup
	for i := 0; i < producers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			f, e := os.Create(filepath.Join(dir, fmt.Sprintf("segment-%06d.ts", i)))
			if e != nil {
				errorsSeen <- e
				return
			}
			defer f.Close()
			chunk := make([]byte, 64<<10)
			for j := range chunk {
				chunk[j] = byte(i + 1)
			}
			// Every writer attempts twice the entire volume. The kernel must
			// stop all of them, independent of speed and sampler scheduling.
			for written := 0; written < 2*ceiling; written += len(chunk) {
				if _, e = f.Write(chunk); e != nil {
					errorsSeen <- e
					return
				}
			}
			errorsSeen <- errors.New("writer exceeded kernel ceiling")
		}(i)
	}
	wg.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if !errors.Is(err, syscall.ENOSPC) {
			t.Fatalf("expected bounded ENOSPC, got %v", err)
		}
	}
	facts, err := readMemoryFacts(memory)
	if err != nil || facts.filesystemLimit != ceiling || facts.free != 0 {
		t.Fatal("kernel did not enforce the declared volume", facts, err)
	}
	bytes, _, err := measureDirectory(dir)
	if err != nil || bytes > ceiling || bytes < ceiling-(1<<20) {
		t.Fatal("unexpected peak output", bytes, err)
	}
	if !h.retryMemoryOutput("stress") {
		t.Fatal("ENOSPC did not retain a retryable memory session")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h.sweepMemory(ctx)
	if h.inMemory["stress"] || h.memorySpills.Load() != 1 {
		t.Fatal("full output did not move to disk")
	}
	diskBytes, _, err := measureDirectory(h.outputDir("stress"))
	if err != nil || diskBytes != bytes {
		t.Fatal("spill lost completed bytes", bytes, diskBytes, err)
	}
	if dir, err := h.createOutput("stress", true); err != nil || filepath.Dir(dir) != h.root {
		t.Fatal("spilled output did not restart on disk", dir, err)
	}
	facts, err = readMemoryFacts(memory)
	if err != nil || facts.free != baseline.free {
		t.Fatal("spill retained tmpfs allocation", facts, err)
	}
	t.Logf("%d concurrent writers stopped at %d bytes without sampling; spill and disk restart passed", producers, bytes)
}
