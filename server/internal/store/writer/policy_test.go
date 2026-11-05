package writer

import (
	"errors"
	"strings"
	"testing"
)

func maxScan() ScanShape {
	s := ScanShape{LibraryID: 1, OrdinaryOwners: 1, RawChapterCount: MaxRawChapters}
	for i := 1; i <= MaxCUEChildren; i++ {
		s.CUEOrdinals = append(s.CUEOrdinals, i)
	}
	for i := 1; i <= MaxVersions; i++ {
		s.VersionAssetIDs = append(s.VersionAssetIDs, int64(i))
	}
	for i := 1; i <= MaxAuthoredParents; i++ {
		s.AuthoredParentIDs = append(s.AuthoredParentIDs, int64(i))
	}
	for c := AudioStream; c <= SubtitleStream; c++ {
		for i := 0; i < MaxStreamsPerClass; i++ {
			s.Streams = append(s.Streams, Stream{int(c-1)*MaxStreamsPerClass + i, c})
		}
	}
	return s
}

func TestScanCapsAndFinalUnion(t *testing.T) {
	if err := ValidateScanShape(maxScan()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*ScanShape)
		want   error
	}{
		{"library", func(s *ScanShape) { s.LibraryID = 0 }, ErrInvalid},
		{"owners", func(s *ScanShape) { s.OrdinaryOwners = 2 }, ErrCap},
		{"negative owners", func(s *ScanShape) { s.OrdinaryOwners = -1 }, ErrInvalid},
		{"cue cap", func(s *ScanShape) { s.CUEOrdinals = append(s.CUEOrdinals, 256) }, ErrCap},
		{"cue missing", func(s *ScanShape) { s.CUEOrdinals[0] = 256 }, ErrInvalid},
		{"cue duplicate", func(s *ScanShape) { s.CUEOrdinals[0] = 2 }, ErrInvalid},
		{"cue zero", func(s *ScanShape) { s.CUEOrdinals[0] = 0 }, ErrInvalid},
		{"mixed", func(s *ScanShape) { s.EpisodeIDs = []int64{1} }, ErrInvalid},
		{"episodes", func(s *ScanShape) { s.CUEOrdinals = nil; s.EpisodeIDs = []int64{1, 2, 3, 4, 5, 6, 7, 8, 9} }, ErrCap},
		{"version union", func(s *ScanShape) { s.VersionAssetIDs = append(s.VersionAssetIDs, 17) }, ErrCap},
		{"version duplicate", func(s *ScanShape) { s.VersionAssetIDs[0] = 2 }, ErrInvalid},
		{"parent union", func(s *ScanShape) { s.AuthoredParentIDs = append(s.AuthoredParentIDs, 101) }, ErrCap},
		{"parent duplicate", func(s *ScanShape) { s.AuthoredParentIDs[0] = 2 }, ErrInvalid},
		{"parent negative", func(s *ScanShape) { s.AuthoredParentIDs[0] = -1 }, ErrInvalid},
		{"chapters before cleanup", func(s *ScanShape) { s.RawChapterCount = 513 }, ErrCap},
		{"negative chapters", func(s *ScanShape) { s.RawChapterCount = -1 }, ErrInvalid},
		{"cross class duplicate index", func(s *ScanShape) { s.Streams[255].Index = 0 }, ErrInvalid},
		{"negative stream index", func(s *ScanShape) { s.Streams[0].Index = -1 }, ErrInvalid},
		{"unknown class", func(s *ScanShape) { s.Streams[0].Class = 0 }, ErrInvalid},
		{"class cap under total", func(s *ScanShape) { s.Streams[255].Class = AudioStream }, ErrCap},
		{"total streams", func(s *ScanShape) { s.Streams = append(s.Streams, Stream{765, AudioStream}) }, ErrCap},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := maxScan()
			tc.mutate(&s)
			if err := ValidateScanShape(s); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
	s := maxScan()
	s.CUEOrdinals = nil
	s.EpisodeIDs = []int64{8, 7, 6, 5, 4, 3, 2, 1}
	if err := ValidateScanShape(s); err != nil {
		t.Fatal(err)
	}
	s.EpisodeIDs[0] = 1
	if err := ValidateScanShape(s); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestMetadataCapsCountUnicodeCharacters(t *testing.T) {
	if err := ValidateMetadataShape(strings.Repeat("界", 1000), strings.Repeat("🟢", 20000), 64, 128, 128); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		title, overview string
		g, t, l         int
		want            error
	}{
		{strings.Repeat("界", 1001), "", 0, 0, 0, ErrCap},
		{"", strings.Repeat("a", 20001), 0, 0, 0, ErrCap},
		{string([]byte{0xff}), "", 0, 0, 0, ErrInvalid},
		{"", "", 65, 0, 0, ErrCap}, {"", "", 0, 129, 0, ErrCap}, {"", "", 0, 0, 129, ErrCap},
		{"", "", -1, 0, 0, ErrInvalid},
	} {
		if err := ValidateMetadataShape(tc.title, tc.overview, tc.g, tc.t, tc.l); !errors.Is(err, tc.want) {
			t.Fatalf("got %v, want %v", err, tc.want)
		}
	}
	for _, tc := range []struct {
		n    int
		want error
	}{{0, nil}, {100, nil}, {101, ErrCap}, {-1, ErrInvalid}} {
		if err := ValidateLibraryCount(tc.n); !errors.Is(err, tc.want) {
			t.Fatalf("%d: %v", tc.n, err)
		}
	}
}

func TestFrozenCapLiteralBoundaries(t *testing.T) {
	// Expected values are literals from the approved policy, not the constants
	// under test. Raising a policy constant must fail these controls.
	for _, tc := range []struct {
		name      string
		got, want int64
	}{
		{"libraries", MaxLibraries, 100}, {"asset parents", MaxAuthoredParents, 100}, {"item collections", MaxAuthoredCollectionsPerItem, 100},
		{"CUE", MaxCUEChildren, 255}, {"episodes", MaxEpisodes, 8}, {"versions", MaxVersions, 16}, {"streams/class", MaxStreamsPerClass, 255},
		{"chapters", MaxRawChapters, 512}, {"genres", MaxGenres, 64}, {"tags", MaxTags, 128}, {"labels", MaxLabels, 128},
		{"title", MaxTitleCharacters, 1000}, {"overview", MaxOverviewCharacters, 20000}, {"playlist", MaxPlaylistEntries, 100000}, {"image bytes", MaxEmbeddedImageBytes, 20_000_000},
	} {
		if tc.got != tc.want {
			t.Fatalf("%s changed: %d want %d", tc.name, tc.got, tc.want)
		}
	}
	ids := func(n int) []int64 {
		out := make([]int64, n)
		for i := range out {
			out[i] = int64(i + 1)
		}
		return out
	}
	for _, tc := range []struct {
		name  string
		n     int
		check func(int) error
	}{
		{"libraries", 100, ValidateLibraryCount}, {"playlist", 100000, ValidatePlaylistEntries},
		{"image bytes", 20_000_000, func(n int) error { return ValidateEmbeddedImageBytes(int64(n)) }},
		{"item collections", 100, func(n int) error { return ValidateItemAuthoredCollections(ids(n)) }},
		{"asset parent union", 100, func(n int) error { return ValidateScanShape(ScanShape{LibraryID: 1, AuthoredParentIDs: ids(n)}) }},
		{"CUE", 255, func(n int) error {
			ord := make([]int, n)
			for i := range ord {
				ord[i] = i + 1
			}
			return ValidateScanShape(ScanShape{LibraryID: 1, CUEOrdinals: ord})
		}},
		{"episodes", 8, func(n int) error { return ValidateScanShape(ScanShape{LibraryID: 1, EpisodeIDs: ids(n)}) }},
		{"versions", 16, func(n int) error { return ValidateScanShape(ScanShape{LibraryID: 1, VersionAssetIDs: ids(n)}) }},
		{"chapters", 512, func(n int) error { return ValidateScanShape(ScanShape{LibraryID: 1, RawChapterCount: n}) }},
		{"genres", 64, func(n int) error { return ValidateMetadataShape("", "", n, 0, 0) }},
		{"tags", 128, func(n int) error { return ValidateMetadataShape("", "", 0, n, 0) }},
		{"labels", 128, func(n int) error { return ValidateMetadataShape("", "", 0, 0, n) }},
		{"title", 1000, func(n int) error { return ValidateMetadataShape(strings.Repeat("a", n), "", 0, 0, 0) }},
		{"overview", 20000, func(n int) error { return ValidateMetadataShape("", strings.Repeat("a", n), 0, 0, 0) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.check(tc.n); err != nil {
				t.Fatal(err)
			}
			if err := tc.check(tc.n + 1); !errors.Is(err, ErrCap) {
				t.Fatalf("N+1 not refused: %v", err)
			}
		})
	}
	for _, class := range []StreamClass{AudioStream, VideoStream, SubtitleStream} {
		streams := make([]Stream, 256)
		for i := range streams {
			streams[i] = Stream{Index: i, Class: class}
		}
		if err := ValidateScanShape(ScanShape{LibraryID: 1, Streams: streams[:255]}); err != nil {
			t.Fatal(err)
		}
		if err := ValidateScanShape(ScanShape{LibraryID: 1, Streams: streams}); !errors.Is(err, ErrCap) {
			t.Fatal(err)
		}
	}
	if err := ValidateEmbeddedImageBytes(-1); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := ValidatePlaylistEntries(-1); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	for _, bad := range [][]int64{{0}, {1, 1}} {
		if err := ValidateItemAuthoredCollections(bad); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
}
