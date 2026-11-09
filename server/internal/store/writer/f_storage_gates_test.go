package writer

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/mutationreceipt"
)

// These private participants may merge as storage preparation. Wiring an outer
// caller requires closing its named OPEN-GATES.md owner task and changing this
// census in the same reviewed composition. Function-value references count too.
func TestFStorageGatePrivateCallerCensus(t *testing.T) {
	want := map[string]map[string]int{
		"holdMPReadingPublication": {}, "mpReadingIntent": {},
		"checkUOPublicReadingReceipt": {}, "saveUOPublicReadingReceipt": {},
		"enterIARetirementUnit": {}, "retireIAMediaParentUnit": {},
		"installMPExternalGuidePin": {}, "releaseMPExternalGuidePin": {},
		"authorizeMPSessionStop": {}, "authorizeMPSessionExpiry": {}, "applyMPSessionStop": {},
		"mergeUOReadingResume": {}, "drainUSReadingPersonal": {}, "pruneUSReadingProfileReceipts": {},
		"pruneUOReadingReceipts": {"us_reading_erasure.go": 1},
		"drainUOReadingResume":   {"us_reading_erasure.go": 1},
	}
	got := make(map[string]map[string]int)
	declared := make(map[string]bool)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		definitions := make(map[*ast.Ident]bool)
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				definitions[fn.Name] = true
				if _, tracked := want[fn.Name.Name]; tracked {
					declared[fn.Name.Name] = true
				}
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			id, ok := node.(*ast.Ident)
			if !ok || definitions[id] {
				return true
			}
			if _, tracked := want[id.Name]; tracked {
				if got[id.Name] == nil {
					got[id.Name] = make(map[string]int)
				}
				got[id.Name][name]++
			}
			return true
		})
	}
	for symbol, expected := range want {
		if !declared[symbol] {
			t.Errorf("missing gated participant %s", symbol)
		}
		if got[symbol] == nil {
			got[symbol] = map[string]int{}
		}
		if !reflect.DeepEqual(got[symbol], expected) {
			t.Errorf("%s non-test references %v, want %v; close the owner gate before wiring", symbol, got[symbol], expected)
		}
	}
}

// Nil turn/authority inputs must refuse at each private participant boundary.
// This is a nil-input control, not proof for an admitted but incomplete owner
// transaction; the non-test caller census above enforces the closed adapter.
func TestFStorageGateUncomposedRefuses(t *testing.T) {
	ctx := context.Background()
	checks := map[string]func() error{
		"MP-06 reading": func() error { _, e := mpReadingIntent(ctx, nil, mpReadingIntentInput{}); return e },
		"UO-02 check":   func() error { _, e := checkUOPublicReadingReceipt(ctx, nil, nil, uoPublicReadingRequest{}); return e },
		"UO-02 save": func() error {
			return saveUOPublicReadingReceipt(ctx, nil, nil, mutationreceipt.Result{}, uoPublicReadingResult{})
		},
		"IA-08/09 entry": func() error {
			_, e := enterIARetirementUnit(ctx, nil, iaRetirementUnitScope{}, nil, ctx, iaEntryBudget{})
			return e
		},
		"IA-08/09 effect": func() error { _, e := retireIAMediaParentUnit(ctx, nil, nil, iaRetirementMediaVictim{}); return e },
		"MP-15 install":   func() error { _, e := installMPExternalGuidePin(ctx, nil, mpGuidePinSelection{}); return e },
		"MP-15 release": func() error {
			_, e := releaseMPExternalGuidePin(ctx, nil, nil, nil, mpGuidePinOccurrence{}, mpPhysicalKey{})
			return e
		},
		"MP-14 stop":   func() error { _, e := authorizeMPSessionStop(ctx, nil, wcProgressScope{}, ctx); return e },
		"MP-14 expiry": func() error { _, e := authorizeMPSessionExpiry(ctx, nil, nil); return e },
		"MP-14 effect": func() error { _, e := applyMPSessionStop(ctx, nil, nil, mpSessionStopInput{}); return e },
		"F-P03 prune":  func() error { _, e := pruneUOReadingReceipts(ctx, nil, uoReadingPruneScope{}); return e },
		"F-P03 resume": func() error { _, e := drainUOReadingResume(ctx, nil, uoReadingPersonalScope{}); return e },
		"F-P03 merge": func() error {
			_, e := mergeUOReadingResume(ctx, nil, uoReadingPersonalScope{}, uoReadingPersonalScope{})
			return e
		},
		"US-21 personal": func() error { _, e := drainUSReadingPersonal(ctx, nil, uoReadingPersonalScope{}); return e },
		"US-26 prune":    func() error { _, e := pruneUSReadingProfileReceipts(ctx, nil, uoReadingPruneScope{}); return e },
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			if err := check(); err == nil {
				t.Fatal("uncomposed participant accepted")
			}
		})
	}
}
