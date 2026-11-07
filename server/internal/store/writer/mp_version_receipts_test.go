package writer

import (
	"strings"
	"testing"
)

func TestMPVersionReceiptOwnsItsOriginal(t *testing.T) {
	v := mpVersionReceipt{header: mpPrepReceiptHeader{identity: CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1}, kind: 745, request: RequestDigest{3}, appliedMS: 1000}, result: mpVersionResult{version: "v", job: "j", sourceRevision: strings.Repeat("a", 64), transformation: strings.Repeat("b", 64), digest: strings.Repeat("c", 64), state: "deleting", retirement: "live", revision: 2, size: 10, physicalPending: true}}
	original, err := mpVersionResultDigest(v)
	if err != nil {
		t.Fatal(err)
	}
	altered := v
	altered.header.identity.Incarnation[0]++
	next, err := mpVersionResultDigest(altered)
	if err != nil || next == original {
		t.Fatal("version owner lost", err)
	}
	altered = v
	altered.result.physicalPending = false
	if _, err = mpVersionResultDigest(altered); err == nil {
		t.Fatal("retirement erased physical obligation")
	}
	altered = v
	altered.header.kind = 746
	if _, err = mpVersionResultDigest(altered); err == nil {
		t.Fatal("job purpose stored version receipt")
	}
	altered = v
	altered.header.kind = 749
	altered.result.state = "deleted"
	altered.result.retirement = "retired"
	altered.result.physicalPending = false
	if _, err = mpVersionResultDigest(altered); err != nil {
		t.Fatal("settled version retirement", err)
	}
}
