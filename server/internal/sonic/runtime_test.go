package sonic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestMain(m *testing.M) {
	if handled, err := mediaexec.RunHelper(os.Args[1:]); handled {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestRuntimeNeverPublishesUnknownModelOrMissingInstallation(t *testing.T) {
	directory := t.TempDir()
	for _, manifest := range []string{`{}`, `{"id":"musicnn-mtt","version":"unverified"}`, `{"id":"essentia-noncommercial"}`} {
		if err := os.WriteFile(filepath.Join(directory, "model.json"), []byte(manifest), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenRuntime(RuntimeOptions{Helper: filepath.Join(directory, "missing"), ModelDirectory: directory, Supervisor: &storage.Supervisor{}}); !errors.Is(err, ErrUnavailable) {
			t.Fatal("unknown model available", err)
		}
	}
	var absent *Runtime
	if state := absent.Availability(); state.State != "unavailable" || state.Model != nil {
		t.Fatal(state)
	}
}

func actualRuntime(t *testing.T) (*Runtime, *storage.Supervisor) {
	t.Helper()
	helper, model := os.Getenv("PORTICO_SONIC_HELPER_TEST"), os.Getenv("PORTICO_SONIC_MODEL_TEST")
	if helper == "" || model == "" {
		t.Skip("actual checked ONNX model/helper are supplied by the runtime conformance job")
	}
	sup := &storage.Supervisor{Limit: 1}
	if _, err := mediaexec.Configure(mediaexec.Options{StateDir: t.TempDir(), SkipOrphanKill: true}); err != nil {
		t.Fatal(err)
	}
	r, err := OpenRuntime(RuntimeOptions{Helper: helper, ModelDirectory: model, Supervisor: sup})
	if err != nil {
		t.Fatal(err)
	}
	return r, sup
}

func TestRuntimeActualCPUAndCurrentVerifiedModel(t *testing.T) {
	r, sup := actualRuntime(t)
	if r.Availability().State != "unavailable" {
		t.Fatal("install metadata fabricated inference availability")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := r.Run(ctx, "actual-cpu", nil, func(yield func([]float32) error) error {
		_, e := LogMel(bytes.NewReader(pcm(97000, 440)), 97000, yield)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Model.ID != ModelID || result.Model.Version != ModelVersion || result.Model.SHA256 != ModelSHA256 || result.Patches != 2 || !result.Embedding.valid() || r.Availability().State != "ready" || sup.Active() != 0 {
		t.Fatalf("unbound actual result: %+v %v", result, r.Availability())
	}
	for _, score := range result.Tags {
		if score < 0 || score > 1 {
			t.Fatal("invalid actual tag", score)
		}
	}
}

func TestRuntimeActualCancellationRetiresHelperAndInput(t *testing.T) {
	r, sup := actualRuntime(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := r.Run(ctx, "cancel", nil, func(yield func([]float32) error) error {
			if err := yield(make([]float32, PatchFrames*MelBands)); err != nil {
				return err
			}
			close(started)
			<-ctx.Done()
			return ctx.Err()
		})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(15 * time.Second):
		t.Fatal("actual helper did not accept its input")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel left an actual model/helper/input goroutine alive")
	}
	if sup.Active() != 0 || r.Availability().State != "unavailable" {
		t.Fatal("cancel released authority before retirement or retained ready state")
	}
}
