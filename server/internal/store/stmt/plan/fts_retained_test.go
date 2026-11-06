package plan_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

type retainedBoundRecorder struct{ args []any }

func (r *retainedBoundRecorder) ExecContext(_ context.Context, _ string, args ...any) (sql.Result, error) {
	r.args = args
	return nil, nil // no native connection or execution authority
}

func nativeRetainedDefinitions() []stmt.Definition {
	var defs []stmt.Definition
	for _, slot := range []stmt.FTSRetainedSlot{stmt.SearchOwnPrefixInsertV1, stmt.SearchOwnPrefixDeleteV1, stmt.SearchFragmentPrefixDelete, stmt.SearchFragmentLexicalDelete, stmt.SearchFragmentGramsDelete} {
		spec, _ := slot.Specification()
		d := stmt.Definition{ID: stmt.ID(slot), SQL: spec.SQL, Kind: stmt.Write, Owner: "plan.fts.test", CallSites: []string{"fts_retained_test.go"}, Plan: stmt.Plan{Hot: true, NativeRetained: slot}}
		if spec.Delete {
			d.Params = append(d.Params, stmt.Param{Name: "control", Type: stmt.Text})
		}
		d.Params = append(d.Params, stmt.Param{Name: "rowid", Type: stmt.Integer})
		for _, column := range spec.Columns {
			d.Params = append(d.Params, stmt.Param{Name: column, Type: stmt.Text})
		}
		for _, text := range []string{"000061", ""} {
			args := []any{}
			if spec.Delete {
				args = append(args, "delete")
			}
			args = append(args, int64(41))
			for range spec.Columns {
				args = append(args, text)
			}
			name := "nonempty"
			if text == "" {
				name = "empty"
			}
			d.Cohorts = append(d.Cohorts, stmt.Cohort{Name: name, Bindings: args})
		}
		defs = append(defs, d)
	}
	return defs
}

func TestFTSRetainedCaptureAndRuntimeControls(t *testing.T) {
	conn, _, f, _ := fixture(t)
	if _, err := conn.ExecContext(t.Context(), "DROP TABLE items"); err != nil {
		t.Fatal(err)
	}
	if err := schema.InitializeEmpty(t.Context(), conn); err != nil {
		t.Fatal(err)
	}
	f.SHA256, f.Implementations = schema.SHA256, "five fixed own-head/retained-token operations; EXPLAIN only"
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	defs := nativeRetainedDefinitions()
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	var before, after int64
	if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	g, err := plan.Capture(t.Context(), conn, catalogue, f, policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&after); err != nil || after != before {
		t.Fatal("native Capture executed effects", before, after, err)
	}
	if len(g.Plans) != 10 || g.ForeignKeys == nil {
		t.Fatal("not exactly five native slots including empty TEXT")
	}
	registry, err := stmt.New(defs, g.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range defs {
		statement, err := registry.Lookup(d.ID)
		if err != nil {
			t.Fatal(err)
		}
		spec, _ := d.Plan.NativeRetained.Specification()
		for _, cohort := range d.Cohorts {
			if _, err := statement.Bind(cohort.Bindings...); err != nil {
				t.Fatal(d.ID, cohort.Name, err)
			}
		}
		args := append([]any(nil), d.Cohorts[0].Bindings...)
		idAt := 0
		if spec.Delete {
			idAt = 1
			args[0] = "rebuild"
			if _, err := statement.Bind(args...); err == nil {
				t.Fatal("wrong hidden delete control inherited registration", d.ID)
			}
			args[0] = "delete"
		}
		args[idAt] = int64(0)
		if _, err := statement.Bind(args...); err == nil {
			t.Fatal("nonpositive native rowid accepted", d.ID)
		}
		args[idAt] = int64(41)
		for n := range spec.Columns {
			at := idAt + 1 + n
			saved := args[at]
			maximum := strings.Repeat("a", spec.MaxBytes[n])
			if spec.MaxRunes[n] != 0 {
				maximum = strings.Repeat("𠀀", spec.MaxRunes[n])
			}
			args[at] = maximum
			if _, err := statement.Bind(args...); err != nil {
				t.Fatal("legal exact native boundary refused", d.ID, n, err)
			}
			args[at] = strings.Repeat("a", spec.MaxBytes[n]+1)
			if _, err := statement.Bind(args...); err == nil {
				t.Fatal("retained payload lost native byte bound", d.ID, n)
			}
			args[at] = "\xff"
			if _, err := statement.Bind(args...); err == nil {
				t.Fatal("invalid native TEXT accepted", d.ID, n)
			}
			if spec.MaxRunes[n] != 0 {
				args[at] = strings.Repeat("a", spec.MaxRunes[n]+1)
				if _, err := statement.Bind(args...); err == nil {
					t.Fatal("native scalar bound ignored", d.ID, n)
				}
			}
			args[at] = saved
		}
		// Every retained string, including the hidden control, is copied.
		// A six-byte value must not retain the caller's multi-megabyte backing.
		backing := "delete000061" + strings.Repeat("x", 2<<20)
		for n := range args {
			if n == idAt {
				continue
			}
			if spec.Delete && n == 0 {
				args[n] = backing[:6]
			} else {
				args[n] = backing[6:12]
			}
		}
		bound, err := statement.Bind(args...)
		if err != nil {
			t.Fatal(err)
		}
		recorder := &retainedBoundRecorder{}
		if _, err := bound.ExecContext(t.Context(), recorder); err != nil {
			t.Fatal(err)
		}
		for n, value := range recorder.args {
			if text, ok := value.(string); ok && unsafe.StringData(text) == unsafe.StringData(args[n].(string)) {
				t.Fatal("bound retained caller backing", d.ID, n)
			}
		}
	}
	// Closed metadata and source audit cannot inherit the ordinary SQL path.
	bad := defs[0]
	bad.Plan.NativeRetained = "unknown"
	if _, err := stmt.Describe([]stmt.Definition{bad}); err == nil {
		t.Fatal("unknown slot described")
	}
	bad = defs[0]
	bad.Plan.NativeInsert = stmt.SearchFragmentPrefixV1
	if _, err := stmt.Describe([]stmt.Definition{bad}); err == nil {
		t.Fatal("mixed native operation inherited authority")
	}
	if _, err := conn.ExecContext(t.Context(), `CREATE TEMP TABLE search_own_content(id,title_prefix_tokens,initial_prefix_tokens)`); err != nil {
		t.Fatal(err)
	}
	if failed, err := plan.Capture(t.Context(), conn, catalogue, f, policy); err == nil || failed.Receipt() != nil {
		t.Fatal("TEMP content shadow inherited native registration", err)
	}
}
