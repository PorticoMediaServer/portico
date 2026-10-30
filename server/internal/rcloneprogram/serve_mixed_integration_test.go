//go:build rcloneintegration && linux

package rcloneprogram

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
)

// Every native fixture runs the same scan, two different-file viewers, two
// same-file viewers and reader, plus both separate metadata transports. This
// measures shared operation admission; identical callers alone do not prove
// that VFS coalesces their upstream reads.
func exerciseMixedSourceBudget(t *testing.T, ctx context.Context, process *ServeProcess, payload []byte, modified time.Time) {
	t.Helper()
	endpoint, err := process.Endpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tags := make(map[string]string)
	for _, name := range []string{"one.mkv", "two.mkv", "book.bin"} {
		entry, err := endpoint.Stat(ctx, name, process.Check)
		if err != nil || entry.Size != int64(len(payload)) {
			t.Fatal("SFTP served facts", name, err)
		}
		tags[name] = entry.ETag
	}
	metadata, err := process.Metadata(ctx)
	if err != nil {
		t.Fatal(err)
	}
	observer, err := process.ObservationEndpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	read := func(name string) error {
		got, err := endpoint.ReadRange(ctx, name, tags[name], int64(len(payload)), 1500001, 127, process.Check)
		if err == nil && !bytes.Equal(got, payload[1500001:1500128]) {
			err = fmt.Errorf("SFTP late extent differs")
		}
		return err
	}
	load := []func() error{
		func() error {
			fresh, err := metadata.Stat(ctx, "one.mkv", process.Check)
			if err == nil && (fresh.Size != int64(len(payload)) || !fresh.Modified.Equal(modified) || len(fresh.Hashes) != 0) {
				err = fmt.Errorf("fresh SFTP metadata differs")
			}
			return err
		},
		func() error {
			entry, err := observer.Stat(ctx, "two.mkv", process.Check)
			if err == nil && entry.Size != int64(len(payload)) {
				err = fmt.Errorf("SFTP observer facts differ")
			}
			return err
		},
		func() error {
			return endpoint.VisitSnapshot(ctx, "", process.Check, func(remotemedia.DAVEntry) error { return nil })
		},
		func() error { return read("one.mkv") },
		func() error { return read("two.mkv") },
		func() error { return read("one.mkv") },
		func() error { return read("one.mkv") },
		func() error {
			body, err := endpoint.OpenFromStart(ctx, "book.bin", tags["book.bin"], int64(len(payload)), process.Check)
			if err != nil {
				return err
			}
			defer body.Close()
			var prefix [64]byte
			_, err = io.ReadFull(body, prefix[:])
			if err == nil && !bytes.Equal(prefix[:], payload[:64]) {
				err = fmt.Errorf("SFTP progressive prefix differs")
			}
			return err
		},
	}
	var done sync.WaitGroup
	for _, call := range load {
		done.Go(func() {
			<-start
			if err := call(); err != nil {
				t.Error("mixed SFTP operation", err)
			}
		})
	}
	close(start)
	done.Wait()
	counts := process.OperationCounts()
	if counts.Capacity != 2 || counts.Active != 0 || counts.Peak != 2 {
		t.Fatal("mixed backend source budget", counts)
	}
}
