package storage

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func playbackFixture(t *testing.T) (*Client, string, os.FileInfo) {
	t.Helper()
	binary, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(t.TempDir(), "source.mp4")
	if e = os.WriteFile(p, bytes.Repeat([]byte("0123456789"), 10000), 0600); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(p)
	if e != nil {
		t.Fatal(e)
	}
	return New(binary), p, info
}
func waitReaderExit(t *testing.T, c *Client) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for c.Supervisor.Active() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond * 5)
	}
	if c.Supervisor.Active() != 0 {
		t.Fatal("helper not reaped")
	}
}
func TestPlaybackStreamActualBytesSeekAndReplacement(t *testing.T) {
	c, p, info := playbackFixture(t)
	f, e := c.OpenPlayback(context.Background(), p, info.Size(), info.ModTime().UnixNano())
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if _, e = f.Seek(7, io.SeekStart); e != nil {
		t.Fatal(e)
	}
	b := make([]byte, 16)
	if _, e = io.ReadFull(f, b); e != nil || string(b) != "7890123456789012" {
		t.Fatalf("range %q %v", b, e)
	}
	if _, e = f.Seek(-4, io.SeekEnd); e != nil {
		t.Fatal(e)
	}
	tail, e := io.ReadAll(f)
	if e != nil || string(tail) != "6789" {
		t.Fatalf("tail %q %v", tail, e)
	}
	q := p + ".replacement"
	if e = os.WriteFile(q, bytes.Repeat([]byte("x"), int(info.Size())), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Chtimes(q, info.ModTime(), info.ModTime()); e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(q, p); e != nil {
		t.Fatal(e)
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		t.Fatal(e)
	}
	if n, e := f.Read(b); n != 0 || e == nil {
		t.Fatal("replaced inode accepted")
	}
	f.Close()
	waitReaderExit(t, c)
}
func TestPlaybackDescriptorActualPinnedHandle(t *testing.T) {
	c, p, info := playbackFixture(t)
	f, e := c.OpenPlaybackDescriptor(context.Background(), p, info.Size(), info.ModTime().UnixNano())
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	b := make([]byte, 10)
	if _, e = io.ReadFull(f, b); e != nil || string(b) != "0123456789" {
		t.Fatalf("descriptor %q %v", b, e)
	}
	waitReaderExit(t, c)
	if _, e = c.OpenPlaybackDescriptor(context.Background(), p, info.Size()+1, info.ModTime().UnixNano()); e == nil {
		t.Fatal("wrong pin accepted")
	}
}
func TestPlaybackAdmissionAndCanceledReader(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c, p, info := playbackFixture(t)
	c.Supervisor.Limit = 1
	ctx, cancel := context.WithCancel(context.Background())
	f, e := c.OpenPlayback(ctx, p, info.Size(), info.ModTime().UnixNano())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.OpenPlayback(context.Background(), p, info.Size(), info.ModTime().UnixNano()); !errors.Is(e, ErrBusy) {
		t.Fatalf("admission %v", e)
	}
	cancel()
	f.Close()
	waitReaderExit(t, c)
	f, e = c.OpenPlayback(context.Background(), p, info.Size(), info.ModTime().UnixNano())
	if e != nil {
		t.Fatal(e)
	}
	f.Close()
	waitReaderExit(t, c)
}
func TestPlaybackProtocolBounds(t *testing.T) {
	var header [5]byte
	binary.BigEndian.PutUint32(header[1:], playbackBlock+1)
	if _, e := readPlaybackFrame(bytes.NewReader(header[:]), playbackBlock); e == nil {
		t.Fatal("oversized frame")
	}
	if _, e := readPlaybackFrame(strings.NewReader("x"), 10); e == nil {
		t.Fatal("truncated frame")
	}
}

func TestPlaybackStreamBadPinFailsWithoutWaitingForDeadline(t *testing.T) {
	c, p, info := playbackFixture(t)
	start := time.Now()
	if _, e := c.OpenPlayback(context.Background(), p, info.Size()+1, info.ModTime().UnixNano()); !errors.Is(e, ErrPlaybackSource) {
		t.Fatal(e)
	}
	if time.Since(start) > time.Second {
		t.Fatal("failed helper waited for deadline")
	}
	waitReaderExit(t, c)
}
