package storage

import (
	"context"
	"errors"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestNativeRetirementGraceIsExactlyFiveSeconds(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := &Supervisor{}
				r := newNativeRetirement(s)
				start := time.Now()
				done := make(chan error, 1)
				go func() { done <- r.wait(cause) }()
				synctest.Wait()
				time.Sleep(5*time.Second - time.Nanosecond)
				select {
				case err := <-done:
					t.Fatal("caller grace ended early", err)
				default:
				}
				time.Sleep(time.Nanosecond)
				synctest.Wait()
				err := <-done
				if time.Since(start) != 5*time.Second || !errors.Is(err, ErrHelperRetirementPending) || !errors.Is(err, cause) {
					t.Fatal("wrong caller grace or typed error", time.Since(start), err)
				}
				if stats := s.Stats(); stats.NativeRetiring != 1 || stats.NativeRetirementOverGrace != 1 {
					t.Fatal("missing bounded native metrics", stats)
				}
				r.retire()
				r.retire()
				if stats := s.Stats(); stats.NativeRetiring != 0 || stats.NativeRetirementOverGrace != 1 {
					t.Fatal("retirement was lost or double-refunded", stats)
				}
			})
		})
	}
}

type blockedNativeWarning struct {
	mu      sync.Mutex
	text    string
	calls   int
	release chan struct{}
}

func (w *blockedNativeWarning) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.text += string(p)
	w.calls++
	w.mu.Unlock()
	<-w.release
	return len(p), nil
}

func TestNativeRetirementWarningIsBoundedAndCannotDelayCaller(t *testing.T) {
	previous := log.Writer()
	sink := &blockedNativeWarning{}
	log.SetOutput(sink)
	defer log.SetOutput(previous)
	synctest.Test(t, func(t *testing.T) {
		// A channel created inside this bubble is durably blocked for synctest.
		sink.release = make(chan struct{})
		defer close(sink.release)
		s := &Supervisor{}
		r := newNativeRetirement(s)
		done := make(chan error, 1)
		go func() { done <- r.wait(context.Canceled) }()
		synctest.Wait()
		time.Sleep(5 * time.Second)
		synctest.Wait()
		if err := <-done; !errors.Is(err, ErrHelperRetirementPending) {
			t.Fatal(err)
		}
		other := newNativeRetirement(s)
		if !other.markPending() {
			t.Fatal("pending observation lost")
		}
		s.warnNativeRetirement()
		synctest.Wait()
		sink.mu.Lock()
		calls, text := sink.calls, sink.text
		sink.mu.Unlock()
		if calls != 1 || !strings.Contains(text, "warning: native storage helper retirement") || !s.nativeWarningBusy.Load() {
			t.Fatal("warnings were absent or unbounded", calls, text)
		}
		if stats := s.Stats(); stats.NativeRetiring != 2 || stats.NativeRetirementOverGrace != 2 {
			t.Fatal("coalesced warnings lost metric observations", stats)
		}
		r.retire()
		other.retire()
		if s.Stats().NativeRetiring != 0 {
			t.Fatal("blocked log sink blocked physical retirement")
		}
	})
}

func TestNativeInventoryStuckWaitBoundsCallerAndRetainsCustody(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("native Linux inventory operation")
	}
	for _, deadline := range []bool{false, true} {
		name := "cancel"
		if deadline {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "movie.mkv"), []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			s := &Supervisor{Limit: 1}
			entered, release := make(chan struct{}), make(chan struct{})
			var enteredOnce, releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			var waits atomic.Int64
			s.waitForTest = func(cmd *exec.Cmd) error {
				waits.Add(1)
				enteredOnce.Do(func() { close(entered) })
				<-release // deterministic stuck-Wait, never kernel D-state
				return cmd.Wait()
			}
			c := New(binary)
			c.Supervisor = s
			owner := NewOperationScope()
			ctx, cancel := context.WithCancel(t.Context())
			if deadline {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), time.Second)
			}
			defer func() {
				cancel()
				unblock()
				owner.Close()
				join, stop := context.WithTimeout(context.Background(), 3*time.Second)
				defer stop()
				if err := owner.Wait(join); err != nil {
					t.Error("fixture did not retire", err)
				}
			}()
			q := NativeInventoryRequest{Root: []byte(root), RelativePath: []byte("."), Limit: 1}
			type result struct {
				page NativeInventoryPage
				err  error
			}
			done := make(chan result, 1)
			go func() {
				page, err := c.NativeInventoryPage(ctx, owner, "stuck-original", q)
				done <- result{page, err}
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("real helper did not enter Wait fixture")
			}
			cancelledAt := time.Now()
			if deadline {
				cancelledAt, _ = ctx.Deadline()
				<-ctx.Done()
			} else {
				cancel()
			}
			select {
			case got := <-done:
				if !errors.Is(got.err, ErrHelperRetirementPending) || !errors.Is(got.err, ctx.Err()) || !reflect.DeepEqual(got.page, NativeInventoryPage{}) {
					t.Fatal("pending return lost typed cause or leaked output", got)
				}
				if time.Since(cancelledAt) < 5*time.Second {
					t.Fatal("caller skipped fixed grace")
				}
			case <-time.After(2 * nativeRetirementGrace):
				t.Fatal("caller hung behind stuck Wait")
			}
			if owner.Active() != 1 || s.Active() != 1 || owner.Context().Err() != nil {
				t.Fatal("caller return released custody or closed shared owner")
			}
			if stats := s.Stats(); stats.NativeRetiring != 1 || stats.NativeRetirementOverGrace != 1 || stats.Queued != 0 {
				t.Fatal("pending accounting", stats)
			}
			// Caller-owned slices may be reused after the zero-output return.
			q.Root[0], q.RelativePath[0] = 'x', 'x'
			q = NativeInventoryRequest{Root: []byte(root), RelativePath: []byte("."), Limit: 1}
			// Even a legacy background-policy pause cannot park a full-pool native
			// attempt. The native capacity reservation precedes that pause.
			dbwork.SetBackgroundPolicy(dbwork.SpeedNormal, time.Now().Add(time.Hour).UnixMilli())
			probe, stop := context.WithTimeout(t.Context(), time.Second)
			page, err := c.NativeInventoryPage(probe, owner, "another-key", q)
			stop()
			dbwork.SetBackgroundPolicy(dbwork.SpeedNormal, 0)
			if !errors.Is(err, ErrBusy) || !reflect.DeepEqual(page, NativeInventoryPage{}) || waits.Load() != 1 || owner.Active() != 1 || s.Active() != 1 {
				t.Fatal("full native pool queued, overlapped or lost custody", err, waits.Load())
			}
			// The unchanged legacy admission still waits at capacity until its own
			// context ends; it does not receive the new native pending error.
			legacy, stop := context.WithTimeout(t.Context(), 20*time.Millisecond)
			_, err = c.InventoryPage(legacy, "legacy-other", InventoryRequest{Root: root, RelativePath: ".", Limit: 1})
			stop()
			if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrHelperRetirementPending) || s.Stats().Queued != 1 {
				t.Fatal("legacy admission changed", err, s.Stats())
			}
			owner.Close()
			short, stop := context.WithTimeout(t.Context(), 10*time.Millisecond)
			if err := owner.Wait(short); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("scope acknowledged stuck physical work", err)
			}
			stop()
			unblock()
			join, stop := context.WithTimeout(t.Context(), 3*time.Second)
			if err := owner.Wait(join); err != nil {
				t.Fatal(err)
			}
			stop()
			if s.Active() != 0 || owner.Active() != 0 || s.Stats().NativeRetiring != 0 || s.Stats().NativeRetirementOverGrace != 1 {
				t.Fatal("actual reaping failed to release exact custody", s.Stats())
			}
		})
	}
}
