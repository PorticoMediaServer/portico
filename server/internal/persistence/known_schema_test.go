package persistence

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// The allow-list is only useful if it is exactly what a fresh install produces.
// When it drifts, this writes the new list beside the package and says so, so
// regenerating it is a copy rather than a chore.
func TestKnownSchemaObjectsMatchAFreshInstall(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "fresh.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT type,name FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' ORDER BY type,name`)
	if err != nil {
		t.Fatal(err)
	}
	var actual []string
	for rows.Next() {
		var kind, name string
		if err = rows.Scan(&kind, &name); err != nil {
			t.Fatal(err)
		}
		actual = append(actual, kind+" "+name)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(actual) < 100 {
		t.Fatalf("a fresh install produced only %d schema objects", len(actual))
	}
	sort.Strings(actual)
	// The startup installer chain's own list only: subsystem tables are installed by their
	// owners on first use and never appear in a fresh install.
	known := parseSchemaObjects(knownSchemaObjects)
	var missing, extra []string
	for _, name := range actual {
		if !known[name] {
			missing = append(missing, name)
		}
	}
	seen := map[string]bool{}
	for _, name := range actual {
		seen[name] = true
	}
	for name := range known {
		if !seen[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	if len(missing) == 0 && len(extra) == 0 {
		// A fresh database must also report nothing foreign about itself.
		unknown, err := UnknownSchemaObjects(context.Background(), db)
		if err != nil {
			t.Fatal(err)
		}
		if len(unknown) != 0 {
			t.Fatalf("a fresh install reported %d foreign schema objects: %v", len(unknown), unknown[:min(5, len(unknown))])
		}
		return
	}
	// Not t.TempDir: the point of this file is to still be there after the test
	// fails, so regenerating the list is a copy rather than a chore.
	directory, mkErr := os.MkdirTemp("", "portico-schema-")
	if mkErr != nil {
		t.Fatal(mkErr)
	}
	generated := filepath.Join(directory, "known_schema.txt")
	_ = os.WriteFile(generated, []byte(strings.Join(actual, "\n")), 0600)
	t.Fatalf(`the schema this build installs no longer matches the generated allow-list
(%d objects this install creates that the list does not have, %d the list has that it does not).

Replace knownSchemaObjects in known_schema.go with the contents of:

	%s
`, len(missing), len(extra), generated)
}

// The demo server reports a count of schema objects it does not define, and a state directory
// that has been through a few builds can hold hundreds of them. This is on a background
// goroutine, but it still has to be a fixed cost rather than something that grows per object:
// the whole answer is one sqlite_schema scan against a set built once.
func TestForeignSchemaObjectsCostOneCatalogRead(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "foreign.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	// A fresh install knows itself; this is the baseline the message is measured against.
	if unknown, e := UnknownSchemaObjects(ctx, db); e != nil || len(unknown) != 0 {
		t.Fatal("a fresh install reported foreign objects", len(unknown), e)
	}
	const foreign = 400
	for i := range foreign {
		name := fmt.Sprintf("another_build_%03d", i)
		if _, e := db.ExecContext(ctx, `CREATE TABLE "`+name+`"(id INTEGER PRIMARY KEY, value TEXT);
 CREATE INDEX "`+name+`_value" ON "`+name+`"(value)`); e != nil {
			t.Fatal(e)
		}
	}
	before := dbwork.DatabaseCalls()
	unknown, err := UnknownSchemaObjects(ctx, db)
	spent := dbwork.DatabaseCalls() - before
	if err != nil {
		t.Fatal(err)
	}
	if len(unknown) != 2*foreign {
		t.Fatalf("expected %d foreign objects, got %d", 2*foreign, len(unknown))
	}
	// One read for the catalogue, whatever is in it. A per-object query would be 800.
	if spent != 1 {
		t.Fatalf("listing %d foreign objects spent %d database calls, not one", len(unknown), spent)
	}
	// It reads and never writes: a build that does not define a table cannot know what
	// dropping it would cost, so nothing here is ever removed.
	var still int
	if e := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE name LIKE 'another_build_%'`).Scan(&still); e != nil || still != 2*foreign {
		t.Fatal("the foreign objects were disturbed", still, e)
	}
	sorted := append([]string(nil), unknown...)
	sort.Strings(sorted)
	if sorted[0] != "index another_build_000_value" || sorted[len(sorted)-1] != "table another_build_399" {
		t.Fatal("the report does not name what it found", sorted[0], sorted[len(sorted)-1])
	}
}

// createStatement matches any remaining runtime schema declaration. Subsystems
// now install through the baseline, so this list should shrink as old installers
// are removed rather than requiring a large fixed set.
var createStatement = regexp.MustCompile(`CREATE\s+(TABLE|UNIQUE\s+INDEX|INDEX|TRIGGER|VIEW)\s+IF\s+NOT\s+EXISTS\s+"?([A-Za-z_0-9]+)"?`)

// Any remaining first-use schema declarations are still this build. The list is
// generated from the sources so it cannot silently drift.
func TestSubsystemSchemaObjectsMatchTheSources(t *testing.T) {
	found := map[string]bool{}
	for _, root := range []string{filepath.Join("..", ".."), filepath.Join("..", "..", "cmd")} {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				// This package's own objects are the chain's list, not this one.
				if info.Name() == "persistence" || info.Name() == "node_modules" || info.Name() == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			raw, readErr := os.ReadFile(filepath.Clean(path))
			if readErr != nil {
				return readErr
			}
			for _, m := range createStatement.FindAllStringSubmatch(string(raw), -1) {
				kind := strings.ToLower(strings.Join(strings.Fields(m[1]), " "))
				if kind == "unique index" {
					kind = "index"
				}
				found[kind+" "+m[2]] = true
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	actual := make([]string, 0, len(found))
	for name := range found {
		actual = append(actual, name)
	}
	sort.Strings(actual)
	listed := parseSchemaObjects(subsystemSchemaObjects)
	var missing, extra []string
	for _, name := range actual {
		if !listed[name] {
			missing = append(missing, name)
		}
	}
	for name := range listed {
		if !found[name] {
			extra = append(extra, name)
		}
	}
	if len(missing) == 0 && len(extra) == 0 {
		return
	}
	sort.Strings(extra)
	directory, mkErr := os.MkdirTemp("", "portico-subsystem-schema-")
	if mkErr != nil {
		t.Fatal(mkErr)
	}
	generated := filepath.Join(directory, "subsystem_schema.txt")
	_ = os.WriteFile(generated, []byte(strings.Join(actual, "\n")), 0600)
	t.Fatalf(`the objects this build installs outside the startup chain no longer match the generated list
(%d the sources create that the list does not have, %d the list has that the sources do not).

Replace subsystemSchemaObjects in known_schema.go with the contents of:

	%s
`, len(missing), len(extra), generated)
}
