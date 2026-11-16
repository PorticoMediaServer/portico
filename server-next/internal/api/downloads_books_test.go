package api

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A book is downloaded in the form the app reads offline (10-reading rule 46). A MOBI comes as
// the EPUB it reads as, and a comic made of a folder of pictures as a CBZ of its pages in
// order, each with its exact size and SHA-256, the same bytes each time it is asked for, and a
// fetch that broke off carries on. An EPUB comes as itself. All name the file they were made
// from as the book route does, so a place made offline is one made online.
func TestABookDownloadsInTheFormTheAppReads(t *testing.T) {
	b := newBookShelf(t)
	token := b.owner.AccessToken
	page := pngPicture(t, 8, 12)
	for _, name := range []string{"10.png", "2.png"} {
		if err := os.MkdirAll(filepath.Join(b.comics, "Nemo", "Nemo 001"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(b.comics, "Nemo", "Nemo 001", name), []byte(page), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b.scan()
	for _, c := range []struct {
		kind             db.ItemKind
		title, container string
		copied           bool
		members          []string
	}{{db.KindBook, "Kindle Book", "epub", true, nil}, {db.KindBook, "Nineteen", "epub", false, nil},
		{db.KindIssue, "Nemo 001", "cbz", true, []string{"00001.png", "00002.png"}}} {
		id := b.find(c.kind, c.title)
		book := get[BookAnswer](t, b.s, token, "/v1/items/"+id+"/book")
		var dl Download
		if w := call(t, b.s, "POST", "/v1/downloads", token, DownloadAsk{ItemID: id, Quality: "original", VersionID: book.Version}, &dl); w.Code != 201 {
			t.Fatalf("%s: asking answered %d: %s", c.title, w.Code, w.Body)
		}
		var item DownloadedItem
		if w := call(t, b.s, "GET", "/v1/downloads/"+dl.ID+"/items/"+id, token, nil, &item); w.Code != 200 || len(item.Files) != 1 {
			t.Fatalf("%s: the files answered %d: %s", c.title, w.Code, w.Body)
		}
		file := item.Files[0]
		if item.Revision != book.Fingerprint || file.Container != c.container || (file.SHA256 != "") != c.copied {
			t.Fatalf("%s: revision %q (book %q), file %+v", c.title, item.Revision, book.Fingerprint, file)
		}
		res := b.fetch(file.URL)
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 || int64(len(body)) != file.SizeBytes {
			t.Fatalf("%s: fetching answered %d with %d of %d bytes", c.title, res.StatusCode, len(body), file.SizeBytes)
		}
		if !c.copied {
			continue
		}
		if sum := sha256.Sum256(body); hex.EncodeToString(sum[:]) != file.SHA256 {
			t.Fatalf("%s: the bytes are not the ones described", c.title)
		}
		archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			t.Fatalf("%s: not a zip: %v", c.title, err)
		}
		var names []string
		for _, f := range archive.File {
			names = append(names, f.Name)
		}
		if c.container == "epub" && (names[0] != "mimetype" || archive.File[0].Method != zip.Store) ||
			c.members != nil && !slices.Equal(names, c.members) {
			t.Fatalf("%s: holds %v", c.title, names)
		}
		var again DownloadedItem
		call(t, b.s, "GET", "/v1/downloads/"+dl.ID+"/items/"+id, token, nil, &again)
		rest := b.fetch(file.URL, "Range", "bytes=100-", "If-Range", res.Header.Get("ETag"))
		tail, _ := io.ReadAll(rest.Body)
		if again.Files[0] != file || rest.StatusCode != 206 || !bytes.Equal(tail, body[100:]) {
			t.Fatalf("%s: asked again %+v; carrying on answered %d", c.title, again.Files[0], rest.StatusCode)
		}
	}
}
