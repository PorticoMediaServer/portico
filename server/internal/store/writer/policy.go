// Package writer owns schema-v1 write admission. It is not connected to the
// shipped server. Only its eventual typed dispatcher may execute writes;
// preparation packages may share the pure policy checks defined here.
package writer

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

const (
	MaxLibraries          = 100
	MaxAuthoredParents    = 100 // distinct shared parents across an asset's owners
	MaxCUEChildren        = 255
	MaxEpisodes           = 8
	MaxVersions           = 16
	MaxStreamsPerClass    = 255
	MaxRawChapters        = 512
	MaxGenres             = 64
	MaxTags               = 128
	MaxLabels             = 128
	MaxTitleCharacters    = 1000
	MaxOverviewCharacters = 20000
)

var (
	ErrInvalid = errors.New("invalid writer input")
	ErrCap     = errors.New("writer admission cap exceeded")
)

// PolicyError identifies a policy field without including private source values.
type PolicyError struct {
	Field string
	Limit int
	Cause error
}

func (e *PolicyError) Error() string {
	return fmt.Sprintf("%s: %v (limit %d)", e.Field, e.Cause, e.Limit)
}
func (e *PolicyError) Unwrap() error { return e.Cause }

type StreamClass uint8

const (
	AudioStream StreamClass = iota + 1
	VideoStream
	SubtitleStream
)

type Stream struct {
	Index int
	Class StreamClass
}

// ScanShape describes the FINAL merged asset state, not just an incoming delta.
// ID lists must be distinct, positive, same-library database identities. The
// writer must obtain or revalidate their library/source witnesses in its own
// accepting transaction. This pure preflight function cannot attest those facts.
// RawChapterCount is measured before cleanup/deduplication, never after it.
type ScanShape struct {
	LibraryID         int64
	OrdinaryOwners    int
	CUEOrdinals       []int // complete 1..N, in any order
	EpisodeIDs        []int64
	VersionAssetIDs   []int64  // final distinct versions of the accepting item
	Streams           []Stream // one source-global index namespace across all three classes
	RawChapterCount   int
	AuthoredParentIDs []int64 // final distinct asset-wide union, not derived counts
}

// ValidateScanShape bounds input lengths before inspecting values or allocating
// maps. There is intentionally no policy on server user count.
func ValidateScanShape(s ScanShape) error {
	if s.LibraryID <= 0 {
		return invalid("library_id")
	}
	for _, c := range []struct {
		field  string
		n, max int
	}{
		{"ordinary_owners", s.OrdinaryOwners, 1},
		{"cue_children", len(s.CUEOrdinals), MaxCUEChildren},
		{"episodes", len(s.EpisodeIDs), MaxEpisodes},
		{"versions", len(s.VersionAssetIDs), MaxVersions},
		{"streams", len(s.Streams), 3 * MaxStreamsPerClass},
		{"raw_chapters", s.RawChapterCount, MaxRawChapters},
		{"authored_parents", len(s.AuthoredParentIDs), MaxAuthoredParents},
	} {
		if err := count(c.field, c.n, c.max); err != nil {
			return err
		}
	}
	if len(s.CUEOrdinals) > 0 && len(s.EpisodeIDs) > 0 {
		return invalid("asset_envelope")
	}
	var cue [MaxCUEChildren + 1]bool
	for _, ordinal := range s.CUEOrdinals {
		if ordinal < 1 || ordinal > len(s.CUEOrdinals) || cue[ordinal] {
			return invalid("cue_ordinal")
		}
		cue[ordinal] = true
	}
	for _, list := range []struct {
		field string
		ids   []int64
	}{
		{"episode_id", s.EpisodeIDs}, {"version_asset_id", s.VersionAssetIDs}, {"authored_parent_id", s.AuthoredParentIDs},
	} {
		seen := make(map[int64]struct{}, len(list.ids))
		for _, id := range list.ids {
			if _, exists := seen[id]; id <= 0 || exists {
				return invalid(list.field)
			}
			seen[id] = struct{}{}
		}
	}
	seen := make(map[int]struct{}, len(s.Streams))
	var classes [4]int
	for _, stream := range s.Streams {
		if stream.Index < 0 || stream.Class < AudioStream || stream.Class > SubtitleStream {
			return invalid("stream")
		}
		if _, exists := seen[stream.Index]; exists {
			return invalid("stream_index")
		}
		seen[stream.Index] = struct{}{}
		classes[stream.Class]++
		if err := count("streams_per_class", classes[stream.Class], MaxStreamsPerClass); err != nil {
			return err
		}
	}
	return nil
}

// ValidateLibraryCount checks the final server library count under writer custody.
func ValidateLibraryCount(n int) error { return count("libraries", n, MaxLibraries) }

// ValidateMetadataShape validates already selected effective metadata. Credits
// are trimmed before persistence by the enrichment preparer, not silently here.
func ValidateMetadataShape(title, overview string, genres, tags, labels int) error {
	for _, text := range []struct {
		field, value string
		max          int
	}{
		{"title", title, MaxTitleCharacters}, {"overview", overview, MaxOverviewCharacters},
	} {
		// UTF-8 is at most four bytes per character; reject large bodies before scanning.
		if len(text.value) > 4*text.max {
			return &PolicyError{Field: text.field, Limit: text.max, Cause: ErrCap}
		}
		if !utf8.ValidString(text.value) {
			return invalid(text.field)
		}
		if err := count(text.field, utf8.RuneCountInString(text.value), text.max); err != nil {
			return err
		}
	}
	for _, c := range []struct {
		field  string
		n, max int
	}{
		{"genres", genres, MaxGenres}, {"tags", tags, MaxTags}, {"labels", labels, MaxLabels},
	} {
		if err := count(c.field, c.n, c.max); err != nil {
			return err
		}
	}
	return nil
}

func invalid(field string) error { return &PolicyError{Field: field, Cause: ErrInvalid} }
func count(field string, n, max int) error {
	if n < 0 {
		return invalid(field)
	}
	if n > max {
		return &PolicyError{Field: field, Limit: max, Cause: ErrCap}
	}
	return nil
}
