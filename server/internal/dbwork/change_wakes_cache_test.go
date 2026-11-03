package dbwork

import (
	"context"
	"path/filepath"
	"testing"
)

// A scan commits the same root sets thousands of times; their trigger closure
// is cached (OPS-1 M2: walking it per commit was a third of the server's CPU).
// The cache must give the same closure, and a schema that adds a trigger must
// clear it, or a new cascade would stop waking its worker.
func TestExpandedChangesCacheFollowsNewTriggers(t *testing.T) {
	db, err := OpenHandle(filepath.Join(t.TempDir(), "closure.sqlite"), DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE closure_a(id INTEGER PRIMARY KEY);CREATE TABLE closure_b(id INTEGER PRIMARY KEY);CREATE TABLE closure_c(id INTEGER PRIMARY KEY);CREATE TRIGGER closure_ab AFTER INSERT ON closure_a BEGIN INSERT INTO closure_b VALUES(NEW.id);END`); err != nil {
		t.Fatal(err)
	}
	if err = InstallWakeDependencies(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	first := expandedChanges(map[string]bool{"closure_a": true})
	if !first["closure_a"] || !first["closure_b"] || first["closure_c"] {
		t.Fatalf("closure of closure_a = %v", first)
	}
	if again := expandedChanges(map[string]bool{"closure_a": true}); !again["closure_b"] || again["closure_c"] {
		t.Fatalf("cached closure of closure_a = %v", again)
	}
	if _, err = db.Exec(`CREATE TRIGGER closure_bc AFTER INSERT ON closure_b BEGIN INSERT INTO closure_c VALUES(NEW.id);END`); err != nil {
		t.Fatal(err)
	}
	if err = InstallWakeDependencies(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if after := expandedChanges(map[string]bool{"closure_a": true}); !after["closure_c"] {
		t.Fatalf("a new trigger's cascade is missing from the cached closure: %v", after)
	}
	if !matchesChanged([]string{"closure_c"}, expandedChanges(map[string]bool{"closure_a": true})) || !matchesChanged([]string{"closure_*"}, map[string]bool{"closure_b": true}) || matchesChanged([]string{"other", "closure_x*"}, map[string]bool{"closure_b": true}) {
		t.Fatal("exact and prefix subscriptions must match as before")
	}
}

// BenchmarkWakeMatching is one commit's wake work against this schema's shape:
// hundreds of tables in a closure, dozens of subscriptions.
func BenchmarkWakeMatching(b *testing.B) {
	changed := map[string]bool{}
	for i := 0; i < 600; i++ {
		changed["catalog_table_"+string(rune('a'+i%26))+string(rune('a'+i/26))] = true
	}
	subscriptions := make([][]string, 40)
	for i := range subscriptions {
		subscriptions[i] = []string{"unrelated_one", "unrelated_two", "unrelated_three"}
	}
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		for _, patterns := range subscriptions {
			_ = matchesChanged(patterns, changed)
		}
	}
}
