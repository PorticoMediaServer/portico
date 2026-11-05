package schema

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
)

// These pins describe the native main.sqlite_schema produced by SQL() on the
// bundled engine, including autoindexes and FTS shadow objects. A DDL or engine
// change requires regenerating both pins and running the fresh-layout test.
const (
	NativeSQLiteVersion = "3.53.4"
	NativeLayoutSHA256  = "8097c1ea92353e2306b21d8eede8908d06c1d447728832e4c0a0e59f1366b309"
	NativeLayoutObjects = 2063
)

var (
	ErrLayoutMismatch    = errors.New("schema: existing database layout mismatch")
	ErrUnsupportedEngine = errors.New("schema: SQLite engine does not match the native layout pin")
)

// AssertExisting verifies the adopted native layout on a caller-owned exclusive
// connection before any runtime users start. It reads metadata only: it neither
// executes SQL() nor repairs, migrates or seeds an existing database. The caller
// must configure every physical connection and prevent concurrent DDL. This is
// a layout check, not an integrity, contents or authority check.
func AssertExisting(ctx context.Context, conn *sql.Conn) error {
	var foreignKeys int
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return fmt.Errorf("schema: check existing foreign keys: %w", err)
	}
	if foreignKeys != 1 {
		return ErrForeignKeysDisabled
	}
	var version string
	if err := conn.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&version); err != nil {
		return fmt.Errorf("schema: inspect existing engine: %w", err)
	}
	if version != NativeSQLiteVersion {
		return fmt.Errorf("%w: got %s, want %s", ErrUnsupportedEngine, version, NativeSQLiteVersion)
	}
	got, count, err := nativeLayout(ctx, conn)
	if err != nil {
		return fmt.Errorf("schema: inspect existing layout: %w", err)
	}
	if got != NativeLayoutSHA256 || count != NativeLayoutObjects {
		return fmt.Errorf("%w: SQL %s, native %s (%d objects), want %s (%d objects)", ErrLayoutMismatch, SHA256, got, count, NativeLayoutSHA256, NativeLayoutObjects)
	}
	return nil
}

func nativeLayout(ctx context.Context, conn *sql.Conn) (string, int, error) {
	rows, err := conn.QueryContext(ctx, `SELECT type,name,tbl_name,sql FROM main.sqlite_schema
ORDER BY type COLLATE BINARY,name COLLATE BINARY,tbl_name COLLATE BINARY`)
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()
	digest := sha256.New()
	io.WriteString(digest, "portico-native-layout-sha256-v1\x00")
	writeLayoutText(digest, SHA256)
	writeLayoutText(digest, NativeSQLiteVersion)
	count := 0
	for rows.Next() {
		var kind, name, table string
		var declaration sql.NullString
		if err := rows.Scan(&kind, &name, &table, &declaration); err != nil {
			return "", 0, err
		}
		writeLayoutText(digest, kind)
		writeLayoutText(digest, name)
		writeLayoutText(digest, table)
		if declaration.Valid {
			digest.Write([]byte{1})
			writeLayoutText(digest, declaration.String)
		} else {
			digest.Write([]byte{0})
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(digest.Sum(nil)), count, nil
}

func writeLayoutText(digest hash.Hash, text string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(text)))
	digest.Write(length[:])
	io.WriteString(digest, text)
}
