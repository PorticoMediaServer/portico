package plan_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"database/sql/driver"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	sqlite "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func enSealOctetDefinitions() []stmt.Definition {
	return []stmt.Definition{
		{ID: "enrichment.seal.receipt_lengths", SQL: `SELECT octet_length(provider_operation_id),octet_length(command_incarnation),octet_length(operation_id),octet_length(request_digest),octet_length(command_kind),octet_length(after_item),octet_length(publication_id),octet_length(result_digest),octet_length(seal_evidence_id),octet_length(seal_parser_version),octet_length(seal_provider),octet_length(seal_subject_kind),octet_length(seal_provider_id),octet_length(seal_language),octet_length(seal_region),octet_length(seal_request_shape),octet_length(seal_observation_digest),octet_length(seal_build_id),octet_length(seal_algorithm),octet_length(seal_commitment),octet_length(upstream_evidence_owner),octet_length(upstream_evidence_incarnation),octet_length(source_dependency_owner),octet_length(source_dependency_incarnation),octet_length(source_dependency_publication_id),octet_length(source_dependency_provider) FROM enrichment_command_receipts WHERE provider_operation_id=? AND command_incarnation=? AND command_sequence=?`, Owner: "EN octet component", Kind: stmt.Read, CallSites: []string{"en_seal_octets_test.go"}, Params: []stmt.Param{{Name: "owner", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob}, {Name: "sequence", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"enrichment_command_receipts"}}, Cohorts: []stmt.Cohort{{Name: "missing_receipt", Bindings: []any{"missing", make([]byte, 16), int64(1)}}}},
		{ID: "enrichment.seal.evidence_header_lengths", SQL: `SELECT octet_length(id),octet_length(parser_version),octet_length(provider),octet_length(subject_kind),octet_length(provider_id),octet_length(language),octet_length(region),octet_length(request_shape),octet_length(digest),octet_length(observed_at),octet_length(source_url),octet_length(etag),octet_length(last_modified),octet_length(title),octet_length(original_title),octet_length(overview),octet_length(tagline),octet_length(content_rating),octet_length(studio),octet_length(network),octet_length(country),octet_length(release_date),octet_length(artist),octet_length(artist_type),octet_length(wikidata_id),octet_length(biography),octet_length(biography_source),octet_length(biography_source_url),octet_length(disambiguation),octet_length(release_group_id),octet_length(group_title),octet_length(primary_type),octet_length(barcode),octet_length(seal_proof_owner),octet_length(seal_proof_incarnation) FROM metadata_evidence INDEXED BY sqlite_autoindex_metadata_evidence_1 WHERE id=? AND subject_id=? AND parser_version=?`, Owner: "EN octet component", Kind: stmt.Read, CallSites: []string{"en_seal_octets_test.go"}, Params: []stmt.Param{{Name: "evidence", Type: stmt.Text}, {Name: "subject", Type: stmt.Integer}, {Name: "parser", Type: stmt.Text}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_metadata_evidence_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "metadata_evidence", Name: "sqlite_autoindex_metadata_evidence_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}}, Cohorts: []stmt.Cohort{{Name: "native_header", Bindings: []any{"native", int64(201), "parser-v1"}}, {Name: "wrong_subject", Bindings: []any{"native", int64(202), "parser-v1"}}}},
	}
}

func enSealOctetFixture(t *testing.T) (*sql.Conn, plan.Fixture, plan.IndexPolicy) {
	t.Helper()
	c, f, p := issuerOctetsFixture(t)
	for _, q := range []string{
		`INSERT INTO metadata_providers(code,adapter,enabled) VALUES('tmdb','tmdb',1)`,
		`INSERT INTO metadata_provider_subjects(id,provider,subject_kind,provider_id,language,region,request_shape,incarnation,revision) VALUES(201,'tmdb','movie','42','en','CA','shape-v1',zeroblob(16),1)`,
		`INSERT INTO metadata_evidence(id,subject_id,parser_version,provider,subject_kind,provider_id,language,region,request_shape,digest,observed_at,source_url) VALUES('native',201,'parser-v1','tmdb','movie','42','en','CA','shape-v1',printf('%064d',0),'2026-11-07T00:00:00Z','https://example.invalid')`,
	} {
		if _, err := c.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.ExecContext(t.Context(), "UPDATE metadata_evidence SET overview=? WHERE id='native'", "é\x00x"); err != nil {
		t.Fatal(err)
	}
	f.Implementations = "canonical010 EN receipt/full-PK and evidence/id byte points; native UTF8+embedded NUL, NULL proof tags; no disclosure/provenance"
	return c, f, p
}

func TestENSealOctetPointsNative(t *testing.T) {
	c, f, p := enSealOctetFixture(t)
	defs := enSealOctetDefinitions()
	for _, d := range defs {
		if len(plan.LintSQL(d.SQL)) == 0 || len(plan.LintSQLOn(t.Context(), c, d.SQL)) == 0 {
			t.Fatal("generic octet grammar widened")
		}
	}
	for _, d := range defs {
		rows, err := c.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+d.SQL, d.Cohorts[0].Bindings...)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var a, b, c int
			var detail string
			if err := rows.Scan(&a, &b, &c, &detail); err != nil {
				t.Fatal(err)
			}
			t.Logf("actual %s: %s", d.ID, detail)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	registry, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	g, err := plan.Capture(t.Context(), c, registry, f, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Plans) != 3 || g.ForeignKeys != nil {
		t.Fatal("wrong read-only native inventory")
	}
	executable, err := stmt.New(defs, g.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	st, err := executable.Lookup("enrichment.seal.evidence_header_lengths")
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.Bind("native", int64(201), "parser-v1")
	if err != nil {
		t.Fatal(err)
	}
	values, dest := make([]any, 35), make([]any, 35)
	for i := range values {
		dest[i] = &values[i]
	}
	if err = b.QueryRowContext(t.Context(), c).Scan(dest...); err != nil {
		t.Fatal(err)
	}
	if values[0] != int64(6) || values[8] != int64(64) || values[15] != int64(4) || values[33] != nil || values[34] != nil {
		t.Fatal("native octets/nullness changed", values)
	}
	data, err := g.JSON()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "en-seal-octets.json.gz")
	source, err := os.Open(path)
	if os.IsNotExist(err) {
		evidence := os.Getenv("PORTICO_LANE_EVIDENCE_DIR")
		if evidence == "" {
			t.Fatal("collect and review the initial hosted EN octet golden")
		}
		if err := os.MkdirAll(evidence, 0700); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		z := gzip.NewWriter(&out)
		if _, err = z.Write(append(data, '\n')); err != nil {
			t.Fatal(err)
		}
		if err = z.Close(); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(evidence, "en-seal-octets.proposed.json.gz"), out.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		t.Log("proposed baseline only; not a reviewed comparison")
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	z, err := gzip.NewReader(source)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	if err = g.Compare(expected); err != nil {
		t.Fatal(err)
	}
}

func TestENSealOctetPointsClosed(t *testing.T) {
	c, f, p := enSealOctetFixture(t)
	for _, original := range enSealOctetDefinitions() {
		for _, change := range []string{"foreign_id", "range", "nullable"} {
			t.Run(string(original.ID)+"/"+change, func(t *testing.T) {
				d := original
				d.Params = append([]stmt.Param(nil), original.Params...)
				switch change {
				case "foreign_id":
					d.ID = "unregistered.en.point"
				case "range":
					d.SQL = strings.Replace(d.SQL, "=?", ">?", 1)
				case "nullable":
					d.Params[0].Nullable = true
				}
				g, err := captureIssuerOctets(t, c, f, p, d)
				if err == nil {
					t.Fatal("nonclosed EN point accepted")
				}
				if _, err := stmt.New([]stmt.Definition{d}, g.Receipt()); err == nil {
					t.Fatal("failed Capture minted capability")
				}
			})
		}
	}
}

func TestENSealOctetPointsRejectShadowedBuiltin(t *testing.T) {
	const marker = "PORTICO_EN_OCTET_SHADOW_CHILD"
	if os.Getenv(marker) != "1" {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestENSealOctetPointsRejectShadowedBuiltin$", "-test.count=1")
		cmd.Env = append(os.Environ(), marker+"=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("shadow control: %v\n%s", err, output)
		}
		return
	}
	if err := sqlite.RegisterDeterministicScalarFunction("OCTET_LENGTH", 1, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) { return int64(777), nil }); err != nil {
		t.Fatal(err)
	}
	c, f, p := enSealOctetFixture(t)
	for _, d := range append(enSealOctetDefinitions(), enEffectiveSourceOctetDefinitions(t)...) {
		if _, err := captureIssuerOctets(t, c, f, p, d); err == nil || !strings.Contains(err.Error(), "unshadowed native") {
			t.Fatal("override accepted", err)
		}
	}
}
