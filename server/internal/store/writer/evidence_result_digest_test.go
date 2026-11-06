package writer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type evidenceResultAnswer struct {
	Name     string
	Identity struct {
		Incarnation string
		Sequence    int64
		OperationID string `json:"operation_id"`
	}
	Request string
	Result  evidenceBeginResult
	WireHex string `json:"wire_hex"`
	SHA256  string
}

func evidenceResultAnswers(t *testing.T) []evidenceResultAnswer {
	t.Helper()
	b, err := os.ReadFile("testdata/evidence-begin-result-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if got := sha256.Sum256(b); hex.EncodeToString(got[:]) != "b75d6fd6952bc56908a8dd2d3c1d48f9154e40cda871a1f6099c91134a078ea3" {
		t.Fatal("independent Python struct/hashlib known answers changed")
	}
	var answers []evidenceResultAnswer
	if err := json.Unmarshal(b, &answers); err != nil || len(answers) != 7 {
		t.Fatal("known answer shape", err)
	}
	return answers
}

func evidenceResultBase() (CommandIdentity, RequestDigest, evidenceBeginResult) {
	var i CommandIdentity
	var q RequestDigest
	for n := range i.Incarnation {
		i.Incarnation[n] = byte(n)
		i.OperationID[n] = byte(n + 16)
	}
	for n := range q {
		q[n] = byte(n + 32)
	}
	i.Sequence = 11
	return i, q, evidenceBeginResult{ProviderOperationID: "op-雪-Ω", EntityID: 17, ObservedCatalogRevision: 23,
		AppliedMS: 1700000000123, AfterItem: "page/é/𐐀", NextOrdinal: 1, ResultCount: 1}
}

func TestEvidenceBeginResultKnownAnswers(t *testing.T) {
	for _, a := range evidenceResultAnswers(t) {
		t.Run(a.Name, func(t *testing.T) {
			var i CommandIdentity
			var q RequestDigest
			for encoded, dest := range map[string][]byte{a.Identity.Incarnation: i.Incarnation[:], a.Identity.OperationID: i.OperationID[:], a.Request: q[:]} {
				raw, err := hex.DecodeString(encoded)
				if err != nil || len(raw) != len(dest) {
					t.Fatal("raw input width", err)
				}
				copy(dest, raw)
			}
			i.Sequence = a.Identity.Sequence
			h := &recordedResultHash{Hash: sha256.New()}
			if err := encodeCanonicalResult(&requestEncoder{h: h}, i, q, a.Result); err != nil {
				t.Fatal(err)
			}
			if hex.EncodeToString(h.bytes.Bytes()) != a.WireHex || h.bytes.Len() != 223+len(a.Result.ProviderOperationID)+len(a.Result.AfterItem) {
				t.Fatal("independent exact wire mismatch")
			}
			d, err := evidenceBeginResultDigest(i, q, a.Result)
			if err != nil || hex.EncodeToString(d[:]) != a.SHA256 || !bytes.Equal(h.Sum(nil), d[:]) {
				t.Fatalf("known digest %x %v", d, err)
			}
		})
	}
}

func TestEvidenceBeginResultCompleteFieldCoverage(t *testing.T) {
	i, q, base := evidenceResultBase()
	original, err := evidenceBeginResultDigest(i, q, base)
	if err != nil {
		t.Fatal(err)
	}
	changes := map[string]func(*evidenceBeginResult){
		"ProviderOperationID":     func(v *evidenceBeginResult) { v.ProviderOperationID += "x" },
		"EntityID":                func(v *evidenceBeginResult) { v.EntityID++ },
		"ObservedCatalogRevision": func(v *evidenceBeginResult) { v.ObservedCatalogRevision++ },
		"AppliedMS":               func(v *evidenceBeginResult) { v.AppliedMS++ },
		"AfterItem":               func(v *evidenceBeginResult) { v.AfterItem += "x" },
		"NextOrdinal":             func(v *evidenceBeginResult) { v.NextOrdinal++ },
		"PublicationID":           func(v *evidenceBeginResult) { s := ""; v.PublicationID = &s },
		"ImageID":                 func(v *evidenceBeginResult) { n := int64(0); v.ImageID = &n },
		"ImageRevision":           func(v *evidenceBeginResult) { n := int64(0); v.ImageRevision = &n },
		"ResultCount":             func(v *evidenceBeginResult) { v.ResultCount++ },
		"MemberCohortLength":      func(v *evidenceBeginResult) { v.MemberCohortLength++ },
	}
	typ := reflect.TypeOf(base)
	if typ.NumField() != len(changes) || reflect.TypeOf(i).NumField() != 3 {
		t.Fatal("new native result/identity field needs encoding or refusal coverage")
	}
	check := func(i CommandIdentity, q RequestDigest, v evidenceBeginResult) {
		t.Helper()
		d, err := evidenceBeginResultDigest(i, q, v)
		if err == nil && d == original || err != nil && (!errors.Is(err, ErrInvalid) || d != (ResultDigest{})) {
			t.Fatal("unbound field or nonzero refusal", err)
		}
	}
	for n := range typ.NumField() {
		name := typ.Field(n).Name
		t.Run(name, func(t *testing.T) {
			change := changes[name]
			if change == nil {
				t.Fatal("uncovered field", name)
			}
			v := base
			change(&v)
			check(i, q, v)
		})
	}
	i.Sequence++
	check(i, q, base)
	i.Sequence--
	for n := range i.Incarnation {
		changed := i
		changed.Incarnation[n] ^= 1
		check(changed, q, base)
		changed = i
		changed.OperationID[n] ^= 1
		check(changed, q, base)
	}
	for n := range q {
		changed := q
		changed[n] ^= 1
		check(i, changed, base)
	}
	// Equal values must not hide a swapped field in the codec.
	v := base
	v.EntityID, v.ObservedCatalogRevision = v.ObservedCatalogRevision, v.EntityID
	check(i, q, v)
	effective := effectiveBeginResult{ProviderOperationID: base.ProviderOperationID, EntityID: base.EntityID,
		ObservedCatalogRevision: base.ObservedCatalogRevision, AppliedMS: base.AppliedMS, AfterItem: base.AfterItem, ImageID: 1, ImageRevision: 1}
	d, err := effectiveBeginResultDigest(i, q, effective)
	if err != nil || d == original {
		t.Fatal("evidence result aliases effective BEGIN", err)
	}
}

func TestEvidenceBeginResultRefusalsBeforeHash(t *testing.T) {
	i, q, base := evidenceResultBase()
	cases := map[string]func(*evidenceBeginResult){
		"empty_owner":    func(v *evidenceBeginResult) { v.ProviderOperationID = "" },
		"owner_utf8":     func(v *evidenceBeginResult) { v.ProviderOperationID = "\xff" },
		"cursor_utf8":    func(v *evidenceBeginResult) { v.AfterItem = "\xff" },
		"negative_time":  func(v *evidenceBeginResult) { v.AppliedMS = -1 },
		"publication":    func(v *evidenceBeginResult) { s := ""; v.PublicationID = &s },
		"image":          func(v *evidenceBeginResult) { n := int64(0); v.ImageID = &n },
		"image_revision": func(v *evidenceBeginResult) { n := int64(0); v.ImageRevision = &n },
		"aggregate_limit": func(v *evidenceBeginResult) {
			s := strings.Repeat("a", 16<<20)
			v.ProviderOperationID, v.AfterItem = s, s
		},
	}
	for _, field := range []string{"EntityID", "ObservedCatalogRevision", "NextOrdinal", "ResultCount", "MemberCohortLength"} {
		for _, n := range []int64{math.MinInt64, -1, 0, 1, 2, math.MaxInt64} {
			valid := n > 0
			if field == "NextOrdinal" || field == "ResultCount" {
				valid = n == 1
			} else if field == "MemberCohortLength" {
				valid = n == 0
			}
			if !valid {
				cases[field+"_"+strconv.FormatInt(n, 10)] = func(v *evidenceBeginResult) { reflect.ValueOf(v).Elem().FieldByName(field).SetInt(n) }
			}
		}
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			v := base
			change(&v)
			h := &countedResultHash{Hash: sha256.New()}
			e := requestEncoder{h: h}
			if err := encodeCanonicalResult(&e, i, q, v); !errors.Is(err, ErrInvalid) || h.writes != 0 || e.written != 0 {
				t.Fatal("invalid result fed hash", h.writes, err)
			}
			if d, err := evidenceBeginResultDigest(i, q, v); !errors.Is(err, ErrInvalid) || d != (ResultDigest{}) {
				t.Fatal("invalid result returned digest", err)
			}
		})
	}
	for _, seq := range []int64{0, -1} {
		i.Sequence = seq
		if d, err := evidenceBeginResultDigest(i, q, base); !errors.Is(err, ErrInvalid) || d != (ResultDigest{}) {
			t.Fatal("invalid identity manufactured digest", err)
		}
	}
}

func TestEvidenceBeginResultLiteralByteBoundary(t *testing.T) {
	i, q, base := evidenceResultBase()
	base.ProviderOperationID = "p"
	text := strings.Repeat("c", (32<<20)-223)
	for _, size := range []int{(32 << 20) - 1, 32 << 20, (32 << 20) + 1} {
		v := base
		v.AfterItem = text[:size-223-1]
		h := &countedResultHash{Hash: sha256.New()}
		e := requestEncoder{h: h}
		err := encodeCanonicalResult(&e, i, q, v)
		if size > 32<<20 {
			if !errors.Is(err, ErrInvalid) || h.writes != 0 || e.written != 0 {
				t.Fatal("ceiling+1 hashed", err)
			}
		} else if err != nil || h.bytes != size || e.written != uint64(size) {
			t.Fatal("literal byte boundary", size, h.bytes, err)
		}
	}
}
