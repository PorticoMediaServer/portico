package plan

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
)

// SchemaEvidence is the single regenerated schema baseline. It contains no
// command SQL, cohorts or statement VM. Those remain in each owner's golden.
type SchemaEvidence struct {
	Engine            Rows                           `json:"engine"`
	Version           int                            `json:"version"`
	SchemaSQLSHA256   string                         `json:"schemaSQLSha256"`
	IndexPolicySHA256 string                         `json:"indexPolicySha256"`
	Tables            map[string]schemaTableEvidence `json:"tables"`
	ForeignKeys       []ForeignKeyPlan               `json:"foreignKeys"`
}
type schemaTableEvidence struct {
	Columns      Rows            `json:"columns"`
	Indexes      Rows            `json:"indexes"`
	IndexColumns map[string]Rows `json:"indexColumns"`
	IndexKeys    map[string]Rows `json:"indexKeys"`
	WithoutRowid bool            `json:"withoutRowid"`
}

// SchemaBinding marks a Capture whose complete live catalogue matched the
// embedded layout. It is evidence metadata, never a registration capability.
type SchemaBinding struct {
	Version   int    `json:"version"`
	SQLSHA256 string `json:"sqlSha256"`
}

//go:embed testdata/schema-evidence.json.gz
var schemaEvidenceBytes []byte
var schemaEvidenceOnce sync.Once
var cachedSchemaEvidence SchemaEvidence
var schemaEvidenceError error

func sharedSchemaEvidence() (*SchemaEvidence, error) {
	schemaEvidenceOnce.Do(func() {
		z, err := gzip.NewReader(bytes.NewReader(schemaEvidenceBytes))
		if err != nil {
			schemaEvidenceError = err
			return
		}
		defer z.Close()
		d := json.NewDecoder(z)
		d.UseNumber()
		schemaEvidenceError = d.Decode(&cachedSchemaEvidence)
		if schemaEvidenceError == nil {
			schemaEvidenceError = checkEvidenceIdentity(cachedSchemaEvidence)
		}
	})
	return &cachedSchemaEvidence, schemaEvidenceError
}
func embeddedPolicySHA() string {
	for _, a := range schema.Contracts() {
		if a.Name == "INT-INDEX-POLICY.json" {
			return a.SHA256
		}
	}
	return ""
}
func checkEvidenceIdentity(e SchemaEvidence) error {
	if e.Version != 1 || e.SchemaSQLSHA256 != schema.SHA256 || e.IndexPolicySHA256 != embeddedPolicySHA() || len(e.Tables) == 0 || len(e.ForeignKeys) == 0 || len(e.Engine.Values) != 1 {
		return fmt.Errorf("plan: shared schema evidence is stale; regenerate it once with the schema embed")
	}
	return nil
}

// CaptureSchemaEvidence is read-only and uses the same caller-owned, enforced
// connection as Capture. Regenerate this ONE file in a schema embed. It must
// pass again with recording disabled; no per-command environment regeneration.
func CaptureSchemaEvidence(ctx context.Context, c *sql.Conn, policy sqliteconn.Config) (SchemaEvidence, error) {
	e := SchemaEvidence{Version: 1, SchemaSQLSHA256: schema.SHA256, IndexPolicySHA256: embeddedPolicySHA(), Tables: map[string]schemaTableEvidence{}}
	var err error
	e.Engine, err = query(ctx, c, "SELECT sqlite_version(),sqlite_source_id()")
	if err != nil {
		return e, err
	}
	catalog, err := query(ctx, c, "SELECT type,name,tbl_name,rootpage,sql FROM main.sqlite_schema ORDER BY type,name")
	if err != nil {
		return e, err
	}
	if err = checkEmbeddedCatalog(catalog); err != nil {
		return e, err
	}
	if _, err = sqliteconn.Assert(ctx, c, policy); err != nil {
		return e, err
	}
	for _, r := range catalog.Values {
		if r[0] != "table" || strings.HasPrefix(r[1].(string), "sqlite_") {
			continue
		}
		name := r[1].(string)
		t := schemaTableEvidence{IndexColumns: map[string]Rows{}, IndexKeys: map[string]Rows{}}
		t.Columns, err = query(ctx, c, "PRAGMA main.table_xinfo("+identifier(name)+")")
		if err != nil {
			return e, err
		}
		t.Indexes, err = query(ctx, c, "PRAGMA main.index_list("+identifier(name)+")")
		if err != nil {
			return e, err
		}
		var wr int64
		if err = c.QueryRowContext(ctx, "SELECT wr FROM main.pragma_table_list() WHERE schema='main' AND name=?", name).Scan(&wr); err != nil {
			return e, err
		}
		t.WithoutRowid = wr != 0
		for _, idx := range t.Indexes.Values {
			n := idx[1].(string)
			t.IndexColumns[n], err = query(ctx, c, "PRAGMA main.index_xinfo("+identifier(n)+")")
			if err != nil {
				return e, err
			}
			t.IndexKeys[n], err = query(ctx, c, "PRAGMA main.index_info("+identifier(n)+")")
			if err != nil {
				return e, err
			}
		}
		e.Tables[name] = t
	}
	f, err := ForeignKeys(ctx, c, policy)
	e.ForeignKeys = f.Plans
	return e, err
}

// This uses the schema owner's exact layout encoding, excluding only SQLite's
// two ANALYZE tables. Root pages are physical addresses: require well-formed,
// unambiguous addresses but bind declarations by exact native object identity.
func checkEmbeddedCatalog(rows Rows) error {
	if !slices.Equal(rows.Columns, []string{"type", "name", "tbl_name", "rootpage", "sql"}) {
		return fmt.Errorf("plan: unexpected schema catalogue columns")
	}
	h := sha256.New()
	io.WriteString(h, "portico-native-layout-sha256-v1\x00")
	text := func(s string) {
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(len(s)))
		h.Write(b[:])
		io.WriteString(h, s)
	}
	text(schema.SHA256)
	text(schema.NativeSQLiteVersion)
	count := 0
	roots := map[int64]bool{}
	last := ""
	for _, row := range rows.Values {
		if len(row) != 5 {
			return fmt.Errorf("plan: malformed schema catalogue")
		}
		kind, k := row[0].(string)
		name, n := row[1].(string)
		table, t := row[2].(string)
		root, r := integerValue(row[3])
		if !k || !n || !t || !r || root < 0 {
			return fmt.Errorf("plan: invalid schema catalogue row")
		}
		order := kind + "\x00" + name
		if order <= last {
			return fmt.Errorf("plan: schema catalogue order/identity changed")
		}
		last = order
		if root > 0 {
			if roots[root] {
				return fmt.Errorf("plan: ambiguous schema root")
			}
			roots[root] = true
		}
		if name == "sqlite_stat1" || name == "sqlite_stat4" {
			continue
		}
		text(kind)
		text(name)
		text(table)
		if row[4] == nil {
			h.Write([]byte{0})
		} else {
			s, ok := row[4].(string)
			if !ok {
				return fmt.Errorf("plan: invalid schema declaration")
			}
			h.Write([]byte{1})
			text(s)
		}
		count++
	}
	if hex.EncodeToString(h.Sum(nil)) != schema.NativeLayoutSHA256 || count != schema.NativeLayoutObjects {
		return fmt.Errorf("plan: live schema does not match the embedded native layout")
	}
	return nil
}
func integerValue(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case json.Number:
		i, e := n.Int64()
		return i, e == nil
	}
	return 0, false
}
func jsonEqual(a, b any) bool {
	x, e := json.Marshal(a)
	y, f := json.Marshal(b)
	return e == nil && f == nil && bytes.Equal(x, y)
}

func schemaSQLSHA() string { return schema.SHA256 }

// CheckSchemaForeignKeys checks completeness AND every native probe against the
// one reviewed embedded-schema baseline. Harnesses must use this instead of a
// copied FK count or a private duplicate FK snapshot.
func CheckSchemaForeignKeys(a *ForeignKeyAudit) error {
	if a == nil || a.SchemaBinding == nil {
		return fmt.Errorf("plan: complete embedded-schema FK audit required")
	}
	if a.SchemaBinding.Version != 1 || a.SchemaBinding.SQLSHA256 != schema.SHA256 {
		return fmt.Errorf("plan: stale FK schema binding")
	}
	ref, err := sharedSchemaEvidence()
	if err != nil {
		return err
	}
	if !jsonEqual(a.Plans, ref.ForeignKeys) {
		return fmt.Errorf("plan: FK audit differs from shared schema evidence")
	}
	return nil
}
