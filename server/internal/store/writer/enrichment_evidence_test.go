package writer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func evidenceHeaderFixture() EvidenceHeaderInput {
	return EvidenceHeaderInput{
		ID: "evidence:1", SubjectID: 42, ParserVersion: "parser-v1",
		Provider: "fixture", SubjectKind: "movie", ProviderID: "opaque:001",
		Language: "en", Region: "CA", RequestShape: "detail-v1",
		ObservationDigest: [32]byte{7}, ObservedAt: "2026-11-05T00:00:00Z",
		Title: "Title", Overview: "Line one\nLine two", Year: -1,
	}
}

func evidenceHeaderHash(t *testing.T, input EvidenceHeaderInput) [32]byte {
	t.Helper()
	header, err := NewEvidenceHeader(input)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	if err := evidenceHeaderDigest(h, header); err != nil {
		t.Fatal(err)
	}
	var digest [32]byte
	copy(digest[:], h.Sum(nil))
	return digest
}

func TestEvidenceHeaderOwnsEveryRetainedText(t *testing.T) {
	input := evidenceHeaderFixture()
	input.ArtistType = EvidenceText{Value: "Person", Present: true}
	rv := reflect.ValueOf(&input).Elem()
	for n := range rv.NumField() {
		field := rv.Field(n)
		if field.Kind() == reflect.String {
			value := field.String()
			if value == "" {
				value = "retained"
			}
			backing := strings.Repeat("x", 1<<20) + value
			field.SetString(backing[len(backing)-len(value):])
		}
	}
	backing := strings.Repeat("x", 1<<20) + "Person"
	input.ArtistType.Value = backing[len(backing)-6:]
	header, err := NewEvidenceHeader(input)
	if err != nil {
		t.Fatal(err)
	}
	actual, original := reflect.ValueOf(header.input), reflect.ValueOf(input)
	var size uint64
	for n := range actual.NumField() {
		if actual.Field(n).Kind() != reflect.String {
			continue
		}
		a, b := actual.Field(n).String(), original.Field(n).String()
		size += uint64(len(a))
		if a != b || unsafe.StringData(a) == unsafe.StringData(b) {
			t.Fatalf("text field %s not independently owned", actual.Type().Field(n).Name)
		}
	}
	size += uint64(len(header.input.ArtistType.Value))
	if header.input.ArtistType != input.ArtistType || unsafe.StringData(header.input.ArtistType.Value) == unsafe.StringData(input.ArtistType.Value) || header.TextBytes() != size {
		t.Fatal("optional text ownership/charge differs")
	}
	input.ID = "changed"
	input.ArtistType.Value = "changed"
	if header.ID() != "evidence:1" || header.SubjectID() != 42 || header.input.ArtistType.Value != "Person" {
		t.Fatal("caller changed frozen header")
	}
}

func TestEvidenceHeaderValidationAndNulls(t *testing.T) {
	for _, change := range []func(*EvidenceHeaderInput){
		func(v *EvidenceHeaderInput) { v.ID = "" },
		func(v *EvidenceHeaderInput) { v.SubjectID = 0 },
		func(v *EvidenceHeaderInput) { v.ParserVersion = "" },
		func(v *EvidenceHeaderInput) { v.SubjectKind = "show" },
		func(v *EvidenceHeaderInput) { v.ProviderID = "" },
		func(v *EvidenceHeaderInput) { v.RequestShape = "" },
		func(v *EvidenceHeaderInput) { v.ObservedAt = "not a timestamp" },
		func(v *EvidenceHeaderInput) { v.Overview = "a\x00b" },
		func(v *EvidenceHeaderInput) { v.Title = string([]byte{0xff}) },
		func(v *EvidenceHeaderInput) { v.ArtistType = EvidenceText{Value: "ignored"} },
		func(v *EvidenceHeaderInput) { v.LengthMillis.Value = 1 },
		func(v *EvidenceHeaderInput) { v.BeginYear.Value = 1 },
		func(v *EvidenceHeaderInput) { v.EndYear.Value = 1 },
		func(v *EvidenceHeaderInput) { v.LengthMillis = EvidenceInteger{Value: -1, Present: true} },
		func(v *EvidenceHeaderInput) { v.Overview = strings.Repeat("x", maxEvidenceHeaderTextBytes+1) },
	} {
		v := evidenceHeaderFixture()
		change(&v)
		if _, err := NewEvidenceHeader(v); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid header accepted: %v", err)
		}
	}
	h := sha256.New()
	empty := h.Sum(nil)
	if err := evidenceHeaderDigest(h, EvidenceHeader{}); !errors.Is(err, ErrInvalid) || !bytes.Equal(h.Sum(nil), empty) {
		t.Fatal("zero header is not an admitted value", err)
	}
	base := evidenceHeaderFixture()
	absent := evidenceHeaderHash(t, base)
	for _, change := range []func(*EvidenceHeaderInput){
		func(v *EvidenceHeaderInput) { v.LengthMillis.Present = true },
		func(v *EvidenceHeaderInput) { v.BeginYear.Present = true },
		func(v *EvidenceHeaderInput) { v.EndYear.Present = true },
		func(v *EvidenceHeaderInput) { v.ArtistType.Present = true },
		func(v *EvidenceHeaderInput) { v.SubjectID++ },
		func(v *EvidenceHeaderInput) { v.Year++ },
		func(v *EvidenceHeaderInput) { v.ObservationDigest[31] = 1 },
	} {
		v := base
		change(&v)
		if evidenceHeaderHash(t, v) == absent {
			t.Fatal("typed request fields collapsed")
		}
	}
	for n := range reflect.TypeOf(base).NumField() {
		if reflect.TypeOf(base).Field(n).Type.Kind() != reflect.String {
			continue
		}
		v := base
		field := reflect.ValueOf(&v).Elem().Field(n)
		switch reflect.TypeOf(base).Field(n).Name {
		case "SubjectKind":
			field.SetString("series")
		case "ObservedAt":
			field.SetString("2026-11-05T00:00:01Z")
		default:
			field.SetString(field.String() + "2")
		}
		if evidenceHeaderHash(t, v) == absent {
			t.Fatalf("codec omitted %s", reflect.TypeOf(base).Field(n).Name)
		}
	}
}

func TestEvidenceHeaderIndependentFieldVector(t *testing.T) {
	// Independent Python hashlib/struct reference, not another invocation of
	// the Go field encoder. This pins the header component, not a full request,
	// whole-evidence seal or any existing credit/owner/PB manifest.
	digest := evidenceHeaderHash(t, evidenceHeaderFixture())
	if hex.EncodeToString(digest[:]) != "e76f0ae7d50fbe1822b89d7e34e37a31796c317f3a03d3b8ae5d7c8c4cbc3416" {
		t.Fatalf("header-field byte contract changed: %x", digest)
	}
}

func TestEvidenceStatementsCompileOnChange001(t *testing.T) {
	definitions := enrichmentEvidenceDefinitions()
	registry, err := stmt.New(definitions)
	if err != nil {
		t.Fatal(err)
	}
	w, _, _ := testExecutor(t)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := sqliteconn.Assert(t.Context(), conn, w.config); err != nil {
		t.Fatal(err)
	}
	for _, d := range definitions {
		if (d.Kind != stmt.Write && d.Kind != stmt.Read) || !strings.HasPrefix(string(d.ID), "enrichment.evidence_stage.") || strings.Contains(strings.ToLower(d.SQL), "replace") {
			t.Fatal("definition escaped closed staging purpose", d.ID)
		}
		for _, cohort := range d.Cohorts {
			s, err := registry.Lookup(d.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.Bind(cohort.Bindings...); err != nil {
				t.Fatal(d.ID, cohort.Name, err)
			}
			// Compile only: EXPLAIN does not execute the accepting mutation. This
			// is not a command/receipt/forecast/hold or HN plan qualification.
			rows, err := conn.QueryContext(t.Context(), "EXPLAIN "+d.SQL, cohort.Bindings...)
			if err != nil {
				t.Fatal(d.ID, cohort.Name, err)
			}
			for rows.Next() {
			}
			err = errors.Join(rows.Err(), rows.Close())
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	v := evidenceHeaderFixture()
	absent := evidenceHeaderValues(v)
	zero := evidenceHeaderPresentZeroCohort(v)
	for _, n := range []int{25, 26, 27, 28} {
		if absent[n] != nil || zero[n] == nil {
			t.Fatalf("SQL NULL collapsed at binding %d", n)
		}
	}
	if absent[5] != "opaque:001" || absent[9] != hex.EncodeToString(v.ObservationDigest[:]) {
		t.Fatal("native ID or source digest binding changed")
	}
	evidenceSQLReadback(t, conn, registry)
}
