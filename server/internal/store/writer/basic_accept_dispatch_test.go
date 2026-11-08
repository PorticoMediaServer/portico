package writer

import (
	"errors"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func TestBasicAcceptDispatchPublishesAfterPhysicalCommitThenResolvesAndACKs(t *testing.T) {
	w, e, authority, in, _ := basicAcceptDispatchFixture(t)
	disclosure := inventoryTestDisclosure(t, w, in.request.Scan)
	value, ack, err := w.effective.runBasic(t.Context(), w, disclosure, in, false)
	if err != nil || ack != (AckResult{}) || value.Outcome.State != Applied || value.Outcome.Replayed || value.Outcome.Reference != in.request.Scan.Receipt || value.AcceptedRunRevision != 42 || len(value.Members) != 1 || value.Members[0].AcceptedAssetRevision != 18 {
		t.Fatal(value, ack, err)
	}
	basicDispatchReturned(t, e, authority)
	if value.Outcome.Evidence == nil || value.Outcome.Evidence.Purpose != BasicAccept || value.Outcome.Evidence.Class != governor.ScanMinimum || value.Outcome.Evidence.Commit != CommitSucceeded {
		t.Fatal(value.Outcome)
	}
	replay, _, err := w.effective.runBasic(t.Context(), w, disclosure, basicInvocation{request: in.request}, false)
	if err != nil || !replay.Outcome.Replayed || replay.Digest != value.Digest || !reflect.DeepEqual(replay.Members, value.Members) {
		t.Fatal(replay, err)
	}
	_, ack, err = w.effective.runBasic(t.Context(), w, disclosure, basicInvocation{request: in.request}, true)
	if err != nil || ack.AcknowledgedThrough != 19 {
		t.Fatal(ack, err)
	}
	retired, _, err := w.effective.runBasic(t.Context(), w, disclosure, basicInvocation{request: in.request}, false)
	if err != nil || retired.Outcome.State != ResultRetired || len(retired.Members) != 0 {
		t.Fatal(retired, err)
	}
	basicDispatchReturned(t, e, authority)
}
func TestBasicAcceptProfileOwnsClosedBoundsAndAllPossibleUses(t *testing.T) {
	b := basicAcceptBounds{nfo: true, nfoBefore: 2, nfoAfter: 3, artwork: 4}
	defs := basicFinalDefinitions(b)
	seen := map[stmt.ID]bool{}
	for _, d := range defs {
		if seen[d.ID] || basicFinalMaxUses(b, d.ID) <= 0 {
			t.Fatal(d.ID)
		}
		seen[d.ID] = true
	}
	if _, err := stmt.Describe(defs); err != nil {
		t.Fatal(err)
	}
	if basicFinalMaxUses(b, basicStageHeader) != 6 || basicFinalMaxUses(b, basicActivationChangeCAS) != 2 || basicFinalMaxUses(b, basicNFODelete) != 2 || basicFinalMaxUses(b, basicNFOInsert) != 3 || basicFinalMaxUses(b, basicArtworkInsert) != 4 || basicFinalMaxUses(b, basicFirstScalarOldText) != 3 {
		t.Fatal("incomplete full operation forecast")
	}
	if basicFinalMaxUses(b, scanInventoryObject) != 0 || basicFinalMaxUses(b, scanReceiptReclaimHeader) != 0 || basicFinalMaxUses(b, basicControlRunCAS) != 0 {
		t.Fatal("unrelated effects admitted")
	}
	command := &basicAcceptCommand{valid: true, request: basicAcceptRequest{nfo: &basicNFOSet{}, artwork: make([]basicArtworkInput, 5)}}
	if err := checkBasicAcceptBounds(command, b); !errors.Is(err, ErrCap) {
		t.Fatal(err)
	}
	command.request.artwork = nil
	if err := checkBasicAcceptBounds(command, b); err != nil {
		t.Fatal(err)
	}
	if got := basicFinalDefinitions(basicAcceptBounds{nfoBefore: 1}); got != nil {
		t.Fatal("unconfigured NFO bounds accepted")
	}
}
