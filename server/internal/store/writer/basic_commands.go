package writer

import (
	"reflect"
	"strings"
)

const basicStageRequestVersion uint16 = 3

type basicStageOperation uint8
type basicSealOperation uint8

const (
	basicStageBeginInput basicStageOperation = iota + 1
	basicStageFactInput
	basicStageMediaInput
	basicStageProbeInput
	basicStagePinShapeInput
	basicStageCopyInput
	basicStageRetireInput
)
const (
	basicSealStreamInput basicSealOperation = iota + 1
	basicSealMediaInput
)

// FIRST constructors cover only these implemented closed operations. Additional
// payloads require their own validated variant before the dispatcher enables
// them. No interface, callback, caller kind or runtime capability is stored.
type basicStageRequest struct {
	original       basicOriginalScope
	purpose        BasicPurpose
	operation      basicStageOperation
	begin          basicBeginEffects
	fact           basicStreamFactStage
	changeRevision int64 // existing change only; allocated BEGIN revision is native
	rows           basicRowsRequest
	start, end     int64 // bounded staging prefix, never a receipt COPY cursor
	probe          basicProbeEffects
	copy           basicCopyRequest
	retire         basicRetireFirstEffects
}
type basicSealRequest struct {
	original       basicOriginalScope
	purpose        BasicPurpose
	operation      basicSealOperation
	source         basicStreamFactStage
	changeRevision int64
	rows           basicRowsRequest
}
type basicStageCommand struct {
	identity CommandIdentity
	request  basicStageRequest
	digest   RequestDigest
	valid    bool
}
type basicSealCommand struct {
	identity CommandIdentity
	request  basicSealRequest
	digest   RequestDigest
	valid    bool
}

func newBasicStageCommand(identity CommandIdentity, in basicStageRequest) (*basicStageCommand, error) {
	source, err := basicStageSource(in)
	if err != nil {
		return nil, err
	}
	// Cover the complete simultaneous native payload BEFORE policy walks,
	// validation maps, derived rows, owned clones or the shared canonical hash.
	if _, err = basicStageRequiredBytes(in); err != nil {
		return nil, err
	}
	if err = basicOriginalPreflight(identity, in.original, source); err != nil {
		return nil, err
	}
	switch in.operation {
	case basicStageRetireInput:
		if _, err = basicRetireFirstCharge(in.retire); err != nil {
			return nil, err
		}
		if in.rows.family != 0 {
			in.rows, err = freezeBasicRowsRequest(source, in.rows)
			if err != nil {
				return nil, err
			}
			if basicRowsCount(in.rows) != in.retire.counts[in.rows.family-1] {
				return nil, ErrInvalid
			}
		}
	case basicStageBeginInput:
		if err = validateBasicOrdinaryCounts(source.libraryID, in.begin.shape, in.begin.counts); err != nil {
			return nil, err
		}
	case basicStageFactInput, basicStageMediaInput, basicStageProbeInput, basicStagePinShapeInput:
		revision := in.changeRevision
		if in.operation == basicStageProbeInput || in.operation == basicStagePinShapeInput {
			revision = in.probe.changeRevision
		}
		if source.changeID <= 0 || source.inputRevision <= 0 || revision <= 0 {
			return nil, invalid("basic_stage_fact")
		}
	}
	if in.operation == basicStageProbeInput || in.operation == basicStagePinShapeInput {
		if err = validateBasicProbeEffects(in.probe); err != nil {
			return nil, err
		}
	}
	if in.operation == basicStagePinShapeInput && in.probe.before != in.probe.after {
		return nil, ErrInvalid
	}
	if in.operation == basicStageMediaInput {
		in.rows, err = freezeBasicRowsRequest(source, in.rows)
		if err != nil {
			return nil, err
		}
	}
	if in.operation == basicStageCopyInput {
		in.copy, err = freezeBasicCopyRequest(in.copy)
		if err != nil {
			return nil, err
		}
	}
	in.original = cloneBasicOriginal(in.original)
	if in.operation == basicStageRetireInput {
		in.retire.source = cloneBasicSource(in.retire.source)
	} else if in.operation == basicStageBeginInput {
		in.begin.source = cloneBasicSource(in.begin.source)
		in.begin.shape.CUEOrdinals = cloneBasicRequestSlice(in.begin.shape.CUEOrdinals)
		in.begin.shape.EpisodeIDs = cloneBasicRequestSlice(in.begin.shape.EpisodeIDs)
		in.begin.shape.VersionAssetIDs = cloneBasicRequestSlice(in.begin.shape.VersionAssetIDs)
		in.begin.shape.Streams = cloneBasicRequestSlice(in.begin.shape.Streams)
		in.begin.shape.AuthoredParentIDs = cloneBasicRequestSlice(in.begin.shape.AuthoredParentIDs)
	} else if in.operation == basicStageProbeInput || in.operation == basicStagePinShapeInput {
		in.probe.source = cloneBasicSource(in.probe.source)
		in.probe.assetPath = strings.Clone(in.probe.assetPath)
		in.probe.before = cloneBasicProbeScalars(in.probe.before)
		in.probe.after = cloneBasicProbeScalars(in.probe.after)
	} else {
		in.fact = cloneBasicSource(in.fact)
	}
	digest, err := canonicalRequestDigest(BasicStage, basicStageRequestVersion, identity, in)
	if err != nil {
		return nil, err
	}
	return &basicStageCommand{identity: identity, request: in, digest: digest, valid: true}, nil
}

func basicStageSource(in basicStageRequest) (basicStreamFactStage, error) {
	if in.operation != basicStageRetireInput && in.retire != (basicRetireFirstEffects{}) {
		return basicStreamFactStage{}, ErrInvalid
	}
	if in.operation != basicStageCopyInput && !reflect.ValueOf(in.copy).IsZero() {
		return basicStreamFactStage{}, ErrInvalid
	}
	if in.operation != basicStageProbeInput && in.operation != basicStagePinShapeInput && in.probe != (basicProbeEffects{}) {
		return basicStreamFactStage{}, ErrInvalid
	}
	if in.purpose != BasicPurposeStage {
		return basicStreamFactStage{}, ErrInvalid
	}
	switch in.operation {
	case basicStageRetireInput:
		if !reflect.ValueOf(in.begin).IsZero() || in.fact != (basicStreamFactStage{}) || in.changeRevision != 0 {
			return basicStreamFactStage{}, ErrInvalid
		}
		if in.rows.family == 0 {
			if !reflect.ValueOf(in.rows).IsZero() || in.start != 0 || in.end != 0 {
				return basicStreamFactStage{}, ErrInvalid
			}
		} else if in.rows.family < 2 || in.rows.family > 9 || in.start < 0 || in.end <= in.start || in.end-in.start > 8 || in.end > basicRowsCount(in.rows) {
			return basicStreamFactStage{}, ErrInvalid
		}
		return in.retire.source, nil
	case basicStageCopyInput:
		if !reflect.ValueOf(in.begin).IsZero() || in.fact != (basicStreamFactStage{}) || !reflect.ValueOf(in.rows).IsZero() || in.changeRevision != 0 || in.start != 0 || in.end != 0 || in.probe != (basicProbeEffects{}) {
			return basicStreamFactStage{}, ErrInvalid
		}
		if _, err := basicCopyRequestBytes(in.copy); err != nil {
			return basicStreamFactStage{}, err
		}
		return in.copy.source, nil
	case basicStageBeginInput:
		if in.fact != (basicStreamFactStage{}) || in.changeRevision != 0 || in.begin.source.changeID != 0 || in.begin.source.inputRevision != 0 || !reflect.ValueOf(in.rows).IsZero() || in.start != 0 || in.end != 0 {
			return basicStreamFactStage{}, ErrInvalid
		}
		return in.begin.source, nil
	case basicStageFactInput:
		if !reflect.ValueOf(in.begin).IsZero() || !reflect.ValueOf(in.rows).IsZero() || in.start != 0 || in.end != 0 {
			return basicStreamFactStage{}, ErrInvalid
		}
		return in.fact, nil
	case basicStageMediaInput:
		if !reflect.ValueOf(in.begin).IsZero() || in.start < 0 || in.end <= in.start || in.end-in.start > 8 || in.end > basicRowsCount(in.rows) {
			return basicStreamFactStage{}, ErrInvalid
		}
		if _, err := basicRowsRequestBytes(in.fact, in.rows); err != nil {
			return basicStreamFactStage{}, err
		}
		return in.fact, nil
	case basicStageProbeInput, basicStagePinShapeInput:
		if !reflect.ValueOf(in.begin).IsZero() || in.fact != (basicStreamFactStage{}) || !reflect.ValueOf(in.rows).IsZero() || in.changeRevision != 0 || in.start != 0 || in.end != 0 {
			return basicStreamFactStage{}, ErrInvalid
		}
		return in.probe.source, nil
	}
	return basicStreamFactStage{}, invalid("basic_stage_operation")
}

func newBasicSealCommand(identity CommandIdentity, in basicSealRequest) (*basicSealCommand, error) {
	if _, err := basicSealRequiredBytes(in); err != nil {
		return nil, err
	}
	if err := basicOriginalPreflight(identity, in.original, in.source); err != nil {
		return nil, err
	}
	if in.purpose != BasicPurposeSeal || in.source.changeID <= 0 || in.source.inputRevision <= 0 || in.changeRevision <= 0 {
		return nil, invalid("basic_seal_operation")
	}
	switch in.operation {
	case basicSealStreamInput:
		if !reflect.ValueOf(in.rows).IsZero() {
			return nil, ErrInvalid
		}
	case basicSealMediaInput:
		rows, err := freezeBasicRowsRequest(in.source, in.rows)
		if err != nil {
			return nil, err
		}
		in.rows = rows
	default:
		return nil, invalid("basic_seal_operation")
	}
	in.original = cloneBasicOriginal(in.original)
	in.source = cloneBasicSource(in.source)
	digest, err := canonicalRequestDigest(BasicSeal, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &basicSealCommand{identity: identity, request: in, digest: digest, valid: true}, nil
}

func basicRowsCount(in basicRowsRequest) int64 {
	return int64(len(in.streams) + len(in.breadth) + len(in.chapterFacts) + len(in.chapters) + len(in.subtitleFacts) + len(in.sidecars) + len(in.subtitles) + len(in.evidence))
}

func cloneBasicRequestSlice[T any](in []T) []T {
	if in == nil {
		return nil
	}
	out := make([]T, len(in)) // retain nil-vs-empty wire identity, no caller backing array
	copy(out, in)
	return out
}

func (c *basicStageCommand) receiptRequest() (BasicReceiptRequest, error) {
	if c == nil || !c.valid {
		return BasicReceiptRequest{}, invalid("basic_stage_command")
	}
	return basicOriginalReference(c.request.original.scope, BasicPurposeStage, c.identity, c.digest), nil
}
func (c *basicSealCommand) receiptRequest() (BasicReceiptRequest, error) {
	if c == nil || !c.valid {
		return BasicReceiptRequest{}, invalid("basic_seal_command")
	}
	return basicOriginalReference(c.request.original.scope, BasicPurposeSeal, c.identity, c.digest), nil
}
func basicOriginalReference(scope inventoryScope, purpose BasicPurpose, identity CommandIdentity, digest RequestDigest) BasicReceiptRequest {
	return BasicReceiptRequest{Purpose: purpose, Scan: ScanReceiptRequest{RunID: scope.runID, LibraryID: scope.libraryID, LibraryKey: scope.libraryKey,
		Receipt: ReceiptReference{Identity: identity, RequestDigest: digest}}}
}

func cloneBasicProbeScalars(v basicProbeScalars) basicProbeScalars {
	v.container = strings.Clone(v.container)
	v.videoCodec = strings.Clone(v.videoCodec)
	v.audioCodec = strings.Clone(v.audioCodec)
	return v
}
