package scan

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/disc"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediatools"
)

// A disc is its main feature: the longest title of a minute or more, with the first clip's
// tracks and its chapters. An encrypted disc is not supported, and a disc with nothing long
// enough is not a film.
func TestADiscIsItsMainFeature(t *testing.T) {
	playlist := 800
	short := disc.Title{DiscOrder: 1, DurationMs: 90_000}
	feature := disc.Title{DiscOrder: 0, Playlist: &playlist, DurationMs: 7_200_000,
		Streams: []disc.Stream{{ClipSequence: 0, Kind: "video", Coding: 0x24}, {ClipSequence: 0, Kind: "video", Coding: 0x24, DolbyVisionEnhancementLayer: true},
			{ClipSequence: 0, Kind: "audio", Coding: 0x83, Language: "eng"}, {ClipSequence: 0, Kind: "audio", Coding: 0x81, Language: "fra"},
			{ClipSequence: 0, Kind: "subtitle", Coding: 0x90, Language: "eng"}, {ClipSequence: 0, Kind: "interactive", Coding: 0x91},
			{ClipSequence: 1, Kind: "video", Coding: 0x24}, {ClipSequence: 1, Kind: "audio", Coding: 0x83, Language: "eng"}},
		Chapters: []disc.Chapter{{StartMs: 0}, {StartMs: 600_000}}}
	facts, err := discFacts(disc.Probe{Kind: "bluray", Packaging: "folder", Encryption: "none", Titles: []disc.Title{feature, short}})
	if err != nil {
		t.Fatal(err)
	}
	if facts.Container != "bluray" || facts.DurationMS != 7_200_000 || len(facts.Streams) != 4 || len(facts.Chapters) != 2 ||
		facts.Chapters[0].EndMS != 600_000 || facts.Chapters[1].EndMS != 7_200_000 {
		t.Fatalf("the disc's facts: %+v", facts)
	}
	if v, a, s := facts.Streams[0], facts.Streams[1], facts.Streams[3]; v.Kind != db.StreamVideo || v.Codec != "hevc" || a.Kind != db.StreamAudio ||
		a.Codec != "truehd" || a.Language != "eng" || s.Kind != db.StreamSubtitle || s.Codec != "hdmv_pgs_subtitle" {
		t.Fatalf("the disc's tracks: %+v", facts.Streams)
	}
	if _, err := discFacts(disc.Probe{Kind: "bluray", Encryption: "aacs", Titles: []disc.Title{feature}}); err != ErrUnsupported {
		t.Fatalf("an encrypted disc: %v", err)
	}
	if _, err := discFacts(disc.Probe{Kind: "dvd", Encryption: "none", Titles: []disc.Title{}}); err != ErrNotMedia {
		t.Fatalf("a disc with no title: %v", err)
	}
	if folder, ok := discOf("/films/Heat (1995)/BDMV/index.bdmv"); !ok || folder != "/films/Heat (1995)/BDMV" {
		t.Fatalf("the disc a file stands for: %q %v", folder, ok)
	}
	if _, ok := discOf("/films/Heat (1995)/BDMV/STREAM/00001.m2ts"); ok {
		t.Fatal("a clip inside a disc was taken for the disc")
	}
}

// With the disc reader installed, the two disc folders kept as fixtures are read for real.
// Where it is not installed this cannot be shown, and the test says so.
func TestTheDiscReaderReadsTheFixtureDiscs(t *testing.T) {
	reader := mediatools.Resolve("portico-disc")
	if _, err := exec.LookPath(reader); err != nil {
		t.Skip("the disc reader (portico-disc) is not installed here, so reading a real disc folder is not shown by this run")
	}
	for folder, kind := range map[string]string{"bluray-two-clip-bdmv/BDMV/index.bdmv": "bluray", "dvd-two-title-video-ts/VIDEO_TS/VIDEO_TS.IFO": "dvd"} {
		file, err := filepath.Abs(filepath.Join("..", "..", "..", "fixtures", "discs", folder))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(file); err != nil {
			t.Skipf("the disc fixture is not in this checkout: %v", err)
		}
		facts, err := FileProber{}.Probe(context.Background(), file)
		if err != nil || facts.Container != kind || facts.DurationMS < 60_000 || len(facts.Streams) == 0 {
			t.Errorf("%s: %+v, %v", folder, facts, err)
		}
	}
}
