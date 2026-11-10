package vod

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestInputCheckPreservesCancellationDuringValidation(t *testing.T) {
	file, err := os.Create(filepath.Join(t.TempDir(), "input"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	killed := errors.New("validator process killed")
	input := Input{File: file, Validate: func(context.Context, *os.File, Identity) error { cancel(); return killed }}
	if err := input.check(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want cancellation", err)
	}
	input.Validate = func(context.Context, *os.File, Identity) error { return ErrChanged }
	if err := input.check(context.Background()); !errors.Is(err, ErrChanged) {
		t.Fatalf("lost identity error: %v", err)
	}
}
