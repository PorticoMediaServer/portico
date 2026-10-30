//go:build rcloneintegration && linux

package rcloneprogram

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// The origin is generated scalar data, not an owner's file. This qualifies
// bounded reads with the real optional VFS cache, not production egress or
// actor authority. Equal metadata still supplies no immutable byte version.
func TestLockedCachedRangeDoesNotMaterializeWholeFile(t *testing.T) {
	program, digest := lockedServeProgram(t)
	const size = int64(64 << 20)
	modified := time.Unix(1700000000, 0).UTC()
	var gets, bytesWritten atomic.Int64
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><a href="large.mp4">large.mp4</a></html>`)
			return
		}
		if r.URL.Path != "/large.mp4" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("ETag", `"fixture-origin"`)
		if r.Method == http.MethodGet {
			gets.Add(1)
		}
		http.ServeContent(cacheCountingWriter{w, &bytesWritten}, r, "large.mp4", modified, &cachePatternFile{size: size})
	}))
	t.Cleanup(origin.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	spec, lease, _ := serveProcessFixture(t, "unused")
	spec.SourceID, spec.Remote = "cached-fixture", "media:"
	spec.Configuration = []byte("[media]\ntype=http\nurl=" + origin.URL + "/\n")
	spec.ApprovedProgramDigest, spec.MetadataPort = digest, backendFixturePort(t)
	spec.Connections, spec.RequestsPerSecond = 2, 50
	spec.CacheDirectory, spec.CacheBytes, spec.CacheFloor = t.TempDir(), 64<<20, 1<<30
	spec.Enforce = func(command *exec.Cmd) error {
		if command.Path != program || spec.Remote != "media:" {
			return ErrServeProgram
		}
		return nil // Only the exact controlled loopback origin is a fixture.
	}
	process, err := StartServe(ctx, Program{Path: program}, spec, lease)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		process.Stop()
		wait, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := process.Wait(wait); err != ErrServeStopped {
			t.Error("cached source did not physically retire", err)
		}
	})
	endpoint, err := process.Endpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := endpoint.Stat(ctx, "large.mp4", process.Check)
	if err != nil || entry.Size != size {
		t.Fatal("cached source stat", entry, err)
	}
	const offset = int64(33<<20 + 17)
	data, err := endpoint.ReadRange(ctx, "large.mp4", entry.ETag, size, offset, 257, process.Check)
	if err != nil || len(data) != 257 {
		t.Fatal("late cached extent", len(data), err)
	}
	for i, value := range data {
		if value != byte((offset+int64(i))%251) {
			t.Fatal("cached late extent differs", i)
		}
	}
	// Permit the controlled downloader to finish its already-open 1 MiB chunk.
	// This is fixture observation, never a fabricated provider retry deadline.
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if count := bytesWritten.Load(); count < int64(len(data)) || count > 2<<20 {
		t.Fatal("bounded cached range fetched more than header and extent chunks", count, size, gets.Load())
	}
	var allocated int64
	if err := filepath.WalkDir(spec.CacheDirectory, func(path string, item os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if item.Type().IsRegular() {
			info, err := item.Info()
			if err != nil {
				return err
			}
			allocated += info.Sys().(*syscall.Stat_t).Blocks * 512
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if allocated > (2<<20)+(64<<10) {
		t.Fatal("bounded cache materialized unrequested file data", allocated, size)
	}
	t.Logf("cached_range_bytes=%d origin_gets=%d origin_bytes=%d cache_allocated_bytes=%d source_size=%d", len(data), gets.Load(), bytesWritten.Load(), allocated, size)
}

type cacheCountingWriter struct {
	http.ResponseWriter
	bytes *atomic.Int64
}

func (w cacheCountingWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.bytes.Add(int64(n))
	return n, err
}

type cachePatternFile struct{ size, offset int64 }

func (f *cachePatternFile) Read(p []byte) (int, error) {
	if f.offset >= f.size {
		return 0, io.EOF
	}
	n := int(min(int64(len(p)), f.size-f.offset))
	for i := range p[:n] {
		p[i] = byte((f.offset + int64(i)) % 251)
	}
	f.offset += int64(n)
	return n, nil
}

func (f *cachePatternFile) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += f.offset
	case io.SeekEnd:
		offset += f.size
	default:
		return 0, fmt.Errorf("invalid fixture seek")
	}
	if offset < 0 {
		return 0, fmt.Errorf("negative fixture seek")
	}
	f.offset = offset
	return offset, nil
}
