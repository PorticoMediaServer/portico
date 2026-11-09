package writer

import (
	"crypto/sha256"
	"database/sql"
	"testing"
)

// The 600 original is a complete closed schema: nullable zero rating, every
// flag, the actual revision, the public identity and the applied time are all
// retained, and only W's sole canonical dispatcher can produce its digest.
func TestUORootFlagsClosedCanonicalResult(t *testing.T) {
	i := CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}
	q := RequestDigest{3}
	v := uoRootFlagsResult{root: "abcdefghijklmnopqrstuv", public: [16]byte{4}, appliedMS: 100,
		effect: uoPersonalFlagsEffect{state: uoPersonalFlagsState{revision: 1, watchlisted: true, favorite: true, rating: sql.NullInt64{Valid: true}}, changed: true, viewerRevision: 2, selectionRevision: 3}}
	p, d, err := encodeUORootCanonical(i, q, v)
	if err != nil {
		t.Fatal(err)
	}
	if sole, err := canonicalResultDigest(i, q, v); err != nil || sole != d {
		t.Fatal("result bypassed W codec", sole, d, err)
	}
	if got, err := decodeUORootFlags(i, q, v.root, p, d); err != nil || got != v {
		t.Fatal(got, err)
	}
	for _, mutate := range []func(*CommandIdentity, *RequestDigest, *string){
		func(i *CommandIdentity, _ *RequestDigest, _ *string) { i.Incarnation[0]++ },
		func(i *CommandIdentity, _ *RequestDigest, _ *string) { i.OperationID[0]++ },
		func(i *CommandIdentity, _ *RequestDigest, _ *string) { i.Sequence++ },
		func(_ *CommandIdentity, q *RequestDigest, _ *string) { q[0]++ },
		func(_ *CommandIdentity, _ *RequestDigest, root *string) { *root = "zyxwvutsrqponmlkjihgfe" },
	} {
		ii, qq, root := i, q, v.root
		mutate(&ii, &qq, &root)
		if _, err := decodeUORootFlags(ii, qq, root, p, d); err == nil {
			t.Fatal("foreign original decoded")
		}
	}
	for _, offset := range []int{24, 25, 26, 27, 36} {
		bad := append([]byte(nil), p...)
		bad[len(bad)-61+offset] = 2
		if _, err := decodeUORootFlags(i, q, v.root, bad, ResultDigest(sha256.Sum256(bad))); err == nil {
			t.Fatal("noncanonical boolean decoded", offset)
		}
	}
	if _, _, err := encodeUORootCanonical(i, q, struct{ Payload []byte }{p}); err == nil {
		t.Fatal("unregistered result accepted")
	}
	v.effect.changed = false
	if _, _, err := encodeUORootCanonical(i, q, v); err == nil {
		t.Fatal("no-op accepted mutation revisions")
	}
	v.effect.viewerRevision, v.effect.selectionRevision = 0, 0
	if _, _, err := encodeUORootCanonical(i, q, v); err != nil {
		t.Fatal("complete no-op rejected", err)
	}
}
