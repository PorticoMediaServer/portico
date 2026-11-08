package writer

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestUORootOperationUsesSoleCanonicalCodec(t *testing.T) {
	i := CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}
	q := RequestDigest{3}
	v := uoRootOperationResult{root: "abcdefghijklmnopqrstuv", kind: SetPersonalFlags, result: uoOperationResult{status: 2, revision: 1, cursor: 2, done: 3, failed: 4, appliedMS: 100, target: "original-operation"}}
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
		func(_ *CommandIdentity, _ *RequestDigest, _ *string, k *CommandID) { *k = USMemberLibraries },
	} {
		ii, qq, root, kind := i, q, v.root, v.kind
		change(&ii, &qq, &root, &kind)
		if _, err := decodeUORootOperation(ii, qq, root, kind, p, d); err == nil {
			t.Fatal("foreign original binding decoded")
		}
	}
	for name, mutate := range map[string]func(*uoRootOperationResult){
		"short-root":        func(v *uoRootOperationResult) { v.root = "short" },
		"status-low":        func(v *uoRootOperationResult) { v.result.status = 0 },
		"status-high":       func(v *uoRootOperationResult) { v.result.status = 5 },
		"negative-field":    func(v *uoRootOperationResult) { v.result.revision = -1 },
		"zero-applied":      func(v *uoRootOperationResult) { v.result.appliedMS = 0 },
		"empty-target":      func(v *uoRootOperationResult) { v.result.target = "" },
		"overlong-target":   func(v *uoRootOperationResult) { v.result.target = strings.Repeat("x", 129) },
		"nul-target":        func(v *uoRootOperationResult) { v.result.target = "a\x00b" },
		"unregistered-kind": func(v *uoRootOperationResult) { v.kind = CommandID(699) },
	} {
		t.Run(name, func(t *testing.T) {
			bad := v
			mutate(&bad)
			if _, _, err := encodeUORootOperation(i, q, bad); !errors.Is(err, ErrInvalid) {
				t.Fatal("malformed root operation accepted", err)
			}
		})
	}
	// The root envelope is closed: an arbitrary compiled or unregistered result
	// type cannot become a canonical root payload.
	if _, _, err := encodeUORootCanonical(i, q, struct{ root string }{root: v.root}); err == nil {
		t.Fatal("unregistered result schema accepted by the root envelope")
	}
	if _, _, err := encodeUORootCanonical(CommandIdentity{}, q, v); !errors.Is(err, ErrInvalid) {
		t.Fatal("empty original identity accepted", err)
	}
}
