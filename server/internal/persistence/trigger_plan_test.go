package persistence

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The statements the sign-out and account-removal triggers run
// search their tables by index instead of scanning them.
func TestTriggerJoinsSearchInsteadOfScan(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for statement, table := range map[string]string{
		`UPDATE playback_sessions SET state='stopped' WHERE session_hash IN(SELECT token_hash FROM authorization_family_tokens WHERE family_id='f')`: "playback_sessions",
		`DELETE FROM topshelf_tokens WHERE authority='local' AND account_id='a'`:                                                                     "topshelf_tokens",
		`DELETE FROM identity_devices WHERE authority='local' AND account_id='a'`:                                                                    "identity_device_families",
		`DELETE FROM identity_pin_lineages WHERE account_id='a'`:                                                                                     "direct_profile_pin_attempts",
		`DELETE FROM identity_pin_lineages WHERE id='l'`:                                                                                             "identity_family_pin_lineages",
		`DELETE FROM identity_device_families WHERE device_id IN (SELECT id FROM identity_devices WHERE authority='local' AND account_id='a')`:       "identity_devices",
	} {
		rows, err := db.Query(`EXPLAIN QUERY PLAN ` + statement)
		if err != nil {
			t.Fatal(err)
		}
		plan := []string{}
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		joined := strings.Join(plan, "\n")
		if strings.Contains(joined, "SCAN "+table) || !strings.Contains(joined, "SEARCH "+table) {
			t.Errorf("%s is not searched by index:\n%s", table, joined)
		}
	}
}

// Every insert into a bounded table runs its storage-limit check. The check
// reads the table's counter first, and only at the limit asks whether the row
// is already there — by its key. As one OR of two key matches, newer SQLite
// planned that question as a scan of the whole table, so each new folder of a
// scan read every folder before it: building a library grew with the square
// of its folders.
func TestStorageLimitChecksReadTheCounterFirstAndSeekTheirKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT name,tbl_name,sql FROM sqlite_schema WHERE type='trigger' AND name LIKE '%\_bound\_check' ESCAPE '\' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	type check struct{ name, table, sql string }
	var checks []check
	for rows.Next() {
		var c check
		if err = rows.Scan(&c.name, &c.table, &c.sql); err != nil {
			t.Fatal(err)
		}
		checks = append(checks, c)
	}
	rows.Close()
	if len(checks) < 50 {
		t.Fatalf("found %d storage-limit checks", len(checks))
	}
	newValue := regexp.MustCompile(`NEW\.\w+`)
	exists := regexp.MustCompile(`NOT EXISTS\((SELECT 1 FROM \w+ WHERE .*?\))\)`)
	for _, c := range checks {
		when := c.sql[strings.Index(c.sql, " WHEN ")+6 : strings.Index(c.sql, " BEGIN ")]
		if !strings.HasPrefix(when, "(SELECT rows>=maximum FROM maintenance_table_bounds WHERE table_name='"+c.table+"') AND ") {
			t.Errorf("%s does not read the counter first: %s", c.name, when)
			continue
		}
		matches := exists.FindAllStringSubmatch(when, -1)
		if len(matches) == 0 {
			t.Errorf("%s has no key check: %s", c.name, when)
		}
		for _, m := range matches {
			query := newValue.ReplaceAllString(m[1], "?")
			plan, err := db.Query(`EXPLAIN QUERY PLAN `+query, make([]any, strings.Count(query, "?"))...)
			if err != nil {
				t.Fatalf("%s: %v", query, err)
			}
			var steps []string
			for plan.Next() {
				var id, parent, unused int
				var detail string
				if err = plan.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				steps = append(steps, detail)
			}
			plan.Close()
			if joined := strings.Join(steps, "\n"); !strings.Contains(joined, "SEARCH "+c.table) {
				t.Errorf("%s reads the whole table for an existing key:\n%s\n%s", c.name, query, joined)
			}
		}
	}
}
