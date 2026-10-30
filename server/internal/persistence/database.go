package persistence

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func Set(db *sql.DB, key, value string) error {
	// Set has no caller context (startup identity and owner settings both call
	// it), so it admits as ordinary interactive work rather than guessing a
	// narrower class. The signature stays context-free; the gate does not.
	_, e := dbwork.ExecWrite(context.Background(), db, dbwork.ClassInteractive, `INSERT INTO configuration VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return e
}
func Get(db *sql.DB, key string) string {
	var v string
	_ = db.QueryRow(`SELECT value FROM configuration WHERE key=?`, key).Scan(&v)
	return v
}
