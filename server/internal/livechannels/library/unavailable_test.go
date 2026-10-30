package librarychannels

import (
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// The API answer stays ErrUnavailable, but the cause is kept for the server log
// and for retry classification.
func TestUnavailableKeepsItsCause(t *testing.T) {
	cause := errors.New("database is locked (5) (SQLITE_BUSY)")
	err := unavailable(cause)
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, cause) {
		t.Fatalf("%v loses its identity", err)
	}
	if err.Error() == ErrUnavailable.Error() {
		t.Fatal("cause missing from the message")
	}
	if dbwork.Classify(err) != dbwork.KindBusy {
		t.Fatal("busy cause not classified")
	}
	if unavailable(nil) != ErrUnavailable || unavailable(err) != err {
		t.Fatal("nil or already-wrapped causes changed")
	}
}
