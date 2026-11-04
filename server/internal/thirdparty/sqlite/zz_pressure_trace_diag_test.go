package sqlite

import (
	"context"
	"testing"
)

func TestPressureNativeTraceSeparatesRootAndTriggerBody(t *testing.T) {
	c, err := newConn(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.exec(context.Background(), `CREATE TABLE a(v); CREATE TABLE b(v); CREATE TRIGGER copy_a AFTER INSERT ON a BEGIN INSERT INTO b(v) VALUES(new.v); END;`, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = WithPressureDomain(ctx, "control")
	if _, err := c.exec(ctx, `INSERT INTO a(v) VALUES(1)`, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.exec(ctx, "-- TRIGGER is only a caller comment here\nINSERT INTO a(v) VALUES(2)", nil); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int64{}
	pressureNativeCounts.Lock()
	for key, count := range pressureNativeCounts.counts {
		if key.Domain == "control" {
			t.Logf("%+v count=%d", key, count)
			counts[key.Level] += count
		}
	}
	pressureNativeCounts.Unlock()
	if counts["root"] != 2 || counts["trigger-entry"] != 2 || counts["trigger-body"] != 2 {
		t.Fatalf("native trace lost root/trigger distinction: %v", counts)
	}
	if _, active := pressureNativeStates.Load(c.db); active {
		t.Fatal("native trace outlived the statement")
	}
}
