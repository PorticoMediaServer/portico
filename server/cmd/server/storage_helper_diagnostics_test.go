package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

const (
	helperDiagnosticMarker = "portico-empty-helper-diagnostics"
	helperDiagnosticSlots  = 4
	helperDiagnosticStack  = 32 << 10
	helperDiagnosticWait   = 500 * time.Millisecond
)

// Only this test's children opt in. Supervisor already passes TMPDIR; no
// production environment forwarding, helper protocol or process policy changes.
func enableStorageHelperDiagnostics(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	if err := os.WriteFile(filepath.Join(dir, helperDiagnosticMarker), nil, 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

type helperIOState struct {
	mu                            sync.Mutex
	stage                         string
	reads, input, frames, outputs int64
	eof                           bool
}

func (s *helperIOState) mark(stage string) {
	s.mu.Lock()
	s.stage = stage
	s.mu.Unlock()
}

func (s *helperIOState) snapshot() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fmt.Sprintf("pid=%d stage=%s stdin_reads=%d stdin_bytes=%d stdin_frames=%d stdin_eof=%t stdout_bytes=%d\n",
		os.Getpid(), s.stage, s.reads, s.input, s.frames, s.eof, s.outputs)
}

type diagnosticHelperInput struct {
	io.Reader
	state *helperIOState
}

func (r diagnosticHelperInput) Read(p []byte) (int, error) {
	r.state.mark("stdin_read_pending")
	n, err := r.Reader.Read(p)
	r.state.mu.Lock()
	r.state.stage = "stdin_read_returned"
	r.state.reads++
	r.state.input += int64(n)
	r.state.frames += int64(bytes.Count(p[:n], []byte{'\n'}))
	r.state.eof = r.state.eof || err == io.EOF
	r.state.mu.Unlock()
	return n, err
}

type diagnosticHelperOutput struct {
	io.Writer
	state *helperIOState
}

func (w diagnosticHelperOutput) Write(p []byte) (int, error) {
	w.state.mark("stdout_write_pending")
	n, err := w.Writer.Write(p)
	w.state.mu.Lock()
	w.state.stage = "stdout_write_returned"
	w.state.outputs += int64(n)
	w.state.mu.Unlock()
	return n, err
}

// The wrappers forward every byte/error immediately, without buffering or an
// EOF handshake. Markers contain counters only, never request/path contents.
func diagnosticStorageHelperIO() (io.Reader, io.Writer, func()) {
	passthrough := func() (io.Reader, io.Writer, func()) { return os.Stdin, os.Stdout, func() {} }
	dir, prefix := helperLaunch.dir, helperLaunch.prefix
	if prefix == "" {
		return passthrough()
	}
	state := &helperIOState{stage: "helper_entered"}
	stop, joined := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(joined)
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if _, err := os.Stat(filepath.Join(dir, helperDiagnosticMarker+".request")); err != nil {
					continue
				}
				captureHelperDiagnostic(prefix, state)
				return
			}
		}
	}()
	finish := func() {
		close(stop)
		<-joined
		publishHelperDiagnostic(prefix+".finished", []byte(state.snapshot()))
	}
	return diagnosticHelperInput{os.Stdin, state}, diagnosticHelperOutput{os.Stdout, state}, finish
}

// The stack writer is tested independently of background filesystem latency.
func captureHelperDiagnostic(prefix string, state *helperIOState) {
	before := state.snapshot()
	stack := make([]byte, helperDiagnosticStack)
	n := runtime.Stack(stack, true)
	data := []byte(fmt.Sprintf("%sstack_bytes=%d/%d (full buffer may be truncated)\n", before, n, len(stack)))
	data = append(data, stack[:n]...)
	data = append(data, state.snapshot()...)
	publishHelperDiagnostic(prefix+".dump", data)
}

func publishHelperDiagnostic(path string, data []byte) {
	if os.WriteFile(path+".tmp", data, 0600) == nil {
		_ = os.Rename(path+".tmp", path)
	}
}

func logStorageHelperDiagnostics(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, helperDiagnosticMarker+".request"), nil, 0600); err != nil {
		t.Logf("empty-source child diagnostic request: %v", err)
		return
	}
	deadline := time.Now().Add(helperDiagnosticWait)
	for time.Now().Before(deadline) {
		ready, found := true, false
		for slot := 0; slot < helperDiagnosticSlots; slot++ {
			prefix := filepath.Join(dir, fmt.Sprintf("helper-%d", slot))
			if _, err := os.Stat(prefix + ".init"); err != nil {
				continue
			}
			found = true
			_, dumped := os.Stat(prefix + ".dump")
			_, finished := os.Stat(prefix + ".finished")
			ready = ready && (dumped == nil || finished == nil)
		}
		if found && ready {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for slot := 0; slot < helperDiagnosticSlots; slot++ {
		prefix := filepath.Join(dir, fmt.Sprintf("helper-%d", slot))
		for _, suffix := range []string{".init", ".started", ".finished", ".dump"} {
			f, err := os.Open(prefix + suffix)
			if err != nil {
				t.Logf("empty-source child %d%s: %v", slot, suffix, err)
				continue
			}
			const max = helperDiagnosticStack + 1024
			data, err := io.ReadAll(io.LimitReader(f, max+1))
			_ = f.Close()
			truncated := len(data) > max
			if truncated {
				data = data[:max]
			}
			t.Logf("empty-source child %d%s (%d/%d bytes, truncated=%t): error=%v\n%s", slot, suffix, len(data), max, truncated, err, data)
		}
	}
}

func TestStorageHelperChildDiagnosticsPreserveDemandAndFrames(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"one.pdf", "two.pdf"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	run := func(diagnostics bool) []byte {
		t.Helper()
		if diagnostics {
			if err := os.WriteFile(filepath.Join(dir, helperDiagnosticMarker), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		input, writer := io.Pipe()
		defer input.Close()
		defer writer.Close()
		cmd := exec.Command(binary, "--portico-storage-helper")
		cmd.Stdin = input
		supervisor := &storage.Supervisor{}
		var wire bytes.Buffer
		retired := make(chan struct{})
		err := supervisor.RunOwnedCompletion(ctx, "inventory-stream:child-diagnostic", cmd, func(reader io.Reader) error {
			defer writer.Close()
			encoder := json.NewEncoder(writer)
			request := struct {
				Operation string                   `json:"operation"`
				Inventory storage.InventoryRequest `json:"inventory"`
			}{"inventory-page", storage.InventoryRequest{Stream: true, Root: root, RelativePath: ".", Limit: 1}}
			if err := encoder.Encode(request); err != nil {
				return err
			}
			decoder := json.NewDecoder(io.TeeReader(reader, &wire))
			for turn, limit := range []int{1, 128} {
				if err := encoder.Encode(struct{ Limit int }{limit}); err != nil {
					return err
				}
				var page storage.InventoryPage
				if err := decoder.Decode(&page); err != nil {
					return err
				}
				if page.Complete != (turn == 1) || len(page.Entries) != 1 {
					return fmt.Errorf("unexpected retained frame %d: %+v", turn, page)
				}
				if turn == 0 && diagnostics {
					// Ask best-effort, but do not spend a 500ms polling window
					// inside this background helper's five-second fixture. macOS
					// proved that a bounded dump may be unavailable in that time.
					if err := os.WriteFile(filepath.Join(dir, helperDiagnosticMarker+".request"), nil, 0600); err != nil {
						return err
					}
				}
			}
			var extra any
			if err := decoder.Decode(&extra); err != io.EOF {
				return fmt.Errorf("terminal frame did not end at EOF: %v", err)
			}
			return nil
		}, nil, func() { close(retired) })
		if err != nil {
			t.Fatal(err)
		}
		<-retired
		if supervisor.Active() != 0 {
			t.Fatal("child did not physically retire")
		}
		if diagnostics {
			logStorageHelperDiagnostics(t, dir)
			for _, suffix := range []string{".init", ".started"} {
				marker, err := os.ReadFile(filepath.Join(dir, "helper-0"+suffix))
				if err != nil || !bytes.Contains(marker, []byte("go_package_init_ns=")) ||
					suffix == ".started" && bytes.Contains(marker, []byte("testmain_entry_ns=0")) {
					t.Fatal("completed child lost launch timestamps", suffix, err)
				}
			}
			if _, err := os.Stat(filepath.Join(dir, "helper-0.dump")); err != nil {
				t.Logf("background child dump unavailable (best-effort; not a transport failure): %v", err)
			}
		}
		return wire.Bytes()
	}
	plain, diagnosed := run(false), run(true)
	// Private retained-name cursors contain a fresh random stream ID. The
	// remaining frames, including the filename bytes, must be byte-identical.
	normalize := func(wire []byte) string {
		var pages []map[string]json.RawMessage
		decoder := json.NewDecoder(bytes.NewReader(wire))
		for {
			var page map[string]json.RawMessage
			err := decoder.Decode(&page)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			delete(page, "nextCursor")
			pages = append(pages, page)
		}
		data, err := json.Marshal(pages)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if normalize(plain) != normalize(diagnosed) || strings.Contains(string(diagnosed), "stage=") {
		t.Fatal("diagnostics changed the helper frames")
	}
}

func TestStorageHelperDiagnosticStackIsBounded(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "capture")
	captureHelperDiagnostic(prefix, &helperIOState{stage: "controlled_capture"})
	data, err := os.ReadFile(prefix + ".dump")
	if err != nil || len(data) > helperDiagnosticStack+1024 || !bytes.Contains(data, []byte("stage=controlled_capture")) || !bytes.Contains(data, []byte("TestStorageHelperDiagnosticStackIsBounded")) {
		t.Fatal("missing or unbounded direct stack capture", len(data), err)
	}
}

func TestHelperLaunchToolOutputIsCapped(t *testing.T) {
	var output boundedLaunchOutput
	input := bytes.Repeat([]byte("x"), 64<<10)
	if n, err := output.Write(input); err != nil || n != len(input) || len(output.data) != 4096 || !output.truncated {
		t.Fatal("launch metadata output cap failed", n, len(output.data), output.truncated, err)
	}
}
