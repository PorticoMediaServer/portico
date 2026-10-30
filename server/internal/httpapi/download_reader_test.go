package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

type downloadTestSource struct {
	*bytes.Reader
	reads, closes int
	afterRead     func()
}

func (s *downloadTestSource) Read(p []byte) (int, error) {
	s.reads++
	n, err := s.Reader.Read(p)
	if s.afterRead != nil {
		s.afterRead()
	}
	return n, err
}

func (s *downloadTestSource) Close() error { s.closes++; return nil }

func TestDownloadReaderCachesAuthorityAndPreservesSeeks(t *testing.T) {
	source := &downloadTestSource{Reader: bytes.NewReader(bytes.Repeat([]byte("01234567"), 16384))}
	calls := 0
	r := newDownloadReader(context.Background(), source, func() error { calls++; return nil })
	defer r.Close()
	buf := make([]byte, 128*1024)
	if n, err := r.Read(buf); n != 32*1024 || err != nil {
		t.Fatalf("bounded read = %d, %v", n, err)
	}
	if pos, err := r.Seek(7, io.SeekStart); pos != 7 || err != nil {
		t.Fatalf("seek = %d, %v", pos, err)
	}
	for i := 0; i < 100; i++ {
		if n, err := r.Read(buf[:1]); n != 1 || err != nil || buf[0] != "70123456"[i%8] {
			t.Fatalf("tiny read %d = %d, %v, %q", i, n, err, buf[:1])
		}
	}
	if calls != 1 {
		t.Fatalf("quiet transfer checked authority %d times", calls)
	}
	dbwork.BumpAuthority()
	if _, err := r.Read(buf[:1]); err != nil || calls != 2 {
		t.Fatalf("generation invalidation: calls=%d, err=%v", calls, err)
	}
}

func TestDownloadReaderClosesOnAuthorityLossOrCancellation(t *testing.T) {
	for _, duringRead := range []bool{false, true} {
		for _, cancelRequest := range []bool{false, true} {
			t.Run(downloadReaderCaseName(duringRead, cancelRequest), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				source := &downloadTestSource{Reader: bytes.NewReader([]byte("private bytes"))}
				denied := errors.New("revoked")
				verdict := error(nil)
				r := newDownloadReader(ctx, source, func() error { return verdict })
				buf := make([]byte, 1)
				if n, err := r.Read(buf); n != 1 || err != nil {
					t.Fatalf("initial read %d, %v", n, err)
				}
				withdraw := func() {
					if cancelRequest {
						cancel()
					} else {
						verdict = denied
						dbwork.BumpAuthority()
					}
				}
				if duringRead {
					source.afterRead = withdraw
				} else {
					withdraw()
				}
				want := denied
				if cancelRequest {
					want = context.Canceled
				}
				if n, err := r.Read(buf); n != 0 || !errors.Is(err, want) {
					t.Fatalf("withdrawn read %d, %v; want %v", n, err, want)
				}
				reads := 1
				if duringRead {
					reads++
				}
				if source.reads != reads || source.closes != 1 {
					t.Fatalf("source reads=%d closes=%d", source.reads, source.closes)
				}
				_ = r.Close()
				if source.closes != 1 {
					t.Fatal("handler cleanup closed the source twice")
				}
			})
		}
	}
}

func downloadReaderCaseName(duringRead, cancelRequest bool) string {
	when, cause := "between_reads", "revocation"
	if duringRead {
		when = "during_read"
	}
	if cancelRequest {
		cause = "cancellation"
	}
	return when + "/" + cause
}

func TestDownloadReaderRechecksTimeBasedAuthority(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	source := &downloadTestSource{Reader: bytes.NewReader([]byte("private bytes"))}
	denied := errors.New("expired")
	verdict := error(nil)
	r := newDownloadReader(context.Background(), source, func() error { return verdict })
	defer r.Close()
	if _, err := r.Read(make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	verdict = denied
	// Expiry need not commit a write. An expired successful verdict must not
	// allow a long transfer to keep reading indefinitely.
	time.Sleep(streamVerdictLife + 10*time.Millisecond)
	if n, err := r.Read(make([]byte, 1)); n != 0 || !errors.Is(err, denied) || source.closes != 1 {
		t.Fatalf("expired read %d, %v; closes=%d", n, err, source.closes)
	}
}
