package plan_test

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func policyKeysetFixture(t *testing.T, analyzed bool, extraSeed ...string) (*sql.Conn, plan.Fixture, plan.IndexPolicy) {
	t.Helper()
	writer, _, f, _ := fixture(t)
	if _, err := writer.ExecContext(t.Context(), "DROP TABLE items"); err != nil {
		t.Fatal(err)
	}
	if err := schema.InitializeEmpty(t.Context(), writer); err != nil {
		t.Fatal(err)
	}
	seed := []string{
		`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('a','alice',x'00','p')`,
		`INSERT INTO access_limits(account_id) VALUES('a')`,
		`INSERT INTO access_denied_labels(account_id,ordinal,label) VALUES('a',0,'alpha'),('a',1,'beta')`,
	}
	seed = append(seed, extraSeed...)
	hash := sha256.New()
	for _, q := range seed {
		if _, err := writer.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
		hash.Write([]byte(q))
	}
	f.Statistics = plan.Unanalyzed
	if analyzed {
		if _, err := writer.ExecContext(t.Context(), "ANALYZE"); err != nil {
			t.Fatal(err)
		}
		f.Statistics = plan.Analyzed
	}
	f.SHA256 = hex.EncodeToString(hash.Sum(nil))
	f.Implementations = "actual CHANGE-007 policy-child PK; one account/two labels; fixed Reader; keyset SQL facts only"
	var seq int64
	var name, path string
	if err := writer.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	f.ConnectionPolicy.Role = sqliteconn.Reader
	f.ConnectionPolicy.CacheKiB = 1024
	f.ConnectionPolicy.ReadLimits = sqliteconn.ReadLimits{LengthBytes: 65536, Columns: 129}
	db, _, err := sqliteconn.Open(t.Context(), path, f.ConnectionPolicy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	reader, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close() })
	data, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(data)
	if err != nil {
		t.Fatal(err)
	}
	return reader, f, policy
}

func policyKeysetDefinition(next bool) stmt.Definition {
	d := stmt.Definition{ID: "auto.policy.first", Owner: "plan-keyset-regression", CallSites: []string{"autoindex_keysets_test.go"}, Kind: stmt.Read,
		SQL:    "SELECT c.ordinal,c.label FROM access_denied_labels AS c INDEXED BY sqlite_autoindex_access_denied_labels_1 WHERE c.account_id=?",
		Params: []stmt.Param{{Name: "account", Type: stmt.Text}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_access_denied_labels_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "access_denied_labels", Name: "sqlite_autoindex_access_denied_labels_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "account_id", Collation: "BINARY"}, {Name: "ordinal", Collation: "BINARY"}}}}}}
	values := []any{"a"}
	missing := []any{"absent"}
	if next {
		d.ID = "auto.policy.next"
		d.SQL += " AND c.ordinal>?"
		d.Params = append(d.Params, stmt.Param{Name: "after", Type: stmt.Integer})
		values = append(values, int64(0))
		missing = append(missing, int64(0))
	}
	d.SQL += " ORDER BY c.ordinal LIMIT ?"
	d.Params = append(d.Params, stmt.Param{Name: "limit", Type: stmt.Integer})
	values = append(values, int64(1))
	missing = append(missing, int64(1))
	d.Cohorts = []stmt.Cohort{{Name: "present", Bindings: values}, {Name: "missing", Bindings: missing}}
	return d
}

func TestPlanAutoindexPolicyChildKeysetGolden(t *testing.T) {
	type snapshot struct {
		Environment                *plan.Environment `json:"environment,omitempty"`
		Fixture                    plan.Fixture
		Connection                 sqliteconn.Settings
		Schema, Policy, Statistics string
		Manifest                   []stmt.Entry
		Plans                      []plan.StatementPlan
	}
	var snapshots []snapshot
	for _, analyzed := range []bool{false, true} {
		conn, f, policy := policyKeysetFixture(t, analyzed)
		defs := []stmt.Definition{policyKeysetDefinition(false), policyKeysetDefinition(true)}
		catalogue, err := stmt.Describe(defs)
		if err != nil {
			t.Fatal(err)
		}
		g, err := plan.Capture(t.Context(), conn, catalogue, f, policy)
		if err != nil {
			t.Fatal(err)
		}
		registry, err := stmt.New(defs, g.Receipt())
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range defs {
			s, err := registry.Lookup(d.ID)
			if err != nil {
				t.Fatal(err)
			}
			b, err := s.Bind(d.Cohorts[0].Bindings...)
			if err != nil {
				t.Fatal(err)
			}
			var ordinal int64
			var label string
			if err := b.QueryRowContext(t.Context(), conn).Scan(&ordinal, &label); err != nil {
				t.Fatal(err)
			}
			want := int64(0)
			if d.ID == "auto.policy.next" {
				want = 1
			}
			if ordinal != want {
				t.Fatal(ordinal, want)
			}
		}
		snapshots = append(snapshots, snapshot{&g.Environment, f, g.Environment.Connection, g.Environment.SchemaSHA256, g.Environment.IndexPolicySHA256, g.Environment.StatisticsSHA256, g.Manifest, g.Plans})
	}
	data, err := json.MarshalIndent(snapshots, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	checkAutoindexKeysetGolden(t, "autoindex-keysets.json.gz", data)
}

func checkAutoindexKeysetGolden(t *testing.T, name string, data []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *recordAutoindexPlans {
		var buf bytes.Buffer
		w := gzip.NewWriter(&buf)
		if _, err := w.Write(append(data, '\n')); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	expected, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := plan.CompareJSON(expected, data); err != nil {
		t.Fatal(err)
	}
}

func TestPlanAutoindexKeyOrderedSubsetAndQuotedHints(t *testing.T) {
	conn, writer, f := autoindexFixture(t, true)
	// A range on a nonadjacent key remains a SEARCH on a=?, so EQP alone
	// cannot detect the unbounded intervening-key filter.
	for _, q := range []string{"CREATE TABLE auto_triple(a TEXT,b INTEGER,c INTEGER,PRIMARY KEY(a,b,c)) WITHOUT ROWID", "INSERT INTO auto_triple VALUES('a',0,0),('a',1,1)"} {
		if _, err := writer.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		where, tail string
		values      []any
		allow       bool
	}{
		{"p.id>?", "", []any{"one"}, false},
		{"p.id>?", " ORDER BY p.id", []any{"one"}, false},
		{"p.id>?", " LIMIT ?", []any{"one", int64(1)}, false},
		{"p.id>?", " ORDER BY p.payload LIMIT ?", []any{"one", int64(1)}, false},
		{"p.id>? AND p.id<?", " ORDER BY p.id LIMIT ?", []any{"one", "two", int64(1)}, false},
		{"p.id IN (?)", "", []any{"one"}, false},
		{"p.id IS NULL", "", nil, false},
		{"p.id=? OR p.id=?", "", []any{"one", "two"}, false},
		{"p.id>?", " ORDER BY p.id LIMIT ?", []any{"one", int64(1)}, true},
		{"p.id<?", " ORDER BY p.id DESC LIMIT ?", []any{"two", int64(1)}, true},
	} {
		for _, hot := range []bool{false, true} {
			d := autoindexDefinitions()[0]
			d.SQL = "SELECT p.id FROM auto_pk AS p INDEXED BY sqlite_autoindex_auto_pk_1 WHERE " + c.where + c.tail
			d.Plan.Hot = hot
			d.Plan.Seeks = nil
			d.Params = nil
			for i, v := range c.values {
				typ := stmt.Text
				if _, ok := v.(int64); ok {
					typ = stmt.Integer
				}
				d.Params = append(d.Params, stmt.Param{Name: string(rune('a' + i)), Type: typ})
			}
			d.Cohorts = []stmt.Cohort{{Name: "probe", Bindings: c.values}}
			catalogue, err := stmt.Describe([]stmt.Definition{d})
			if err != nil {
				t.Fatal(err)
			}
			g, err := plan.Capture(t.Context(), conn, catalogue, f, indexPolicy(t))
			if (err == nil) != c.allow || !c.allow && g.Receipt() != nil {
				t.Fatal("key-order subset", d.SQL, hot, err)
			}
		}
	}
	d := autoindexDefinitions()[0]
	d.SQL = "SELECT t.a,t.b,t.c FROM auto_triple AS t INDEXED BY sqlite_autoindex_auto_triple_1 WHERE t.a=? AND t.c>? ORDER BY t.b,t.c LIMIT ?"
	d.Params = []stmt.Param{{Name: "a", Type: stmt.Text}, {Name: "c", Type: stmt.Integer}, {Name: "limit", Type: stmt.Integer}}
	d.Cohorts = []stmt.Cohort{{Name: "nonadjacent", Bindings: []any{"a", int64(0), int64(1)}}}
	d.Plan = stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_auto_triple_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "auto_triple", Name: "sqlite_autoindex_auto_triple_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "a", Collation: "BINARY"}, {Name: "b", Collation: "BINARY"}, {Name: "c", Collation: "BINARY"}}}}}
	if err := plan.CheckEQP(d, reviewRawEQP(t, conn, d.SQL, "a", int64(0), int64(1))); err != nil {
		t.Fatal("negative must reach the specific access guard", err)
	}
	catalogue, err := stmt.Describe([]stmt.Definition{d})
	if err != nil {
		t.Fatal(err)
	}
	if g, err := plan.Capture(t.Context(), conn, catalogue, f, indexPolicy(t)); err == nil || g.Receipt() != nil {
		t.Fatal("nonadjacent range qualified", err)
	}
	for _, quote := range [][2]string{{"\"", "\""}, {"`", "`"}, {"[", "]"}} {
		index := quote[0] + "sqlite_autoindex_auto_pk_1" + quote[1]
		for _, q := range []string{"SELECT p.id FROM auto_pk AS p INDEXED BY " + index + " WHERE p.id=?", "UPDATE auto_pk INDEXED BY " + index + " SET payload=payload WHERE id=? RETURNING id", "DELETE FROM auto_pk INDEXED BY " + index + " WHERE id=? RETURNING id"} {
			if len(plan.LintSQL(q)) == 0 {
				t.Fatal("quoted autoindex passed lint", q)
			}
			d := autoindexDefinitions()[0]
			d.SQL = q
			policy := f
			target := conn
			if !strings.HasPrefix(q, "SELECT") {
				d.Kind = stmt.Write
				policy.ConnectionPolicy.Role = sqliteconn.Writer
				policy.ConnectionPolicy.ReadLimits = sqliteconn.ReadLimits{}
				target = writer
			}
			catalogue, err := stmt.Describe([]stmt.Definition{d})
			if err != nil {
				t.Fatal(err)
			}
			if g, err := plan.Capture(t.Context(), target, catalogue, policy, indexPolicy(t)); err == nil || g.Receipt() != nil {
				t.Fatal("quoted autoindex qualified", q, err)
			}
		}
	}
}

func TestPlanAutoindexPolicyChildOrderingVariants(t *testing.T) {
	conn, f, policy := policyKeysetFixture(t, true)
	for _, c := range []struct {
		order string
		allow bool
	}{
		{"c.ordinal DESC", true},
		{"c.account_id,c.ordinal", true},
		{"c.ordinal,c.account_id", false},
	} {
		d := policyKeysetDefinition(true)
		d.SQL = strings.Replace(d.SQL, "ORDER BY c.ordinal", "ORDER BY "+c.order, 1)
		// The reversed fixed-prefix placement is not rejected merely because
		// of a temp sort: SQLite can still deliver it from the same SEARCH.
		if err := plan.CheckEQP(d, reviewRawEQP(t, conn, d.SQL, "a", int64(0), int64(1))); err != nil {
			t.Fatal(err)
		}
		catalogue, err := stmt.Describe([]stmt.Definition{d})
		if err != nil {
			t.Fatal(err)
		}
		g, err := plan.Capture(t.Context(), conn, catalogue, f, policy)
		if (err == nil) != c.allow || !c.allow && g.Receipt() != nil {
			t.Fatal(c.order, err)
		}
	}
}

func TestPlanAutoindexLimitBindingRefusesUnboundedExecution(t *testing.T) {
	conn, f, policy := policyKeysetFixture(t, false)
	d := policyKeysetDefinition(true)
	d.SQL = strings.Replace(d.SQL, "LIMIT ?", "LIMIT /* ? ignored slot */ ?", 1)
	catalogue, err := stmt.Describe([]stmt.Definition{d})
	if err != nil {
		t.Fatal(err)
	}
	golden, err := plan.Capture(t.Context(), conn, catalogue, f, policy)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := stmt.New([]stmt.Definition{d}, golden.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	s, err := registry.Lookup(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int64{-2, -1, 0} {
		bound, err := s.Bind("a", int64(-1), limit)
		if err == nil {
			t.Error("non-positive LIMIT bound", limit)
		}
		executor := &admissionExecutor{conn: conn}
		rows, err := bound.QueryContext(t.Context(), executor)
		if rows != nil {
			rows.Close()
		}
		if err == nil || rows != nil {
			t.Fatal("invalid LIMIT reached query", limit, err)
		}
		if result, err := bound.ExecContext(t.Context(), executor); err == nil || result != nil {
			t.Fatal("invalid LIMIT reached exec", limit, err)
		}
		var value int64
		if err := bound.QueryRowContext(t.Context(), executor).Scan(&value); err == nil {
			t.Fatal("invalid LIMIT reached query-row", limit)
		}
		if executor.queries != 0 || executor.execs != 0 || executor.rows != 0 {
			t.Fatal("bad LIMIT reached native executor", executor)
		}
	}
	bound, err := s.Bind("a", int64(-1), int64(1))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := bound.QueryContext(t.Context(), conn)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("positive page is unbounded", count)
	}
	bad := registry.Manifest()[0].Definition
	bad.Cohorts[0].Bindings[len(bad.Params)-1] = int64(-1)
	if _, err := stmt.Describe([]stmt.Definition{bad}); err == nil {
		t.Fatal("negative qualification cohort accepted")
	}
	for _, typ := range []stmt.Type{stmt.Text, stmt.Integer} {
		bad := registry.Manifest()[0].Definition
		last := len(bad.Params) - 1
		bad.Params[last].Type = typ
		bad.Params[last].Nullable = true
		if _, err := stmt.Describe([]stmt.Definition{bad}); err == nil {
			t.Fatal("non-integer/nullable LIMIT admitted", typ)
		}
	}
	for _, literal := range []string{"0", "00", "9223372036854775808"} {
		bad := registry.Manifest()[0].Definition
		bad.SQL = strings.Replace(bad.SQL, "LIMIT /* ? ignored slot */ ?", "LIMIT "+literal, 1)
		bad.Params = bad.Params[:len(bad.Params)-1]
		for i := range bad.Cohorts {
			bad.Cohorts[i].Bindings = bad.Cohorts[i].Bindings[:len(bad.Params)]
		}
		catalogue, err := stmt.Describe([]stmt.Definition{bad})
		if err != nil {
			t.Fatal(err)
		}
		if g, err := plan.Capture(t.Context(), conn, catalogue, f, policy); err == nil || g.Receipt() != nil {
			t.Fatal("non-positive/overflow literal qualified", literal, err)
		}
	}
}

func TestPlanAutoindexInclusiveChannelKeysetsGolden(t *testing.T) {
	type snapshot struct {
		Environment                *plan.Environment `json:"environment,omitempty"`
		Fixture                    plan.Fixture
		Connection                 sqliteconn.Settings
		Schema, Policy, Statistics string
		Manifest                   []stmt.Entry
		Plans                      []plan.StatementPlan
	}
	var snapshots []snapshot
	for _, analyzed := range []bool{false, true} {
		conn, f, policy := policyKeysetFixture(t, analyzed,
			`INSERT INTO access_channel_rules(account_id,ordinal,channel_id) VALUES('a',0,'zero'),('a',2,'two')`)
		f.Implementations = "actual CHANGE-007 channel-rule PK; one account/two channels; fixed Reader; inclusive endpoints and one-row keyset SQL facts only"
		var defs []stmt.Definition
		for _, c := range []struct {
			id, operator, order, native string
			endpoint                    int64
		}{
			{"ge", ">=", "", ">", 0},
			{"le", "<=", " DESC", "<", 2},
		} {
			defs = append(defs, stmt.Definition{ID: stmt.ID("auto.policy.channel_" + c.id), Owner: "plan-keyset-regression", CallSites: []string{"autoindex_keysets_test.go"}, Kind: stmt.Read,
				SQL:     "SELECT c.ordinal,c.channel_id FROM access_channel_rules AS c INDEXED BY sqlite_autoindex_access_channel_rules_1 WHERE c.account_id=? AND c.ordinal" + c.operator + "? ORDER BY c.ordinal" + c.order + " LIMIT 1",
				Params:  []stmt.Param{{Name: "account", Type: stmt.Text}, {Name: "endpoint", Type: stmt.Integer}},
				Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{"a", c.endpoint}}, {Name: "missing", Bindings: []any{"absent", c.endpoint}}},
				Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_access_channel_rules_1"}, Seeks: []string{"account_id=? AND ordinal" + c.native + "?"},
					AutoIndexes: []stmt.AutoIndexBinding{{Table: "access_channel_rules", Name: "sqlite_autoindex_access_channel_rules_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "account_id", Collation: "BINARY"}, {Name: "ordinal", Collation: "BINARY"}}}}}})
		}
		catalogue, err := stmt.Describe(defs)
		if err != nil {
			t.Fatal(err)
		}
		g, err := plan.Capture(t.Context(), conn, catalogue, f, policy)
		if err != nil {
			t.Fatal(err)
		}
		registry, err := stmt.New(defs, g.Receipt())
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range defs {
			s, err := registry.Lookup(d.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, cohort := range d.Cohorts {
				bound, err := s.Bind(cohort.Bindings...)
				if err != nil {
					t.Fatal(err)
				}
				rows, err := bound.QueryContext(t.Context(), conn)
				if err != nil {
					t.Fatal(err)
				}
				count := 0
				for rows.Next() {
					var ordinal int64
					var channel string
					if err := rows.Scan(&ordinal, &channel); err != nil {
						rows.Close()
						t.Fatal(err)
					}
					if ordinal != cohort.Bindings[1].(int64) {
						rows.Close()
						t.Fatal("inclusive SQL lost its endpoint", d.ID, ordinal, cohort.Bindings[1])
					}
					count++
				}
				if err := rows.Err(); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				want := 1
				if cohort.Name == "missing" {
					want = 0
				}
				if count != want {
					t.Fatal("inclusive page cardinality", d.ID, cohort.Name, count, want)
				}
			}
		}
		bad := registry.Manifest()[0].Definition
		bad.SQL = strings.Replace(bad.SQL, " ORDER BY", " AND c.ordinal<=? ORDER BY", 1)
		bad.Params = append(bad.Params, stmt.Param{Name: "other_endpoint", Type: stmt.Integer})
		for i := range bad.Cohorts {
			bad.Cohorts[i].Bindings = append(bad.Cohorts[i].Bindings, int64(2))
		}
		catalogue, err = stmt.Describe([]stmt.Definition{bad})
		if err != nil {
			t.Fatal(err)
		}
		if denied, err := plan.Capture(t.Context(), conn, catalogue, f, policy); err == nil || denied.Receipt() != nil || !strings.Contains(err.Error(), "leading key prefix") {
			t.Fatal("two inclusive ranges qualified or failed before the range guard", err)
		}
		snapshots = append(snapshots, snapshot{&g.Environment, f, g.Environment.Connection, g.Environment.SchemaSHA256, g.Environment.IndexPolicySHA256, g.Environment.StatisticsSHA256, g.Manifest, g.Plans})
	}
	data, err := json.MarshalIndent(snapshots, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	checkAutoindexKeysetGolden(t, "autoindex-inclusive.json.gz", data)
}
