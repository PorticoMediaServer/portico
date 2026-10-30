package main

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// The conversion fallback reads a member's current remote cap through this
// reader: the stored limit in bits per second, none for a member without
// limits, and an error, never "none", for limits that cannot be read.
func TestMemberCeilingReaderReadsTheCurrentCap(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('member','member',X'00','member-profile',1),('someone-else','someone-else',X'00','someone-else-profile',1)`); err != nil {
		t.Fatal(err)
	}
	read := func(account string) (int, error) {
		t.Helper()
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		return memberCeilingReader()(context.Background(), tx, account)
	}
	set := func(account, body string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO access_limits VALUES(?,1,0,?) ON CONFLICT(account_id) DO UPDATE SET body=excluded.body,revision=access_limits.revision+1`, account, body); err != nil {
			t.Fatal(err)
		}
	}
	if limit, err := read("member"); err != nil || limit != 0 {
		t.Fatalf("a member without limits: %d %v", limit, err)
	}
	set("member", `{"remoteBitrateKbps":2000}`)
	if limit, err := read("member"); err != nil || limit != 2_000_000 {
		t.Fatalf("a member capped at 2000 kbit/s: %d %v", limit, err)
	}
	set("member", `{"remoteBitrateKbps":1000}`)
	if limit, err := read("member"); err != nil || limit != 1_000_000 {
		t.Fatalf("after the cap was lowered: %d %v", limit, err)
	}
	if limit, err := read("someone-else"); err != nil || limit != 0 {
		t.Fatalf("another account: %d %v", limit, err)
	}
	// A missing account is not an unrestricted member; stale authority must fail.
	if limit, err := read("missing-account"); err == nil {
		t.Fatalf("missing account was taken for an unlimited member: %d", limit)
	}
	set("member", `{"remoteBitrateKbps":"many"}`)
	if limit, err := read("member"); err == nil {
		t.Fatalf("limits that cannot be read were taken for a cap of %d", limit)
	}
}

// The producer is given that reader where it is built.
func TestMainWiresTheMemberCeilingReader(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	wired := false
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		method, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || method.Sel.Name != "ConfigureMemberCeiling" {
			return true
		}
		if reader, ok := call.Args[0].(*ast.CallExpr); ok {
			if name, ok := reader.Fun.(*ast.Ident); ok && name.Name == "memberCeilingReader" {
				wired = true
			}
		}
		return true
	})
	if !wired {
		t.Fatal("main.go does not give the HLS producer the member ceiling reader")
	}
}
