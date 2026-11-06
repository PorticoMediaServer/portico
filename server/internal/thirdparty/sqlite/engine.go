package sqlite

import (
	"database/sql"
	"fmt"

	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

// Engine reads the bundled library identity through the held physical
// connection. Wrappers must forward SQLiteEngine; no SQL or mutable hook is
// involved. A library identity alone proves neither connection policy nor a
// stored search profile's compatibility.
func Engine(c *sql.Conn) (version, sourceID string, err error) {
	if c == nil {
		return "", "", fmt.Errorf("sqlite: nil connection for engine identity")
	}
	err = c.Raw(func(raw any) error {
		getter, ok := raw.(interface {
			SQLiteEngine() (string, string, error)
		})
		if !ok {
			return fmt.Errorf("sqlite: connection %T lacks SQLiteEngine", raw)
		}
		version, sourceID, err = getter.SQLiteEngine()
		return err
	})
	if err != nil {
		return "", "", err
	}
	return version, sourceID, nil
}

// SQLiteEngine attests the library servicing THIS live native connection.
// SQLite's library strings are immutable, but a closed connection is never a
// source of fresh evidence. Store does not accept caller-supplied identities.
func (c *conn) SQLiteEngine() (version, sourceID string, err error) {
	c.Lock()
	defer c.Unlock()
	if c.db == 0 {
		return "", "", fmt.Errorf("sqlite: engine identity on closed connection")
	}
	return libc.GoString(sqlite3.Xsqlite3_libversion(c.tls)), libc.GoString(sqlite3.Xsqlite3_sourceid(c.tls)), nil
}
