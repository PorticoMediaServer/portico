package writer

import (
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog/identityvalue"
)

func ordinaryFilenameInput(raw []byte, explicit, language string) InventoryOrdinaryFilenameInput {
	root := filepath.Join(string(filepath.Separator), "media")
	return InventoryOrdinaryFilenameInput{IdentityRoot: []byte(root), AssetPath: []byte(filepath.Join(root, string(raw))), RelativePath: raw, ExplicitSortTitle: explicit, MetadataLanguage: language}
}

func TestProjectOrdinaryFilenameCompleteFacts(t *testing.T) {
	got, err := ProjectOrdinaryFilename(ordinaryFilenameInput([]byte("The Film (2020).mkv"), "", "en-US"))
	want := InventoryOrdinaryFilename{SourceKey: "movie:the film:2020", Year: 2020, Metadata: InventoryFilenameMetadata{Title: "The Film", SortTitle: "", SortKey: "film", SortHead: "f", SortKeyKeepArticles: "the film", SortHeadKeepArticles: "t", MetadataLanguage: "en-US"}}
	if err != nil || got != want {
		t.Fatal(got, err)
	}
	got, err = ProjectOrdinaryFilename(ordinaryFilenameInput([]byte("The Film (2020).mkv"), "Zebra", "en-US"))
	if err != nil || got.SourceKey != want.SourceKey || got.Metadata.SortTitle != "Zebra" || got.Metadata.SortKey != "zebra" || got.Metadata.SortKeyKeepArticles != "zebra" {
		t.Fatal(got, err)
	}
	got, err = ProjectOrdinaryFilename(ordinaryFilenameInput([]byte("!!!.mkv"), "", "en-US"))
	if err != nil || got.SourceKey != "file:!!!.mkv" {
		t.Fatal("canonical locator fallback", got, err)
	}
	raw := []byte("Amélie (2001).mkv")
	got, err = ProjectOrdinaryFilename(ordinaryFilenameInput(raw, "", "fr"))
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
		got, err := ProjectOrdinaryFilename(ordinaryFilenameInput([]byte(c.Path), "", "en-US"))
		if err != nil {
			t.Fatalf("%q: %v", c.Path, err)
		}
		key := identityvalue.MovieKey(c.Title, c.Year)
		if identityvalue.IdentityText(c.Title) == "" {
			input := ordinaryFilenameInput([]byte(c.Path), "", "en-US")
			reading := naming.Read(c.Path, "movie")
			part := 0
			if len(reading.Parts) > 0 {
				part = reading.Parts[0].Value
			}
			key = identityvalue.ItemKey(string(input.IdentityRoot), string(input.AssetPath), part)
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

func TestProjectOrdinaryFilenameCompleteLegacyLocatorCall(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "retained-library-root")
	relative := []byte("!!! - pt2.mkv")
	reading := naming.Read(string(relative), "movie")
	title, _ := reading.PreferredTitle()
	if identityvalue.IdentityText(title) != "" || len(reading.Parts) == 0 || reading.Parts[0].Value != 2 {
		t.Fatalf("locator fixture does not exercise a split unnamed movie: %+v", reading)
	}
	inside := InventoryOrdinaryFilenameInput{IdentityRoot: []byte(root), AssetPath: []byte(filepath.Join(root, string(relative))), RelativePath: relative, MetadataLanguage: "en-US"}
	out := inside
	out.AssetPath = []byte(filepath.Join(string(filepath.Separator), "other-physical-root", string(relative)))
	a, err := ProjectOrdinaryFilename(inside)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ProjectOrdinaryFilename(out)
	if err != nil {
		t.Fatal(err)
	}
	// identityvalue.ItemKey is independently byte-proved against the frozen
	// pre-extraction implementation in the dependency's complete corpus test.
	if a.SourceKey != identityvalue.ItemKey(root, string(inside.AssetPath), 2) || b.SourceKey != identityvalue.ItemKey(root, string(out.AssetPath), 2) || a.SourceKey == b.SourceKey || b.Part != 2 {
		t.Fatal(a, b)
	}
	if b.SourceKey == identityvalue.ItemKey("", string(relative), 2) {
		t.Fatal("outside-root path was replaced by a synthetic relative locator", b)
	}
	// A required display fallback must not invent a logical movie identity
	// that the legacy preferred-title interpretation did not supply.
	empty := ordinaryFilenameInput([]byte("pt2.mkv"), "", "en-US")
	r := naming.Read(string(empty.RelativePath), "movie")
	canonical, _ := r.PreferredTitle()
	if canonical != "" || len(r.Parts) == 0 {
		t.Fatalf("empty-title fixture changed: %+v", r)
	}
	got, err := ProjectOrdinaryFilename(empty)
	if err != nil || got.Metadata.Title != "pt2" || got.SourceKey != identityvalue.ItemKey(string(empty.IdentityRoot), string(empty.AssetPath), r.Parts[0].Value) {
		t.Fatal(got, err)
	}
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
			got, err := ProjectOrdinaryFilename(ordinaryFilenameInput(c.raw, "", "en-US"))
			if !errors.Is(err, c.cause) || !reflect.DeepEqual(got, InventoryOrdinaryFilename{}) {
				t.Fatal(got, err)
			}
			if c.cause == ErrInvalid && errors.Is(err, ErrFilenameUnrepresentable) {
				t.Fatal("invalid input became poison", err)
			}
		})
	}
	if got, err := ProjectOrdinaryFilename(ordinaryFilenameInput([]byte("Film.mkv"), "", strings.Repeat("x", maxCanonicalRequestBytes))); !errors.Is(err, ErrCap) || got != (InventoryOrdinaryFilename{}) {
		t.Fatal("late field escaped complete preflight", got, err)
	}
	if got, err := ProjectOrdinaryFilename(ordinaryFilenameInput([]byte("Film.mkv"), "\xff", "en-US")); !errors.Is(err, ErrInvalid) || errors.Is(err, ErrFilenameUnrepresentable) || got != (InventoryOrdinaryFilename{}) {
		t.Fatal("metadata is not a native-path poison reason", got, err)
	}
}
