package governor

import (
	"database/sql/driver"
	"errors"
	"testing"
)

type dqsEvidenceConn struct {
	driver.Conn
	ddl, dml, calls int
	err             error
}

func (c *dqsEvidenceConn) SQLiteDQS() (int, int, error) { c.calls++; return c.ddl, c.dml, c.err }
func TestSnapshotOwnedDQSGetterForwardsExactEvidence(t *testing.T) {
	injected := errors.New("DQS evidence unavailable")
	for _, test := range []struct {
		ddl, dml int
		err      error
	}{{0, 0, nil}, {1, 0, nil}, {0, 1, nil}, {0, 0, injected}} {
		native := &dqsEvidenceConn{ddl: test.ddl, dml: test.dml, err: test.err}
		owner := &snapshotOwnedConn{Conn: native}
		ddl, dml, err := owner.SQLiteDQS()
		if ddl != test.ddl || dml != test.dml || err != test.err || native.calls != 1 {
			t.Fatal("DQS evidence altered", ddl, dml, err, native.calls)
		}
	}
	var missing driver.Conn
	owner := &snapshotOwnedConn{Conn: missing}
	if _, _, err := owner.SQLiteDQS(); err == nil {
		t.Fatal("missing getter reported successful policy evidence")
	}
}
