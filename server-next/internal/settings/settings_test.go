package settings

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

type sample struct {
	Folder string `json:"folder"`
}

var sampleDoc = Define("sample", sample{Folder: "conversions"}, nil)

// A saved change is seen by every reader at once and by a watcher before the save returns,
// is refused from an out-of-date read, and outlasts a restart.
func TestSaveAndWatch(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "main.db")
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s, damaged, err := Open(ctx, d)
	if err != nil || len(damaged) > 0 {
		t.Fatal(err, damaged)
	}
	var seen []string
	sampleDoc.Watch(s, func(v sample) { seen = append(seen, v.Folder) })
	doc, err := s.Write(ctx, 0, "sample", json.RawMessage(`{"folder":"/fast"}`), 0, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := sampleDoc.Get(s); got.Folder != "/fast" || doc.Revision != 1 || len(seen) != 1 || seen[0] != "/fast" {
		t.Errorf("after the save the document is %+v and the watcher saw %v", got, seen)
	}
	if _, err := s.Write(ctx, 0, "sample", json.RawMessage(`{"folder":"/slow"}`), 0, time.Now()); !errors.Is(err, ErrStale) {
		t.Errorf("a save from an out-of-date read answered %v", err)
	}
	again, _, err := Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if got := sampleDoc.Get(again); got.Folder != "/fast" {
		t.Errorf("after a restart the document is %+v", got)
	}
}

type listed struct {
	Hours []hours        `json:"hours"`
	Tags  map[string]int `json:"tags"`
}

type hours struct {
	From int `json:"from"`
	To   int `json:"to"`
}

var listedDoc = Define("listed", listed{}, nil)

// A list or a map that is saved is the one sent, never mixed with the one before it, and a
// field the document does not have is refused instead of answered as saved.
func TestASavedListReplacesTheOldOne(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s, _, err := Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{`{"hours":[{"from":1,"to":6}],"tags":{"a":1,"b":2}}`, `{"hours":[{"from":9}],"tags":{"a":5}}`} {
		if _, err := s.Write(ctx, 0, "listed", json.RawMessage(change), -1, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if got := listedDoc.Get(s); len(got.Hours) != 1 || got.Hours[0] != (hours{From: 9}) || len(got.Tags) != 1 || got.Tags["a"] != 5 {
		t.Errorf("the second save left %+v", got)
	}
	var invalid *Invalid
	for _, change := range []string{`{"hour":[]}`, `{"hours":null}`, `[]`, `{"hours":[{"from":"nine"}]}`} {
		if _, err := s.Write(ctx, 0, "listed", json.RawMessage(change), -1, time.Now()); !errors.As(err, &invalid) {
			t.Errorf("%s answered %v", change, err)
		}
	}
}
