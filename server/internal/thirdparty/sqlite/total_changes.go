package sqlite

import sqlite3 "modernc.org/sqlite/lib"

// TotalChanges returns the connection's cumulative changed-row count, including
// trigger and foreign-key effects. Like the other connection operations, it
// must be serialized with statements: database/sql callers use Conn.Raw.
func (c *conn) TotalChanges() (int64, bool) {
	c.Lock()
	defer c.Unlock()
	if c.db == 0 || c.tls == nil {
		return 0, false
	}
	return sqlite3.Xsqlite3_total_changes64(c.tls, c.db), true
}
