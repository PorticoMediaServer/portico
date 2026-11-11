package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

type sample struct {
	Folder     string `json:"folder"`
	Passphrase string `json:"passphrase" settings:"secret"`
}

var sampleDoc = Define("sample", sample{Folder: "conversions"}, nil)

// xorSealer stands in for the server's key: it is enough to see that what is stored is not
// the secret and that it opens only for the field it was sealed for.
type xorSealer struct{}

func (xorSealer) Seal(belongsTo string, plain []byte) []byte {
	return append([]byte(belongsTo+":"), bytes.ToUpper(plain)...)
}

func (xorSealer) Open(belongsTo string, sealed []byte) ([]byte, error) {
	rest, ok := bytes.CutPrefix(sealed, []byte(belongsTo+":"))
	if !ok {
		return nil, errors.New("not for this field")
	}
	return bytes.ToLower(rest), nil
}

// A saved change is seen by every reader at once and by a watcher before the save returns,
// and outlasts a restart. A secret is stored sealed, never read back, kept when a save leaves
// it out, and removed by an empty one.
func TestSaveWatchAndSecrets(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "main.db")
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s, damaged, err := Open(ctx, d, xorSealer{})
	if err != nil || len(damaged) > 0 {
		t.Fatal(err, damaged)
	}
	var seen []string
	sampleDoc.Watch(s, func(v sample) { seen = append(seen, v.Folder) })
	if _, err := s.Write(ctx, 0, "sample", json.RawMessage(`{"passphrase":"open sesame"}`), 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Write(ctx, 0, "sample", json.RawMessage(`{"folder":"/fast"}`), 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := sampleDoc.Get(s); got.Folder != "/fast" || got.Passphrase != "open sesame" {
		t.Errorf("after the saves the document is %+v", got)
	}
	if strings.Contains(string(doc.Values), "sesame") || !doc.Secrets["passphrase"] || len(seen) != 2 || seen[1] != "/fast" {
		t.Errorf("the answer was %s %v and the watcher saw %v", doc.Values, doc.Secrets, seen)
	}
	if stored, _, _ := d.SettingDocument(ctx, "sample"); strings.Contains(stored, "sesame") {
		t.Errorf("the secret is stored as it was typed: %s", stored)
	}
	if _, err := s.Write(ctx, 0, "sample", json.RawMessage(`{"folder":"/slow"}`), 1, time.Now()); !errors.Is(err, ErrStale) {
		t.Errorf("a save from an out-of-date read answered %v", err)
	}

	again, _, err := Open(ctx, d, xorSealer{})
	if err != nil {
		t.Fatal(err)
	}
	if got := sampleDoc.Get(again); got.Folder != "/fast" || got.Passphrase != "open sesame" {
		t.Errorf("after a restart the document is %+v", got)
	}
	if _, err := again.Write(ctx, 0, "sample", json.RawMessage(`{"passphrase":""}`), -1, time.Now()); err != nil {
		t.Fatal(err)
	}
	if doc, _ := again.Read("sample"); doc.Secrets["passphrase"] {
		t.Errorf("an empty passphrase did not remove it")
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
	s, _, err := Open(ctx, d, nil)
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
