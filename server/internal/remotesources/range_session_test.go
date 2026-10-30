package remotesources

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func scannedRangeObservation() remotemedia.FileObservation {
	return remotemedia.FileObservation{SourceID: "source", Incarnation: "root-1", Root: "disc", Object: "BDMV/clip", Size: 100, ModifiedNS: 123, ModifiedKnown: true, ProviderHashes: []remotemedia.ProviderHash{{Kind: "md5", Value: "opaque"}}, ProviderETag: `"provider"`, ServeETag: `"served"`}
}

func TestRangeSessionDiscardsPostReadChangeAndNeverReconnects(t *testing.T) {
	for _, field := range []string{"source", "incarnation", "root", "object", "size", "mtime", "hash", "provider_etag", "served_etag"} {
		t.Run(field, func(t *testing.T) {
			scan := scannedRangeObservation()
			current := scan
			current.ProviderHashes = append([]remotemedia.ProviderHash(nil), scan.ProviderHashes...)
			reads, notices := 0, 0
			s := &RangeSession{admission: RangeAdmission{Scan: scan, Check: func(context.Context) error { return nil }, Observe: func(context.Context) (remotemedia.FileObservation, error) { return current, nil }, OnChanged: func() { notices++ }}}
			s.readRange = func(context.Context, string, string, int64, int64, int64) ([]byte, error) {
				reads++
				switch field {
				case "source":
					current.SourceID = "another"
				case "incarnation":
					current.Incarnation = "root-2"
				case "root":
					current.Root = "another"
				case "object":
					current.Object = "another"
				case "size":
					current.Size++
				case "mtime":
					current.ModifiedNS++
				case "hash":
					current.ProviderHashes[0].Value = "new"
				case "provider_etag":
					current.ProviderETag = `"new"`
				case "served_etag":
					current.ServeETag = `"new"`
				}
				return []byte("new!"), nil
			}
			p := []byte("----")
			for range 2 {
				n, err := s.ReadAt(context.Background(), p, 90)
				if n != 0 || !errors.Is(err, storage.ErrRemoteChanged) || string(p) != "----" {
					t.Fatalf("exposed changed bytes: %d %q %v", n, p, err)
				}
			}
			if reads != 1 || notices != 1 {
				t.Fatalf("reads=%d notices=%d", reads, notices)
			}
		})
	}
}

func TestRangeSessionAuthorityAndFreshObservationSurroundRead(t *testing.T) {
	scan := scannedRangeObservation()
	events := []string{}
	s := &RangeSession{admission: RangeAdmission{Scan: scan, Check: func(context.Context) error { events = append(events, "authority"); return nil }, Observe: func(context.Context) (remotemedia.FileObservation, error) {
		events = append(events, "observe")
		return scan, nil
	}, OnChanged: func() {}}}
	s.readRange = func(_ context.Context, relative, tag string, size, offset, length int64) ([]byte, error) {
		events = append(events, "read")
		if relative != "disc/BDMV/clip" || tag != scan.ServeETag || size != 100 || offset != 90 || length != 4 {
			t.Fatal("wrong member or extent")
		}
		return []byte("old!"), nil
	}
	p := make([]byte, 4)
	if n, err := s.ReadAt(context.Background(), p, 90); err != nil || n != 4 || string(p) != "old!" {
		t.Fatalf("%d %q %v", n, p, err)
	}
	if strings.Join(events, ",") != "authority,observe,authority,read,authority,observe,authority" {
		t.Fatal(events)
	}
}

func TestRangeSessionMissingAuthorityAndReadFailure(t *testing.T) {
	if _, err := OpenRangeSession(context.Background(), nil, RangeAdmission{}); !errors.Is(err, storage.ErrRemoteConfig) {
		t.Fatal(err)
	}
	scan := scannedRangeObservation()
	reads := 0
	s := &RangeSession{admission: RangeAdmission{Scan: scan, Check: func(context.Context) error { return nil }, Observe: func(context.Context) (remotemedia.FileObservation, error) { return scan, nil }, OnChanged: func() {}}}
	s.readRange = func(context.Context, string, string, int64, int64, int64) ([]byte, error) {
		reads++
		return nil, remotemedia.ErrStalled
	}
	for range 2 {
		if n, err := s.ReadAt(context.Background(), make([]byte, 4), 0); n != 0 || !errors.Is(err, remotemedia.ErrStalled) {
			t.Fatal(n, err)
		}
	}
	if reads != 1 {
		t.Fatal("retried failed session")
	}
}

func TestRangeSessionCloseCancelsInFlightReadWithoutPublishingBytes(t *testing.T) {
	scan := scannedRangeObservation()
	life, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	s := &RangeSession{life: life, cancel: cancel, admission: RangeAdmission{Scan: scan, Check: func(context.Context) error { return nil }, Observe: func(context.Context) (remotemedia.FileObservation, error) { return scan, nil }, OnChanged: func() {}}}
	reads := 0
	s.readRange = func(ctx context.Context, _ string, _ string, _, _, _ int64) ([]byte, error) {
		reads++
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)
	buffer := []byte("----")
	go func() {
		n, err := s.ReadAt(context.Background(), buffer, 0)
		done <- result{n, err}
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("read never started")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.n != 0 || got.err == nil || string(buffer) != "----" {
			t.Fatal("closing exposed incomplete bytes", got, string(buffer))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("closing did not cancel the active read")
	}
	if n, err := s.ReadAt(context.Background(), buffer, 0); n != 0 || err == nil || reads != 1 {
		t.Fatal("closed acquisition reopened", n, err, reads)
	}
}
