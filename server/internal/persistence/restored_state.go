package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// A database the server can run on is more than one that opens and passes its
// integrity check. Start-up, and every service it constructs, runs statements
// against the whole schema and reads rows a fresh install always has. Nothing
// recreates those: the baseline is never run again. The two
// checks here say whether a database still has them. They read only, and they
// are derived from the same sources a fresh install is built from, so a new
// table or a new seeded row is covered the day it is added.
var (
	// ErrSchemaIncomplete is a database missing a table, index, trigger or view
	// this build's baseline create.
	ErrSchemaIncomplete = errors.New("the database is missing part of the schema this version requires")
	// ErrSeededRowMissing is a database missing a row every install starts with.
	ErrSeededRowMissing = errors.New("the database is missing a row every install starts with")
)

// VerifySchemaObjects reports whether the database holds every schema object a
// fresh install of this build creates, and whether each of its tables still
// has every column a fresh install gives it. Both lists are generated and
// compiled in, and a test keeps each equal to a fresh install, so the check
// needs nothing but the database it is asked about. An extra object or column
// is not an error here; a missing one is. Trigger and view bodies are not
// compared.
func VerifySchemaObjects(ctx context.Context, db *sql.DB) error {
	rows, err := dbwork.Query(ctx, db, `SELECT type,name FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	present := map[string]bool{}
	for rows.Next() {
		var kind, name string
		if err = rows.Scan(&kind, &name); err != nil {
			return err
		}
		present[kind+" "+name] = true
	}
	if err = rows.Err(); err != nil {
		return err
	}
	rows.Close()
	required := make([]string, 0, len(present))
	for object := range parseSchemaObjects(knownSchemaObjects) {
		required = append(required, object)
	}
	sort.Strings(required)
	for _, object := range required {
		if !present[object] {
			return fmt.Errorf("%w: %s", ErrSchemaIncomplete, object)
		}
	}
	held, err := tableColumns(ctx, db)
	if err != nil {
		return err
	}
	columns := make([]string, 0, len(held))
	for column := range parseSchemaObjects(knownSchemaColumns) {
		columns = append(columns, column)
	}
	sort.Strings(columns)
	for _, column := range columns {
		if !held[column] {
			return fmt.Errorf("%w: column %s", ErrSchemaIncomplete, column)
		}
	}
	return nil
}

// tableColumns answers every "table.column" the database has.
func tableColumns(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	rows, err := dbwork.Query(ctx, db, `SELECT m.name,c.name FROM sqlite_schema m JOIN pragma_table_xinfo(m.name) c WHERE m.type='table' AND m.name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var table, column string
		if err = rows.Scan(&table, &column); err != nil {
			return nil, err
		}
		out[table+"."+column] = true
	}
	return out, rows.Err()
}

var (
	seededTable = regexp.MustCompile(`(?m)^INSERT INTO "?([A-Za-z0-9_]+)"?\s*(?:\([^)]*\))?\s*(?:VALUES|SELECT)\b`)

	seededOnce   sync.Once
	seededTables []string
	seededErr    error
)

// ownerReplacedSeeds are seeded tables the owner's own settings replace: the
// maintenance schedule starts with one nightly window, and saving a schedule
// with none leaves these empty. Every other seeded table is only ever read or
// updated in place.
var ownerReplacedSeeds = map[string]bool{"maintenance_windows": true, "maintenance_window_days": true, "maintenance_window_tasks": true}

// SeededTables lists the tables this build's baseline populate with fixed
// rows: the settings, policies, revisions, cursors and vocabularies that exist
// from the first start. Start-up and the services it constructs read them and
// nothing recreates them. Tables the owner's settings replace are left out.
func SeededTables() ([]string, error) {
	seededOnce.Do(func() {
		seeded := map[string]bool{}
		for _, match := range seededTable.FindAllStringSubmatch(baselineSQL, -1) {
			seeded[match[1]] = true
		}
		for name := range seeded {
			if !ownerReplacedSeeds[name] {
				seededTables = append(seededTables, name)
			}
		}
		sort.Strings(seededTables)
	})
	return seededTables, seededErr
}

// VerifySeededRows reports whether every one of those tables still holds rows.
// Most hold exactly one, pinned by their primary key, so for them this is
// whether that row is there. A missing table is ErrSchemaIncomplete's
// business; run that check first.
func VerifySeededRows(ctx context.Context, db *sql.DB) error {
	gated, err := dbwork.BeginSnapshot(ctx, db)
	if err != nil {
		return err
	}
	defer gated.Rollback()
	return verifySeededRows(ctx, gated.Tx())
}

func verifySeededRows(ctx context.Context, tx *sql.Tx) error {
	tables, err := SeededTables()
	if err != nil {
		return err
	}
	for _, table := range tables {
		// The name comes from this build's own baseline, never from a caller.
		var present bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM "`+table+`")`).Scan(&present); err != nil {
			return err
		}
		if !present {
			return fmt.Errorf("%w: %s", ErrSeededRowMissing, table)
		}
	}
	return nil
}
