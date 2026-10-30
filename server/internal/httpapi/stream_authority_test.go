package httpapi

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestStreamAuthorityReusesAVerdictUntilAuthorityMoves(t *testing.T) {
	calls := 0
	a := newStreamAuthority(func() error { calls++; return nil })
	for i := 0; i < 200; i++ {
		if err := a.check(); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("a quiet stream re-authorized %d times for 200 reads", calls)
	}
	dbwork.BumpAuthority()
	if err := a.check(); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("an authority change did not force a fresh check: %d", calls)
	}
}

func TestStreamAuthorityNeverCachesADenial(t *testing.T) {
	denied := errors.New("denied")
	allow := true
	a := newStreamAuthority(func() error {
		if allow {
			return nil
		}
		return denied
	})
	if err := a.check(); err != nil {
		t.Fatal(err)
	}
	allow = false
	dbwork.BumpAuthority()
	for i := 0; i < 3; i++ {
		if err := a.check(); !errors.Is(err, denied) {
			t.Fatalf("read %d after revocation: %v", i, err)
		}
	}
	allow = true
	if err := a.check(); err != nil {
		t.Fatalf("a denial was cached past its cause: %v", err)
	}
}

// The guard's promise survives the verdict: a revocation that commits while a
// read is blocked discards the bytes that read returned.
func TestStreamAuthorityRevocationDuringReadDiscardsBytes(t *testing.T) {
	revoked := false
	a := newStreamAuthority(func() error {
		if revoked {
			return errors.New("revoked")
		}
		return nil
	})
	source := revokeDuringRead{bytes.NewReader([]byte("private")), func() { revoked = true; dbwork.BumpAuthority() }}
	r := &guardedReadSeeker{ReadSeeker: source, check: a.check}
	buf := make([]byte, 16)
	if n, err := r.Read(buf); err == nil || n != 0 {
		t.Fatalf("bytes crossed a revocation: n=%d err=%v", n, err)
	}
	_ = io.EOF
}
