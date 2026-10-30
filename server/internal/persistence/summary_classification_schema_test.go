package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestSummaryClassificationRevisionEffectiveChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "classification.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	revision := func() int64 {
		t.Helper()
		var n int64
		if err := db.QueryRow(`SELECT revision FROM catalog_summary_classification_revision WHERE singleton=1`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := revision()
	cases := []struct {
		sql   string
		delta int64
	}{
		{`INSERT INTO content_rating_ages VALUES('summary-test',7)`, 1},
		{`UPDATE content_rating_ages SET minimum_age=7 WHERE value_key='summary-test'`, 0},
		{`INSERT INTO content_rating_ages VALUES('summary-test',7) ON CONFLICT(value_key) DO UPDATE SET minimum_age=excluded.minimum_age`, 0},
		{`INSERT INTO content_rating_ages VALUES('summary-test',8) ON CONFLICT(value_key) DO UPDATE SET minimum_age=excluded.minimum_age`, 1},
		{`UPDATE content_rating_ages SET value_key='summary-rename' WHERE value_key='summary-test'`, 1},
		{`UPDATE content_rating_ages SET value_key=value_key,minimum_age=minimum_age WHERE value_key='summary-rename'`, 0},
		{`DELETE FROM content_rating_ages WHERE value_key='summary-rename'`, 1},
		{`DELETE FROM content_rating_ages WHERE value_key='absent'`, 0},
	}
	for _, gated := range []bool{false, true} {
		for _, c := range cases {
			if gated {
				err = dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error { _, e := tx.Exec(c.sql); return e })
			} else {
				_, err = db.Exec(c.sql)
			}
			if err != nil {
				t.Fatalf("%s: %v", c.sql, err)
			}
			next := revision()
			if next != before+c.delta {
				t.Fatalf("%s: %d -> %d want delta %d", c.sql, before, next, c.delta)
			}
			before = next
		}
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO content_rating_ages VALUES('rollback-summary',10)`); err != nil {
		t.Fatal(err)
	}
	var inside int64
	if err = tx.QueryRow(`SELECT revision FROM catalog_summary_classification_revision`).Scan(&inside); err != nil {
		t.Fatal(err)
	}
	if inside != before+1 {
		t.Fatal(inside, before)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if revision() != before {
		t.Fatal("rollback advanced classification")
	}
	sentinel := fmt.Errorf("rollback")
	err = dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO content_rating_ages VALUES('gated-rollback-summary',10)`); err != nil {
			return err
		}
		return sentinel
	})
	if err != sentinel || revision() != before {
		t.Fatal("gated rollback advanced classification", err)
	}
	var cols int
	if err = db.QueryRow(`SELECT count(*) FROM pragma_table_info('catalog_summary_classification_revision') WHERE (name='singleton' AND type='INTEGER' AND pk=1) OR (name='revision' AND type='INTEGER' AND "notnull"=1)`).Scan(&cols); err != nil || cols != 2 {
		t.Fatal("singleton columns", cols, err)
	}
	var fks int
	if err = db.QueryRow(`SELECT count(*) FROM pragma_foreign_key_list('catalog_summary_classification_revision')`).Scan(&fks); err != nil || fks != 0 {
		t.Fatal("unexpected FK", fks, err)
	}
	for _, q := range []string{`INSERT INTO catalog_summary_classification_revision VALUES(2,1)`, `UPDATE catalog_summary_classification_revision SET revision=0`} {
		if _, err = db.Exec(q); err == nil {
			t.Fatal("singleton constraint accepted", q)
		}
	}

	for _, name := range []string{"insert", "delete", "update"} {
		var ddl string
		if err = db.QueryRow(`SELECT sql FROM sqlite_schema WHERE name=?`, "summary_classification_"+name).Scan(&ddl); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.ToUpper(ddl), "AFTER "+strings.ToUpper(name)) {
			t.Fatal(ddl)
		}
		if name == "update" {
			re := regexp.MustCompile(`(?i)UPDATE OF (.*?) ON content_rating_ages`)
			m := re.FindStringSubmatch(ddl)
			if len(m) != 2 {
				t.Fatal(ddl)
			}
			watched := strings.Split(strings.ReplaceAll(m[1], " ", ""), ",")
			old := regexp.MustCompile(`OLD\.([a-z_]+)`).FindAllStringSubmatch(ddl, -1)
			newc := regexp.MustCompile(`NEW\.([a-z_]+)`).FindAllStringSubmatch(ddl, -1)
			var oc, nc []string
			for _, m := range old {
				oc = append(oc, m[1])
			}
			for _, m := range newc {
				nc = append(nc, m[1])
			}
			if !reflect.DeepEqual(watched, []string{"value_key", "minimum_age"}) || !reflect.DeepEqual(oc, watched) || !reflect.DeepEqual(nc, watched) || strings.Count(ddl, " IS NOT ") != 2 {
				t.Fatal(ddl)
			}
		}
	}
	if BaselineDigest() != schemaSourceDigest {
		t.Fatal("digest mismatch")
	}
	for _, v := range []string{"table catalog_summary_classification_revision", "trigger summary_classification_insert", "trigger summary_classification_delete", "trigger summary_classification_update"} {
		if !strings.Contains(knownSchemaObjects, v) {
			t.Fatal(v)
		}
	}
	for _, v := range []string{"catalog_summary_classification_revision.singleton", "catalog_summary_classification_revision.revision"} {
		if !strings.Contains(knownSchemaColumns, v) {
			t.Fatal(v)
		}
	}
}
