package writer

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type evidenceHeaderVector struct {
	Name         string              `json:"name"`
	Input        EvidenceHeaderInput `json:"input"`
	CanonicalHex string              `json:"canonical_hex"`
	SHA256       string              `json:"sha256"`
}

func evidenceHeaderVectors(t *testing.T) []evidenceHeaderVector {
	t.Helper()
	data, err := os.Open("testdata/enrichment_evidence_header_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	var fixture struct {
		Codec   string                 `json:"codec"`
		Vectors []evidenceHeaderVector `json:"vectors"`
	}
	decoder := json.NewDecoder(data)
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Codec != "portico.evidence_header_fields.v1" || len(fixture.Vectors) != 5 {
		t.Fatal("wrong header vector contract")
	}
	return fixture.Vectors
}

type evidenceCanonicalRecorder struct {
	hash.Hash
	canonical []byte
}

func (r *evidenceCanonicalRecorder) Write(p []byte) (int, error) {
	r.canonical = append(r.canonical, p...)
	return r.Hash.Write(p)
}

func TestEvidenceHeaderFullyPopulatedCanonicalVectors(t *testing.T) {
	// Expected bytes were produced by a separate Python hashlib/struct encoder
	// with literal semantic field names. No product codec or binding helper is
	// used to construct the expected stream or its field positions.
	vectors := evidenceHeaderVectors(t)
	if vectors[0].Name != "fully_populated" || vectors[0].SHA256 != "b8654c0f5b594439a907721fc7d9377179c59e680063088c224a096f2c1762f9" {
		t.Fatal("fully populated contract pin changed")
	}
	full := vectors[0].Input
	seen := map[string]bool{}
	rv := reflect.ValueOf(full)
	for n := range rv.NumField() {
		if rv.Field(n).Kind() != reflect.String {
			continue
		}
		value := rv.Field(n).String()
		if value == "" || seen[value] {
			t.Fatalf("populated fixture field %s is empty/repeated", rv.Type().Field(n).Name)
		}
		seen[value] = true
	}
	if !full.ArtistType.Present || full.ArtistType.Value == "" || seen[full.ArtistType.Value] {
		t.Fatal("populated optional text is not distinct")
	}
	numbers := map[int64]bool{full.SubjectID: true, full.Year: true}
	if full.SubjectID == 0 || full.Year == 0 || full.SubjectID == full.Year {
		t.Fatal("populated native integers are not distinct")
	}
	for _, v := range []EvidenceInteger{full.LengthMillis, full.BeginYear, full.EndYear} {
		if !v.Present || v.Value == 0 || numbers[v.Value] {
			t.Fatal("populated optional integer is absent/zero/repeated")
		}
		numbers[v.Value] = true
	}
	names, digests := map[string]bool{}, map[string]bool{}
	for _, vector := range vectors {
		t.Run(vector.Name, func(t *testing.T) {
			if names[vector.Name] || digests[vector.SHA256] {
				t.Fatal("duplicate named vector or NULL/zero/empty digest")
			}
			names[vector.Name], digests[vector.SHA256] = true, true
			expected, err := hex.DecodeString(vector.CanonicalHex)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(expected)
			if hex.EncodeToString(sum[:]) != vector.SHA256 {
				t.Fatal("reference stream and digest disagree")
			}
			header, err := NewEvidenceHeader(vector.Input)
			if err != nil {
				t.Fatal(err)
			}
			recorded := &evidenceCanonicalRecorder{Hash: sha256.New()}
			if err := evidenceHeaderDigest(recorded, header); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(recorded.canonical, expected) {
				t.Fatal("canonical bytes differ from field-position vector")
			}
			if hex.EncodeToString(recorded.Sum(nil)) != vector.SHA256 {
				t.Fatal("canonical digest differs from vector")
			}
		})
	}
}

// This independent SELECT/Scan mapping must not be generated from the INSERT
// column list, evidenceHeaderStrings, or evidenceHeaderValues: a valid INSERT
// column permutation must change the observed named fields and fail this check.
const evidenceReadbackColumns = `id,subject_id,parser_version,provider,subject_kind,provider_id,
 language,region,request_shape,digest,observed_at,source_url,etag,last_modified,
 title,original_title,overview,tagline,content_rating,studio,network,country,
 release_date,year,artist,length_millis,artist_type,begin_year,end_year,wikidata_id,
 biography,biography_source,biography_source_url,disambiguation,release_group_id,
 group_title,primary_type,barcode,sealed,seal_proof_owner,seal_proof_incarnation,seal_proof_sequence,seal_proof_kind,
 birth_date,death_date,birth_place,ip_seal_owner,ip_seal_incarnation,ip_seal_sequence,ip_seal_kind`

func evidenceSQLReadback(t *testing.T, conn *sql.Conn, registry *stmt.Registry) {
	t.Helper()
	// Refuse a schema change that leaves a newly added column unobserved.
	// Include the adopted generated proof kind too: an added stored OR generated
	// column must be independently listed/scanned, never filled from PRAGMA.
	rows, err := conn.QueryContext(t.Context(), "PRAGMA table_xinfo(metadata_evidence)")
	if err != nil {
		t.Fatal(err)
	}
	var columns []string
	for rows.Next() {
		var cid, required, key, hidden int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &required, &defaultValue, &key, &hidden); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		columns = append(columns, name)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	for i, column := range strings.Split(evidenceReadbackColumns, ",") {
		if i >= len(columns) || columns[i] != strings.TrimSpace(column) {
			t.Fatal("header SELECT no longer covers schema columns in order")
		}
	}
	if len(columns) != len(strings.Split(evidenceReadbackColumns, ",")) {
		t.Fatal("header SELECT omitted a schema column")
	}
	insert, err := registry.Lookup(enrichmentEvidenceInsert)
	if err != nil {
		t.Fatal(err)
	}
	for _, vector := range evidenceHeaderVectors(t) {
		t.Run("readback_"+vector.Name, func(t *testing.T) {
			want := vector.Input
			header, err := NewEvidenceHeader(want)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := conn.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err = tx.ExecContext(t.Context(), "INSERT INTO metadata_providers(code,adapter,enabled) VALUES(?,?,1)", want.Provider, "readback-fixture"); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.ExecContext(t.Context(), `INSERT INTO metadata_provider_subjects(
 id,provider,subject_kind,provider_id,language,region,request_shape,incarnation,revision
) VALUES(?,?,?,?,?,?,?,?,1)`, want.SubjectID, want.Provider, want.SubjectKind, want.ProviderID, want.Language, want.Region, want.RequestShape, bytes.Repeat([]byte{9}, 16)); err != nil {
				t.Fatal(err)
			}
			bound, err := insert.Bind(evidenceHeaderValues(header.input)...)
			if err != nil {
				t.Fatal(err)
			}
			result, err := bound.ExecContext(t.Context(), tx)
			if err != nil {
				t.Fatal(err)
			}
			if n, err := result.RowsAffected(); err != nil || n != 1 {
				t.Fatalf("header insert changed %d rows: %v", n, err)
			}
			var got EvidenceHeaderInput
			var digest string
			var length, begin, end sql.NullInt64
			var artistType sql.NullString
			var sealed int64
			var sealProofOwner, sealProofKind sql.NullString
			var sealProofIncarnation []byte
			var sealProofSequence sql.NullInt64
			var birthDate, deathDate, birthPlace string
			var ipSealOwner, ipSealKind sql.NullString
			var ipSealIncarnation []byte
			var ipSealSequence sql.NullInt64
			err = tx.QueryRowContext(t.Context(), "SELECT "+evidenceReadbackColumns+" FROM metadata_evidence WHERE id=?", want.ID).Scan(
				&got.ID, &got.SubjectID, &got.ParserVersion, &got.Provider, &got.SubjectKind, &got.ProviderID,
				&got.Language, &got.Region, &got.RequestShape, &digest, &got.ObservedAt, &got.SourceURL, &got.ETag, &got.LastModified,
				&got.Title, &got.OriginalTitle, &got.Overview, &got.Tagline, &got.ContentRating, &got.Studio, &got.Network, &got.Country,
				&got.ReleaseDate, &got.Year, &got.Artist, &length, &artistType, &begin, &end, &got.WikidataID,
				&got.Biography, &got.BiographySource, &got.BiographySourceURL, &got.Disambiguation, &got.ReleaseGroupID,
				&got.GroupTitle, &got.PrimaryType, &got.Barcode, &sealed,
				&sealProofOwner, &sealProofIncarnation, &sealProofSequence, &sealProofKind,
				&birthDate, &deathDate, &birthPlace, &ipSealOwner, &ipSealIncarnation, &ipSealSequence, &ipSealKind,
			)
			if err != nil {
				t.Fatal(err)
			}
			rawDigest, err := hex.DecodeString(digest)
			if err != nil || len(rawDigest) != len(got.ObservationDigest) {
				t.Fatal("bad stored observation digest", err)
			}
			copy(got.ObservationDigest[:], rawDigest)
			got.LengthMillis = EvidenceInteger{Value: length.Int64, Present: length.Valid}
			got.BeginYear = EvidenceInteger{Value: begin.Int64, Present: begin.Valid}
			got.EndYear = EvidenceInteger{Value: end.Int64, Present: end.Valid}
			got.ArtistType = EvidenceText{Value: artistType.String, Present: artistType.Valid}
			if !reflect.DeepEqual(got, want) || sealed != 0 {
				t.Fatalf("named header SQL readback differs: got=%+v sealed=%d want=%+v", got, sealed, want)
			}
			if sealProofOwner.Valid || sealProofIncarnation != nil || sealProofSequence.Valid || sealProofKind.Valid {
				t.Fatalf("unsealed header proof fields must be NULL: owner=%+v incarnation=%v sequence=%+v kind=%+v",
					sealProofOwner, sealProofIncarnation, sealProofSequence, sealProofKind)
			}
			// EN's explicit header INSERT neither supplies person data nor acquires
			// an IP seal. Independently observe all seven adopted D defaults.
			if birthDate != "" || deathDate != "" || birthPlace != "" || ipSealOwner.Valid || ipSealIncarnation != nil || ipSealSequence.Valid || ipSealKind.Valid {
				t.Fatalf("EN header acquired person fields or IP proof: birth=%q death=%q place=%q owner=%+v incarnation=%v sequence=%+v kind=%+v",
					birthDate, deathDate, birthPlace, ipSealOwner, ipSealIncarnation, ipSealSequence, ipSealKind)
			}
			violations, err := tx.QueryContext(t.Context(), "PRAGMA foreign_key_check")
			if err != nil {
				t.Fatal(err)
			}
			bad := violations.Next()
			if err := errors.Join(violations.Err(), violations.Close()); err != nil || bad {
				t.Fatalf("readback fixture broke foreign keys: %v", err)
			}
		})
	}
}
