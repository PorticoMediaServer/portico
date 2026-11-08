package plan

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func uoMembershipOctetControl(m uoMembershipOctet, tail bool) stmt.Definition {
	variant := "first"
	seek := "profile_id=?"
	params := []stmt.Param{{Name: "token", Type: stmt.Text}}
	if tail {
		variant = "tail"
		seek = "profile_id=? AND item_public_id>?"
		params = append(params, stmt.Param{Name: "after", Type: stmt.Blob})
	}
	key := func(table, name, column string) stmt.AutoIndexBinding {
		return stmt.AutoIndexBinding{Table: table, Name: name, Origin: "pk", Columns: []stmt.IndexColumn{{Name: column, Collation: "BINARY"}}}
	}
	token := key("authorization_family_tokens", "sqlite_autoindex_authorization_family_tokens_1", "token_hash")
	family := key("authorization_session_families", "sqlite_autoindex_authorization_session_families_1", "id")
	return stmt.Definition{
		ID: stmt.ID("read.foreground.uo.membership." + m.name + "." + variant + ".bytes"), Owner: "store/read UO membership", Kind: stmt.Read,
		SQL:    uoMembershipOctetSQL(m, tail, true),
		Params: params,
		Plan: stmt.Plan{
			Hot: true, PrimaryKeys: []string{"e"},
			Indexes:     []string{token.Name, family.Name, m.index},
			AutoIndexes: []stmt.AutoIndexBinding{token, family},
			Seeks:       []string{"token_hash=?", "id=?", seek},
		},
	}
}

func TestUOMembershipOctetPurpose(t *testing.T) {
	for _, m := range uoMembershipOctets {
		for _, tail := range []bool{false, true} {
			d := uoMembershipOctetControl(m, tail)
			if !closedOctetDefinition(d) || checkSyntax(d) != nil || len(LintSQL(d.SQL)) == 0 {
				t.Fatal("closed membership point widened generic grammar", d.ID)
			}
			mutations := []func(*stmt.Definition){
				func(d *stmt.Definition) { d.ID += ".foreign-purpose" },
				func(d *stmt.Definition) { d.SQL += " " },
				func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, m.column+"=1", m.column+"=2", 1) },
				func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, m.index, "personal_current_live", 1) },
				func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, "LIMIT 64", "LIMIT 65", 1) },
				func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, " LIMIT 64", "", 1) },
				func(d *stmt.Definition) { d.Owner = "foreign-owner" },
				func(d *stmt.Definition) { d.Params[0].Type = stmt.Integer },
				func(d *stmt.Definition) { d.Params[0].Nullable = true },
				func(d *stmt.Definition) { d.Plan.Hot = false },
				func(d *stmt.Definition) { d.Plan.Seeks = nil },
				func(d *stmt.Definition) { d.Plan.PrimaryKeys = nil },
				func(d *stmt.Definition) { d.Plan.AutoIndexes[0].Columns[0].Collation = "NOCASE" },
			}
			for n, mutate := range mutations {
				changed := uoMembershipOctetControl(m, tail)
				mutate(&changed)
				if closedOctetDefinition(changed) || checkSyntax(changed) == nil {
					t.Fatal("membership octet authority borrowed", m.name, tail, n, changed.ID)
				}
			}
		}
	}
}

func uoMembershipEQP(details ...string) Rows {
	rows := Rows{Columns: []string{"id", "parent", "aux", "detail"}}
	for n, detail := range details {
		rows.Values = append(rows.Values, []any{int64(n), int64(0), "<notused>", detail})
	}
	return rows
}

func TestUOMembershipOctetPoint(t *testing.T) {
	for _, m := range uoMembershipOctets {
		token := "SEARCH t USING INDEX sqlite_autoindex_authorization_family_tokens_1 (token_hash=?)"
		family := "SEARCH f USING INDEX sqlite_autoindex_authorization_session_families_1 (id=?)"
		first := "SEARCH p USING INDEX " + m.index + " (profile_id=?)"
		tail := "SEARCH p USING INDEX " + m.index + " (profile_id=? AND item_public_id>?)"
		entity := "SEARCH e USING INTEGER PRIMARY KEY (rowid=?) LEFT-JOIN"
		if err := uoMembershipOctetPoint(uoMembershipEQP(token, family, first, entity), m.index, false); err != nil {
			t.Fatal(m.name, "first", err)
		}
		if err := uoMembershipOctetPoint(uoMembershipEQP(token, family, tail, entity), m.index, true); err != nil {
			t.Fatal(m.name, "tail", err)
		}
		for name, rows := range map[string]Rows{
			"scan":            uoMembershipEQP(token, family, "SCAN p", entity),
			"foreign-index":   uoMembershipEQP(token, family, "SEARCH p USING INDEX personal_current_live (profile_id=?)", entity),
			"lost-keyset":     uoMembershipEQP(token, family, first, entity),
			"temp-btree":      uoMembershipEQP(token, family, tail, entity, "USE TEMP B-TREE FOR ORDER BY"),
			"no-entity-point": uoMembershipEQP(token, family, tail),
			"no-token":        uoMembershipEQP(family, tail, entity, family),
			"extra-access":    uoMembershipEQP(token, family, tail, entity, "SCAN catalog_entities"),
		} {
			if err := uoMembershipOctetPoint(rows, m.index, true); err == nil {
				t.Fatalf("%s/%s: unusable membership plan was accepted", m.name, name)
			}
		}
	}
}
