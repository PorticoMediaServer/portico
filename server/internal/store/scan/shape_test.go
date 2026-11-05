package scan

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

func shapeIDs(n int) []int64 {
	ids := make([]int64, n)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	return ids
}

func cueOrdinals(n int) []int {
	ordinals := make([]int, n)
	for i := range ordinals {
		ordinals[i] = i + 1
	}
	return ordinals
}

func observedStreams(kind string, n, first int) []ObservedStream {
	streams := make([]ObservedStream, n)
	for i := range streams {
		streams[i] = ObservedStream{Index: first + i, Type: kind}
	}
	return streams
}

func TestBasicShapeAcceptsCompleteMaximumFrame(t *testing.T) {
	streams := observedStreams("audio", 255, 0)
	streams = append(streams, observedStreams("video", 255, 255)...)
	streams = append(streams, observedStreams("subtitle", 255, 510)...)
	raw := BasicShapeInput{
		LibraryID: 1, OrdinaryOwners: 1, CUEOrdinals: cueOrdinals(255),
		VersionAssetIDs: shapeIDs(16), AuthoredParentIDs: shapeIDs(100),
		Streams: streams, RawStreamCount: 765, RawChapterCount: 512,
	}
	// A complete cohort need not arrive in CUE order; identity bytes/ordinals
	// are preserved, not sorted or renumbered by scan preparation.
	raw.CUEOrdinals[0], raw.CUEOrdinals[254] = raw.CUEOrdinals[254], raw.CUEOrdinals[0]
	got, err := PrepareBasicShape(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.LibraryID != 1 || got.OrdinaryOwners != 1 || got.RawChapterCount != 512 || len(got.Streams) != 765 ||
		!reflect.DeepEqual(got.CUEOrdinals, raw.CUEOrdinals) || !reflect.DeepEqual(got.VersionAssetIDs, raw.VersionAssetIDs) ||
		!reflect.DeepEqual(got.AuthoredParentIDs, raw.AuthoredParentIDs) {
		t.Fatalf("maximum raw frame was changed: %+v", got)
	}
	classes := []writer.StreamClass{writer.AudioStream, writer.VideoStream, writer.SubtitleStream}
	for i, stream := range got.Streams {
		if stream.Index != i || stream.Class != classes[i/255] {
			t.Fatalf("stream %d = %+v", i, stream)
		}
	}
	if err := writer.ValidateScanShape(got); err != nil {
		t.Fatalf("adapter produced a shape rejected by writer: %v", err)
	}
	// Both layers operate on independent buffers. W must still revalidate its
	// current database state; copying the hint is not a final-count proof.
	raw.Streams[0].Index = -1
	raw.CUEOrdinals[0] = -1
	raw.VersionAssetIDs[0] = -1
	raw.AuthoredParentIDs[0] = -1
	if got.Streams[0].Index != 0 || got.CUEOrdinals[0] != 255 || got.VersionAssetIDs[0] != 1 || got.AuthoredParentIDs[0] != 1 {
		t.Fatal("prepared shape aliases caller buffers")
	}
	got.Streams[1].Index = -2
	got.CUEOrdinals[1] = -2
	got.VersionAssetIDs[1] = -2
	got.AuthoredParentIDs[1] = -2
	if raw.Streams[1].Index != 1 || raw.CUEOrdinals[1] != 2 || raw.VersionAssetIDs[1] != 2 || raw.AuthoredParentIDs[1] != 2 {
		t.Fatal("caller buffers alias prepared shape")
	}
}

func TestBasicShapeRejectsOverflowWithoutTruncation(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*BasicShapeInput)
	}{
		{"audio256", func(b *BasicShapeInput) { b.Streams = observedStreams("audio", 256, 0); b.RawStreamCount = 256 }},
		{"video256", func(b *BasicShapeInput) { b.Streams = observedStreams("video", 256, 0); b.RawStreamCount = 256 }},
		{"subtitle256", func(b *BasicShapeInput) { b.Streams = observedStreams("subtitle", 256, 0); b.RawStreamCount = 256 }},
		{"total766", func(b *BasicShapeInput) { b.Streams = observedStreams("audio", 766, 0); b.RawStreamCount = 766 }},
		{"rawtotal766", func(b *BasicShapeInput) { b.RawStreamCount = 766 }},
		{"chapters513 before cleanup", func(b *BasicShapeInput) { b.RawChapterCount = 513 }},
		{"cue256", func(b *BasicShapeInput) { b.CUEOrdinals = cueOrdinals(256) }},
		{"episodes9", func(b *BasicShapeInput) { b.EpisodeIDs = shapeIDs(9) }},
		{"final versions17", func(b *BasicShapeInput) { b.VersionAssetIDs = shapeIDs(17) }},
		{"asset-wide parents101", func(b *BasicShapeInput) { b.AuthoredParentIDs = shapeIDs(101) }},
		{"ordinary owners2", func(b *BasicShapeInput) { b.OrdinaryOwners = 2 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := BasicShapeInput{LibraryID: 1, OrdinaryOwners: 1}
			tc.edit(&raw)
			got, err := PrepareBasicShape(raw)
			if !errors.Is(err, writer.ErrCap) || !reflect.DeepEqual(got, writer.ScanShape{}) {
				t.Fatalf("overflow returned a partial shape: %+v, %v", got, err)
			}
		})
	}
}

func TestBasicShapeRejectsFilteredOrInvalidProbe(t *testing.T) {
	private := strings.Repeat("private-probe-value", 1<<10)
	for _, tc := range []struct {
		name string
		edit func(*BasicShapeInput)
	}{
		{"filtered stream prefix", func(b *BasicShapeInput) { b.RawStreamCount = 256; b.Streams = observedStreams("audio", 255, 0) }},
		{"missing count", func(b *BasicShapeInput) { b.Streams = observedStreams("audio", 1, 0) }},
		{"excess supplied streams", func(b *BasicShapeInput) { b.RawStreamCount = 1; b.Streams = observedStreams("audio", 2, 0) }},
		{"negative raw count", func(b *BasicShapeInput) { b.RawStreamCount = -1 }},
		{"negative raw chapters", func(b *BasicShapeInput) { b.RawChapterCount = -1 }},
		{"unknown class", func(b *BasicShapeInput) {
			b.Streams = []ObservedStream{{Index: 0, Type: private}}
			b.RawStreamCount = 1
		}},
		{"duplicate global index", func(b *BasicShapeInput) {
			b.Streams = []ObservedStream{{Index: 0, Type: "audio"}, {Index: 0, Type: "subtitle"}}
			b.RawStreamCount = 2
		}},
		{"negative index", func(b *BasicShapeInput) {
			b.Streams = []ObservedStream{{Index: -1, Type: "video"}}
			b.RawStreamCount = 1
		}},
		{"mixed cue and episode", func(b *BasicShapeInput) { b.CUEOrdinals = []int{1}; b.EpisodeIDs = []int64{1} }},
		{"cue missing ordinal", func(b *BasicShapeInput) { b.CUEOrdinals = []int{1, 3} }},
		{"cue duplicate ordinal", func(b *BasicShapeInput) { b.CUEOrdinals = []int{1, 1} }},
		{"duplicate version IDs", func(b *BasicShapeInput) { b.VersionAssetIDs = []int64{1, 1} }},
		{"duplicate episode IDs", func(b *BasicShapeInput) { b.EpisodeIDs = []int64{1, 1} }},
		{"non-native parent ID", func(b *BasicShapeInput) { b.AuthoredParentIDs = []int64{0} }},
		{"missing library", func(b *BasicShapeInput) { b.LibraryID = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := BasicShapeInput{LibraryID: 1, OrdinaryOwners: 1}
			tc.edit(&raw)
			got, err := PrepareBasicShape(raw)
			if !errors.Is(err, writer.ErrInvalid) || !reflect.DeepEqual(got, writer.ScanShape{}) {
				t.Fatalf("invalid observation returned a partial shape: %+v, %v", got, err)
			}
			if strings.Contains(err.Error(), private) {
				t.Fatal("policy error exposed raw probe text")
			}
		})
	}
}

func TestBasicShapeEpisodeVersionsAndEmptyProbe(t *testing.T) {
	// Final versions already contain an idempotent retry's source once; this
	// adapter accepts the complete final set without appending another version.
	raw := BasicShapeInput{LibraryID: 1, EpisodeIDs: shapeIDs(8), VersionAssetIDs: shapeIDs(16)}
	got, err := PrepareBasicShape(raw)
	if err != nil || !reflect.DeepEqual(got.EpisodeIDs, raw.EpisodeIDs) || len(got.VersionAssetIDs) != 16 {
		t.Fatalf("complete episode/version set = %+v, %v", got, err)
	}
	raw.EpisodeIDs[0] = -1
	if got.EpisodeIDs[0] != 1 {
		t.Fatal("episode buffer was not copied")
	}
	// Zero observations create no technical compatibility or availability fact.
	got, err = PrepareBasicShape(BasicShapeInput{LibraryID: 1})
	if err != nil || len(got.Streams) != 0 || got.RawChapterCount != 0 {
		t.Fatalf("empty shape = %+v, %v", got, err)
	}
}
