package plan_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	sqlite "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// Inert literals copied from W's pinned data packet, never W's draft verifier.
func enEffectiveSourceOctetDefinitions(t *testing.T) []stmt.Definition {
	t.Helper()
	raw, err := os.ReadFile("testdata/en-effective-source-octets-input.json")
	if err != nil {
		t.Fatal(err)
	}
	var input struct {
		Source      string            `json:"source"`
		Packet      string            `json:"source_packet_sha256"`
		Definitions []stmt.Definition `json:"definitions"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err = d.Decode(&input); err != nil {
		t.Fatal(err)
	}
	if input.Source != "bb4974a29069f5e445c4e192a397a7469b7a1101" || input.Packet != "33ba0d732e20ad3d2ce3ea3b656177d5b7e4c854184e9228f4b48775392092dd" || len(input.Definitions) != 3 {
		t.Fatal("unreviewed Effective byte-point input")
	}
	for i := range input.Definitions {
		def := &input.Definitions[i]
		for j := range def.Cohorts[0].Bindings {
			switch def.Params[j].Type {
			case stmt.Blob:
				def.Cohorts[0].Bindings[j], err = base64.StdEncoding.DecodeString(def.Cohorts[0].Bindings[j].(string))
			case stmt.Integer:
				def.Cohorts[0].Bindings[j], err = def.Cohorts[0].Bindings[j].(json.Number).Int64()
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		def.Cohorts[0].Name = "missing_exact_key"
		switch def.ID {
		case "enrichment.effective_source.pin_evidence_lengths":
			def.Cohorts = append(def.Cohorts, stmt.Cohort{Name: "native_evidence", Bindings: []any{"native"}})
		case "enrichment.effective_source.pin_publication_lengths":
			def.Cohorts[0].Bindings[0] = strings.Repeat("f", 32)
			def.Cohorts = append(def.Cohorts, stmt.Cohort{Name: "native_publication", Bindings: []any{"00112233445566778899aabbccddeeff"}})
		case "enrichment.effective_source.pin_receipt_lengths":
		default:
			t.Fatal("unexpected ID", def.ID)
		}
	}
	return input.Definitions
}

func TestENEffectiveSourceOctetPointsNative(t *testing.T) {
	c, f, policy := enEffectiveSourceOctetFixture(t)
	for _, sql := range []string{
		`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0)`,
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'lib','/fixture')`,
		`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(101,zeroblob(16),7,1)`,
		`INSERT INTO credit_source_publications(publication_id,entity_id,provider,accepted_record_reference,observed_at,member_count,sealed) VALUES('00112233445566778899aabbccddeeff',101,'tmdb','opaque:retained','2026-11-07T00:00:00Z',3,0)`,
	} {
		if _, err := c.ExecContext(t.Context(), sql); err != nil {
			t.Fatalf("fixture %s: %v", sql, err)
		}
	}
	f.Implementations = "current canonical three closed Effective source pin points; native E/P unsealed with NULL proof tags, receipt deliberately absent; no verifier/authority"
	defs := enEffectiveSourceOctetDefinitions(t)
	r, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	g, err := plan.Capture(t.Context(), c, r, f, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Plans) != 5 || len(g.Manifest) != 3 || g.ForeignKeys != nil {
		t.Fatal("wrong Effective byte point inventory")
	}
	exec, err := stmt.New(defs, g.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id   stmt.ID
		key  string
		want []any
	}{
		{"enrichment.effective_source.pin_evidence_lengths", "native", []any{int64(6), int64(201), int64(9), int64(4), int64(0), nil, nil, nil, nil}},
		{"enrichment.effective_source.pin_publication_lengths", "00112233445566778899aabbccddeeff", []any{int64(32), int64(101), int64(4), int64(3), int64(0), nil, nil, nil, nil}},
	} {
		s, err := exec.Lookup(tc.id)
		if err != nil {
			t.Fatal(err)
		}
		b, err := s.Bind(tc.key)
		if err != nil {
			t.Fatal(err)
		}
		got, dest := make([]any, len(tc.want)), make([]any, len(tc.want))
		for i := range got {
			dest[i] = &got[i]
		}
		if err = b.QueryRowContext(t.Context(), c).Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("%s column%d = %#v, want %#v", tc.id, i, got[i], tc.want[i])
			}
		}
	}
	data, err := g.JSON()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "en-effective-source-octets.json.gz")
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR")
		if dir == "" {
			t.Fatal("collect and review proposed Effective byte point golden")
		}
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		z := gzip.NewWriter(&b)
		if _, err = z.Write(append(data, '\n')); err != nil {
			t.Fatal(err)
		}
		if err = z.Close(); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "en-effective-source-octets.proposed.json.gz"), b.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		t.Log("proposal only; owner review and committed comparison still required")
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	z, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	if err = g.Compare(baseline); err != nil {
		t.Fatal(err)
	}
}

func TestENEffectiveSourceOctetPointsClosed(t *testing.T) {
	c, f, p := enEffectiveSourceOctetFixture(t)
	for _, original := range enEffectiveSourceOctetDefinitions(t) {
		if len(plan.LintSQL(original.SQL)) == 0 || len(plan.LintSQLOn(t.Context(), c, original.SQL)) == 0 {
			t.Fatal("generic octet grammar widened")
		}
		for _, change := range []string{"foreign_id", "range", "typeof", "cast"} {
			t.Run(string(original.ID)+"/"+change, func(t *testing.T) {
				d := original
				switch change {
				case "foreign_id":
					d.ID = "unregistered.effective.pin"
				case "range":
					d.SQL = strings.Replace(d.SQL, "=?", ">?", 1)
				case "typeof":
					d.SQL = strings.Replace(d.SQL, "SELECT ", "SELECT typeof(1),", 1)
				case "cast":
					d.SQL = strings.Replace(d.SQL, "octet_length(", "octet_length(CAST(", 1)
					i := strings.Index(d.SQL, ")")
					d.SQL = d.SQL[:i] + " AS BLOB)" + d.SQL[i:]
				}
				g, err := captureIssuerOctets(t, c, f, p, d)
				if err == nil {
					t.Fatal("nonexact Effective octet shape accepted")
				}
				if _, err := stmt.New([]stmt.Definition{d}, g.Receipt()); err == nil {
					t.Fatal("failed capture minted qualification")
				}
			})
		}
	}
}

func enEffectiveSourceOctetFixture(t *testing.T) (*sql.Conn, plan.Fixture, plan.IndexPolicy) {
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
	return c, f, p
}
func TestENEffectiveSourceOctetPointsRejectShadowedBuiltin(t *testing.T) {
	const marker = "PORTICO_EN_EFFECTIVE_OCTET_SHADOW_CHILD"
	if os.Getenv(marker) != "1" {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestENEffectiveSourceOctetPointsRejectShadowedBuiltin$", "-test.count=1")
		cmd.Env = append(os.Environ(), marker+"=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("shadow control: %v\n%s", err, out)
		}
		return
	}
	if err := sqlite.RegisterDeterministicScalarFunction("OCTET_LENGTH", 1, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) { return int64(777), nil }); err != nil {
		t.Fatal(err)
	}
	c, f, p := enEffectiveSourceOctetFixture(t)
	for _, d := range enEffectiveSourceOctetDefinitions(t) {
		if _, err := captureIssuerOctets(t, c, f, p, d); err == nil || !strings.Contains(err.Error(), "unshadowed native") {
			t.Fatal("override accepted", err)
		}
	}
}
