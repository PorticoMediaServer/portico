package sqlite

import (
	"database/sql"
	"fmt"
	"unsafe"

	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

// DQS reads both double-quoted string compatibility flags without changing
// them. Owned connection wrappers must forward the read-only SQLiteDQS method.
func DQS(c *sql.Conn) (ddl, dml int, err error) {
	if c == nil {
		return 0, 0, fmt.Errorf("sqlite: nil connection for DQS")
	}
	err = c.Raw(func(raw any) error {
		getter, ok := raw.(interface{ SQLiteDQS() (int, int, error) })
		if !ok {
			return fmt.Errorf("sqlite: connection %T lacks SQLiteDQS", raw)
		}
		ddl, dml, err = getter.SQLiteDQS()
		return err
	})
	return ddl, dml, err
}

// SQLiteDQS is a read-only optional driver interface. There is no public
// DBCONFIG setter: connection policy is selected before statement preparation.
func (c *conn) SQLiteDQS() (ddl, dml int, err error) {
	c.Lock()
	defer c.Unlock()
	if c.db == 0 {
		return 0, 0, fmt.Errorf("sqlite: DQS on closed connection")
	}
	// Each vararg occupies eight bytes, independently of native pointer width.
	// Reserve a separate aligned int result after the two vararg slots.
	p, err := c.malloc(24)
	if err != nil {
		return 0, 0, err
	}
	defer c.free(p)
	for i, op := range []int32{sqlite3.SQLITE_DBCONFIG_DQS_DDL, sqlite3.SQLITE_DBCONFIG_DQS_DML} {
		result := p + 16
		rc := sqlite3.Xsqlite3_db_config(c.tls, c.db, op, libc.VaList(p, int32(-1), result))
		if rc != sqlite3.SQLITE_OK {
			return ddl, dml, fmt.Errorf("sqlite: read DQS op %d: %w", op, c.errstr(rc))
		}
		value := int(*(*int32)(unsafe.Pointer(result)))
		if i == 0 {
			ddl = value
		} else {
			dml = value
		}
	}
	return ddl, dml, nil
}
