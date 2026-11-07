package writer

import (
	"math"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/medialimits"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Closed typed normalized rows. No SQL/table selector, arbitrary JSON frame,
// callback, public principal, source proof or resource capability is accepted.
type basicMediaRow interface{ isBasicMediaRow() }
type basicChapterFact struct {
	revision            int64
	status, fingerprint string
}
type basicChapter struct {
	index      int64
	title      string
	start, end float64
}
type basicSubtitleFact struct {
	revision, originUS  int64
	timingKnown         bool
	status, fingerprint string
}
type basicSubtitleSidecar struct {
	id, path, digest string
	size, modifiedNS int64
}
type basicSubtitle struct {
	id, origin, locator, sidecarID, format, language, title, reason string
	streamIndex                                                     int64
	isDefault, isForced, hearingImpaired                            bool
}
type basicSubtitleEvidence struct {
	revision int64
	evidence string
}

func (basicChapterFact) isBasicMediaRow()      {}
func (basicChapter) isBasicMediaRow()          {}
func (basicSubtitleFact) isBasicMediaRow()     {}
func (basicSubtitleSidecar) isBasicMediaRow()  {}
func (basicSubtitle) isBasicMediaRow()         {}
func (basicSubtitleEvidence) isBasicMediaRow() {}

// Values correspond to the exact native columns AFTER change_id. The scalar
// debt formula is the adopted replacement of each removed staging trigger;
// it is not a grant or physical-memory estimate. Bound every TEXT before args.
func basicMediaValues(source basicStreamFactStage, ordinal int64, value basicMediaRow) (family int64, id stmt.ID, args []any, charge int64, err error) {
	texts, family, id, charge, err := basicMediaTextFields(source, value)
	if err != nil {
		return 0, "", nil, 0, err
	}
	for _, s := range texts {
		if int64(len(s)) > 33554432-charge {
			return 0, "", nil, 0, ErrCap
		}
		charge += int64(len(s))
	}
	// A native sidecar path is not an opaque metadata/revision field. Preserve
	// CHANGE-003's typed representability cause after complete row admission;
	// NUL remains invalid input, never poison/skip authority.
	var nativePath string
	switch r := value.(type) {
	case basicSubtitleSidecar:
		nativePath = r.path
	case basicSubtitle:
		if r.origin == "sidecar" {
			nativePath = r.locator
		}
	}
	if strings.IndexByte(nativePath, 0) >= 0 {
		return 0, "", nil, 0, ErrInvalid
	}
	if !utf8.ValidString(nativePath) {
		return 0, "", nil, 0, &PolicyError{Field: "basic_subtitle_path", Cause: ErrFilenameUnrepresentable}
	}
	for _, s := range texts {
		if !utf8.ValidString(s) {
			return 0, "", nil, 0, ErrInvalid
		}
	}
	if ordinal < 0 {
		return 0, "", nil, 0, ErrInvalid
	}
	switch r := value.(type) {
	case basicStreamRow:
		if err = validateBasicStreamRow(r); err != nil {
			return 0, "", nil, 0, err
		}
		args = []any{ordinal, source.assetToken, r.index, r.streamType, r.codec, r.language, r.title, r.channels, r.channelLayout, basicBool(r.isDefault), basicBool(r.isForced), r.colorTransfer, r.colorPrimaries, r.detail}
	case basicStreamBreadth:
		if err = validateBasicStreamBreadth(r); err != nil {
			return 0, "", nil, 0, err
		}
		args = []any{ordinal, source.assetToken, r.index, basicBool(r.audioDescription), r.audioVariant, r.chroma, r.fpsNumerator, r.fpsDenominator, r.frameMode, r.interlace, basicBool(r.telecine), basicBool(r.doviRPU), basicBool(r.doviBase), basicBool(r.doviKnown), r.doviEnhancement, r.doviCM, r.playbackFramesRevision, r.packetPeakRevision}
	case basicChapterFact:
		if r.revision <= 0 || (r.status != "known" && r.status != "invalid") || r.fingerprint == "" || ordinal != 0 {
			return 0, "", nil, 0, ErrInvalid
		}
		args = []any{ordinal, source.assetToken, r.revision, source.size, source.modifiedNS, r.status, r.fingerprint}
	case basicChapter:
		if r.index < 0 || math.IsNaN(r.start) || math.IsNaN(r.end) || math.IsInf(r.start, 0) || math.IsInf(r.end, 0) || r.start < 0 || r.end <= r.start || len(r.title) > 500 {
			return 0, "", nil, 0, ErrInvalid
		}
		args = []any{ordinal, source.assetToken, r.index, r.title, r.start, r.end}
	case basicSubtitleFact:
		if r.revision <= 0 || r.status == "" || r.fingerprint == "" || ordinal != 0 {
			return 0, "", nil, 0, ErrInvalid
		}
		args = []any{ordinal, source.assetToken, r.revision, source.size, source.modifiedNS, r.originUS, basicBool(r.timingKnown), r.status, r.fingerprint}
	case basicSubtitleSidecar:
		if r.id == "" || r.path == "" || r.digest == "" || r.size < 0 || strings.IndexByte(r.path, 0) >= 0 {
			return 0, "", nil, 0, ErrInvalid
		}
		args = []any{ordinal, r.id, r.path, r.size, r.modifiedNS, r.digest}
	case basicSubtitle:
		var sidecar any
		if r.origin == "embedded" {
			if r.sidecarID != "" || r.streamIndex < 0 {
				return 0, "", nil, 0, ErrInvalid
			}
		} else if r.origin == "sidecar" {
			if r.sidecarID == "" || r.streamIndex != -1 {
				return 0, "", nil, 0, ErrInvalid
			}
			sidecar = r.sidecarID
		} else {
			return 0, "", nil, 0, ErrInvalid
		}
		if r.id == "" || r.locator == "" || strings.IndexByte(r.locator, 0) >= 0 {
			return 0, "", nil, 0, ErrInvalid
		}
		args = []any{ordinal, r.id, source.assetToken, r.origin, r.locator, r.streamIndex, sidecar, r.format, r.language, r.title, basicBool(r.isDefault), basicBool(r.isForced), r.reason, basicBool(r.hearingImpaired)}
	case basicSubtitleEvidence:
		if r.revision <= 0 || r.evidence == "" || ordinal != 0 {
			return 0, "", nil, 0, ErrInvalid
		}
		args = []any{ordinal, source.assetToken, r.revision, r.evidence}
	}
	return family, id, args, charge, nil
}

func basicMediaFamilyLimit(family int64) int64 {
	switch family {
	case 2, 3:
		return 3 * MaxStreamsPerClass
	case 4, 6, 9:
		return 1
	case 5:
		return MaxRawChapters
	case 7, 8:
		return medialimits.MaxSubtitleTracks
	}
	return 0
}

// Fixed-size value array lets whole-frame admission precede ALL row/argument
// allocation. Trailing empty entries cost zero bytes.
func basicMediaTextFields(source basicStreamFactStage, value basicMediaRow) (texts [9]string, family int64, id stmt.ID, base int64, err error) {
	switch r := value.(type) {
	case basicStreamRow:
		texts = [9]string{source.assetToken, r.streamType, r.codec, r.language, r.title, r.channelLayout, r.colorTransfer, r.colorPrimaries, r.detail}
		family, id, base = 2, basicMediaStreamInsert, 248
	case basicStreamBreadth:
		texts = [9]string{source.assetToken, r.audioVariant, r.chroma, r.frameMode, r.interlace, r.doviEnhancement, r.doviCM, r.playbackFramesRevision, r.packetPeakRevision}
		family, id, base = 3, basicMediaBreadthInsert, 280
	case basicChapterFact:
		texts = [9]string{source.assetToken, r.status, r.fingerprint}
		family, id, base = 4, basicMediaChapterFactInsert, 192
	case basicChapter:
		texts = [9]string{source.assetToken, r.title}
		family, id, base = 5, basicMediaChapterInsert, 184
	case basicSubtitleFact:
		texts = [9]string{source.assetToken, r.status, r.fingerprint}
		family, id, base = 6, basicMediaSubtitleFactInsert, 208
	case basicSubtitleSidecar:
		texts = [9]string{r.id, r.path, r.digest}
		family, id, base = 7, basicMediaSidecarInsert, 184
	case basicSubtitle:
		texts = [9]string{r.id, source.assetToken, r.origin, r.locator, r.sidecarID, r.format, r.language, r.title, r.reason}
		family, id, base = 8, basicMediaSubtitleInsert, 248
	case basicSubtitleEvidence:
		texts = [9]string{source.assetToken, r.evidence}
		family, id, base = 9, basicMediaEvidenceInsert, 168
	default:
		err = ErrInvalid
	}
	return
}
func cloneBasicMediaRow(value basicMediaRow) basicMediaRow {
	switch r := value.(type) {
	case basicStreamRow:
		return cloneBasicStreamRow(r)
	case basicStreamBreadth:
		return cloneBasicStreamBreadth(r)
	case basicChapterFact:
		r.status = strings.Clone(r.status)
		r.fingerprint = strings.Clone(r.fingerprint)
		return r
	case basicChapter:
		r.title = strings.Clone(r.title)
		return r
	case basicSubtitleFact:
		r.status = strings.Clone(r.status)
		r.fingerprint = strings.Clone(r.fingerprint)
		return r
	case basicSubtitleSidecar:
		r.id = strings.Clone(r.id)
		r.path = strings.Clone(r.path)
		r.digest = strings.Clone(r.digest)
		return r
	case basicSubtitle:
		r.id = strings.Clone(r.id)
		r.origin = strings.Clone(r.origin)
		r.locator = strings.Clone(r.locator)
		r.sidecarID = strings.Clone(r.sidecarID)
		r.format = strings.Clone(r.format)
		r.language = strings.Clone(r.language)
		r.title = strings.Clone(r.title)
		r.reason = strings.Clone(r.reason)
		return r
	case basicSubtitleEvidence:
		r.evidence = strings.Clone(r.evidence)
		return r
	}
	return nil
}
