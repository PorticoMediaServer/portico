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

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// These pins describe the native main.sqlite_schema produced by SQL() on the
// bundled engine, including autoindexes and FTS shadow objects. A DDL or engine
// change requires regenerating both pins and running the fresh-layout test.
const (
	NativeSQLiteVersion = "3.53.4"
	NativeLayoutSHA256  = "dc87abe719ec075a2b09ee265099637b10c12da207a82008186272c1510d909c"
	NativeLayoutObjects = 2172

	// Adopted012+013+014+015 bootstrap header values. These remain zero deliberately;
	// native layout verification, not a version PRAGMA, proves the layout.
	ApplicationID    = 0
	UserVersion      = 0
	DatabaseEncoding = "UTF-8"
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
	if _, err := sqliteconn.AssertEnforcement(ctx, conn); err != nil {
		return err
	}
	var version string
	if err := conn.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&version); err != nil {
		return fmt.Errorf("schema: inspect existing engine: %w", err)
	}
	if version != NativeSQLiteVersion {
		return fmt.Errorf("%w: got %s, want %s", ErrUnsupportedEngine, version, NativeSQLiteVersion)
	}
	if err := assertDatabaseHeader(ctx, conn); err != nil {
		return err
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

func assertDatabaseHeader(ctx context.Context, conn *sql.Conn) error {
	var applicationID, userVersion int64
	var encoding string
	for _, query := range []struct {
		sql  string
		dest any
	}{
		{"PRAGMA main.application_id", &applicationID},
		{"PRAGMA main.user_version", &userVersion},
		{"PRAGMA main.encoding", &encoding},
	} {
		if err := conn.QueryRowContext(ctx, query.sql).Scan(query.dest); err != nil {
			return fmt.Errorf("schema: inspect database header: %w", err)
		}
	}
	if applicationID != ApplicationID || userVersion != UserVersion || encoding != DatabaseEncoding {
		return fmt.Errorf("%w: header application_id=%d user_version=%d encoding=%q, want %d/%d/%q", ErrLayoutMismatch, applicationID, userVersion, encoding, ApplicationID, UserVersion, DatabaseEncoding)
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
