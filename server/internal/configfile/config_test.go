package configfile

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestPrecedenceTypesRereadAndInvalidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "portico.toml")
	l := New(path)
	b := Binding{Key: "example.limit", Env: "PORTICO_TEST_ONLY_LIMIT", Default: int64(7), ParseEnv: func(s string) (any, error) { return strconv.ParseInt(s, 10, 64) }}
	if _, err := l.Reload(); err != nil {
		t.Fatal(err)
	}
	if got, err := l.Resolve(b); err != nil || got.Value != int64(7) || got.Source != Default {
		t.Fatal(got, err)
	}
	if err := os.WriteFile(path, []byte("[example]\nlimit = 12\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if changed, err := l.Reload(); !changed || err != nil {
		t.Fatal(changed, err)
	}
	if got, _ := l.Resolve(b); got.Value != int64(12) || got.Source != File {
		t.Fatal(got)
	}
	t.Setenv(b.Env, "19")
	if got, _ := l.Resolve(b); got.Value != int64(19) || !got.Locked || got.Source != Environment {
		t.Fatal(got)
	}
	t.Setenv(b.Env, "")
	if _, err := l.Resolve(b); err == nil {
		t.Fatal("empty environment silently ignored")
	}
	os.Unsetenv(b.Env)
	os.WriteFile(path, []byte("[broken"), 0600)
	if _, err := l.Reload(); err == nil {
		t.Fatal("bad TOML accepted")
	}
	if got, _ := l.Resolve(b); got.Value != int64(12) {
		t.Fatal("lost last valid document", got)
	}
	// Same-length rewrite is detected by content, independent of timestamps.
	os.WriteFile(path, []byte("[example]\nlimit = 13\n"), 0600)
	if changed, err := l.Reload(); !changed || err != nil {
		t.Fatal(changed, err)
	}
	var typed struct {
		Example struct {
			Limit int64 `toml:"limit"`
		} `toml:"example"`
	}
	if err := l.Decode(&typed); err != nil || typed.Example.Limit != 13 {
		t.Fatal(typed, err)
	}
}
func TestAtomicWriteWatchAndSymlinkRefusal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "portico.toml")
	l := New(path)
	if err := l.Write(map[string]any{"example": "first"}); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	changed := make(chan struct{}, 1)
	go func() {
		defer close(done)
		l.Watch(ctx, time.Millisecond, func() {
			select {
			case changed <- struct{}{}:
			default:
			}
		}, nil)
	}()
	os.WriteFile(path, []byte("example = 'next'\n"), 0600)
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("watch missed write")
	}
	cancel()
	<-done
	os.Remove(path)
	os.Symlink(filepath.Join(t.TempDir(), "other"), path)
	if _, err := l.Reload(); err == nil {
		t.Fatal("linked config accepted")
	}
}
