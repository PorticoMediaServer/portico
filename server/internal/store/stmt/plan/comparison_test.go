package plan_test

import (
	"context"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func TestPlanRejectsTupleAndSubqueryComparisonSpellings(t *testing.T) {
	conn, registry, f, _ := fixture(t)
	statement, err := registry.Lookup("sort.tail")
	if err != nil {
		t.Fatal(err)
	}
	original, err := statement.Definition()
	if err != nil {
		t.Fatal(err)
	}
	subquery := "SELECT key FROM items INDEXED BY item_sort WHERE library_id=? AND " + predicate + " AND id>?"
	conditions := map[string]string{
		"review-keyword-adjacency": "AND(key,id)>(?,?)",
		"review-grouped-tuple":     "AND ((key,id))>(?,?)",
		"review-right-subquery":    "AND key > (" + subquery + ")",
		"nested-left-subquery":     "AND (((" + subquery + "))) < key",
		"nested-right-subquery":    "AND key > (((" + subquery + ")))",
		"comments-and-groups":      "AND/* (, ) */(((key, /* ) */ id))) > ((?,?))",
		"right-tuple":              "AND(?,?)<((key,id))",
		"literal-parentheses":      "AND printf('text(,)')<>'' AND ((key,id))>(?,?)",
		"nested-scalar-function":   "AND coalesce((" + subquery + "),0)>key",
		"case-subquery":            "AND key > CASE WHEN 1 THEN (" + subquery + ") ELSE 0 END",
		"between-upper":            "AND key BETWEEN 0 AND (" + subquery + ")",
		"is-not-distinct":          "AND key IS NOT DISTINCT FROM (" + subquery + ")",
		"in-subquery":              "AND key IN (" + subquery + ")",
		"cte-subquery":             "AND key > (WITH scoped AS (" + subquery + ") SELECT key FROM scoped)",
	}
	for name, condition := range conditions {
		t.Run(name, func(t *testing.T) {
			d := original
			d.SQL = "SELECT id,key FROM items INDEXED BY item_sort WHERE library_id=? AND kind=? " + condition + " AND " + predicate + " ORDER BY key,id LIMIT ?"
			r, err := stmt.Describe([]stmt.Definition{d})
			if err != nil {
				t.Fatal(err)
			}
			_, err = plan.Capture(context.Background(), conn, r, f, indexPolicy(t))
			if err == nil {
				t.Fatal("forbidden comparison qualified")
			}
			if !strings.Contains(err.Error(), "unapproved SQL lint") && !strings.Contains(err.Error(), "forbidden plan") {
				t.Fatalf("control did not reach comparison gate: %v", err)
			}
		})
	}
}

func TestPlanAllowsClosedGrammarFunctionsAndQuotedPunctuation(t *testing.T) {
	conn, registry, f, _ := fixture(t)
	statement, _ := registry.Lookup("sort.tail")
	original, _ := statement.Definition()
	for name, condition := range map[string]string{
		"quoted-identifier":     "AND \"key\"=? AND id>?",
		"in-list":               "AND key IN(0,1) AND key=? AND id>?",
		"scalar-like-call":      "AND like('a%',key)=? AND id>?",
		"function-with-comment": "AND coalesce /* ) (, */ (key,0)=? AND id>?",
		"literal-parentheses":   "AND instr('x(,)', ')')>0 AND key=? AND id>?",
		"keyword-function":      "AND replace('x(,)','x','y')<>'' AND key=? AND id>?",
		"escaped-literal":       "AND printf('it''s (,)')<>'' AND key=? AND id>?",
	} {
		t.Run(name, func(t *testing.T) {
			d := original
			d.SQL = "SELECT id,key FROM items INDEXED BY item_sort WHERE library_id=? AND kind=? " + condition + " AND " + predicate + " ORDER BY key,id LIMIT ?"
			r, err := stmt.Describe([]stmt.Definition{d})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := plan.Capture(context.Background(), conn, r, f, indexPolicy(t)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
