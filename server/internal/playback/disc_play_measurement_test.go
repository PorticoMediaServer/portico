//go:build linux || darwin

package playback

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/playbackdisc"
)

// These regressions exercise the pure measured-snapshot, title-selection,
// preparation-context and configured-input helpers. They use source fixtures
// only: no decoder, helper, provider, source actor or real media bytes.

func discPlaylistNumber(n int) *int { return &n }

func discMeasurementStructure() disc.Probe {
	return disc.Probe{
		Schema: 1, Kind: "bluray", Packaging: "iso", Encryption: "none",
		Titles: []disc.Title{
			{DiscOrder: 1, Playlist: discPlaylistNumber(800), DurationMs: 3_600_000, AngleCount: 1},
			{DiscOrder: 2, Playlist: discPlaylistNumber(801), DurationMs: 300_000, AngleCount: 1},
		},
	}
}

func discMeasurementTitle() disc.Title { return discMeasurementStructure().Titles[0] }

func discMeasurementPins() playbackdisc.ToolPins {
	return playbackdisc.ToolPins{DiscHelperDigest: "disc-helper-1", FFmpegDigest: "ffmpeg-1", FFprobeDigest: "ffprobe-1", RuntimeIdentity: "runtime-1"}
}

func discMeasurementFacts() assets.Facts {
	return assets.Facts{
		Container: "m2ts", VideoCodec: "h264", AudioCodec: "flac",
		Width: 1920, Height: 1080,
		Streams: []assets.Stream{
			{Index: 0, Type: "video", Codec: "h264", Default: true, Detail: assets.StreamDetail{Width: 1920, Height: 1080, BitDepth: 8, DynamicRange: assets.RangeSDR}},
			{Index: 1, Type: "audio", Codec: "flac", Channels: 2, Default: true, Language: "eng", Detail: assets.StreamDetail{StreamBreadth: assets.StreamBreadth{AudioVariant: "flac"}, SampleRate: 48_000, BitsPerSample: 24}},
		},
		Chapters: []assets.Chapter{{Title: "Opening", Start: 0, End: 120}},
	}
}

func discMeasuredSnapshot(t *testing.T, facts assets.Facts, revision string) playbackdisc.Snapshot {
	t.Helper()
	snapshot, e := measuredDiscSnapshot(playbackdisc.Snapshot{
		Actor:     playbackdisc.Actor{ServerID: "server-1"},
		Operation: playbackdisc.Operation{QueueID: "queue-1"},
	}, playbackdisc.Input{}, discMeasurementPins(), discMeasurementStructure(), discMeasurementTitle(), revision, facts)
	if e != nil {
		t.Fatal(e)
	}
	return snapshot
}

// The measured snapshot keeps the selected title and the full stream detail,
// breadth and dispositions, and fingerprints all three revision families.
func TestMeasuredDiscSnapshotPinsDetailAndRevisions(t *testing.T) {
	snapshot := discMeasuredSnapshot(t, discMeasurementFacts(), "structure-1")
	if snapshot.Disc.SelectedTitleKey != "bd:00800" || snapshot.Media.DurationMs != 3_600_000 {
		t.Fatalf("snapshot selected %q with duration %d", snapshot.Disc.SelectedTitleKey, snapshot.Media.DurationMs)
	}
	if snapshot.Disc.StructureRevision != "structure-1" || snapshot.Disc.StreamRevision == "" || snapshot.Disc.ChapterRevision == "" || snapshot.Disc.FactsRevision == "" {
		t.Fatal("snapshot did not carry its structure and measured revisions")
	}
	if snapshot.Media.Streams[1].Detail.SampleRate != 48_000 || snapshot.Media.Streams[1].Detail.AudioVariant != "flac" {
		t.Fatal("stream detail or breadth was dropped from the measured snapshot")
	}
	if snapshot.Actor.ServerID != "server-1" || snapshot.Operation.QueueID != "queue-1" {
		t.Fatal("template actor or operation was not preserved")
	}
	if snapshot.Tools != discMeasurementPins() {
		t.Fatal("measured snapshot did not retain the actual tool pins")
	}
	if same := discMeasuredSnapshot(t, discMeasurementFacts(), "structure-1"); !reflect.DeepEqual(snapshot, same) {
		t.Fatal("the same exact input and facts produced different revisions")
	}
}

// Each actual stream planning or disposition change moves StreamRevision and
// FactsRevision without touching ChapterRevision.
func TestMeasuredDiscSnapshotStreamDetailMovesRevisions(t *testing.T) {
	base := discMeasuredSnapshot(t, discMeasurementFacts(), "structure-1")
	cases := []struct {
		name   string
		mutate func(*assets.Facts)
	}{
		{"sample rate", func(f *assets.Facts) { f.Streams[1].Detail.SampleRate = 44_100 }},
		{"object audio", func(f *assets.Facts) { f.Streams[1].Detail.ObjectAudio = "atmos" }},
		{"audio variant", func(f *assets.Facts) { f.Streams[1].Detail.AudioVariant = "dtsx" }},
		{"hearing impaired", func(f *assets.Facts) { f.Streams[1].HearingImpaired = true }},
		{"commentary", func(f *assets.Facts) { f.Streams[1].Commentary = true }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			facts := discMeasurementFacts()
			test.mutate(&facts)
			changed := discMeasuredSnapshot(t, facts, "structure-1")
			if changed.Disc.StreamRevision == base.Disc.StreamRevision {
				t.Fatal("stream revision ignored an actual detail or disposition change")
			}
			if changed.Disc.FactsRevision == base.Disc.FactsRevision {
				t.Fatal("facts revision ignored an actual detail or disposition change")
			}
			if changed.Disc.ChapterRevision != base.Disc.ChapterRevision {
				t.Fatal("a stream change moved the chapter revision")
			}
		})
	}
}

// Actual chapter changes move ChapterRevision and FactsRevision, and a different
// structure revision scopes all three fingerprints.
func TestMeasuredDiscSnapshotChapterAndStructureScope(t *testing.T) {
	base := discMeasuredSnapshot(t, discMeasurementFacts(), "structure-1")
	facts := discMeasurementFacts()
	facts.Chapters[0].End = 121
	chapters := discMeasuredSnapshot(t, facts, "structure-1")
	if chapters.Disc.ChapterRevision == base.Disc.ChapterRevision || chapters.Disc.FactsRevision == base.Disc.FactsRevision {
		t.Fatal("a changed chapter did not move the chapter and facts revisions")
	}
	if chapters.Disc.StreamRevision != base.Disc.StreamRevision {
		t.Fatal("a chapter change moved the stream revision")
	}
	scoped := discMeasuredSnapshot(t, discMeasurementFacts(), "structure-2")
	if scoped.Disc.StructureRevision == base.Disc.StructureRevision || scoped.Disc.StreamRevision == base.Disc.StreamRevision ||
		scoped.Disc.ChapterRevision == base.Disc.ChapterRevision || scoped.Disc.FactsRevision == base.Disc.FactsRevision {
		t.Fatal("a different structure revision did not scope every measured revision")
	}
}

// Root65 supplies no disc-to-audio adapter: a file without actual video or any
// stream is refused instead of prepared.
func TestMeasuredDiscSnapshotRefusesNoVideoOrNoStreams(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*assets.Facts)
	}{
		{"no video", func(f *assets.Facts) { f.VideoCodec = "" }},
		{"no streams", func(f *assets.Facts) { f.Streams = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			facts := discMeasurementFacts()
			test.mutate(&facts)
			snapshot, e := measuredDiscSnapshot(playbackdisc.Snapshot{}, playbackdisc.Input{}, discMeasurementPins(), discMeasurementStructure(), discMeasurementTitle(), "structure-1", facts)
			if !errors.Is(e, ErrIncompatible) || snapshot.Disc.StructureRevision != "" {
				t.Fatalf("an unqualified source returned %v and a prepared snapshot", e)
			}
		})
	}
}

// An exact nonempty key resolves only its own title; the intended main title is
// the fallback for an empty key, never for an unknown or duplicate one.
func TestMeasuredDiscTitleResolvesExactKeyAndRefusesOthers(t *testing.T) {
	structure := discMeasurementStructure()
	title, e := measuredDiscTitle(structure, "bd:00801")
	if e != nil || title.Key(structure.Kind) != "bd:00801" || title.DurationMs != 300_000 {
		t.Fatalf("exact key resolved %q with %v", title.Key(structure.Kind), e)
	}
	if _, e = measuredDiscTitle(structure, "bd:09999"); !errors.Is(e, ErrStaleOffer) {
		t.Fatalf("unknown key returned %v", e)
	}
	duplicated := discMeasurementStructure()
	duplicated.Titles[1].Playlist = discPlaylistNumber(800)
	if _, e = measuredDiscTitle(duplicated, "bd:00800"); !errors.Is(e, ErrStaleOffer) {
		t.Fatalf("duplicate key returned %v", e)
	}
	main, e := measuredDiscTitle(structure, "")
	if e != nil || main.DurationMs != 3_600_000 {
		t.Fatalf("empty key resolved %d with %v", main.DurationMs, e)
	}
	short := discMeasurementStructure()
	short.Titles[0].DurationMs = 30_000
	short.Titles[1].DurationMs = 30_000
	if _, e = measuredDiscTitle(short, ""); !errors.Is(e, ErrStaleOffer) {
		t.Fatalf("missing main title returned %v", e)
	}
}

// Preparation requires the finite original Play wait: bounded, unexpired and
// already live.
func TestDiscPlayPreparationContextRequiresFiniteOriginalWait(t *testing.T) {
	for _, wait := range []time.Duration{time.Minute, 120 * time.Second} {
		ctx, cancel := context.WithTimeout(context.Background(), wait)
		t.Cleanup(cancel)
		if e := discPlayPreparationContext(ctx); e != nil {
			t.Fatalf("%s wait returned %v", wait, e)
		}
		cancel()
	}
	if e := discPlayPreparationContext(context.Background()); !errors.Is(e, disc.ErrProtocol) {
		t.Fatalf("missing deadline returned %v", e)
	}
	long, cancelLong := context.WithTimeout(context.Background(), 125*time.Second)
	defer cancelLong()
	if e := discPlayPreparationContext(long); !errors.Is(e, disc.ErrProtocol) {
		t.Fatalf("deadline beyond the shared wait returned %v", e)
	}
	expired, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelExpired()
	if e := discPlayPreparationContext(expired); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("expired wait returned %v", e)
	}
	stopped, cancelStopped := context.WithTimeout(context.Background(), time.Minute)
	cancelStopped()
	if e := discPlayPreparationContext(stopped); !errors.Is(e, context.Canceled) {
		t.Fatalf("canceled wait returned %v", e)
	}
}

// Only fixed, absolute, already-clean helper and probe paths are configured.
func TestDiscPlayConfiguredInputsRequiresFixedAbsoluteBinaries(t *testing.T) {
	tool := disc.Tool{Binary: "/usr/bin/portico-disc"}
	probe := assets.Probe{Binary: "/usr/bin/ffprobe"}
	if !discPlayConfiguredInputs(tool, probe) {
		t.Fatal("fixed absolute helper and probe were rejected")
	}
	for _, test := range []struct {
		name  string
		tool  disc.Tool
		probe assets.Probe
	}{
		{"empty helper", disc.Tool{}, probe},
		{"empty probe", tool, assets.Probe{}},
		{"relative helper", disc.Tool{Binary: "bin/portico-disc"}, probe},
		{"relative probe", tool, assets.Probe{Binary: "ffprobe"}},
		{"unclean helper", disc.Tool{Binary: "/usr/bin/../bin/portico-disc"}, probe},
		{"unclean probe", tool, assets.Probe{Binary: "/usr/bin/./ffprobe"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if discPlayConfiguredInputs(test.tool, test.probe) {
				t.Fatal("a non-fixed helper or probe path was accepted")
			}
		})
	}
}
