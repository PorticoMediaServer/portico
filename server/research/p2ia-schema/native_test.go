package iaschema

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	_ "github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

type control struct {
	Name, SQL, Query, PlanSQL, Seek, ErrorContains string
	Want                                           [][]any
}

func (c *control) UnmarshalJSON(b []byte) error {
	var x struct {
		Name  string  `json:"name"`
		SQL   string  `json:"sql"`
		Query string  `json:"query"`
		Plan  string  `json:"plan_sql"`
		Seek  string  `json:"seek"`
		Error string  `json:"error_contains"`
		Want  [][]any `json:"want"`
	}
	if err := json.Unmarshal(b, &x); err != nil {
		return err
	}
	*c = control{x.Name, x.SQL, x.Query, x.Plan, x.Seek, x.Error, x.Want}
	return nil
}

type outcome struct {
	Name                        string   `json:"name"`
	Problems                    []string `json:"problems"`
	Checks                      int      `json:"checks"`
	Engine, SourceID, Integrity string
	FKRows                      int
	Plans                       map[string][]string `json:"plans"`
	LoadError                   string              `json:"load_error,omitempty"`
}

func nativeRows(db *sql.DB, query string) ([][]any, error) {
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := [][]any{}
	for rows.Next() {
		r := make([]any, len(cols))
		p := make([]any, len(cols))
		for i := range r {
			p[i] = &r[i]
		}
		if err = rows.Scan(p...); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func suite(name, ddl string, controls []control) outcome {
	o := outcome{Name: name, Plans: map[string][]string{}}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		o.LoadError = err.Error()
		return o
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		o.LoadError = err.Error()
		return o
	}
	if _, err = db.Exec(ddl); err != nil {
		o.LoadError = err.Error()
		return o
	}
	fail := func(s string) { o.Problems = append(o.Problems, s) }
	if err = db.QueryRow("SELECT sqlite_version(),sqlite_source_id()").Scan(&o.Engine, &o.SourceID); err != nil {
		fail(err.Error())
	}
	if o.Engine != "3.53.4" {
		fail("unexpected bundled engine " + o.Engine)
	}
	// Targeted prepare checks only; no inherited all-object matrix.
	for _, table := range []string{"scan_command_receipts", "catalog_removal_progress", "scan_reconcile_members", "catalog_removal_receipts", "catalog_configuration_operations", "catalog_configuration_receipts", "catalog_configuration_sources"} {
		cols, e := db.Query("PRAGMA table_xinfo(" + table + ")")
		if e != nil {
			fail(e.Error())
			continue
		}
		sets := []string{}
		for cols.Next() {
			var cid, nn, pk, hidden int
			var n, typ string
			var def any
			if e = cols.Scan(&cid, &n, &typ, &nn, &def, &pk, &hidden); e != nil {
				break
			}
			if hidden == 0 {
				sets = append(sets, n+"="+n)
			}
		}
		cols.Close()
		if e != nil {
			fail(e.Error())
			continue
		}
		for _, q := range []string{"EXPLAIN INSERT INTO " + table + " DEFAULT VALUES", "EXPLAIN DELETE FROM " + table, "EXPLAIN UPDATE " + table + " SET " + strings.Join(sets, ",")} {
			if _, e = nativeRows(db, q); e != nil {
				fail(q + ": " + e.Error())
			}
		}
	}
	for _, c := range controls {
		o.Checks++
		if c.SQL != "" {
			if _, err = db.Exec("SAVEPOINT ia_control"); err != nil {
				fail(c.Name + ": " + err.Error())
				continue
			}
			_, e := db.Exec(c.SQL)
			if c.ErrorContains != "" {
				if e == nil || !strings.Contains(e.Error(), c.ErrorContains) {
					fail(c.Name + ": expected " + c.ErrorContains + ", got " + fmt.Sprint(e))
				}
				db.Exec("ROLLBACK TO ia_control")
			} else if e != nil {
				fail(c.Name + ": " + e.Error())
				db.Exec("ROLLBACK TO ia_control")
			}
			if _, e = db.Exec("RELEASE ia_control"); e != nil {
				fail(c.Name + ": " + e.Error())
			}
		}
		if c.Query != "" {
			got, e := nativeRows(db, c.Query)
			if e != nil {
				fail(c.Name + ": " + e.Error())
				continue
			}
			a, _ := json.Marshal(got)
			b, _ := json.Marshal(c.Want)
			if string(a) != string(b) {
				fail(c.Name + ": got " + string(a) + ", want " + string(b))
			}
		}
		if c.PlanSQL != "" {
			rows, e := nativeRows(db, "EXPLAIN QUERY PLAN "+c.PlanSQL)
			if e != nil {
				fail(c.Name + ": " + e.Error())
				continue
			}
			detail := []string{}
			for _, r := range rows {
				detail = append(detail, fmt.Sprint(r[3]))
			}
			o.Plans[c.Name] = detail
			s := strings.Join(detail, "\n")
			if !strings.Contains(s, "SEARCH ") || strings.Contains(s, "SCAN ") || strings.Contains(s, "TEMP B-TREE") || !strings.Contains(s, c.Seek) {
				fail(c.Name + ": indexed equality/range proof missing: " + s)
			}
		}
	}
	if err = db.QueryRow("PRAGMA integrity_check").Scan(&o.Integrity); err != nil {
		fail(err.Error())
	}
	if o.Integrity != "ok" {
		fail("integrity " + o.Integrity)
	}
	fk, e := nativeRows(db, "PRAGMA foreign_key_check")
	if e != nil {
		fail(e.Error())
	}
	o.FKRows = len(fk)
	if len(fk) != 0 {
		fail(fmt.Sprint("FK violations ", fk))
	}
	var triggers int
	if err = db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE type='trigger'").Scan(&triggers); err != nil || triggers != 0 {
		fail(fmt.Sprint("application triggers ", triggers, err))
	}
	return o
}
func TestIASchemaPacket(t *testing.T) {
	if runtime.Version() != "go1.26.10" {
		t.Fatal("requires exact Go1.26.10, got", runtime.Version())
	}
	body, e := os.ReadFile("candidate.sql")
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile("controls.json")
	if e != nil {
		t.Fatal(e)
	}
	var controls []control
	if e = json.Unmarshal(b, &controls); e != nil {
		t.Fatal(e)
	}
	m, e := os.ReadFile("manifest.json")
	if e != nil {
		t.Fatal(e)
	}
	var manifest struct {
		Candidate string `json:"candidate"`
	}
	if e = json.Unmarshal(m, &manifest); e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != manifest.Candidate {
		t.Fatal("candidate pin mismatch")
	}
	candidate := string(body)
	out := []outcome{suite("candidate", candidate, controls)}
	// Real mutants must still LOAD and be killed by their named delta falsifier.
	lines := strings.Split(candidate, "\n")
	matches := 0
	for i, line := range lines {
		if strings.Contains(line, "CONSTRAINT ia_no_legacy_slots CHECK") {
			lines[i] = " CONSTRAINT ia_no_legacy_slots CHECK(1),"
			matches++
		}
	}
	if matches != 1 {
		t.Fatal("missing exact no-alias mutation target")
	}
	out = append(out, suite("allow_basic_slot_alias", strings.Join(lines, "\n"), controls))
	before := "REFERENCES catalog_removal_progress(original_operation_id,command_incarnation,kind,target)\n ON DELETE NO ACTION ON UPDATE NO ACTION"
	if strings.Count(candidate, before) != 1 {
		t.Fatal("missing exact retirement FK mutation target")
	}
	out = append(out, suite("cascade_retirement_results", strings.Replace(candidate, before, strings.Replace(before, "ON DELETE NO ACTION", "ON DELETE CASCADE", 1), 1), controls))
	// Two additional native mutants for the approved configuration amendment.
	before = "REFERENCES catalog_configuration_receipts(original_operation_id,command_incarnation,command_sequence,command_kind)\n ON DELETE NO ACTION ON UPDATE NO ACTION"
	if strings.Count(candidate, before) != 1 {
		t.Fatal("missing exact configuration child FK mutation target")
	}
	out = append(out, suite("cascade_configuration_sources", strings.Replace(candidate, before, strings.Replace(before, "ON DELETE NO ACTION", "ON DELETE CASCADE", 1), 1), controls))
	lines = strings.Split(candidate, "\n")
	matches = 0
	for i, line := range lines {
		if strings.Contains(line, "CONSTRAINT ia_configuration_source_bytes CHECK") {
			lines[i] = " CONSTRAINT ia_configuration_source_bytes CHECK(1),"
			matches++
		}
	}
	if matches != 1 {
		t.Fatal("missing exact configuration byte mutation target")
	}
	out = append(out, suite("bypass_configuration_source_bytes", strings.Join(lines, "\n"), controls))
	report := struct {
		Go        string
		Candidate string
		Results   []outcome
	}{runtime.Version(), manifest.Candidate, out}
	encoded, _ := json.MarshalIndent(report, "", "  ")
	if dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); dir != "" {
		if e = os.MkdirAll(dir, 0755); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, "ia-schema-native.json"), encoded, 0644); e != nil {
			t.Fatal(e)
		}
	}
	for _, o := range out {
		t.Logf("%s: checks=%d problems=%v load=%s", o.Name, o.Checks, o.Problems, o.LoadError)
	}
	if out[0].LoadError != "" || len(out[0].Problems) > 0 {
		t.Errorf("candidate refused: %+v", out[0])
	}
	for i, target := range []string{"Basic slots cannot carry IA result", "retirement owner cannot disappear with retained receipt", "configuration header cannot disappear with retained sources", "configuration source exact byte debt cannot be bypassed"} {
		o := out[i+1]
		killed := false
		for _, p := range o.Problems {
			if strings.HasPrefix(p, target+":") {
				killed = true
			}
		}
		if o.LoadError != "" || !killed {
			t.Errorf("mutant did not load and fail its targeted control: %+v", o)
		}
	}
}
