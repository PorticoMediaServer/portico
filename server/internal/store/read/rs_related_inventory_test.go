package read

import (
	"strings"
	"testing"
)

func TestRSRelatedCanonicalInventory(t *testing.T) {
	for _, tc := range []struct {
		name, sql string
		available bool
	}{
		{"current object overrides asset flag", `INSERT INTO library_sources(id,library_id,kind,root,configured_root,incarnation,health) VALUES('source','movies','local','/media','/media','inc','ready'); INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state) VALUES('object','source','2','inc','2','r','{}',1,1,'available'); UPDATE catalog_assets SET available=0 WHERE id=2`, true},
		{"stale incarnation forbids fallback", `INSERT INTO library_sources(id,library_id,kind,root,configured_root,incarnation,health) VALUES('source','movies','local','/media','/media','inc','ready'); INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state) VALUES('object','source','2','stale','2','r','{}',1,1,'available')`, false},
		{"retired object forbids fallback", `INSERT INTO library_sources(id,library_id,kind,root,configured_root,incarnation,health) VALUES('source','movies','local','/media','/media','inc','ready'); INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state,retired) VALUES('object','source','2','inc','2','r','{}',1,1,'available',1)`, false},

		{"link flag is not truth", `UPDATE catalog_asset_links SET available=0 WHERE entity_id=2`, true},
		{"asset fallback", `UPDATE catalog_assets SET available=0 WHERE id=2`, false},
		{"disabled matching root", `INSERT INTO library_sources(id,library_id,kind,root,configured_root,incarnation,health,enabled) VALUES('source','movies','local','/media','/media','inc','ready',0)`, false},
		{"unmatched healthy fallback", `INSERT INTO library_sources(id,library_id,kind,root,configured_root,incarnation,health) VALUES('source','movies','local','/elsewhere','/elsewhere','inc','ready')`, true},
		{"changed fallback root", `INSERT INTO library_sources(id,library_id,kind,root,configured_root,incarnation,health) VALUES('source','movies','local','/elsewhere','/media','inc','root_changed')`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, r := rsRelatedFixture(t)
			f.exec(t, tc.sql)
			if strings.Contains(tc.sql, "INSERT INTO library_sources") {
				f.exec(t, `INSERT INTO library_sources(id,library_id,kind,root,configured_root,incarnation,health) VALUES('seed-source','movies','local','/seed','/seed','inc','ready')`)
				f.exec(t, `UPDATE catalog_assets SET path='/seed/title' WHERE id=1`)
			}
			var oracle int
			if err := f.conn.QueryRowContext(t.Context(), `SELECT available FROM inventory_item_availability WHERE item_id=2`).Scan(&oracle); err != nil {
				t.Fatal(err)
			}
			if (oracle != 0) != tc.available {
				t.Fatal("bad independent oracle fixture", oracle)
			}
			in := RelatedInput{View: f.input, Item: 1, Windows: 64}
			in.View.Limit = 40
			p := rsFinish(t, f, r, in, rsBegin(t, f, in, true), true)
			found := false
			for _, row := range p.Rows {
				for _, e := range row.Entries {
					found = found || e.ID == 2
				}
			}
			if found != (oracle != 0) {
				t.Fatal("canonical disagreement", found, oracle)
			}
		})
	}
}

func TestRSRelatedContainerAcceptedBands(t *testing.T) {
	f, _ := rsRelatedFixture(t)
	defs := map[string]string{}
	for _, d := range RSRelatedDefinitions() {
		defs[string(d.ID)] = d.SQL
	}
	q := func(name string, args ...any) ([][]any, error) {
		rows, err := f.conn.QueryContext(t.Context(), defs["read.browse."+name], args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			return nil, err
		}
		var out [][]any
		for rows.Next() {
			r := make([]any, len(cols))
			p := make([]any, len(cols))
			for i := range r {
				p[i] = &r[i]
			}
			if err := rows.Scan(p...); err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		return out, rows.Err()
	}
	for _, tc := range []struct {
		name           string
		count, unrated int
		minimum        any
		allow          int64
		want           bool
	}{
		{"mixed", 3, 1, 8, 0, true}, {"hidden", 2, 0, 18, 0, false}, {"unrated denied", 2, 2, nil, 0, false}, {"unrated allowed", 2, 2, nil, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.exec(t, `UPDATE catalog_entities SET admission_member_count=?,admission_unrated_members=?,admission_min_known_age=?,admission_own_age=99 WHERE id=2000`, tc.count, tc.unrated, tc.minimum)
			e, err := relatedEntityRead(q, "p", 2000)
			if err != nil {
				t.Fatal(err)
			}
			ok, err := relatedEligibility(q, e, browseFence{libraryID: 1, maximumAge: 12, allowUnrated: tc.allow})
			if err != nil || ok != tc.want {
				t.Fatal(ok, err)
			}
		})
	}
}
