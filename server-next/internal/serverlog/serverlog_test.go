package serverlog

import (
	"io"
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
