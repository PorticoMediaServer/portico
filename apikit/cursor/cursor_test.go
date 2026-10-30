package cursor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/apikit/apierror"
)

func TestBindingExpiryAndTampering(t *testing.T) {
	s, err := New([]byte(strings.Repeat("k", 32)), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	s.now = func() time.Time { return now }
	binding := Binding{Principal: "profile", Authority: "1", Snapshot: "snapshot", Filter: "filter", Sort: "title"}
	token, err := s.Seal(binding, 42)
	if err != nil {
		t.Fatal(err)
	}
	var value int
	if err = s.Open(token, binding, &value); err != nil || value != 42 {
		t.Fatal(value, err)
	}
	other := binding
	other.Authority = "2"
	if err := s.Open(token, other, &value); err == nil || !cursorCode(err, "invalid_cursor") {
		t.Fatal("authority fence did not return invalid_cursor", err)
	}
	if s.Open("x"+token, binding, &value) == nil {
		t.Fatal("tamper accepted")
	}
	now = now.Add(2 * time.Minute)
	if s.Open(token, binding, &value) == nil {
		t.Fatal("expired token accepted")
	}
}

func cursorCode(err error, code string) bool {
	var api *apierror.Error
	return errors.As(err, &api) && api.Code == code
}

func TestStateSignerPersistsAndRotatesWithoutStrandingLiveCursors(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStateSigner(dir, time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	binding := Binding{Principal: "account:profile", Authority: "9", Snapshot: "catalog:4"}
	before, err := s.Seal(binding, "row-1")
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(filepath.Join(dir, keyFile)); err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatalf("cursor key was not stored privately: %v", err)
	}
	restarted, err := NewStateSigner(dir, time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var position string
	if err := restarted.Open(before, binding, &position); err != nil || position != "row-1" {
		t.Fatalf("restart lost cursor: %q %v", position, err)
	}
	if err := restarted.Rotate(); err != nil {
		t.Fatal(err)
	}
	after, err := restarted.Seal(binding, "row-2")
	if err != nil {
		t.Fatal(err)
	}
	restartedAgain, err := NewStateSigner(dir, time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := restartedAgain.Open(before, binding, &position); err != nil || position != "row-1" {
		t.Fatalf("rotation stranded live cursor: %q %v", position, err)
	}
	if err := restartedAgain.Open(after, binding, &position); err != nil || position != "row-2" {
		t.Fatalf("restart lost rotated key: %q %v", position, err)
	}
	if err := restartedAgain.Rotate(); err == nil {
		t.Fatal("second rotation discarded a still-live predecessor")
	}
	restartedAgain.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if err := restartedAgain.Open(before, binding, &position); !cursorCode(err, "invalid_cursor") {
		t.Fatalf("retired key remained valid: %v", err)
	}
	if err := restartedAgain.Open(after, binding, &position); !cursorCode(err, "cursor_expired") {
		t.Fatalf("expired cursor remained valid: %v", err)
	}
}

func TestFailedBackgroundRotationDoesNotBreakPaging(t *testing.T) {
	s, err := NewStateSigner(t.TempDir(), time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return s.createdAt.Add(2 * time.Hour) }
	s.statePath = filepath.Join(t.TempDir(), "missing", keyFile)
	if err := s.RotateIfDue(); err == nil {
		t.Fatal("unwritable key path accepted")
	}
	binding := Binding{Principal: "profile", Authority: "epoch", Snapshot: "revision"}
	token, err := s.Seal(binding, "next")
	if err != nil {
		t.Fatal("failed background rotation broke paging", err)
	}
	var position string
	if err := s.Open(token, binding, &position); err != nil || position != "next" {
		t.Fatalf("current key lost after failed rotation: %q %v", position, err)
	}
}
