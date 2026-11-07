package writer

import (
	"errors"
	"testing"
	"time"
)

func TestEC5OpenFailureAndBoundedCoverage(t *testing.T) {
	origin := time.Now()
	var ledger ec5OpenLedger
	for i := 0; i < 17; i++ {
		ledger.observe(origin, nil, origin, origin.Add(time.Millisecond), errors.New("native opening failed"))
	}
	if ledger.used != 16 || !ledger.overflow {
		t.Fatal("dropped overflow without incompleteness", ledger.used, ledger.overflow)
	}
	first := ledger.records[0]
	if first.Error == "" || first.CacheCurrent != nil || first.SchemaCurrent != nil || first.StatementCurrent != nil || first.ElapsedNS != int64(time.Millisecond) || first.Unavailable["native_memory"] == "" {
		t.Fatal(first)
	}
}
