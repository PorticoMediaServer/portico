//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
)

func TestInstanceLockStartupFailuresKeepStateClosed(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"unavailable", "silent"} {
		t.Run(mode, func(t *testing.T) {
			root, err := os.MkdirTemp("", "lock-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(root) })
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, "-test.run=^TestInstanceLockStartupFailureHelper$", "-test.v")
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "PORTICO_") {
					cmd.Env = append(cmd.Env, entry)
				}
			}
			cmd.Env = append(cmd.Env, "PORTICO_LOCK_FAILURE_HELPER="+mode, "PORTICO_DATA_DIR="+filepath.Join(root, "state"), fmt.Sprintf("PORTICO_BIND=127.0.0.1:%d", freePort(t)), "PORTICO_DISCOVERY=0")
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("isolated %s lock startup: %v\n%s", mode, err, output)
			}
		})
	}
}

func TestInstanceLockStartupFailureHelper(t *testing.T) {
	mode := os.Getenv("PORTICO_LOCK_FAILURE_HELPER")
	if mode == "" {
		t.Skip("isolated subprocess only")
	}
	state := os.Getenv("PORTICO_DATA_DIR")
	layout := storagepaths.At(state)
	if err := layout.Prepare(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{layout.Temp, layout.Cache} {
		if err := os.WriteFile(filepath.Join(dir, "active-work"), []byte("preserve"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := layout.RequestCacheEmpty(); err != nil {
		t.Fatal(err)
	}
	var phases []string
	startupPhaseHook = func(phase string) { phases = append(phases, phase) }
	checked := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		<-started
		defer func() {
			if process, err := os.FindProcess(os.Getpid()); err == nil {
				_ = process.Signal(os.Interrupt)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		client := &http.Client{Timeout: 2 * time.Second}
		for {
			req, err := http.NewRequestWithContext(ctx, "GET", "http://"+os.Getenv("PORTICO_BIND")+"/v1/readiness", nil)
			if err != nil {
				checked <- err
				return
			}
			response, err := client.Do(req)
			if err != nil {
				checked <- err
				return
			}
			var body struct{ Status, Message, Phase string }
			err = json.NewDecoder(response.Body).Decode(&body)
			response.Body.Close()
			if err != nil {
				checked <- err
				return
			}
			if body.Status == "starting" {
				continue
			}
			if response.StatusCode != 503 || body.Status != "startup_failed" || !strings.Contains(body.Message, state) || !strings.Contains(body.Message, instanceLockStorageAdvice) || !strings.Contains(body.Message, errInstanceLockUnsupported.Error()) {
				checked <- fmt.Errorf("recovery diagnostic: status=%d body=%+v", response.StatusCode, body)
				return
			}
			req, err = http.NewRequestWithContext(ctx, "GET", "http://"+os.Getenv("PORTICO_BIND")+"/", nil)
			if err != nil {
				checked <- err
				return
			}
			req.Header.Set("Accept", "text/html")
			response, err = client.Do(req)
			if err != nil {
				checked <- err
				return
			}
			raw, err := io.ReadAll(response.Body)
			response.Body.Close()
			// The HTML template escapes apostrophes, so check the operator's path and
			// the storage advice's stable text rather than comparing escaped markup.
			if err != nil || response.StatusCode != 503 || !strings.Contains(string(raw), state) || !strings.Contains(string(raw), "working file locks") || !strings.Contains(string(raw), "Media libraries can stay on network storage") {
				checked <- fmt.Errorf("HTML recovery diagnostic: status=%d error=%v body=%s", response.StatusCode, err, raw)
				return
			}
			checked <- nil
			return
		}
	}()
	err := runWithInstanceLock(func(path string) (*instanceLock, error) {
		lock, err := acquireInstanceLockWith(path, func(*os.File) error {
			if mode == "silent" {
				return nil
			}
			return errInstanceLockUnsupported
		})
		close(started)
		return lock, err
	})
	var lockFailure *instanceLockFailure
	if !errors.As(err, &lockFailure) || !errors.Is(err, errInstanceLockUnsupported) {
		t.Fatalf("startup did not preserve lock failure: %v", err)
	}
	if err := <-checked; err != nil {
		t.Fatal(err)
	}
	for _, phase := range phases {
		if phase != "listener" && phase != "instance" {
			t.Fatalf("startup proceeded past lock into %q", phase)
		}
	}
	if len(phases) != 2 {
		t.Fatalf("startup phases %v", phases)
	}
	if _, err := os.Stat(storagepaths.Path(state, "server.sqlite")); !os.IsNotExist(err) {
		t.Fatalf("database was opened: %v", err)
	}
	keys, err := os.ReadDir(layout.Keys)
	if err != nil || len(keys) != 0 {
		t.Fatalf("authority opened before exclusive ownership: keys=%d error=%v", len(keys), err)
	}
	for _, dir := range []string{layout.Temp, layout.Cache} {
		raw, err := os.ReadFile(filepath.Join(dir, "active-work"))
		if err != nil || string(raw) != "preserve" {
			t.Fatalf("unowned startup changed %s: %v", dir, err)
		}
	}
	// The existing log includes the same actionable diagnostic as the page.
	logs, err := os.ReadDir(layout.Logs)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range logs {
		if !entry.IsDir() {
			raw, err := os.ReadFile(filepath.Join(layout.Logs, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), state) && strings.Contains(string(raw), instanceLockStorageAdvice) {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("startup log omitted actionable locking diagnostic")
	}
}
