package mediaartifact

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

type artifactOpenResult struct {
	reader *Reader
	err    error
}

func awaitArtifactOpen(t *testing.T, result <-chan artifactOpenResult) artifactOpenResult {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("unrelated artifact operation waited behind verification")
		return artifactOpenResult{}
	}
}

func TestArtifactVerificationReservesOnlyItsOriginalObject(t *testing.T) {
	s := fixture(t)
	original, other := seal(t, s, "original"), seal(t, s, "other")
	started, unblock := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(unblock) }) }
	defer release()
	result := make(chan artifactOpenResult, 1)
	go func() {
		r, err := s.open(context.Background(), original, func(ctx context.Context, path string, o Object) (*os.File, error) {
			close(started)
			<-unblock
			return openVerified(ctx, path, o)
		})
		result <- artifactOpenResult{r, err}
	}()
	<-started
	independent := make(chan artifactOpenResult, 1)
	go func() {
		r, err := s.Open(context.Background(), other)
		if err == nil {
			err = r.Close()
		}
		if err == nil {
			err = s.Remove(other)
		}
		if err == nil {
			var w *Writer
			w, err = s.Begin(128)
			if err == nil {
				defer w.Abort()
				_, err = w.Write([]byte("created during verification"))
				if err == nil {
					_, err = w.Seal(context.Background())
				}
			}
		}
		independent <- artifactOpenResult{nil, err}
	}()
	if got := awaitArtifactOpen(t, independent); got.err != nil {
		t.Fatal(got.err)
	}
	if err := s.Remove(original); !errors.Is(err, ErrLeased) {
		t.Fatalf("pending original was not reserved: %v", err)
	}
	release()
	got := awaitArtifactOpen(t, result)
	if got.err != nil {
		t.Fatal(got.err)
	}
	defer got.reader.Close()
	bytes, err := io.ReadAll(got.reader)
	if err != nil || string(bytes) != "original" {
		t.Fatalf("original descriptor bytes %q: %v", bytes, err)
	}
	if err = s.Remove(original); !errors.Is(err, ErrLeased) {
		t.Fatalf("verified original lost reservation: %v", err)
	}
	if err = got.reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err = got.reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err = s.Remove(original); err != nil {
		t.Fatalf("last close did not release exactly once: %v", err)
	}
}

func TestArtifactCloseWaitsForAdmittedVerification(t *testing.T) {
	s := fixture(t)
	original := seal(t, s, "survives close")
	started, unblock := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(unblock) }) }
	defer release()
	result := make(chan artifactOpenResult, 1)
	go func() {
		r, err := s.open(context.Background(), original, func(ctx context.Context, path string, o Object) (*os.File, error) {
			close(started)
			<-unblock
			return openVerified(ctx, path, o)
		})
		result <- artifactOpenResult{r, err}
	}()
	<-started
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		stopped := s.closed
		s.mu.Unlock()
		if stopped {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Close failed to close admission")
		}
		runtime.Gosched()
	}
	select {
	case err := <-closed:
		t.Fatalf("Close passed pending verification: %v", err)
	default:
	}
	if _, err := s.Open(context.Background(), original); !errors.Is(err, ErrState) {
		t.Fatalf("new open after Close admission: %v", err)
	}
	release()
	got := awaitArtifactOpen(t, result)
	if got.err != nil {
		t.Fatal(got.err)
	}
	defer got.reader.Close()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not finish after verification")
	}
	bytes, err := io.ReadAll(got.reader)
	if err != nil || string(bytes) != "survives close" {
		t.Fatalf("admitted reader did not outlive store: %q %v", bytes, err)
	}
}

func TestArtifactVerificationFailureReleasesReservationAndStillHashesEveryOpen(t *testing.T) {
	s := fixture(t)
	original := seal(t, s, "immutable")
	r, err := s.Open(context.Background(), original)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.Open(cancelled, original); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled verification: %v", err)
	}
	s.mu.Lock()
	readers := s.readers[original.Digest]
	s.mu.Unlock()
	if readers != 0 {
		t.Fatalf("cancelled reservation retained: %d", readers)
	}
	// Same path, inode and byte count are insufficient: every later Open still
	// verifies the complete digest, including an object verified successfully above.
	path := filepath.Join(s.objects, original.Digest)
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("corrupted"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Open(context.Background(), original); !errors.Is(err, ErrIdentity) {
		t.Fatalf("same-size tampering accepted after prior verification: %v", err)
	}
	s.mu.Lock()
	readers = s.readers[original.Digest]
	s.mu.Unlock()
	if readers != 0 {
		t.Fatalf("failed verification reservation retained: %d", readers)
	}
	if err = s.Remove(original); err != nil {
		t.Fatalf("failed original could not retire: %v", err)
	}
	if _, err = s.Open(context.Background(), original); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing verification: %v", err)
	}
	s.mu.Lock()
	readers = s.readers[original.Digest]
	s.mu.Unlock()
	if readers != 0 {
		t.Fatalf("missing verification reservation retained: %d", readers)
	}
}

func TestArtifactVerificationPanicReleasesPendingReservation(t *testing.T) {
	s := fixture(t)
	original := seal(t, s, "panic cleanup")
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("verifier did not panic")
			}
		}()
		_, _ = s.open(context.Background(), original, func(context.Context, string, Object) (*os.File, error) { panic("injected verifier panic") })
	}()
	if err := s.Remove(original); err != nil {
		t.Fatalf("panicked verification stranded original reservation: %v", err)
	}
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("panicked verification stranded shutdown")
	}
}

func TestArtifactCloseWaitsForFailedVerification(t *testing.T) {
	s := fixture(t)
	original := seal(t, s, "cancel during shutdown")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	result := make(chan artifactOpenResult, 1)
	go func() {
		r, err := s.open(ctx, original, func(ctx context.Context, _ string, _ Object) (*os.File, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		})
		result <- artifactOpenResult{r, err}
	}()
	<-started
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		stopped := s.closed
		s.mu.Unlock()
		if stopped {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Close failed to close admission")
		}
		runtime.Gosched()
	}
	select {
	case err := <-closed:
		t.Fatalf("Close passed pending failing verifier: %v", err)
	default:
	}
	cancel()
	if got := awaitArtifactOpen(t, result); got.reader != nil || !errors.Is(got.err, context.Canceled) {
		t.Fatalf("cancelled original: %+v", got)
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("failed verification stranded Close")
	}
	s.mu.Lock()
	readers := s.readers[original.Digest]
	s.mu.Unlock()
	if readers != 0 {
		t.Fatalf("failed verification retained %d readers", readers)
	}
}
