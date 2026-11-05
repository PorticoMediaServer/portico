package scan

import (
	"slices"

	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// ObservedStream is an unfiltered probe stream, before any cleanup or selection.
// Type must be the probe adapter's canonical audio/video/subtitle classification.
// Unknown classes and duplicate source-global indexes must not be filtered out.
type ObservedStream struct {
	Index int
	Type  string
}

// BasicShapeInput adapts a complete raw probe to the writer's admission policy.
// Membership lists describe the final merged set, including retained versions
// and the asset-wide union of authored parents. They are native database IDs,
// not source tokens or episode numbers. The accepting writer must independently
// obtain/recheck current same-library membership and its exact source witnesses.
type BasicShapeInput struct {
	LibraryID         int64
	OrdinaryOwners    int
	CUEOrdinals       []int
	EpisodeIDs        []int64
	VersionAssetIDs   []int64
	AuthoredParentIDs []int64
	Streams           []ObservedStream
	// Counts come from the parser BEFORE filtering, truncation, deduplication or
	// chapter cleanup. RawStreamCount must equal the complete supplied stream
	// list, so a retained prefix cannot pass as a complete observation.
	RawStreamCount  int
	RawChapterCount int
}

// PrepareBasicShape performs no IO, persistence, normalization or truncation.
// W owns every numerical policy limit and the final accepting check. This is a
// preparation hint, not same-library proof, a grant, or an Applied receipt.
// The returned shape owns its slices, independently of the caller's buffers.
// All allocations are bounded by W's caps; no raw probe text is copied here.
func PrepareBasicShape(raw BasicShapeInput) (writer.ScanShape, error) {
	shape := writer.ScanShape{
		LibraryID: raw.LibraryID, OrdinaryOwners: raw.OrdinaryOwners,
		CUEOrdinals: raw.CUEOrdinals, EpisodeIDs: raw.EpisodeIDs,
		VersionAssetIDs: raw.VersionAssetIDs, AuthoredParentIDs: raw.AuthoredParentIDs,
		RawChapterCount: raw.RawChapterCount,
	}
	// Validate every non-stream length/value before allocating mapped streams
	// or copying any incoming membership slice.
	if err := writer.ValidateScanShape(shape); err != nil {
		return writer.ScanShape{}, err
	}
	maxStreams := 3 * writer.MaxStreamsPerClass
	if raw.RawStreamCount > maxStreams || len(raw.Streams) > maxStreams {
		return writer.ScanShape{}, &writer.PolicyError{Field: "streams", Limit: maxStreams, Cause: writer.ErrCap}
	}
	if raw.RawStreamCount < 0 || raw.RawStreamCount != len(raw.Streams) {
		return writer.ScanShape{}, &writer.PolicyError{Field: "raw_stream_count", Cause: writer.ErrInvalid}
	}
	if len(raw.Streams) > 0 {
		shape.Streams = make([]writer.Stream, len(raw.Streams))
	}
	for i, stream := range raw.Streams {
		var class writer.StreamClass
		switch stream.Type {
		case "audio":
			class = writer.AudioStream
		case "video":
			class = writer.VideoStream
		case "subtitle":
			class = writer.SubtitleStream
		default:
			return writer.ScanShape{}, &writer.PolicyError{Field: "stream_class", Cause: writer.ErrInvalid}
		}
		shape.Streams[i] = writer.Stream{Index: stream.Index, Class: class}
	}
	// This checks per-class caps and the single index namespace across classes,
	// as well as the complete final membership/RAW chapter shape again.
	if err := writer.ValidateScanShape(shape); err != nil {
		return writer.ScanShape{}, err
	}
	shape.CUEOrdinals = slices.Clone(raw.CUEOrdinals)
	shape.EpisodeIDs = slices.Clone(raw.EpisodeIDs)
	shape.VersionAssetIDs = slices.Clone(raw.VersionAssetIDs)
	shape.AuthoredParentIDs = slices.Clone(raw.AuthoredParentIDs)
	return shape, nil
}
