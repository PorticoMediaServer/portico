package enrich

import (
	"errors"
	"io"
	"strings"
	"testing"
)

type tmdbCountedReader struct{ remaining, observed int64 }

func (r *tmdbCountedReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := int(min(int64(len(p)), r.remaining))
	for i := range n {
		p[i] = 'x'
	}
	r.remaining -= int64(n)
	r.observed += int64(n)
	return n, nil
}

type tmdbFailingReader struct{ err error }

func (r tmdbFailingReader) Read(p []byte) (int, error) { copy(p, "1234"); return min(4, len(p)), r.err }

func TestTMDBResponseBoundedReadRefusesWholeOversize(t *testing.T) {
	const limit int64 = 4194304
	r := &tmdbCountedReader{remaining: limit * 20}
	body, err := readTMDBResponse(r, limit)
	var size *ResponseSizeError
	if body != nil || !errors.Is(err, ErrResponse) || !errors.As(err, &size) || size.LimitBytes != limit || size.ObservedBytes != limit+1 || !size.ObservedIsLowerBound || r.observed != limit+1 {
		t.Fatal("unbounded/truncated response", r, size, err)
	}
	for _, input := range []string{"", "123", "1234"} {
		body, err := readTMDBResponse(strings.NewReader(input), 4)
		if err != nil || string(body) != input {
			t.Fatal("legal response shrunk", len(input), err)
		}
	}
	for _, bad := range []int64{-1, 0, limit + 1, 1 << 62} {
		if body, err := readTMDBResponse(strings.NewReader("x"), bad); body != nil || !errors.Is(err, ErrTMDBPolicy) {
			t.Fatal("invalid bound", bad, err)
		}
	}
}

func TestTMDBResponseReadFailureKeepsSizeDiagnostic(t *testing.T) {
	readErr := errors.New("injected read failure")
	body, err := readTMDBResponse(tmdbFailingReader{readErr}, 3)
	var size *ResponseSizeError
	if body != nil || !errors.Is(err, readErr) || !errors.Is(err, ErrResponse) || !errors.As(err, &size) || size.ObservedBytes != 4 {
		t.Fatal("size/read cause lost", err)
	}
	body, err = readTMDBResponse(tmdbFailingReader{readErr}, 10)
	if body != nil || !errors.Is(err, readErr) || errors.Is(err, ErrResponse) {
		t.Fatal("partial read became success/oversize", body, err)
	}
}
