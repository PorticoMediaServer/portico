package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// Scanner M1 (OPS-1, planning/v3/rebuilds/SCANNER-REVIEW.md): a stat, a
// directory page or a small read of a local library root runs in this process.
// A helper process cost 26 ms on a fast host and 90–390 ms on the demo VPS,
// and a scan made ~30 of them per film. The same handler code runs either
// way (Helper), with the same containment: every path is resolved beneath the
// opened root (os.Root, openat2 RESOLVE_BENEATH on Linux), symlinks are refused
// or must stay inside the root, and the root identity is re-proven per call.
//
// What the helper process still owns:
//   - roots on network or FUSE filesystems and managed mounts, where a syscall
//     can hang and only a killable process contains it;
//   - operations that exec a tool (ffprobe/ffmpeg behind the decoder sandbox),
//     pass a descriptor to another process, or live long (the folder watcher,
//     playback streams, mount registration).
// Pool admission and the duplicate-operation fence are unchanged.

// inProcessOperations are the helper operations that only stat, list or read
// bounded bytes beneath a root.
var inProcessOperations = map[string]bool{
	"root": true, "root-probe": true, "stat": true, "blob": true, "subtitle-blob": true,
	"inventory": true, "subtitle-candidates": true, "inventory-page": true, "inventory-stat": true,
	"inventory-absence": true, "inventory-pages": true, "inventory-directories": true,
	"inventory-pages-budget": true, "inventory-directory": true, "inventory-audio-cover": true,
	"inventory-audio-tags": true, "inventory-audio-prefix": true, "inventory-blob": true,
	"inventory-subtitle-blob": true, "lyrics-local": true, "disc-members": true,
}

// localFilesystem reports whether path is on a local filesystem, from the
// mount table only (it never touches path itself, which could hang). It is a
// seam for tests.
var localFilesystem = mountIsLocal

// inProcessRequest reports the request bytes of a helper command that may run
// in this process. A command it declines keeps its stdin intact.
func inProcessRequest(cmd *exec.Cmd) ([]byte, bool) {
	if cmd == nil || len(cmd.Args) != 2 || cmd.Args[1] != "--portico-storage-helper" || !selfHelper(cmd.Path) {
		return nil, false
	}
	stdin, ok := cmd.Stdin.(*bytes.Reader)
	if !ok {
		return nil, false
	}
	data, err := io.ReadAll(stdin)
	cmd.Stdin = bytes.NewReader(data)
	if err != nil || len(data) > 64<<10 {
		return nil, false
	}
	var r request
	if json.Unmarshal(data, &r) != nil || !inProcessOperations[r.Operation] || r.MountedRoot != "" {
		return nil, false
	}
	roots := []string{}
	switch {
	case r.Inventory != nil:
		roots = append(roots, r.Inventory.Root)
	case len(r.Inventories) > 0:
		for _, inventory := range r.Inventories {
			roots = append(roots, inventory.Root)
		}
	case r.LyricsRoot != "":
		roots = append(roots, r.LyricsRoot)
	default:
		roots = append(roots, r.Path)
	}
	for _, root := range roots {
		if !filepath.IsAbs(root) || !localFilesystem(filepath.Clean(root)) {
			return nil, false
		}
	}
	return data, true
}

// runInProcess serves one admitted helper request with Helper on a pipe, so the
// caller's consumer reads exactly the bytes a helper process would have
// written. release runs once both sides have finished, as a helper's slot is
// released only after its physical wait. Background work keeps the policies a
// background helper process had (nice 19, idle IO): it runs on one of the
// dedicated low-priority threads (runLowPriority).
func runInProcess(ctx context.Context, data []byte, consume func(io.Reader) error, release func(), background bool) error {
	reader, writer := io.Pipe()
	helperDone, consumeDone := make(chan error, 1), make(chan error, 1)
	serve := func() {
		var err error
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("storage operation failed: %v", r)
			}
			_ = writer.Close()
			helperDone <- err
		}()
		err = inProcessHelper(bytes.NewReader(data), writer)
	}
	supervise.Go("storage.in-process", func() {
		if background && !lowPriorityInline {
			runLowPriority(serve)
			return
		}
		serve()
	})
	supervise.Go("storage.in-process-consume", func() {
		err := consume(reader)
		// A consumer that stops early must not leave the handler blocked on
		// the pipe.
		_ = reader.CloseWithError(errInProcessConsumerDone)
		consumeDone <- err
	})
	finish := func() (error, error) { return <-consumeDone, <-helperDone }
	select {
	case <-ctx.Done():
		_ = reader.CloseWithError(ctx.Err())
		supervise.Go("storage.in-process-release", func() { finish(); release() })
		return ctx.Err()
	case readErr := <-consumeDone:
		helperErr := <-helperDone
		release()
		if readErr != nil {
			return readErr
		}
		if helperErr != nil && !errors.Is(helperErr, errInProcessConsumerDone) {
			// A helper process reported failure as an exit status with its
			// message; keep that opaque contract so no caller starts matching
			// on errors it never received before.
			return errors.New(helperErr.Error())
		}
		return nil
	}
}

// inProcessHelper is Helper; a seam for tests.
var inProcessHelper = Helper

var errInProcessConsumerDone = errors.New("storage consumer finished")

var self struct {
	sync.Once
	path string
}

// selfHelper reports whether binary is this executable, the only helper the
// server configures. Another binary is a deliberate stand-in (a test's fault
// injector or call counter) and keeps its process.
func selfHelper(binary string) bool {
	self.Do(func() {
		if path, err := os.Executable(); err == nil {
			if resolved, err := filepath.EvalSymlinks(path); err == nil {
				path = resolved
			}
			self.path = path
		}
	})
	if self.path == "" || binary == "" {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(binary); err == nil {
		binary = resolved
	}
	return binary == self.path
}

// lowPriorityInline runs background in-process work on the caller's own
// goroutine, at normal priority, in test binaries. A testing/synctest bubble
// can't hand its work to these threads: they would be started inside the
// first bubble that used them and never exit (a deadlock when its test
// returns), and every job carries the caller's pipe and context, whose
// channels and timers can't be used from outside the bubble. The pool's own
// tests opt back in (useLowPriorityThreads).
var lowPriorityInline = testing.Testing()

// lowPriorityThreads is how many dedicated threads serve background in-process
// operations; more wait their turn. Admission already bounds background work
// to a few at a time.
const lowPriorityThreads = 8

var lowPriority struct {
	once sync.Once
	work chan func()
}

// runLowPriority runs fn on a dedicated OS thread with the background policy
// (nice 19, idle IO), and returns when fn has. The threads are locked for the
// life of the process and never exit. An unprivileged process can't restore a
// thread's priority, so a lowered thread must never return to the runtime;
// and it must never exit either, because Linux delivers a child's parent-death
// signal when the *thread* that started it exits: bubblewrap's
// --die-with-parent (decoders, transcodes, probe workers) would kill sandboxes
// started earlier on that thread by unrelated goroutines.
func runLowPriority(fn func()) {
	lowPriority.once.Do(func() {
		lowPriority.work = make(chan func())
		for i := 0; i < lowPriorityThreads; i++ {
			supervise.Go("storage.low-priority-thread", func() {
				runtime.LockOSThread()
				lowerCurrentThread()
				for job := range lowPriority.work {
					job()
				}
			})
		}
	})
	// A panic in fn is caught on the worker thread and raised again here, in
	// the caller's goroutine: the locked thread must outlive every job.
	done := make(chan any, 1)
	lowPriority.work <- func() {
		defer func() { done <- recover() }()
		fn()
	}
	if p := <-done; p != nil {
		panic(p)
	}
}
