package writer

import (
	"errors"
	"reflect"
	"testing"
)

func TestBasicAcceptInvocationKeepsStageAndSealClosed(t *testing.T) {
	q := BasicReceiptRequest{Scan: ScanReceiptRequest{RunID: "run", LibraryID: 7, LibraryKey: "library", Receipt: ReceiptReference{Identity: CommandIdentity{Sequence: 1}}}}
	for _, purpose := range []BasicPurpose{BasicPurposeStage, BasicPurposeSeal, "", "inventory_accept"} {
		q.Purpose = purpose
		if _, err := (&basicInvocation{request: q}).validate(); !errors.Is(err, ErrInvalid) {
			t.Fatal("another arm entered Accept", purpose, err)
		}
	}
	q.Purpose = BasicPurposeAccept
	if kind, err := (&basicInvocation{request: q}).validate(); err != nil || kind != BasicAccept {
		t.Fatal(kind, err)
	}
}

func TestBasicAcceptUnconfiguredProfileHasNoNativeEffect(t *testing.T) {
	w, e, a, in, _ := basicAcceptDispatchFixture(t)
	disclosure := inventoryTestDisclosure(t, w, in.request.Scan)
	w.effective.basicAcceptPlan = nil
	var before, after int64
	if err := e.db.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	got, ack, err := w.effective.runBasic(t.Context(), w, disclosure, in, false)
	if !errors.Is(err, ErrNotImplemented) || !reflect.DeepEqual(got, BasicResult{}) || ack != (AckResult{}) {
		t.Fatal(got, ack, err)
	}
	if err := e.db.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&after); err != nil || after != before {
		t.Fatal("closed profile changed native state", before, after, err)
	}
	basicDispatchReturned(t, e, a)
}
