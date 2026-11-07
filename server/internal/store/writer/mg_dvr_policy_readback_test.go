package writer

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Keep UPDATE RETURNING and the stored row honest; corrupt only the separate
// post-update SELECT with another valid integer. An error-only driver fault
// cannot prove the value-equality guard is necessary.
type mgDVRPolicyMismatchConnection struct {
	writerDriverConn
	table    string
	updated  bool
	observed *bool
}

func (c *mgDVRPolicyMismatchConnection) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.writerDriverConn.QueryContext(ctx, q, args)
	if err != nil {
		return rows, err
	}
	if c.updated && strings.HasPrefix(q, "SELECT ") && strings.Contains(q, " FROM "+c.table+" WHERE singleton=") {
		return &mgDVRPolicyMismatchRows{Rows: rows, observed: c.observed}, nil
	}
	if strings.HasPrefix(q, "UPDATE "+c.table+" SET ") {
		c.updated = true
	}
	return rows, nil
}

type mgDVRPolicyMismatchRows struct {
	driver.Rows
	observed *bool
}

func (r *mgDVRPolicyMismatchRows) Next(values []driver.Value) error {
	if err := r.Rows.Next(values); err != nil {
		return err
	}
	value, ok := values[1].(int64)
	if !ok {
		return fmt.Errorf("native policy integer has type %T", values[1])
	}
	values[1] = value + 1
	*r.observed = true
	return nil
}
func TestMGDVRPolicyReadbackValueMismatchRequiresWholeRollback(t *testing.T) {
	for _, table := range []string{"dvr_defaults", "dvr_storage_policy"} {
		t.Run(table, func(t *testing.T) {
			observed := false
			w, before, storage := mgDVRPolicyFixture(t, func(c driver.Conn) driver.Conn {
				return &mgDVRPolicyMismatchConnection{writerDriverConn: c.(writerDriverConn), table: table, observed: &observed}
			})
			turn := mgDVRPolicyTurn(t, w)
			if table == "dvr_defaults" {
				out, err := setMGDVRDefaults(t.Context(), turn, before, mgDVRDefaultsDesired{120, 300, 7, 0, "stop-watching"})
				if !errors.Is(err, ErrConflict) || out != (mgDVRDefaultsSnapshot{}) {
					t.Fatal("value-mismatched defaults released", out, err)
				}
			} else {
				out, err := setMGDVRStorage(t.Context(), turn, storage, mgDVRStorageDesired{7, 10, 1024, 8192})
				if !errors.Is(err, ErrConflict) || out != (mgDVRStorageSnapshot{}) {
					t.Fatal("value-mismatched storage released", out, err)
				}
			}
			if !observed {
				t.Fatal("control missed the independent value readback")
			}
			// This projection is deliberately different from the corrupted full SELECT.
			column, wanted, original, revision := "before_seconds", int64(120), before.beforeSeconds, before.revision
			if table == "dvr_storage_policy" {
				column = "retention_days"
				wanted = 7
				original = storage.policy.retentionDays
				revision = storage.revision
			}
			query := "SELECT revision," + column + " FROM " + table + " WHERE singleton IN(1)"
			var actualRevision, actualValue int64
			if err := turn.transaction.conn.QueryRowContext(t.Context(), query).Scan(&actualRevision, &actualValue); err != nil || actualRevision != revision+1 || actualValue != wanted {
				t.Fatal("control missed the native update", actualRevision, actualValue, err)
			}
			if err := turn.closeRows(); err != nil {
				t.Fatal(err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			if err := w.db.QueryRowContext(t.Context(), query).Scan(&actualRevision, &actualValue); err != nil || actualRevision != revision || actualValue != original {
				t.Fatal("failed readback escaped rollback", actualRevision, actualValue, err)
			}
		})
	}
}
