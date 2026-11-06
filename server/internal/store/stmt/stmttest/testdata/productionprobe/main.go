package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func main() {
	d := stmt.Definition{ID: "production.probe", SQL: "SELECT 1", Owner: "probe", CallSites: []string{"main.go"}, Kind: stmt.Read, Cohorts: []stmt.Cohort{{Name: "one"}}, Plan: stmt.Plan{Hot: true}}
	if r, err := stmttest.New(&testing.T{}, []stmt.Definition{d}); !errors.Is(err, stmttest.ErrTestOnly) || r != nil {
		panic("unit-fixture adapter authorized an ordinary binary")
	}
	if r, err := stmt.New([]stmt.Definition{d}); !errors.Is(err, stmt.ErrUnqualified) || r != nil {
		panic("unreceipted production registration succeeded")
	}
	fmt.Println("production fixture registration refused")
}
