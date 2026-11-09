package disc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"testing"
	"time"
)

func remoteTitleFixture(t *testing.T, payload []byte, guard func(context.Context) error, waitErr error) *remoteTitleStream {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	// Only a short synthetic payload is placed in the pipe before consumption.
	if _, err = writer.Write(payload); err != nil {
		reader.Close()
		writer.Close()
		t.Fatal(err)
	}
	writer.Close()
	done := make(chan struct{})
	close(done)
	s := &remoteTitleStream{ctx: context.Background(), file: reader, guard: guard, stop: func() {}, done: done, waitErr: waitErr}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestRemoteTitleStreamNeverReportsFailedProducerAsSuccessfulEOF(t *testing.T) {
	s := remoteTitleFixture(t, nil, func(context.Context) error { return nil }, errors.New("producer failed"))
	_, err := s.Read(make([]byte, 10))
	if !errors.Is(err, ErrProtocol) || errors.Is(err, io.EOF) {
		t.Fatal("a failed producer became a successful end-of-title")
	}
}

func TestRemoteTitleStreamRetirementDoesNotCopyBufferedBytes(t *testing.T) {
	retired, calls := errors.New("original caller retired"), 0
	s := remoteTitleFixture(t, []byte("media"), func(context.Context) error {
		calls++
		if calls > 1 {
			return retired
		}
		return nil
	}, nil)
	out := []byte("xxxxx")
	n, err := s.Read(out)
	if n != 0 || !errors.Is(err, retired) || !bytes.Equal(out, []byte("xxxxx")) {
		t.Fatal("retired buffered media reached the consumer")
	}
}

func TestRemoteTitleStreamPreservesSuccessfulTailThenEOF(t *testing.T) {
	s := remoteTitleFixture(t, []byte("media"), func(context.Context) error { return nil }, nil)
	out, err := io.ReadAll(s)
	if err != nil || !bytes.Equal(out, []byte("media")) {
		t.Fatal("a successful source tail was lost")
	}
}

func TestRemoteTitleStreamRequiresOriginalAdmission(t *testing.T) {
	_, _, err := (Tool{Binary: "/must-not-launch"}).StreamRemoteBluRay(context.Background(), nil, Probe{}, Title{}, 0, nil)
	if !errors.Is(err, ErrRemoteUnqualified) {
		t.Fatal("missing original admission was not refused")
	}
}

type heldRemoteTitleRead struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	closed  bool
}

func (r *heldRemoteTitleRead) Read(p []byte) (int, error) {
	close(r.entered)
	<-r.release
	return copy(p, "media"), nil
}

func (r *heldRemoteTitleRead) unblock() { r.once.Do(func() { close(r.release) }) }
func (r *heldRemoteTitleRead) Close() error {
	r.closed = true
	r.unblock()
	return nil
}

func TestRemoteTitleStreamRefusesFailureArrivingDuringHeldRead(t *testing.T) {
	input := &heldRemoteTitleRead{entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan struct{})
	stops := 0
	s := &remoteTitleStream{ctx: context.Background(), file: input, guard: func(context.Context) error { return nil },
		done: done, stop: func() { stops++ }}
	var completion sync.Once
	fail := func() {
		completion.Do(func() {
			s.waitErr = errors.New("producer or final source observation failed")
			close(done)
		})
	}
	t.Cleanup(func() { fail(); input.unblock(); s.Close() })
	out := []byte("xxxxx")
	result := make(chan error, 1)
	go func() {
		n, err := s.Read(out)
		if n != 0 || !bytes.Equal(out, []byte("xxxxx")) {
			result <- errors.New("failed producer's held bytes were published")
			return
		}
		result <- err
	}()
	// Read has passed its opening completion check and is actually blocked.
	select {
	case <-input.entered:
	case err := <-result:
		t.Fatalf("read did not reach the held source: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("read did not reach the held source")
	}
	fail()
	input.unblock()
	select {
	case err := <-result:
		if !errors.Is(err, ErrProtocol) {
			t.Fatalf("held failure was not preserved: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("failed held read did not retire")
	}
	if !input.closed || stops != 1 {
		t.Fatal("failed invocation did not close exactly its original stream")
	}
	s.Close()
	if stops != 1 {
		t.Fatal("failure cleanup was repeated")
	}
}
