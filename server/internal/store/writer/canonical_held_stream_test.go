package writer

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestCanonicalHeldStreamTransport(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("runner Unix socket transport")
	}
	t.Setenv("PORTICO_CANONICAL_HELD_STREAM", "unix-gzip-v1")
	path := filepath.Join(t.TempDir(), "raw.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	received := make(chan []byte, 1)
	go func() {
		c, err := listener.AcceptUnix()
		if err != nil {
			received <- nil
			return
		}
		defer c.Close()
		raw, _ := io.ReadAll(c)
		received <- raw
	}()
	file, streamOnly, err := openCanonicalHeldOutput(path)
	if err != nil || !streamOnly {
		t.Fatal(streamOnly, err)
	}
	s := &n1Stream{file: file, streamOnly: streamOnly, origin: time.Now(), base: map[string]any{"unchanged": "record content"}}
	if err := s.append("attempt", map[string]any{"nullable": nil, "observed_zero": 0}); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(<-received, &record); err != nil {
		t.Fatal(err)
	}
	if record["kind"] != "attempt" || record["sequence"] != float64(1) || record["unchanged"] != "record content" || record["nullable"] != nil || record["observed_zero"] != float64(0) || len(record) != 6 {
		t.Fatal(record)
	}
	// Broken or unknown transports cannot silently fall back to disk.
	if _, _, err := openCanonicalHeldOutput(filepath.Join(t.TempDir(), "absent.sock")); err == nil {
		t.Fatal("missing observer accepted")
	}
	t.Setenv("PORTICO_CANONICAL_HELD_STREAM", "other")
	fallback := filepath.Join(t.TempDir(), "must-not-exist")
	if _, _, err := openCanonicalHeldOutput(fallback); err == nil {
		t.Fatal("unknown transport accepted")
	}
	if _, err := os.Stat(fallback); !os.IsNotExist(err) {
		t.Fatal("disk fallback", err)
	}
}
