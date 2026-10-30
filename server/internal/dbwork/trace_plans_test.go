package dbwork

import (
	"context"
	"strings"
	"testing"
)

func TestQueryPlanTraceKeepsCompleteQueriesWithinBounds(t *testing.T) {
	query := "SELECT " + strings.Repeat("long_column,", 200) + "id FROM titles WHERE id=?"
	ctx, read := TraceQueryPlans(context.Background())
	traceStatement(ctx, query)
	if got := read(); len(got) != 1 || got[0] != query {
		t.Fatal("plan trace truncated a normal query")
	}
	for i := 0; i < 1100; i++ {
		traceStatement(ctx, strings.Repeat("x", 100000))
	}
	got := read()
	if len(got) != 1024 || len(got[1]) != 64*1024 {
		t.Fatalf("trace bounds: count=%d bytes=%d", len(got), len(got[1]))
	}
	ordinary, readOrdinary := TraceStatements(context.Background())
	traceStatement(ordinary, query)
	if len(readOrdinary()[0]) != 110 {
		t.Fatal("ordinary statement shapes changed")
	}
}
