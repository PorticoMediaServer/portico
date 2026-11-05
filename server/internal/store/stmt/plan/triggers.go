package plan

import (
	"context"
	"database/sql"
	"fmt"
)

// The approved store has zero user triggers. TEMP and attached schemas must not
// supply hidden work either. FK enforcement programs are not sqlite_schema rows.
func assertNoTriggers(ctx context.Context, conn *sql.Conn) error {
	dbs, err := query(ctx, conn, "PRAGMA database_list")
	if err != nil {
		return err
	}
	for _, db := range dbs.Values {
		name := db[1].(string)
		triggers, err := query(ctx, conn, "SELECT name FROM "+identifier(name)+".sqlite_schema WHERE type='trigger' ORDER BY name LIMIT 1")
		if err != nil {
			return err
		}
		if len(triggers.Values) != 0 {
			return fmt.Errorf("plan: user trigger forbidden: %s.%s", name, triggers.Values[0][0])
		}
	}
	return nil
}
