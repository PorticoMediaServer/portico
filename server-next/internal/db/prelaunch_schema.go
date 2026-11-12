package db

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
)

var ErrOldTestFolder = errors.New("This data folder was made by an older test build of Portico. Start with a fresh data folder.")

// Before release, different schemas have the same version. Compare names, not data;
// an extra table or column is harmless, but anything missing would fail a later read.
func checkTestSchema(ctx context.Context, path string) error {
	fresh, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return err
	}
	defer fresh.Close()
	fresh.SetMaxOpenConns(1)
	if _, err := fresh.ExecContext(ctx, schemaSQL); err != nil {
		return err
	}
	if _, err := fresh.ExecContext(ctx, `ATTACH DATABASE ? AS held`, "file:"+url.PathEscape(path)+"?mode=ro"); err != nil {
		return err
	}
	var missing bool
	err = fresh.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT t.name, c.name FROM main.sqlite_schema t JOIN pragma_table_xinfo(t.name, 'main') c WHERE t.type = 'table'
		EXCEPT SELECT t.name, c.name FROM held.sqlite_schema t JOIN pragma_table_xinfo(t.name, 'held') c WHERE t.type = 'table'
	)`).Scan(&missing)
	if err == nil && missing {
		return ErrOldTestFolder
	}
	return err
}
