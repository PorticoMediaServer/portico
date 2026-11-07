package writer

import (
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"testing"
)

func basicProbeCommandFixture() (basicStageRequest, CommandIdentity) {
	stage, seal, identity := basicCommandFixture()
	return basicStageRequest{original: stage.original, purpose: BasicPurposeStage, operation: basicStageProbeInput,
		probe: basicProbeEffects{source: seal.source, changeRevision: 13, assetPath: "/media/Arrival.mkv",
			before: basicProbeScalars{"old", "vp9", "aac", 1920, 1080, 90}, after: basicProbeScalars{"mkv", "h264", "", 0, 0, 100},
			playbackIncarnation: [16]byte{1}, playbackRevision: 101, preparedRevision: 103, fileSizeRevision: 113, invalidationRevision: 109}}, identity
}

func TestBasicProbeCommandOwnsCoherentTupleAndPurpose(t *testing.T) {
	in, identity := basicProbeCommandFixture()
	command, err := newBasicStageCommand(identity, in)
	if err != nil {
		t.Fatal(err)
	}
	reference, err := command.receiptRequest()
	if err != nil || reference.Purpose != BasicPurposeStage {
		t.Fatal(reference, err)
	}
	in.probe.after.width = 640
	in.original.source.resolvedRoot[0] = '!'
	if command.request.probe.after.width != 0 || string(command.request.original.source.resolvedRoot) != "/media" {
		t.Fatal("retained caller values")
	}
	if got, err := command.receiptRequest(); err != nil || got != reference {
		t.Fatal(got, err)
	}
	changed, identity := basicProbeCommandFixture()
	changed.probe.preparedRevision++
	other, err := newBasicStageCommand(identity, changed)
	if err != nil || other.digest == command.digest {
		t.Fatal("unbound native counter", err)
	}
	changed, identity = basicProbeCommandFixture()
	changed.fact = changed.probe.source
	if got, err := newBasicStageCommand(identity, changed); got != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("inactive source alias accepted", got, err)
	}
}

func TestBasicProbeCommandNativePathRefusalIsTyped(t *testing.T) {
	for _, tc := range []struct {
		path string
		want error
	}{
		{string([]byte{'/', 0xff}), ErrFilenameUnrepresentable},
		{"/media/nul\x00.mkv", ErrInvalid},
	} {
		in, identity := basicProbeCommandFixture()
		in.probe.assetPath = tc.path
		got, err := newBasicStageCommand(identity, in)
		if got != nil || !errors.Is(err, tc.want) {
			t.Fatal(got, err)
		}
		if tc.want == ErrInvalid && errors.Is(err, ErrFilenameUnrepresentable) {
			t.Fatal("NUL became poison", err)
		}
	}
}

func TestBasicShapePinRequestDoesNotAliasRawProbe(t *testing.T) {
	in, identity := basicProbeCommandFixture()
	in.probe.before = in.probe.after
	probe, err := newBasicStageCommand(identity, in)
	if err != nil {
		t.Fatal(err)
	}
	in.operation = basicStagePinShapeInput
	pinned, err := newBasicStageCommand(identity, in)
	if err != nil || pinned.digest == probe.digest {
		t.Fatal("distinct raw/progress operation was aliased", err)
	}
	for _, id := range []stmt.ID{basicShapeLookup, basicShapeInsert, basicShapeRefCAS, basicShapeHeaderCAS} {
		if basicStageRequestMaxUses(in, id) != 1 {
			t.Fatal("unforecast dictionary work", id)
		}
	}
	if basicStageRequestMaxUses(in, basicProbeOldInsert) != 0 || basicStageRequestMaxUses(in, basicProbeAssetCAS) != 0 {
		t.Fatal("pin borrowed raw replacement effects")
	}
	in.probe.before.width++
	if got, err := newBasicStageCommand(identity, in); got != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("pin accepted an unapplied raw tuple", got, err)
	}
}
