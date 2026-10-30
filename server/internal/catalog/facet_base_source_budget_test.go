package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestFacetColdMemberBatchMatchesOrdinaryReducer(t *testing.T) {
	db, scope := facetRestrictedFixture(t, false)
	age := 13
	scope.Policy = FacetBasePolicy(identity.ContentRestrictions{MaximumAge: &age, BlockedLabels: []string{"x|y"}})
	if _, err := db.Exec(`WITH RECURSIVE n(i) AS(SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<100) INSERT INTO catalog_entities SELECT i,1 FROM n;
 INSERT INTO catalog_browse_rows SELECT id,1,1,id,2000 FROM catalog_entities;
 INSERT INTO catalog_item_attribute_edges SELECT id,CASE WHEN id%3=0 THEN 2 ELSE 1 END,'rating' FROM catalog_entities;
 INSERT INTO catalog_item_attribute_edges SELECT id,3,'X|Y' FROM catalog_entities WHERE id%7=0;
 INSERT INTO catalog_facet_members SELECT id,'studio',printf('Value%02d',id%23),1 FROM catalog_entities;`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE catalog_facet_members SET value=? WHERE entity_id=1`, strings.Repeat("wide", 80000)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	producer := RestrictedFacetBaseProducer{}
	bases := FacetBases{DB: db, Producer: producer, MaxBytes: 64 << 20}
	build := func(authority string, cold bool) int64 {
		var id int64
		var witness restrictedFacetWitness
		facetBaseTx(t, db, func(tx *sql.Tx) error {
			raw, err := CaptureRestrictedFacetBaseWitness(ctx, tx, scope)
			if err != nil {
				return err
			}
			if err = json.Unmarshal([]byte(raw), &witness); err != nil {
				return err
			}
			id, err = bases.Begin(ctx, tx, scope, raw, authority)
			return err
		})
		for turn := 0; turn < 500; turn++ {
			done := false
			facetBaseTx(t, db, func(tx *sql.Tx) error {
				next := ctx
				if cold {
					if err := compactcatalog.ValidateFacetProducerFence(ctx, tx, witness.Fence); err != nil {
						return err
					}
					next = context.WithValue(ctx, facetColdSourceKey{}, &facetColdSourceBudget{raw: 512, bytes: 256 << 10, started: time.Now(), tx: tx, fence: witness.Fence})
				}
				var err error
				done, err = bases.Step(next, tx, id)
				return err
			})
			if done {
				return id
			}
		}
		t.Fatal("member batch did not finish")
		return 0
	}
	ordinary := build("ordinary", false)
	cold := build("cold", true)
	image := func(id int64) map[string]int64 {
		rows, err := db.Query(`SELECT v.value,-r.negative_count FROM catalog_facet_base_rows r JOIN catalog_facet_base_values v ON v.id=r.value_id WHERE r.base_id=?`, id)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]int64{}
		for rows.Next() {
			var key string
			var count int64
			if err := rows.Scan(&key, &count); err != nil {
				t.Fatal(err)
			}
			out[key] = count
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got, want := image(cold), image(ordinary); !reflect.DeepEqual(got, want) {
		t.Fatalf("cold batch changed ordinary policy/count/wide-scalar oracle: got %d values, want %d", len(got), len(want))
	}
}
