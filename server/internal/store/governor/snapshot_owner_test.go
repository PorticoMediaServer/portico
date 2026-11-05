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

type snapshotCloseCounter struct {
	driver.Conn
	calls int
}

func (c *snapshotCloseCounter) Close() error { c.calls++; return nil }
func TestSnapshotResourceCloseFailureRetainsOwnership(t *testing.T) {
	native := &snapshotCloseCounter{}
	owner := &snapshotOwnedConnector{failed: make(map[*snapshotOwnedConn]error)}
	c := &snapshotOwnedConn{Conn: native, owner: owner}
	rowCalls, stmtCalls := 0, 0
	injected := errors.New("row disposal uncertain")
	c.track(true, func() error {
		rowCalls++
		if rowCalls == 1 {
			return injected
		}
		return nil
	})
	c.track(false, func() error { stmtCalls++; return nil })
	if err := c.Close(); !errors.Is(err, injected) || len(owner.failed) != 1 || native.calls != 0 || stmtCalls != 0 {
		t.Fatal("failed rows lost ownership or finalized their statement", err, native.calls, stmtCalls)
	}
	if err := c.Close(); err != nil || rowCalls != 2 || stmtCalls != 1 || native.calls != 1 || len(owner.failed) != 0 || len(c.resources) != 0 {
		t.Fatal("owned cleanup retry failed", err, rowCalls, stmtCalls, native.calls)
	}
	if err := c.Close(); err != nil || native.calls != 1 || stmtCalls != 1 {
		t.Fatal("native close repeated", err, native.calls, stmtCalls)
	}
}
