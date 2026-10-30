package catalog

import (
	"database/sql"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// seekOnlyPlan fails when a statement's plan materialises a view or table,
// builds an automatic index, or scans a table: every catalogue, personal and
// availability table must be reached by key. `bounded` names the aliases that
// may be materialised and scanned: the statement's own subqueries and CTEs
// whose size its keys bound.
func seekOnlyPlan(t *testing.T, db *sql.DB, label, query string, args []any, bounded ...string) {
	t.Helper()
	rows, err := db.Query(`EXPLAIN QUERY PLAN `+query, args...)
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	defer rows.Close()
	allowed := map[string]bool{"json_each": true, "CONSTANT": true}
	for _, name := range bounded {
		allowed[name] = true
	}
	scan := regexp.MustCompile(`^(?:SCAN|MATERIALIZE) (\S+)`)
	subquery := regexp.MustCompile(`^\(subquery-\d+\)$`)
	plan, bad := []string{}, []string{}
	for rows.Next() {
		var node, parent, unused int
		var detail string
		if err = rows.Scan(&node, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
		if strings.Contains(detail, "AUTOMATIC") {
			bad = append(bad, detail)
		}
		if m := scan.FindStringSubmatch(detail); m != nil && !allowed[m[1]] && !subquery.MatchString(m[1]) {
			bad = append(bad, detail)
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(bad) > 0 {
		t.Errorf("%s reads beyond its keys: %q\nplan:\n%s", label, bad, strings.Join(plan, "\n"))
	}
}

// TestHomeStatementsNeverMaterialiseLegacyViews pins NEW-47: SQLite cannot
// flatten a LEFT JOIN onto a catalog_*_legacy view that is itself a join, so
// such a statement copies every episode (or song, or book file) of every
// library before its first row. Continue Watching's per-show continuation and
// both Recently Added walks are on the Home hot path; each must stay a walk
// of its own index with key seeks, restricted or not.
func TestHomeStatementsNeverMaterialiseLegacyViews(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "plans.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	age := 13
	for _, r := range []HomeRequest{
		{Profile: "p", Libraries: []string{"tv"}},
		{Profile: "p", Libraries: []string{"tv"}, Restrictions: identity.ContentRestrictions{MaximumAge: &age, BlockUnrated: true, BlockedLabels: []string{"Adult"}}},
	} {
		label := "unrestricted"
		if r.Restrictions.Active() {
			label = "restricted"
		}
		cutoffs, premieres := `{"tv":""}`, `{"tv":1}`
		query, args := homeShowContinuation(r, cutoffs, premieres, 8, "", &showActivityKey{lastActivity: "2026-09-01", showID: 1})
		// w is the profile's started-shows window (LIMIT window), chosen its
		// result; wp and defaults are personalstate's one-row profile and its
		// at most four container defaults, each a primary-key seek; next is one
		// show's later episodes in number order, reached through the show index.
		bounded := []string{"w", "chosen", "wp", "defaults", "next"}
		seekOnlyPlan(t, db, label+" continue watching", query, args, bounded...)
		query, args = homeShowContinuation(r, cutoffs, premieres, 1, "s", nil)
		seekOnlyPlan(t, db, label+" show workspace next", query, args, bounded...)

		phases, phaseArgs := homeRecentPhases(r, nil)
		for i, phase := range phases {
			args := append(append([]any{}, phaseArgs[i]...), "tv", "\uffff", "\uffff")
			// The walk follows its recent-order index (a SEARCH on the library
			// prefix, windowed); nothing else may be scanned.
			// br is a 256-row window of the index; member a work's 64 newest members.
			seekOnlyPlan(t, db, label+" recently added phase "+string(rune('1'+i)), phase, args, "br", "member")
		}
	}
}
