package writer

import (
	"errors"
	"testing"
)

func TestBasicCopyStageCommandFreezesV2NativeChunk(t *testing.T) {
	stage, seal, identity := basicCommandFixture()
	source := seal.source
	input := basicStageRequest{original: stage.original, purpose: BasicPurposeStage, operation: basicStageCopyInput, copy: basicCopyRequest{
		operation: basicCopyMediaRow, source: source, position: basicCopyPosition{changeID: source.changeID, changeRevision: 15, phase: 2, afterEntity: 31, family: 2, id: 1}, shapeID: 61, fileSizeRevision: 1, invalidation: 1, expectedRows: 2, startOrdinal: 1,
		rows: basicRowsRequest{family: 2, streams: []basicStreamRow{{index: 3, streamType: "audio", codec: "aac", language: "en", channels: 2, channelLayout: "stereo"}}},
	}}
	command, err := newBasicStageCommand(identity, input)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := command.receiptRequest()
	if err != nil {
		t.Fatal(err)
	}
	input.original.source.resolvedRoot[0] = '!'
	input.copy.rows.streams[0].codec = "mutated"
	if command.request.copy.rows.streams[0].codec != "aac" || string(command.request.original.source.resolvedRoot) == string(input.original.source.resolvedRoot) {
		t.Fatal("copy request retained mutable caller data")
	}
	digest, err := canonicalRequestDigest(BasicStage, basicStageRequestVersion, identity, command.request)
	if err != nil || digest != saved.Scan.Receipt.RequestDigest {
		t.Fatal(digest, err)
	}
	// A changed frozen schema has a new request version; historical references
	// are opaque saved identity/digest coordinates, never rebuilt with this ctor.
	alias, err := canonicalRequestDigest(BasicStage, 1, identity, command.request)
	if err != nil || alias == digest {
		t.Fatal("request version lost", err)
	}
	again, err := command.receiptRequest()
	if err != nil || again != saved {
		t.Fatal("original reference changed", again, err)
	}
	input = command.request
	input.fact = source
	if got, err := newBasicStageCommand(identity, input); got != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("copy borrowed another operation payload", got, err)
	}
}
