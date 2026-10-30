package operations

import (
	"context"
	"fmt"
	"testing"
)

func TestAlertsRetainOlderOpenAlerts(t *testing.T) {
	s, _, auth := consoleFixture(t)
	tx, err := s.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 101; i++ {
		id := fmt.Sprintf("open-%03d", i)
		_, err = tx.Exec(`INSERT INTO console_alerts(id,code,severity,status,first_ms,last_ms,occurrences,revision) VALUES(?,?,'warning','open',1,1,1,1)`, id, id)
		if err != nil {
			break
		}
	}
	for i := 0; err == nil && i < 100; i++ {
		id := fmt.Sprintf("resolved-%03d", i)
		_, err = tx.Exec(`INSERT INTO console_alerts(id,code,severity,status,first_ms,last_ms,occurrences,revision) VALUES(?,?,'warning','resolved',2,2,1,1)`, id, id)
	}
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	got, err := s.Alerts(context.Background(), auth)
	if err != nil || len(got) != 201 || got[100].Status != "open" {
		t.Fatalf("alerts=%d: %v", len(got), err)
	}
}
