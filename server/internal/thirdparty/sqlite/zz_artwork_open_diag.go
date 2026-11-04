package sqlite

import (
	"context"
	"database/sql/driver"
	"log"
	"os"
	"strings"
	"time"
)

func artworkOpenTrace(c *conn, stage string, started time.Time) {
	if os.Getenv("PORTICO_ARTWORK_DIAGNOSTIC") == "1" {
		log.Printf("ARTWORK-OPEN conn=%p stage=%s elapsed=%s", c, stage, time.Since(started))
	}
}
func artworkOpenCounts(c *conn, functions, collations, hooks int) {
	if os.Getenv("PORTICO_ARTWORK_DIAGNOSTIC") == "1" {
		log.Printf("ARTWORK-OPEN conn=%p functions=%d collations=%d hooks=%d", c, functions, collations, hooks)
	}
}
func (c *conn) artworkOpenExec(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	started := time.Now()
	name := strings.FieldsFunc(strings.TrimSpace(strings.TrimPrefix(strings.ToLower(query), "pragma")), func(r rune) bool { return r == ' ' || r == '(' || r == '=' })[0]
	defer artworkOpenTrace(c, "pragma-"+name, started)
	return c.exec(ctx, query, args)
}
