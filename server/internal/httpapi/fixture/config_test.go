package fixture

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestCanonicalCreditsAreExplicitAndCacheIsolated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	ctx := context.Background()
	type credit struct {
		entity                 int64
		ordinal                int
		provider, person, name string
	}
	var original []credit
	for _, run := range []struct {
		name        string
		config      Config
		departments []string
	}{
		{"default", Config{}, []string{"cast", "directing", "writing"}},
		{"canonical", Config{CanonicalCreditDepartments: true}, []string{"Acting", "Directing", "Writing"}},
		{"default-after-canonical", Config{}, []string{"cast", "directing", "writing"}},
	} {
		t.Run(run.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fixture.sqlite")
			var err error
			if run.config.CanonicalCreditDepartments {
				_, err = BuildWithConfig(ctx, Tiny(), run.config, path, nil)
			} else {
				_, err = Build(ctx, Tiny(), path, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			db, err := persistence.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			departments, err := db.QueryContext(ctx, `SELECT DISTINCT d.label FROM catalog_credits c JOIN catalog_entities e ON e.id=c.entity_id JOIN catalog_credit_labels d ON d.id=c.department_id WHERE e.kind=? ORDER BY d.label`, compactcatalog.Movie)
			if err != nil {
				t.Fatal(err)
			}
			var labels []string
			for departments.Next() {
				var label string
				if err := departments.Scan(&label); err != nil {
					t.Fatal(err)
				}
				labels = append(labels, label)
			}
			if err := departments.Err(); err != nil {
				t.Fatal(err)
			}
			departments.Close()
			if !reflect.DeepEqual(labels, run.departments) {
				t.Fatalf("credit departments: %v, want %v", labels, run.departments)
			}
			rows, err := db.QueryContext(ctx, `SELECT entity_id,source_ordinal,provider,provider_person_id,credited_name FROM catalog_credits ORDER BY entity_id,source_ordinal`)
			if err != nil {
				t.Fatal(err)
			}
			var credits []credit
			for rows.Next() {
				var c credit
				if err := rows.Scan(&c.entity, &c.ordinal, &c.provider, &c.person, &c.name); err != nil {
					t.Fatal(err)
				}
				credits = append(credits, c)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			if len(credits) == 0 {
				t.Fatal("fixture contains no recommendation credit inputs")
			}
			if original == nil {
				original = credits
			} else if !reflect.DeepEqual(credits, original) {
				t.Fatal("credit opt-in changed identities, people or ordinals")
			}
			var actor int64
			err = db.QueryRowContext(ctx, `SELECT c.entity_id FROM catalog_credits c JOIN catalog_credit_labels d ON d.id=c.department_id WHERE d.label='Acting' AND c.provider_person_id<>'' LIMIT 1`).Scan(&actor)
			if run.config.CanonicalCreditDepartments && err != nil {
				t.Fatalf("canonical starring input: %v", err)
			}
			if !run.config.CanonicalCreditDepartments && err != sql.ErrNoRows {
				t.Fatal(fmt.Sprintf("default fixture gained canonical starring input: entity=%d err=%v", actor, err))
			}
		})
	}
}
