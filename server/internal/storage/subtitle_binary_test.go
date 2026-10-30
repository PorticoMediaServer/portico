package storage

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestSubtitleBinaryReaderDoesNotChangeMetadataLimit(t *testing.T) {
	binary, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	client := New(binary)
	path := filepath.Join(t.TempDir(), "movie.sup")
	content := []byte("small subtitle")
	if e = os.WriteFile(path, content, 0600); e != nil {
		t.Fatal(e)
	}
	got, e := client.ReadSubtitleBinary(context.Background(), "subtitle", path, 32<<20)
	if e != nil || !bytes.Equal(got, content) {
		t.Fatal(got, e)
	}
	rooted := WithScanInput(context.Background(), client, "source", InventoryRequest{Root: filepath.Dir(path)})
	for _, limit := range []int64{32 << 20, 128 << 20, 512 << 20} {
		got, e = client.ReadSubtitleBinary(rooted, "rooted-subtitle", path, limit)
		if e != nil || !bytes.Equal(got, content) {
			t.Fatal("rooted subtitle", limit, got, e)
		}
	}
	if _, e = client.ReadSubtitleBinary(rooted, "rooted-subtitle", path, 513<<20); e == nil {
		t.Fatal("rooted subtitle binary bound widened")
	}
	if _, e = client.ReadSmall(rooted, "rooted-metadata", path, 128<<20); e == nil {
		t.Fatal("rooted metadata accepted the subtitle binary bound")
	}

	if _, e = client.ReadSmall(context.Background(), "metadata", path, 32<<20); e == nil {
		t.Fatal("metadata accepted a subtitle binary bound")
	}
	if _, e = client.ReadSubtitleBinary(context.Background(), "subtitle", path, 513<<20); e == nil {
		t.Fatal("subtitle binary bound widened")
	}
	if _, e = client.ReadSubtitleBinary(context.Background(), "subtitle", filepath.Join(filepath.Dir(path), "movie.nfo"), 32<<20); e == nil {
		t.Fatal("metadata used the binary reader")
	}
}

func TestSubtitleCandidateInventoryIgnoresUnrelatedDirectoryEntries(t *testing.T) {
	binary, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	client := New(binary)
	root := t.TempDir()
	for i := 0; i < 4200; i++ {
		name := filepath.Join(root, fmt.Sprintf("unrelated-%05d.nfo", i))
		if e = os.WriteFile(name, nil, 0600); e != nil {
			t.Fatal(e)
		}
	}
	for _, name := range []string{"Film.en.srt", "Film.eng.forced.srt", "Film.en.sdh.ass", "Film.pt-BR.srt", "Film.English.default.srt"} {
		if e = os.WriteFile(filepath.Join(root, name), nil, 0600); e != nil {
			t.Fatal(e)
		}
	}
	entries := []Snapshot{}
	if e = client.InventorySubtitleCandidates(context.Background(), "candidates", root, "Film", func(v Snapshot) error { entries = append(entries, v); return nil }); e != nil {
		t.Fatal(e)
	}
	if len(entries) != 5 {
		t.Fatal("unrelated entries consumed the subtitle inventory bound", len(entries))
	}
}
