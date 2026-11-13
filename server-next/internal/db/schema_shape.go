package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// CheckSchema says whether the database open on conn is one this program can run on, before
// a copy of it is kept as a backup or put in place by a restore.
//
// A database at this program's version must hold exactly what a new one holds. One at an
// older version is accepted, since the next start upgrades it, as long as it holds no trigger
// and no view: those are what could run or hide something, and Portico's schema has none. One
// from a newer version is refused.
//
// Tables are compared by their columns, keys and references, not by the text that made them:
// a table that gained a column in an upgrade never reads like the schema file's, though it is
// the same table.
func CheckSchema(ctx context.Context, conn *sql.DB) error {
	var version sql.NullInt64
	if err := conn.QueryRowContext(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&version); err != nil || version.Int64 < firstVersion {
		return errors.New("this is not a Portico database")
	}
	if version.Int64 > int64(schemaVersion()) {
		return errors.New("it was made by a newer version of Portico")
	}
	have, err := schemaShape(ctx, conn)
	if err != nil {
		return err
	}
	names := func(shape map[string]string) []string {
		out := make([]string, 0, len(shape))
		for name := range shape {
			out = append(out, name)
		}
		slices.Sort(out)
		return out
	}
	if version.Int64 < int64(schemaVersion()) {
		for _, name := range names(have) {
			if strings.HasPrefix(name, "trigger ") || strings.HasPrefix(name, "view ") {
				return fmt.Errorf("it holds %s, which Portico's schema does not define", name)
			}
		}
		return nil
	}
	fresh, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		return err
	}
	defer fresh.Close()
	fresh.SetMaxOpenConns(1)
	if _, err := fresh.ExecContext(ctx, schemaSQL); err != nil {
		return err
	}
	want, err := schemaShape(ctx, fresh)
	if err != nil {
		return err
	}
	// A copy with a table or an index missing would open and then fail on the first request
	// that needs it.
	for _, name := range names(want) {
		if _, held := have[name]; !held {
			return fmt.Errorf("%w: it lacks %s, which Portico's schema defines", ErrOldTestFolder, name)
		}
	}
	for _, name := range names(have) {
		if shape, defined := want[name]; !defined {
			return fmt.Errorf("%w: it holds %s, which Portico's schema does not define", ErrOldTestFolder, name)
		} else if shape != have[name] {
			return fmt.Errorf("%w: its %s is not as Portico's schema defines it", ErrOldTestFolder, name)
		}
	}
	return nil
}

// schemaShape describes everything a database holds, by kind and name ("table items",
// "index items_by_title"). A table is described by its columns in order (name, type, whether
// required, default, place in the key), its references, the keys and unique sets declared
// with it, and whether it is STRICT or WITHOUT ROWID. A CHECK on a column is not seen. An
// index, a trigger or a view is described by the SQL that made it, white space aside; SQLite
// never rewrites that for an index, so a step must spell an index as the schema file does.
func schemaShape(ctx context.Context, conn *sql.DB) (map[string]string, error) {
	type object struct{ kind, name, text string }
	var objects []object
	// Every row is read, SQLite's own names included: a trigger or a view can be given a name
	// beginning sqlite_ by writing to the schema table directly, and SQLite runs it.
	rows, err := conn.QueryContext(ctx, `SELECT type, name, ifnull(sql, '') FROM sqlite_master`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var o object
		if err := rows.Scan(&o.kind, &o.name, &o.text); err != nil {
			rows.Close()
			return nil, err
		}
		objects = append(objects, o)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, o := range objects {
		switch {
		case o.kind == "table" && (o.name == "sqlite_sequence" || o.name == "sqlite_stat1" || o.name == "sqlite_stat4"):
			// SQLite's own tables for row numbers and query statistics appear by themselves.
		case o.kind == "index" && o.text == "":
			// The index SQLite makes for a key or a unique set: described with its table.
		case o.kind == "table":
			var shape string
			err := conn.QueryRowContext(ctx, `SELECT
				(SELECT 'strict ' || strict || ' without rowid ' || wr FROM pragma_table_list WHERE schema = 'main' AND name = ?1)
				|| ' columns ' || (SELECT group_concat(name || ' ' || upper(type) || ' ' || "notnull" || ' ' || ifnull(dflt_value, 'none') || ' ' || pk || ' ' || hidden, ', ' ORDER BY cid)
					FROM pragma_table_xinfo(?1))
				|| ' references ' || (SELECT ifnull(group_concat("from" || ' ' || "table" || ' ' || ifnull("to", '') || ' ' || on_update || ' ' || on_delete, ', ' ORDER BY "from", "table", "to"), '')
					FROM pragma_foreign_key_list(?1))
				|| ' keys ' || (SELECT ifnull(group_concat(columns, ', ' ORDER BY columns), '') FROM (
					SELECT origin || ' ' || (SELECT group_concat(name, ' ' ORDER BY seqno) FROM pragma_index_info(made.name)) AS columns
					FROM pragma_index_list(?1) AS made WHERE origin != 'c'))`, o.name).Scan(&shape)
			if err != nil {
				return nil, fmt.Errorf("reading table %s: %w", o.name, err)
			}
			out["table "+o.name] = shape
		default:
			out[o.kind+" "+o.name] = strings.Join(strings.Fields(o.text), " ")
		}
	}
	return out, nil
}
