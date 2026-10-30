//go:build !windows

package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestValidatePlaybackDescriptorUsesPinnedFile(t *testing.T) {
	c, p, info := playbackFixture(t)
	f, err := c.OpenPlaybackDescriptor(context.Background(), p, info.Size(), info.ModTime().UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	waitReaderExit(t, c)
	if _, err = f.Seek(7, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	// Removing the name proves validation cannot depend on reopening its path.
	if err = os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err = c.ValidatePlaybackDescriptor(context.Background(), f, info.Size(), info.ModTime().UnixNano()); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 3)
	if _, err = io.ReadFull(f, b); err != nil || string(b) != "789" {
		t.Fatalf("caller descriptor/offset changed: %q %v", b, err)
	}
	for _, pin := range [][2]int64{{info.Size() + 1, info.ModTime().UnixNano()}, {info.Size(), info.ModTime().UnixNano() + 1}} {
		if err = c.ValidatePlaybackDescriptor(context.Background(), f, pin[0], pin[1]); !errors.Is(err, ErrPlaybackSource) {
			t.Fatalf("mismatched identity: %v", err)
		}
	}
	waitReaderExit(t, c)
}

func TestValidatePlaybackDescriptorRejectsMutationAndInvalidFiles(t *testing.T) {
	c, p, info := playbackFixture(t)
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = os.Truncate(p, 1); err != nil {
		t.Fatal(err)
	}
	if err = c.ValidatePlaybackDescriptor(context.Background(), f, info.Size(), info.ModTime().UnixNano()); !errors.Is(err, ErrPlaybackSource) {
		t.Fatal(err)
	}
	d, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err = c.ValidatePlaybackDescriptor(context.Background(), d, 0, 0); !errors.Is(err, ErrPlaybackSource) {
		t.Fatal(err)
	}
	if err = c.ValidatePlaybackDescriptor(context.Background(), nil, 0, 0); !errors.Is(err, ErrPlaybackSource) {
		t.Fatal(err)
	}
	f.Close()
	if err = c.ValidatePlaybackDescriptor(context.Background(), f, info.Size(), info.ModTime().UnixNano()); !errors.Is(err, ErrPlaybackSource) {
		t.Fatal(err)
	}
	waitReaderExit(t, c)
}

func TestValidatePlaybackDescriptorAdmissionAndCancellation(t *testing.T) {
	c, p, info := playbackFixture(t)
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c.Supervisor.Limit = 1
	ctx, cancel := context.WithCancel(context.Background())
	reader, err := c.OpenPlayback(ctx, p, info.Size(), info.ModTime().UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	if err = c.ValidatePlaybackDescriptor(context.Background(), f, info.Size(), info.ModTime().UnixNano()); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	cancel()
	reader.Close()
	waitReaderExit(t, c)
	if err = c.ValidatePlaybackDescriptor(ctx, f, info.Size(), info.ModTime().UnixNano()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "blocked-validator")
	if err = os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	c.Binary = script
	c.Timeout = 40 * time.Millisecond
	started := time.Now()
	if err = c.ValidatePlaybackDescriptor(context.Background(), f, info.Size(), info.ModTime().UnixNano()); !errors.Is(err, ErrPlaybackTimeout) {
		t.Fatal(err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("validation deadline did not bound caller")
	}
	waitReaderExit(t, c)
}
