package writer

import (
	"bytes"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"strings"
	"testing"
)

type creditManifestTestRow struct {
	EntityID         int64   `json:"entity_id"`
	Ordinal          int64   `json:"ord"`
	PersonID         *int64  `json:"person_id"`
	RoleID           int64   `json:"role_id"`
	DepartmentID     int64   `json:"department_id"`
	CreditedName     string  `json:"credited_name"`
	Provider         string  `json:"provider"`
	CreditID         string  `json:"credit_id"`
	ProviderPersonID string  `json:"provider_person_id"`
	SourceOrdinal    int64   `json:"source_ordinal"`
	SourceRowID      *string `json:"source_row_id"`
	OwnerEntryID     *string `json:"owner_entry_id"`
	OwnerIncarnation *string `json:"owner_incarnation_hex"`
	OwnerRevision    *int64  `json:"owner_revision"`
	SourceStatus     string  `json:"source_status"`
}
type creditManifestTestVector struct {
	Name           string                  `json:"name"`
	EntityID       int64                   `json:"entity_id"`
	CurrentInput   string                  `json:"current_input_digest_hex"`
	SourceManifest string                  `json:"source_manifest_digest_hex"`
	Language       string                  `json:"metadata_language"`
	Count          int64                   `json:"count"`
	Bytes          int64                   `json:"bytes"`
	Rows           []creditManifestTestRow `json:"rows"`
	Seed           string                  `json:"seed_hex"`
	Trace          []struct {
		Tuple string `json:"tuple_hex"`
		Leaf  string `json:"leaf_hex"`
		Chain string `json:"chain_hex"`
	} `json:"trace"`
	Digest string `json:"digest_hex"`
}
type creditManifestTestNegative struct {
	Name       string          `json:"name"`
	ChangedRow int             `json:"changed_row"`
	Changes    json.RawMessage `json:"changes"`
	Digest     string          `json:"digest_hex"`
	Error      string          `json:"error"`
	Matches    bool            `json:"matches_original"`
}
type creditManifestTestVectors struct {
	Source    string                       `json:"source_sha256"`
	Algorithm string                       `json:"algorithm"`
	Positive  []creditManifestTestVector   `json:"positive"`
	Negative  []creditManifestTestNegative `json:"negative"`
}

func readCreditManifestTestVectors(t *testing.T) creditManifestTestVectors {
	t.Helper()
	raw, err := os.ReadFile("testdata/effective-credit-manifest-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var data creditManifestTestVectors
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(&data); err != nil {
		t.Fatal(err)
	}
	if data.Source != "7cdded3d6a90ea72a6a06896ce119c238b6150eb67ec1b66cab35637716b8a5d" || data.Algorithm != effectiveCreditManifestAlgorithm || len(data.Positive) != 5 || len(data.Negative) != 15 {
		t.Fatal("wrong independent vector contract")
	}
	return data
}
func creditManifestTestDigest(t *testing.T, s string) [32]byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 32 {
		t.Fatal("digest width", err)
	}
	var out [32]byte
	copy(out[:], b)
	return out
}
func creditManifestNativeTestRow(t *testing.T, r creditManifestTestRow) effectiveCreditTuple {
	t.Helper()
	out := effectiveCreditTuple{entityID: r.EntityID, ordinal: r.Ordinal, roleID: r.RoleID, departmentID: r.DepartmentID, creditedName: r.CreditedName, provider: r.Provider, creditID: r.CreditID, providerPersonID: r.ProviderPersonID, sourceOrdinal: r.SourceOrdinal, sourceStatus: r.SourceStatus}
	if r.PersonID != nil {
		out.personID = sql.NullInt64{Int64: *r.PersonID, Valid: true}
	}
	if r.SourceRowID != nil {
		out.sourceRowID = sql.NullString{String: *r.SourceRowID, Valid: true}
	}
	if r.OwnerEntryID != nil {
		out.ownerEntryID = sql.NullString{String: *r.OwnerEntryID, Valid: true}
	}
	if r.OwnerRevision != nil {
		out.ownerRevision = sql.NullInt64{Int64: *r.OwnerRevision, Valid: true}
	}
	if r.OwnerIncarnation != nil {
		b, err := hex.DecodeString(*r.OwnerIncarnation)
		if err != nil || len(b) != 16 {
			t.Fatal("incarnation width", err)
		}
		out.ownerIncarnation.present = true
		copy(out.ownerIncarnation.value[:], b)
	}
	return out
}
func creditManifestTestContext(t *testing.T, v creditManifestTestVector) effectiveCreditManifestContext {
	return effectiveCreditManifestContext{entityID: v.EntityID, currentInput: creditManifestTestDigest(t, v.CurrentInput), sourceManifest: creditManifestTestDigest(t, v.SourceManifest), language: v.Language, count: v.Count, canonicalBytes: v.Bytes}
}

func TestEffectiveCreditManifestIndependentVectorsAndCheckpoints(t *testing.T) {
	vectors := readCreditManifestTestVectors(t)
	for _, v := range vectors.Positive {
		t.Run(v.Name, func(t *testing.T) {
			context := creditManifestTestContext(t, v)
			cursor, err := newEffectiveCreditManifestCheckpoint(context, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			if cursor.seed != creditManifestTestDigest(t, v.Seed) || cursor.chain != cursor.seed {
				t.Fatal("seed wire mismatch")
			}
			if len(v.Rows) != len(v.Trace) {
				t.Fatal("incomplete byte trace")
			}
			snapshots := []effectiveCreditManifestCheckpoint{cursor}
			for i, r := range v.Rows {
				native := creditManifestNativeTestRow(t, r)
				encoded, err := effectiveCreditTupleBytes(native, 1<<20)
				if err != nil || hex.EncodeToString(encoded) != v.Trace[i].Tuple {
					t.Fatal("exact tuple wire mismatch", i, err)
				}
				leaf := effectiveCreditManifestLeaf(encoded)
				if leaf != creditManifestTestDigest(t, v.Trace[i].Leaf) {
					t.Fatal("leaf domain/wire mismatch", i)
				}
				cursor, err = advanceEffectiveCreditManifest(context, cursor, native, 1<<20)
				if err != nil || cursor.chain != creditManifestTestDigest(t, v.Trace[i].Chain) {
					t.Fatal("chain mismatch", i, err)
				}
				snapshots = append(snapshots, cursor)
			}
			if cursor.nextOrdinal != v.Count || cursor.canonicalBytes != v.Bytes || cursor.chain != creditManifestTestDigest(t, v.Digest) {
				t.Fatal("complete vector mismatch")
			}
			// Resume only the native scalar checkpoint and raw32byte values; no hash
			// object's opaque state, whole-image rows or caller 'verified' flag persists.
			for cut, stored := range snapshots {
				resumed := effectiveCreditManifestCheckpoint{seed: stored.seed, chain: stored.chain, nextOrdinal: stored.nextOrdinal, canonicalBytes: stored.canonicalBytes}
				for _, r := range v.Rows[cut:] {
					resumed, err = advanceEffectiveCreditManifest(context, resumed, creditManifestNativeTestRow(t, r), 1<<20)
					if err != nil {
						t.Fatal(err)
					}
				}
				if resumed != cursor {
					t.Fatal("portable checkpoint mismatch", cut)
				}
			}
		})
	}
}

func TestEffectiveCreditManifestIndependentNegativeVectors(t *testing.T) {
	data := readCreditManifestTestVectors(t)
	base := data.Positive[3]
	for _, negative := range data.Negative {
		t.Run(negative.Name, func(t *testing.T) {
			rawRows, err := json.Marshal(base.Rows)
			if err != nil {
				t.Fatal(err)
			}
			var rows []creditManifestTestRow
			if err = json.Unmarshal(rawRows, &rows); err != nil {
				t.Fatal(err)
			}
			context := creditManifestTestContext(t, base)
			if len(negative.Changes) > 0 {
				if err := json.Unmarshal(negative.Changes, &rows[negative.ChangedRow]); err != nil {
					t.Fatal(err)
				}
			}
			switch negative.Name {
			case "language":
				context.language = "zh-CN"
			case "nullable-owner-revision":
				rows = []creditManifestTestRow{data.Positive[2].Rows[0]}
				rows[0].OwnerRevision = nil
			case "nullable-owner-incarnation":
				rows = []creditManifestTestRow{data.Positive[2].Rows[0]}
				rows[0].OwnerIncarnation = nil
			case "reordered":
				rows = []creditManifestTestRow{rows[1], rows[0]}
			case "missing":
				rows = rows[1:]
			case "cross-owner":
				rows = []creditManifestTestRow{rows[0]}
				rows[0].EntityID++
			}
			context.count = int64(len(rows))
			context.canonicalBytes = 0
			var encodedErr error
			for _, r := range rows {
				b, err := effectiveCreditTupleBytes(creditManifestNativeTestRow(t, r), 1<<20)
				if err != nil {
					encodedErr = err
					break
				}
				context.canonicalBytes += int64(len(b))
			}
			if encodedErr != nil {
				if negative.Error == "" {
					t.Fatal(encodedErr)
				}
				return
			}
			cursor, err := newEffectiveCreditManifestCheckpoint(context, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rows {
				cursor, err = advanceEffectiveCreditManifest(context, cursor, creditManifestNativeTestRow(t, r), 1<<20)
				if err != nil {
					break
				}
			}
			if negative.Error != "" {
				if err == nil {
					t.Fatal("malformed/omitted/reordered native cohort accepted")
				}
				return
			}
			if err != nil || cursor.chain != creditManifestTestDigest(t, negative.Digest) || cursor.chain == creditManifestTestDigest(t, base.Digest) {
				t.Fatal("independent negative bytes differ", err)
			}
		})
	}
}

func TestEffectiveCreditManifestNativeSchemaFieldCoverage(t *testing.T) {
	// Native schema, not DTO reflection, enumerates every effective value. No
	// accepting SQL runs; only canonical initialization and table metadata read.
	w, _, _ := testExecutor(t)
	rows, err := w.db.QueryContext(t.Context(), "PRAGMA table_xinfo(catalog_credits)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	base := creditManifestNativeTestRow(t, readCreditManifestTestVectors(t).Positive[3].Rows[1])
	mutations := map[string]func(*effectiveCreditTuple){
		"entity_id": func(r *effectiveCreditTuple) { r.entityID++ }, "ord": func(r *effectiveCreditTuple) { r.ordinal++ },
		"person_id": func(r *effectiveCreditTuple) { r.personID = sql.NullInt64{Int64: 613, Valid: true} },
		"role_id":   func(r *effectiveCreditTuple) { r.roleID++ }, "department_id": func(r *effectiveCreditTuple) { r.departmentID++ },
		"credited_name": func(r *effectiveCreditTuple) { r.creditedName += "x" }, "provider": func(r *effectiveCreditTuple) { r.provider += "x" },
		"credit_id": func(r *effectiveCreditTuple) { r.creditID += "x" }, "provider_person_id": func(r *effectiveCreditTuple) { r.providerPersonID += "x" },
		"source_ordinal": func(r *effectiveCreditTuple) { r.sourceOrdinal-- }, "source_row_id": func(r *effectiveCreditTuple) { r.sourceRowID = sql.NullString{String: "source:617", Valid: true} },
		"owner_entry_id": func(r *effectiveCreditTuple) { r.ownerEntryID.String += "x" }, "owner_incarnation": func(r *effectiveCreditTuple) { r.ownerIncarnation.value[15] ^= 1 },
		"owner_revision": func(r *effectiveCreditTuple) { r.ownerRevision.Int64++ }, "source_status": func(r *effectiveCreditTuple) { r.sourceStatus = "missing" },
	}
	original, err := effectiveCreditTupleBytes(base, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for rows.Next() {
		var cid, notNull, pk, hidden int
		var name, typ string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk, &hidden); err != nil {
			t.Fatal(err)
		}
		if hidden != 0 {
			t.Fatal("unclassified generated credit value", name)
		}
		seen[name] = true
		if name == "image_id" {
			continue
		} // Storage identity bound by native FK/CAS/receipt, deliberately excluded by v1.
		mutate, ok := mutations[name]
		if !ok {
			t.Fatal("native credit column missing from manifest contract", name)
		}
		changed := base
		mutate(&changed)
		encoded, err := effectiveCreditTupleBytes(changed, 1<<20)
		if err != nil || bytes.Equal(encoded, original) {
			t.Fatal("native credit field omitted/collapsed", name, err)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(seen) != len(mutations)+1 || !seen["image_id"] {
		t.Fatal("stale native credit coverage", len(seen), len(mutations))
	}
}

func TestEffectiveCreditManifestBoundsAndShape(t *testing.T) {
	data := readCreditManifestTestVectors(t)
	context := creditManifestTestContext(t, data.Positive[1])
	row := creditManifestNativeTestRow(t, data.Positive[1].Rows[0])
	encoded, err := effectiveCreditTupleBytes(row, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := effectiveCreditTupleBytes(row, int64(len(encoded))); err != nil {
		t.Fatal("exact tuple bound", err)
	}
	if out, err := effectiveCreditTupleBytes(row, int64(len(encoded)-1)); err == nil || out != nil {
		t.Fatal("tuple budget bypass")
	}
	for _, mutate := range []func(*effectiveCreditTuple){
		func(r *effectiveCreditTuple) { r.entityID = 0 }, func(r *effectiveCreditTuple) { r.ordinal = -1 },
		func(r *effectiveCreditTuple) { r.personID = sql.NullInt64{Int64: 7} }, func(r *effectiveCreditTuple) { r.sourceRowID = sql.NullString{String: "inactive"} },
		func(r *effectiveCreditTuple) { r.ownerEntryID = sql.NullString{String: "owner:1", Valid: true} }, func(r *effectiveCreditTuple) { r.ownerIncarnation.present = true },
		func(r *effectiveCreditTuple) { r.ownerIncarnation.value[0] = 1 }, func(r *effectiveCreditTuple) { r.ownerRevision = sql.NullInt64{Int64: 1, Valid: true} },
		func(r *effectiveCreditTuple) { r.creditedName = string([]byte{255}) }, func(r *effectiveCreditTuple) { r.sourceStatus = "unknown" },
	} {
		changed := row
		mutate(&changed)
		if _, err := effectiveCreditTupleBytes(changed, 1<<20); !errors.Is(err, ErrInvalid) {
			t.Fatal("malformed tuple accepted", err)
		}
	}
	cursor, err := newEffectiveCreditManifestCheckpoint(context, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*effectiveCreditManifestCheckpoint){
		func(c *effectiveCreditManifestCheckpoint) { c.seed[0] ^= 1 }, func(c *effectiveCreditManifestCheckpoint) { c.chain[0] ^= 1 },
		func(c *effectiveCreditManifestCheckpoint) { c.nextOrdinal = -1 }, func(c *effectiveCreditManifestCheckpoint) { c.nextOrdinal = 1 },
		func(c *effectiveCreditManifestCheckpoint) { c.canonicalBytes = -1 }, func(c *effectiveCreditManifestCheckpoint) { c.canonicalBytes = 1 },
	} {
		changed := cursor
		mutate(&changed)
		after, err := advanceEffectiveCreditManifest(context, changed, row, 1<<20)
		if err == nil || after != changed {
			t.Fatal("checkpoint refusal mutated state", err)
		}
	}
	short := context
	short.canonicalBytes--
	empty, err := newEffectiveCreditManifestCheckpoint(short, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := advanceEffectiveCreditManifest(short, empty, row, 1<<20); err == nil || got != empty {
		t.Fatal("declared-byte overflow accepted")
	}
	for _, mutate := range []func(*effectiveCreditManifestContext){
		func(c *effectiveCreditManifestContext) { c.entityID = 0 }, func(c *effectiveCreditManifestContext) { c.count = -1 }, func(c *effectiveCreditManifestContext) { c.canonicalBytes = -1 }, func(c *effectiveCreditManifestContext) { c.language = string([]byte{255}) },
	} {
		changed := context
		mutate(&changed)
		if _, err := effectiveCreditManifestSeed(changed, 1<<20); err == nil {
			t.Fatal("invalid context accepted")
		}
	}
	// Native I64 source sentinel and exact UTF-8 (including NUL/NFC/NFD) are not
	// normalized or coerced by the byte primitive. Reference resolution is separate.
	signed := row
	signed.sourceOrdinal = math.MinInt64
	b, err := effectiveCreditTupleBytes(signed, 1<<20)
	if err != nil || bytes.Equal(b, encoded) {
		t.Fatal("signed source ordinal lost", err)
	}
	nfc, nfd := row, row
	nfc.creditedName = "é\x00"
	nfd.creditedName = "e\u0301\x00"
	a, err := effectiveCreditTupleBytes(nfc, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	b, err = effectiveCreditTupleBytes(nfd, 1<<20)
	if err != nil || bytes.Equal(a, b) {
		t.Fatal("native text normalized", err)
	}
	huge := row
	huge.creditedName = strings.Repeat("x", 1<<20)
	if out, err := effectiveCreditTupleBytes(huge, 128); err == nil || out != nil {
		t.Fatal("preallocation envelope bypass")
	}
	beforeName := strings.Clone(row.creditedName)
	owned, err := effectiveCreditTupleBytes(row, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	at := bytes.Index(owned, []byte(beforeName))
	if at < 0 {
		t.Fatal("missing credited name bytes")
	}
	owned[at] ^= 1
	again, err := effectiveCreditTupleBytes(row, 1<<20)
	if err != nil || row.creditedName != beforeName || !bytes.Equal(again, encoded) {
		t.Fatal("encoded output aliases retained tuple", err)
	}
}

func TestEffectiveCreditManifestContextCoverageAndTerminalBounds(t *testing.T) {
	data := readCreditManifestTestVectors(t)
	context := creditManifestTestContext(t, data.Positive[1])
	cursor, err := newEffectiveCreditManifestCheckpoint(context, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	changes := map[string]func(*effectiveCreditManifestContext){
		"entity":          func(c *effectiveCreditManifestContext) { c.entityID++ },
		"current_input":   func(c *effectiveCreditManifestContext) { c.currentInput[0] ^= 1 },
		"source_manifest": func(c *effectiveCreditManifestContext) { c.sourceManifest[31] ^= 1 },
		"language":        func(c *effectiveCreditManifestContext) { c.language += "x" },
		"count":           func(c *effectiveCreditManifestContext) { c.count++ },
		"canonical_bytes": func(c *effectiveCreditManifestContext) { c.canonicalBytes++ },
	}
	row := creditManifestNativeTestRow(t, data.Positive[1].Rows[0])
	for name, mutate := range changes {
		t.Run(name, func(t *testing.T) {
			changed := context
			mutate(&changed)
			seed, err := effectiveCreditManifestSeed(changed, 1<<20)
			if err != nil || seed == cursor.seed {
				t.Fatal("context field omitted from seed", err)
			}
			after, err := advanceEffectiveCreditManifest(changed, cursor, row, 1<<20)
			if err == nil || after != cursor {
				t.Fatal("checkpoint admitted in another context")
			}
		})
	}
	done, err := advanceEffectiveCreditManifest(context, cursor, row, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	extra := row
	extra.ordinal = 1
	if after, err := advanceEffectiveCreditManifest(context, done, extra, 1<<20); err == nil || after != done {
		t.Fatal("extra tuple beyond declared count accepted")
	}
	// Arithmetic stays in nonnegative SQLiteINTEGER without count/byte wrap.
	enormous := context
	enormous.count = math.MaxInt64
	enormous.canonicalBytes = math.MaxInt64
	huge, err := newEffectiveCreditManifestCheckpoint(enormous, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	huge.nextOrdinal = math.MaxInt64 - 1
	huge.canonicalBytes = math.MaxInt64
	extra.ordinal = huge.nextOrdinal
	if after, err := advanceEffectiveCreditManifest(enormous, huge, extra, 1<<20); err == nil || after != huge {
		t.Fatal("impossible or overflowing checkpoint accepted")
	}
	if _, err := effectiveCreditManifestSeed(context, 1); err == nil {
		t.Fatal("seed encoding budget ignored")
	}
	wide := context
	wide.language = strings.Repeat("x", 1<<20)
	if _, err := effectiveCreditManifestSeed(wide, 128); err == nil {
		t.Fatal("context size not preflighted")
	}
}
