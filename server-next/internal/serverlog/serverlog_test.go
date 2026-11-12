package serverlog

import (
	"io"
	stdlog "log"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Messages reads the log newest first a page at a time, filtered by level, never shows a
// secret, and follows new lines; "Record more detail" lets debug lines in until it ends.
func TestReadPagesFilterAndDetail(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	log := slog.New(l.Handler()).With("area", "test")
	for n := range 250 {
		if n%10 == 0 {
			log.Warn("slow disk", "n", n)
		} else {
			log.Info("line", "n", n, "password", "hunter2")
		}
	}
	log.Debug("not recorded")

	var seen []string
	cursor := ""
	for page := 0; ; page++ {
		lines, next, err := l.Read(Query{Cursor: cursor, Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range lines {
			seen = append(seen, e.Attrs["n"])
		}
		if next == "" || page > 5 {
			break
		}
		cursor = next
	}
	if len(seen) != 250 || seen[0] != "249" || seen[249] != "0" {
		t.Fatalf("read %d lines, first %q", len(seen), seen[:min(3, len(seen))])
	}
	warnings, _, _ := l.Read(Query{Level: "warn", Limit: 100})
	if len(warnings) != 25 || warnings[0].Attrs["area"] != "test" {
		t.Errorf("%d warnings, first %+v", len(warnings), warnings[0])
	}
	raw, _ := os.ReadFile(filepath.Join(dir, fileName(time.Now().Format(time.DateOnly))))
	if strings.Contains(string(raw), "hunter2") || strings.Contains(string(raw), "not recorded") {
		t.Errorf("the file holds a password or a debug line")
	}

	recent, more := l.Since(0)
	if len(recent) != 250 || recent[0].Attrs["n"] != "0" {
		t.Errorf("%d lines in memory", len(recent))
	}
	log.With("log_kind", "scan").Info("scan ended")
	kinds, _, _ := l.Read(Query{Kind: "scan", Limit: 100})
	if len(kinds) != 1 || kinds[0].Kind != "scan" || kinds[0].Message != "scan ended" {
		t.Fatalf("the kind filter returned %+v", kinds)
	}
	if lines, _ := l.Since(250); len(lines) != 1 || lines[0].Kind != "scan" {
		t.Fatalf("the live log lost the kind: %+v", lines)
	}
	l.Detail(time.Now().Add(time.Hour))
	log.Debug("recorded now", "n", strconv.Itoa(1000))
	select {
	case <-more:
	default:
		t.Errorf("a follower was not told of a new line")
	}
	if detail, _, _ := l.Read(Query{Text: "recorded now", Limit: 10}); len(detail) != 1 {
		t.Errorf("a debug line under Record more detail was not kept")
	}
	l.Detail(time.Time{})
	if !l.DetailUntil().IsZero() {
		t.Errorf("Record more detail did not end")
	}
}

// A secret is hidden wherever it stands: in an error's text (an address with its key in the
// query), in a tool's own output written through the standard logger, as a value registered
// when it was loaded, and under any name that says it is one. Nothing of it reaches the file,
// the lines in memory or a reader.
func TestSecretsAreHiddenByValue(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	logger := slog.New(l.Handler())
	Hide("hunter2-remote-password")
	logger.Error("a provider did not answer", "error", `Get "https://api.example.invalid/search?query=heat&api_key=abc123secret": EOF`)
	stdlog.New(slogWriter{logger}, "", 0).Print("rclone: token = {\"access_token\":\"ya29.secretvalue\"} pass = rcpass99")
	logger.Warn("signing in failed for hunter2-remote-password", "api_key", "zzz-key-zzz", "Authorization", "Bearer abcdefghijklmnop")
	raw, _ := os.ReadFile(filepath.Join(dir, fileName(time.Now().Format(time.DateOnly))))
	for _, secret := range []string{"abc123secret", "ya29.secretvalue", "rcpass99", "hunter2-remote-password", "zzz-key-zzz", "abcdefghijklmnop"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("the log file holds %q", secret)
		}
	}
	lines, _ := l.Since(0)
	for _, e := range lines {
		if strings.Contains(e.Message, "abc123secret") || strings.Contains(e.Attrs["error"], "abc123secret") {
			t.Errorf("a line in memory holds the key: %+v", e)
		}
	}
	if !strings.Contains(string(raw), "a provider did not answer") {
		t.Errorf("the line itself was lost")
	}
}

// slogWriter is the standard logger's output sent to a slog logger, as slog.SetDefault does.
type slogWriter struct{ l *slog.Logger }

func (w slogWriter) Write(p []byte) (int, error) {
	w.l.Info(strings.TrimSpace(string(p)))
	return len(p), nil
}

// One day's file stops at its limit with one line saying so; later lines are not kept there.
func TestOneDaysFileIsBounded(t *testing.T) {
	saved := dayFileLimit
	dayFileLimit = 4 << 10
	defer func() { dayFileLimit = saved }()
	dir := t.TempDir()
	l, err := Open(dir, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	logger := slog.New(l.Handler())
	for n := range 500 {
		logger.Info("a failing loop", "try", n)
	}
	info, _ := os.Stat(filepath.Join(dir, fileName(time.Now().Format(time.DateOnly))))
	raw, _ := os.ReadFile(filepath.Join(dir, fileName(time.Now().Format(time.DateOnly))))
	if info.Size() > 5<<10 || !strings.Contains(string(raw), "file is full") {
		t.Errorf("one day's file grew to %d bytes", info.Size())
	}
}
