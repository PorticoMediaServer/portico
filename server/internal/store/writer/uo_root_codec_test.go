package writer

import (
	"bytes"
	"testing"
)

func TestUORootOperationUsesSoleCanonicalCodec(t *testing.T) {
	i := CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}
	q := RequestDigest{3}
	v := uoRootOperationResult{root: "abcdefghijklmnopqrstuv", kind: BeginPersonalJob, result: uoOperationResult{status: 2, revision: 1, appliedMS: 100, target: "original-operation"}}
	p, d, err := encodeUORootOperation(i, q, v)
	if err != nil || !bytes.HasPrefix(p, []byte(canonicalResultCodec)) || len(p) > uoRootPayloadCap {
		t.Fatal(len(p), err)
	}
	sole, err := canonicalResultDigest(i, q, v)
	if err != nil || sole != d {
		t.Fatal("independent result codec", sole, d, err)
	}
	if r, err := decodeUORootOperation(i, q, v.root, v.kind, p, d); err != nil || r != v.result {
		t.Fatal(r, err)
	}
	for _, change := range []func(*CommandIdentity, *RequestDigest, *string, *CommandID){
		func(i *CommandIdentity, _ *RequestDigest, _ *string, _ *CommandID) { i.Sequence++ },
		func(_ *CommandIdentity, q *RequestDigest, _ *string, _ *CommandID) { q[0]++ },
		func(_ *CommandIdentity, _ *RequestDigest, r *string, _ *CommandID) { *r = "zyxwvutsrqponmlkjihgfe" },
		func(_ *CommandIdentity, _ *RequestDigest, _ *string, k *CommandID) { *k = StepPersonalJob },
	} {
		ii, qq, root, kind := i, q, v.root, v.kind
		change(&ii, &qq, &root, &kind)
		if _, err := decodeUORootOperation(ii, qq, root, kind, p, d); err == nil {
			t.Fatal("foreign original binding decoded")
		}
	}
	v.kind = SetPersonalFlags
	if _, _, err := encodeUORootOperation(i, q, v); err == nil {
		t.Fatal("flags accepted an incomplete operation result shape")
	}
}
