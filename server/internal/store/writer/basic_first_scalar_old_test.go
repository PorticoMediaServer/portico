package writer

import (
	"database/sql"
	"math"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func basicFirstScalarOldCapture() []basicFirstOldScalar {
	return []basicFirstOldScalar{
		{field: 4, kind: 3, text: sql.NullString{String: "é\x00old", Valid: true}},
		{field: 7, kind: 1, integer: sql.NullInt64{Int64: 0, Valid: true}},
		{field: 9, kind: 2, real: sql.NullFloat64{Float64: 0, Valid: true}},
	}
}
func TestBasicFirstScalarOldValidation(t *testing.T) {
	before := basicFirstScalarOldCapture()
	charge, err := validateBasicFirstScalarOld(before)
	if err != nil || charge != 528+int64(len(before[0].text.String)) {
		t.Fatal(charge, err)
	}
	clone, err := cloneBasicFirstScalarOld(before)
	if err != nil {
		t.Fatal(err)
	}
	clone[0].field = 5
	if before[0].field != 4 {
		t.Fatal("aliased slice")
	}
	for _, mutate := range []func([]basicFirstOldScalar){
		func(v []basicFirstOldScalar) { v[0].field = 1 },
		func(v []basicFirstOldScalar) { v[1].field = 4 },
		func(v []basicFirstOldScalar) { v[0].kind = 1 },
		func(v []basicFirstOldScalar) { v[0].integer.Int64 = 1 },
		func(v []basicFirstOldScalar) { v[0].real.Float64 = math.NaN() },
		func(v []basicFirstOldScalar) { v[2].real.Float64 = math.Inf(1) },
		func(v []basicFirstOldScalar) { v[1].text.String = "hidden" },
		func(v []basicFirstOldScalar) { v[0].text.String = strings.Repeat("x", 33554432) },
	} {
		v := basicFirstScalarOldCapture()
		mutate(v)
		if got, e := validateBasicFirstScalarOld(v); e == nil || got != 0 {
			t.Fatal(got, e)
		}
	}
	if got, e := validateBasicFirstScalarOld(make([]basicFirstOldScalar, 7)); e == nil || got != 0 {
		t.Fatal(got, e)
	}
	if got, e := validateBasicFirstScalarOld(nil); e != nil || got != 0 {
		t.Fatal(got, e)
	}
}
func basicFirstScalarOldDebt(t *testing.T, turn *commandTurn, before []basicFirstOldScalar) int64 {
	t.Helper()
	charge, err := validateBasicFirstScalarOld(before)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range before {
		var i, r, s any
		if v.integer.Valid {
			i = v.integer.Int64
		}
		if v.real.Valid {
			r = v.real.Float64
		}
		if v.text.Valid {
			s = v.text.String
		}
		if _, err = turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO catalog_info_scalar_old(change_id,field,kind,old_integer,old_real,old_text) VALUES(51,?,?,?,?,?)`, v.field, v.kind, i, r, s); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE catalog_info_input_budget SET used_bytes=used_bytes+? WHERE singleton=1`, charge); err != nil {
		t.Fatal(err)
	}
	return charge
}
func TestBasicFirstScalarOldDischargeAndPublish(t *testing.T) {
	defs := append(basicPublishLeafTestDefinitions(), basicFirstScalarOldDefinitions()...)
	_, turn, in := basicPublishLeafFixture(t, defs)
	before := basicFirstScalarOldCapture()
	basicFirstScalarOldDebt(t, turn, before)
	got, err := dischargeBasicFirstScalarOld(t.Context(), turn, in, before)
	if err != nil || got != 3 {
		t.Fatal(got, err)
	}
	var used, count int64
	if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT used_bytes,(SELECT count(*) FROM catalog_info_scalar_old) FROM catalog_info_input_budget WHERE singleton=1`).Scan(&used, &count); err != nil || used != 1000 || count != 0 {
		t.Fatal(used, count, err)
	}
	if _, err = publishBasicFirstLeaf(t.Context(), turn, in); err != nil {
		t.Fatal(err)
	}
	basicMediaCommit(t, turn)
}
func TestBasicFirstScalarOldLateFailureRollback(t *testing.T) {
	for _, fault := range []string{"budget_cas", "returning"} {
		t.Run(fault, func(t *testing.T) {
			defs := append(basicPublishLeafTestDefinitions(), basicFirstScalarOldDefinitions()...)
			for n := range defs {
				if fault == "budget_cas" && defs[n].ID == basicStageBudgetCAS {
					defs[n].SQL = strings.Replace(defs[n].SQL, "RETURNING used_bytes", "RETURNING cap_bytes", 1)
				}
				if fault == "returning" && defs[n].ID == basicFirstScalarOldReal {
					defs[n].SQL = strings.Replace(defs[n].SQL, "RETURNING change_id,field,kind", "RETURNING field,change_id,kind", 1)
				}
			}
			w, turn, in := basicPublishLeafFixture(t, defs)
			before := basicFirstScalarOldCapture()
			basicFirstScalarOldDebt(t, turn, before)
			got, err := dischargeBasicFirstScalarOld(t.Context(), turn, in, before)
			if err == nil || got != 0 {
				t.Fatal(got, err)
			}
			var remaining int64
			if e := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_info_scalar_old WHERE change_id=51`).Scan(&remaining); e != nil || remaining != 0 {
				t.Fatal("expected physical deletions before late failure", remaining, e)
			}
			_ = turn.closeRows()
			_, _ = turn.transaction.rollback(err)
			var intact bool
			if e := w.db.QueryRowContext(t.Context(), `SELECT (SELECT used_bytes=1000 FROM catalog_info_input_budget WHERE singleton=1) AND (SELECT info_epoch=17 AND info_publication_pending=1 FROM catalog_assets WHERE id=41) AND (SELECT phase=3 FROM catalog_info_changes WHERE id=51) AND (SELECT count(*)=0 FROM catalog_info_scalar_old)`).Scan(&intact); e != nil || !intact {
				t.Fatal(intact, e)
			}
		})
	}
}
func TestBasicFirstScalarOldDefinitionsClosed(t *testing.T) {
	defs := basicFirstScalarOldDefinitions()
	if _, err := stmt.Describe(defs); err != nil {
		t.Fatal(err)
	}
	for _, d := range defs {
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
	}
}

func TestBasicFirstScalarOldRequiresCompleteOwnedKeys(t *testing.T) {
	for _, fault := range []string{"omitted", "unowned", "empty"} {
		t.Run(fault, func(t *testing.T) {
			defs := append(basicPublishLeafTestDefinitions(), basicFirstScalarOldDefinitions()...)
			_, turn, in := basicPublishLeafFixture(t, defs)
			before := basicFirstScalarOldCapture()
			basicFirstScalarOldDebt(t, turn, before)
			switch fault {
			case "omitted":
				before = before[:2]
			case "empty":
				before = nil
			case "unowned":
				if _, err := turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO catalog_info_scalar_old(change_id,field,kind,old_integer) VALUES(51,1,1,1)`); err != nil {
					t.Fatal(err)
				}
			}
			got, err := dischargeBasicFirstScalarOld(t.Context(), turn, in, before)
			if err == nil || got != 0 {
				t.Fatal(got, err)
			}
			var count int64
			want := int64(3)
			if fault == "unowned" {
				want = 4
			}
			if e := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_info_scalar_old WHERE change_id=51`).Scan(&count); e != nil || count != want {
				t.Fatal(count, e)
			}
			_ = turn.closeRows()
			_, _ = turn.transaction.rollback(err)
		})
	}
}
func TestBasicFirstScalarOldEmptyDischarge(t *testing.T) {
	defs := append(basicPublishLeafTestDefinitions(), basicFirstScalarOldDefinitions()...)
	_, turn, in := basicPublishLeafFixture(t, defs)
	got, err := dischargeBasicFirstScalarOld(t.Context(), turn, in, nil)
	if err != nil || got != 0 {
		t.Fatal(got, err)
	}
	if _, err = publishBasicFirstLeaf(t.Context(), turn, in); err != nil {
		t.Fatal(err)
	}
	basicMediaCommit(t, turn)
}
