package scan

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// PreparedBasic freezes the complete input/reuse vector and raw/final cap
// shape. It contains no technical output, native proof, command, receipt or
// Basic-ready claim. W's accepting command must independently recheck both.
type PreparedBasic struct {
	input BasicInput
	shape writer.ScanShape
	bytes int64
}

func (p PreparedBasic) Input() BasicInput { return p.input }
func (p PreparedBasic) Bytes() int64      { return p.bytes }

// Shape returns an owned bounded copy; a consumer cannot mutate queued input.
// The driver must retain custody of that distinct copy until actual release.
func (p PreparedBasic) Shape() writer.ScanShape {
	s := p.shape
	s.CUEOrdinals = slices.Clone(s.CUEOrdinals)
	s.EpisodeIDs = slices.Clone(s.EpisodeIDs)
	s.VersionAssetIDs = slices.Clone(s.VersionAssetIDs)
	s.AuthoredParentIDs = slices.Clone(s.AuthoredParentIDs)
	s.Streams = slices.Clone(s.Streams)
	return s
}

// PrepareBasic bounds ALL input string payloads before validation/copying and
// applies W's existing raw/final shape policy. payloadLimit counts string bytes,
// not heap/RSS or a grant. Driver reservation must already cover real working
// memory; this function mints no resource, source, stage or reuse authority.
func PrepareBasic(in BasicInput, raw BasicShapeInput, payloadLimit int64) (PreparedBasic, error) {
	if payloadLimit <= 0 {
		return PreparedBasic{}, ErrCapacity
	}
	if len(raw.Streams) > 3*writer.MaxStreamsPerClass {
		return PreparedBasic{}, &writer.PolicyError{Field: "streams", Limit: 3 * writer.MaxStreamsPerClass, Cause: writer.ErrCap}
	}
	remaining := payloadLimit
	texts := [...]*string{&in.File.Source.SourceID, &in.File.Source.Incarnation, &in.File.Source.Revision,
		&in.File.RelativePath, &in.File.ObjectIdentity, &in.File.Revision, &in.ProbeRecipe,
		&in.NFO.Revision, &in.Artwork.Revision, &in.Subtitles.Revision}
	for _, v := range texts {
		if int64(len(*v)) > remaining {
			return PreparedBasic{}, ErrCapacity
		}
		remaining -= int64(len(*v))
	}
	for _, stream := range raw.Streams {
		if int64(len(stream.Type)) > remaining {
			return PreparedBasic{}, ErrCapacity
		}
		remaining -= int64(len(stream.Type))
	}
	// Preserve CHANGE-003's native filename cause before fs.ValidPath in
	// reusable() can collapse it. NUL remains invalid input, never poison.
	if strings.IndexByte(in.File.RelativePath, 0) >= 0 {
		return PreparedBasic{}, ErrInvalid
	}
	if !utf8.ValidString(in.File.RelativePath) {
		return PreparedBasic{}, &writer.PolicyError{Field: "basic_relative_path", Cause: writer.ErrFilenameUnrepresentable}
	}
	if !in.reusable() || raw.LibraryID != in.File.Source.LibraryID {
		return PreparedBasic{}, ErrInvalid
	}
	for _, v := range texts {
		if !utf8.ValidString(*v) {
			return PreparedBasic{}, ErrInvalid
		}
	}
	shape, err := PrepareBasicShape(raw)
	if err != nil {
		return PreparedBasic{}, err
	}
	for _, v := range texts {
		*v = strings.Clone(*v)
	}
	return PreparedBasic{input: in, shape: shape, bytes: payloadLimit - remaining}, nil
}
