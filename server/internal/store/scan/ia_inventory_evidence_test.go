package scan

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/naming"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

func iaEvidenceFile(path string) FileObservation {
	return FileObservation{SourceKey: "existing-source-key", Witness: FileWitness{
		Source:       SourceWitness{LibraryID: 42, SourceID: "source", Incarnation: "source-incarnation", Revision: "source-revision"},
		RelativePath: path, ObjectIdentity: "native-object", Revision: "object-revision", Size: 100, ModifiedNS: -1,
	}}
}

func TestIAMediaEvidencePreservesEpisodeCohort(t *testing.T) {
	file := iaEvidenceFile("Show/Season 2/Show - S02E01-E03.mkv")
	e, err := PrepareMediaEvidence("tv", file, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	r := e.Naming()
	if len(r.Episodes) != 1 || !slices.Equal(r.Episodes[0].Value, []int{1, 2, 3}) || len(r.Seasons) == 0 || r.Seasons[0].Value != 2 {
		t.Fatalf("lost complete episode cohort: %+v", r)
	}
	r.Episodes[0].Value[1] = 99
	r.Episodes[0].Path = "changed"
	r.Titles = nil
	again := e.Naming()
	if !slices.Equal(again.Episodes[0].Value, []int{1, 2, 3}) || again.Episodes[0].Path != file.Witness.RelativePath || len(again.Titles) == 0 {
		t.Fatal("mutable returned naming evidence changed retained input")
	}
	file.Witness.Source.Revision = "new-source"
	file.Witness.RelativePath = "new-path"
	if e.File().Witness.Source.Revision != "source-revision" || e.Kind() != "tv" {
		t.Fatal("retained witness changed")
	}
}

func TestIAMediaEvidenceKeepsRejectedRangeDiagnostic(t *testing.T) {
	e, err := PrepareMediaEvidence("anime", iaEvidenceFile("Show/Show.S01E01-E20.mkv"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	r := e.Naming()
	if len(r.Episodes) != 0 || !slices.Contains(r.Diagnostics, "ambiguous_episode_range") {
		t.Fatalf("invalid range became a first-episode prefix: %+v", r)
	}
	r.Diagnostics[0] = "changed"
	if !slices.Contains(e.Naming().Diagnostics, "ambiguous_episode_range") {
		t.Fatal("diagnostic slice escaped")
	}
}

func TestIAMediaEvidenceAudioAndExtrasRemainDistinct(t *testing.T) {
	music, err := PrepareMediaEvidence("music", iaEvidenceFile("Artist/Album/Bonus/01 - Song.flac"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	m := music.Naming()
	if len(m.Extras) != 0 || len(m.TrackNumbers) != 1 || m.TrackNumbers[0].Value != 1 || len(m.Albums) == 0 {
		t.Fatalf("audio routed into video extras: %+v", m)
	}
	extra, err := PrepareMediaEvidence("tv", iaEvidenceFile("Show/Trailers/Preview.mkv"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	x := extra.Naming()
	if len(x.Extras) != 1 || x.Extras[0].Value.Kind != "trailer" || x.Extras[0].Value.ParentPath != "Show" {
		t.Fatalf("lost trailer ownership evidence: %+v", x)
	}
}

func TestIAMediaEvidenceBudgetAndRefusals(t *testing.T) {
	file := iaEvidenceFile("Show/Season 2/Show.S02E01-E03.mkv")
	prepared, err := PrepareMediaEvidence("tv", file, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareMediaEvidence("tv", file, prepared.Bytes()); err != nil {
		t.Fatalf("exact budget refused: %v", err)
	}
	if e, err := PrepareMediaEvidence("tv", file, prepared.Bytes()-1); !errors.Is(err, ErrCapacity) || e.Bytes() != 0 || e.Kind() != "" {
		t.Fatalf("partial over-budget evidence: %+v %v", e, err)
	}
	cases := []struct {
		name, kind, path string
		want             error
	}{
		{"movie remains SC", "movie", "Film.mkv", ErrInvalid},
		{"unknown kind", "unknown", "File.mkv", ErrInvalid},
		{"traversal", "tv", "../Show.mkv", ErrInvalid},
		{"native bytes", "tv", "Show/\xff.mkv", writer.ErrFilenameUnrepresentable},
		{"NUL precedence", "tv", "Show/\xff\x00.mkv", ErrInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, err := PrepareMediaEvidence(tc.kind, iaEvidenceFile(tc.path), 1<<20)
			if !errors.Is(err, tc.want) || e.Bytes() != 0 || e.Kind() != "" {
				t.Fatalf("evidence=%+v err=%v, want %v", e, err, tc.want)
			}
		})
	}
}

func TestIAMediaEvidenceRetainsExistingNamingAlternatives(t *testing.T) {
	for _, row := range []struct{ kind, path string }{
		{"anime", "[Group] Show - 123.mkv"},
		{"audiobook", "Author/Book/Disc 2/Part 3.mp3"},
		{"book", "Author/Book (2020).epub"},
		{"comic", "Series/Series - Issue 03.cbz"},
		{"magazine", "Magazine/Magazine 2026-11.pdf"},
		{"newspaper", "Newspaper/Newspaper 2026-11-07.pdf"},
		{"tv", "Show/Season 1/Show {edition-Extended} S01E01 - pt2.mkv"},
	} {
		t.Run(row.kind, func(t *testing.T) {
			file := iaEvidenceFile(row.path)
			e, err := PrepareMediaEvidence(row.kind, file, 1<<20)
			if err != nil || e.File() != file {
				t.Fatalf("witness=%+v err=%v", e.File(), err)
			}
			if got, want := e.Naming(), naming.Read(row.path, row.kind); !reflect.DeepEqual(got, want) {
				t.Fatalf("naming changed:\n%+v\nwant %+v", got, want)
			}
		})
	}
}
