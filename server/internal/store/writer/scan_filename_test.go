package writer

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog/identityvalue"
)

func TestProjectOrdinaryFilenameCompleteFacts(t *testing.T) {
	got, err := ProjectOrdinaryFilename([]byte("The Film (2020).mkv"), "", "en-US")
	want := InventoryOrdinaryFilename{SourceKey: "movie:the film:2020", Year: 2020, Metadata: InventoryFilenameMetadata{Title: "The Film", SortTitle: "", SortKey: "film", SortHead: "f", SortKeyKeepArticles: "the film", SortHeadKeepArticles: "t", MetadataLanguage: "en-US"}}
	if err != nil || got != want {
		t.Fatal(got, err)
	}
	got, err = ProjectOrdinaryFilename([]byte("The Film (2020).mkv"), "Zebra", "en-US")
	if err != nil || got.SourceKey != want.SourceKey || got.Metadata.SortTitle != "Zebra" || got.Metadata.SortKey != "zebra" || got.Metadata.SortKeyKeepArticles != "zebra" {
		t.Fatal(got, err)
	}
	got, err = ProjectOrdinaryFilename([]byte("!!!.mkv"), "", "en-US")
	if err != nil || got.SourceKey != "file:!!!.mkv" {
		t.Fatal("canonical locator fallback", got, err)
	}
	raw := []byte("Amélie (2001).mkv")
	got, err = ProjectOrdinaryFilename(raw, "", "fr")
	if err != nil || got.SourceKey != "movie:amelie:2001" {
		t.Fatal(got, err)
	}
	for n := range raw {
		raw[n] = 'x'
	}
	if got.Metadata.Title != "Amélie" || got.Metadata.MetadataLanguage != "fr" {
		t.Fatal("retained mutable raw buffer", got)
	}
}

func TestProjectOrdinaryFilenameExistingMovieCorpus(t *testing.T) {
	raw, err := os.ReadFile("../../naming/testdata/corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Kind, Path, Title string
		Year              int
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range cases {
		if c.Kind != "movie" {
			continue
		}
		n++
		got, err := ProjectOrdinaryFilename([]byte(c.Path), "", "en-US")
		if err != nil {
			t.Fatalf("%q: %v", c.Path, err)
		}
		key := identityvalue.MovieKey(c.Title, c.Year)
		if identityvalue.IdentityText(c.Title) == "" {
			key = identityvalue.ItemKey("", c.Path, 0)
		}
		metadata, e := ProjectFilenameMetadata(c.Title, "", "en-US")
		if e != nil || got.SourceKey != key || got.Year != c.Year || got.Metadata != metadata {
			t.Fatalf("%q: got%+v expected key%q year%d metadata%+v (%v)", c.Path, got, key, c.Year, metadata, e)
		}
	}
	if n == 0 {
		t.Fatal("no ordinary movie corpus")
	}
	t.Logf("complete projection matches %d existing ordinary movie cases", n)
}

func TestProjectOrdinaryFilenameRawPolicyAndBudget(t *testing.T) {
	for _, c := range []struct {
		name  string
		raw   []byte
		cause error
	}{
		{"nil", nil, ErrInvalid}, {"empty", []byte{}, ErrInvalid}, {"invalid_utf8", []byte{'f', 0xff}, ErrFilenameUnrepresentable},
		{"nul", []byte{'f', 0}, ErrInvalid}, {"mixed_nul_bad_utf8", []byte{0xff, 0}, ErrInvalid},
		{"outside", []byte("../Film.mkv"), ErrInvalid}, {"absolute", []byte("/Film.mkv"), ErrInvalid},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := ProjectOrdinaryFilename(c.raw, "", "en-US")
			if !errors.Is(err, c.cause) || !reflect.DeepEqual(got, InventoryOrdinaryFilename{}) {
				t.Fatal(got, err)
			}
			if c.cause == ErrInvalid && errors.Is(err, ErrFilenameUnrepresentable) {
				t.Fatal("invalid input became poison", err)
			}
		})
	}
	if got, err := ProjectOrdinaryFilename([]byte("Film.mkv"), "", strings.Repeat("x", maxCanonicalRequestBytes)); !errors.Is(err, ErrCap) || got != (InventoryOrdinaryFilename{}) {
		t.Fatal("late field escaped complete preflight", got, err)
	}
	if got, err := ProjectOrdinaryFilename([]byte("Film.mkv"), "\xff", "en-US"); !errors.Is(err, ErrInvalid) || errors.Is(err, ErrFilenameUnrepresentable) || got != (InventoryOrdinaryFilename{}) {
		t.Fatal("metadata is not a native-path poison reason", got, err)
	}
}
