package writer

import (
	"math"
	"reflect"
	"strings"
)

// Closed BasicStage/BasicSeal operations. Each command stages or seals exactly
// ONE family of the SAME begun catalog_info_changes row on the Basic receipt
// owner (inventory_runs). Neither creates a change, pins a target, copies,
// activates or publishes; those remain distinct closed steps.
type basicControlOperation uint8

const (
	basicControlStreamFact   basicControlOperation = iota + 1 // family 1 stream fact
	basicControlMediaRows                                     // one media family 2..9, bounded row range for Stage
	basicControlProbeScalars                                  // Stage: coherent raw NEW scalar tuple (phase 0)
	basicControlPinShape                                      // Stage: pin the target shape, copy_family 0->1 (phase 0)
	basicControlFreeze                                        // Stage: phase 0->1 after nine sealed families
	basicControlSelectEntity                                  // Stage: phase 1->2, after_entity = sole leaf
	basicControlCopyFamily                                    // Stage: one copy turn of one family (phase 2)
	basicControlAbandon                                       // Stage: one abandon turn of an unaccepted input
	basicControlResume                                        // Stage: reset a dropped FIRST walk and copy one native cursor turn
)

// Exactly one pointer is set. A closed concrete variant keeps the canonical
// request encoder free of interface values; it is not a second row model.
type basicStageMediaRow struct {
	streamRow        *basicStreamRow
	streamBreadth    *basicStreamBreadth
	chapterFact      *basicChapterFact
	chapter          *basicChapter
	subtitleFact     *basicSubtitleFact
	subtitleSidecar  *basicSubtitleSidecar
	subtitle         *basicSubtitle
	subtitleEvidence *basicSubtitleEvidence
}

func basicStageMediaRowOf(value basicMediaRow) (basicStageMediaRow, error) {
	switch r := cloneBasicMediaRow(value).(type) {
	case basicStreamRow:
		return basicStageMediaRow{streamRow: &r}, nil
	case basicStreamBreadth:
		return basicStageMediaRow{streamBreadth: &r}, nil
	case basicChapterFact:
		return basicStageMediaRow{chapterFact: &r}, nil
	case basicChapter:
		return basicStageMediaRow{chapter: &r}, nil
	case basicSubtitleFact:
		return basicStageMediaRow{subtitleFact: &r}, nil
	case basicSubtitleSidecar:
		return basicStageMediaRow{subtitleSidecar: &r}, nil
	case basicSubtitle:
		return basicStageMediaRow{subtitle: &r}, nil
	case basicSubtitleEvidence:
		return basicStageMediaRow{subtitleEvidence: &r}, nil
	}
	return basicStageMediaRow{}, ErrInvalid
}

func (v basicStageMediaRow) row() (basicMediaRow, error) {
	var out basicMediaRow
	n := 0
	if v.streamRow != nil {
		out, n = *v.streamRow, n+1
	}
	if v.streamBreadth != nil {
		out, n = *v.streamBreadth, n+1
	}
	if v.chapterFact != nil {
		out, n = *v.chapterFact, n+1
	}
	if v.chapter != nil {
		out, n = *v.chapter, n+1
	}
	if v.subtitleFact != nil {
		out, n = *v.subtitleFact, n+1
	}
	if v.subtitleSidecar != nil {
		out, n = *v.subtitleSidecar, n+1
	}
	if v.subtitle != nil {
		out, n = *v.subtitle, n+1
	}
	if v.subtitleEvidence != nil {
		out, n = *v.subtitleEvidence, n+1
	}
	if n != 1 {
		return nil, ErrInvalid
	}
	return cloneBasicMediaRow(out), nil
}

// The complete frozen original for one Stage or Seal command.
type basicControlRequest struct {
	original    basicOriginalScope
	purpose     BasicPurpose
	operation   basicControlOperation
	source      basicStreamFactStage
	family      int64 // 1 for the stream fact; the media family otherwise
	rawChapters int64
	rows        []basicStageMediaRow // complete family rows (media only)
	start, end  int64                // Stage media row range; zero for Seal and stream
	// Header-transition and copy operations only; zero otherwise.
	probe          *basicProbeEffects
	changeRevision int64 // catalog_info_changes.job_revision on entry
	entityID       int64 // the sole FIRST leaf entity
	shapeID        int64 // the pinned target shape
	copy           basicCopyCursor
	abandon        *basicAbandonExpectation
}

type basicStageCommand struct {
	identity CommandIdentity
	request  basicControlRequest
	digest   RequestDigest
	valid    bool
}

type basicSealCommand struct {
	identity CommandIdentity
	request  basicControlRequest
	digest   RequestDigest
	valid    bool
}

// The prepared media family for this request, rebuilt from the frozen rows.
func (in basicControlRequest) mediaFamily() (*basicMediaFamily, error) {
	if in.operation != basicControlMediaRows {
		return nil, ErrInvalid
	}
	rows := make([]basicMediaRow, len(in.rows))
	for n, v := range in.rows {
		r, err := v.row()
		if err != nil {
			return nil, err
		}
		rows[n] = r
	}
	return prepareBasicMediaFamily(in.source, in.family, in.rawChapters, rows)
}

func validateBasicControlRequest(identity CommandIdentity, in basicControlRequest, purpose BasicPurpose) error {
	if in.purpose != purpose || (purpose != BasicPurposeStage && purpose != BasicPurposeSeal) {
		return ErrInvalid
	}
	if err := basicOriginalPreflight(identity, in.original, in.source); err != nil {
		return err
	}
	s := in.source
	if s.changeID <= 0 || s.inputRevision <= 0 || s.expectedEpoch <= 0 || s.expectedEpoch == math.MaxInt64 || s.objectRevision == "" ||
		s.runID != in.original.scope.runID || s.libraryID != in.original.scope.libraryID || s.sourceID != in.original.source.id || s.incarnation != in.original.source.incarnation {
		return ErrInvalid
	}
	if in.operation == basicControlResume {
		return validateBasicResumeRequest(in, purpose)
	}
	if in.operation == basicControlAbandon {
		return validateBasicAbandonRequest(in, purpose)
	}
	if in.abandon != nil {
		return ErrInvalid
	}
	transition := in.probe != nil || in.changeRevision != 0 || in.entityID != 0 || in.shapeID != 0 || in.copy != (basicCopyCursor{})
	switch in.operation {
	case basicControlProbeScalars, basicControlPinShape, basicControlFreeze, basicControlSelectEntity, basicControlCopyFamily:
		return validateBasicTransitionRequest(in, purpose)
	}
	if transition {
		return ErrInvalid
	}
	switch in.operation {
	case basicControlStreamFact:
		if in.family != 1 || in.rawChapters != 0 || in.rows != nil || in.start != 0 || in.end != 0 {
			return ErrInvalid
		}
	case basicControlMediaRows:
		if in.family < 2 || in.family > 9 {
			return ErrInvalid
		}
		f, err := in.mediaFamily()
		if err != nil {
			return err
		}
		if purpose == BasicPurposeStage {
			if in.start < 0 || in.end <= in.start || in.end-in.start > basicStageMaxRows || in.end > int64(len(f.rows)) {
				return ErrInvalid
			}
		} else if in.start != 0 || in.end != 0 {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

// Header transitions and copy turns are Stage-only and carry no family rows.
func validateBasicTransitionRequest(in basicControlRequest, purpose BasicPurpose) error {
	if purpose != BasicPurposeStage || in.rawChapters != 0 || in.rows != nil || in.start != 0 || in.end != 0 {
		return ErrInvalid
	}
	switch in.operation {
	case basicControlProbeScalars, basicControlPinShape:
		if in.probe == nil || in.probe.source != in.source || in.family != 0 || in.changeRevision != 0 || in.entityID != 0 || in.shapeID != 0 || in.copy != (basicCopyCursor{}) {
			return ErrInvalid
		}
		if in.operation == basicControlPinShape && in.probe.before != in.probe.after {
			return ErrInvalid
		}
		return validateBasicProbeEffects(*in.probe)
	case basicControlFreeze, basicControlSelectEntity:
		if in.probe != nil || in.family != 0 || in.copy != (basicCopyCursor{}) {
			return ErrInvalid
		}
	case basicControlCopyFamily:
		if in.probe != nil || in.family < 1 || in.family > 9 {
			return ErrInvalid
		}
		if (in.family == 1 && in.copy != (basicCopyCursor{})) || (in.family > 1 && in.copy.family != in.family) {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	if in.changeRevision <= 0 || in.changeRevision == math.MaxInt64 || in.entityID <= 0 || in.shapeID <= 0 {
		return ErrInvalid
	}
	return nil
}

// Matches stageBasicMediaRows' own per-turn row bound.
const basicStageMaxRows = 8

func cloneBasicControlRequest(in basicControlRequest) basicControlRequest {
	in.original = cloneBasicOriginal(in.original)
	in.source = cloneBasicSource(in.source)
	if in.probe != nil {
		p := *in.probe
		p.source = cloneBasicSource(p.source)
		p.assetPath = strings.Clone(p.assetPath)
		p.before.container, p.before.videoCodec, p.before.audioCodec = strings.Clone(p.before.container), strings.Clone(p.before.videoCodec), strings.Clone(p.before.audioCodec)
		p.after.container, p.after.videoCodec, p.after.audioCodec = strings.Clone(p.after.container), strings.Clone(p.after.videoCodec), strings.Clone(p.after.audioCodec)
		in.probe = &p
	}
	if in.abandon != nil {
		a := *in.abandon
		a.after.container, a.after.videoCodec, a.after.audioCodec = strings.Clone(a.after.container), strings.Clone(a.after.videoCodec), strings.Clone(a.after.audioCodec)
		in.abandon = &a
	}
	in.copy.key, in.copy.key2, in.copy.key3 = strings.Clone(in.copy.key), strings.Clone(in.copy.key2), strings.Clone(in.copy.key3)
	if in.rows != nil {
		rows := make([]basicStageMediaRow, len(in.rows))
		for n, v := range in.rows {
			r, err := v.row()
			if err != nil {
				return basicControlRequest{} // validated before cloning; never reached
			}
			rows[n], _ = basicStageMediaRowOf(r)
		}
		in.rows = rows
	}
	return in
}

// Same complete-payload accounting as Accept, over this closed request.
func basicControlRequiredBytes(in basicControlRequest) (uint64, error) {
	remaining := uint64(maxCanonicalRequestBytes)
	if err := basicAcceptPayloadBytes(reflect.ValueOf(in), &remaining); err != nil {
		return 0, err
	}
	return 16384 + 64*(uint64(maxCanonicalRequestBytes)-remaining), nil
}

func newBasicStageCommand(identity CommandIdentity, in basicControlRequest) (*basicStageCommand, error) {
	if err := validateBasicControlRequest(identity, in, BasicPurposeStage); err != nil {
		return nil, err
	}
	if _, err := basicControlRequiredBytes(in); err != nil {
		return nil, err
	}
	in = cloneBasicControlRequest(in)
	digest, err := canonicalRequestDigest(BasicStage, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &basicStageCommand{identity, in, digest, true}, nil
}

func newBasicSealCommand(identity CommandIdentity, in basicControlRequest) (*basicSealCommand, error) {
	if err := validateBasicControlRequest(identity, in, BasicPurposeSeal); err != nil {
		return nil, err
	}
	if _, err := basicControlRequiredBytes(in); err != nil {
		return nil, err
	}
	in = cloneBasicControlRequest(in)
	digest, err := canonicalRequestDigest(BasicSeal, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &basicSealCommand{identity, in, digest, true}, nil
}

func basicControlReceiptRequest(identity CommandIdentity, digest RequestDigest, in basicControlRequest) BasicReceiptRequest {
	return BasicReceiptRequest{Purpose: in.purpose, Scan: ScanReceiptRequest{RunID: in.original.scope.runID, LibraryID: in.original.scope.libraryID,
		LibraryKey: in.original.scope.libraryKey, Receipt: ReceiptReference{Identity: identity, RequestDigest: digest}}}
}

func (c *basicStageCommand) receiptRequest() (BasicReceiptRequest, error) {
	if c == nil || !c.valid || c.request.purpose != BasicPurposeStage {
		return BasicReceiptRequest{}, ErrInvalid
	}
	return basicControlReceiptRequest(c.identity, c.digest, c.request), nil
}

func (c *basicSealCommand) receiptRequest() (BasicReceiptRequest, error) {
	if c == nil || !c.valid || c.request.purpose != BasicPurposeSeal {
		return BasicReceiptRequest{}, ErrInvalid
	}
	return basicControlReceiptRequest(c.identity, c.digest, c.request), nil
}
