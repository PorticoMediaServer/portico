package writer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// A typed command/protocol result with a caller lifetime is a release boundary,
// even if it only delegates to another guarded entry. Adding a success return
// cannot silently opt out. Pure unimplemented stubs are the only exception.
func unguardedCommandFinishes(file *ast.File) []string {
	var bad []string
	resultTypes := map[string]bool{"ReclaimResult": true, "ReclaimSnapshot": true, "ReclaimCommand": true, "wcReclaimLookup": true, "wcReclaimSnapshot": true, "wcReclaimApplied": true, "SecurityResult": true, "SecuritySnapshot": true, "SecurityCommand": true, "wcSecurityReceiptLookup": true, "ProgressResult": true, "ProgressSnapshot": true, "ProgressCommand": true, "wcProgressLookup": true, "commandOutput": true, "SearchImageStageResult": true, "InventoryResult": true, "EffectiveStageResult": true, "AckResult": true, "EffectiveStageBeginResult": true, "inventoryReceiptLookup": true, "publisherReceiptLookup": true, "publisherInvocation": true, "scanLifecycleReceiptLookup": true, "scanLifecycleCommandResult": true, "scanSourceFacts": true, "searchFragmentStageResult": true, "ScanRunSnapshot": true, "ScanRunOperation": true, "ScanRunResult": true, "ScanRunLease": true}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Type.Results == nil {
			continue
		}
		hasContext := false
		for _, p := range fn.Type.Params.List {
			if typ, ok := p.Type.(*ast.SelectorExpr); ok {
				if pkg, ok := typ.X.(*ast.Ident); ok && pkg.Name == "context" && typ.Sel.Name == "Context" {
					hasContext = true
				}
			}
		}
		if !hasContext {
			continue
		}
		needs := fn.Name.Name == "disclose" || strings.HasPrefix(fn.Name.Name, "checkInventory") || fn.Name.Name == "requireInventoryMemory" || fn.Name.Name == "inventoryTimeMS" || fn.Name.Name == "searchImageEngine"
		needs = needs || fn.Name.Name == "scanCreateTimeMS" || fn.Name.Name == "scanClaimTimeMS" || fn.Name.Name == "requireScanLifecycleMemory"
		needs = needs || fn.Name.Name == "applyInventoryCreate" || fn.Name.Name == "applyInventoryClaim" || fn.Name.Name == "persistScanRunLifecycleResult"
		for _, r := range fn.Type.Results.List {
			typ := r.Type
			if pointer, ok := typ.(*ast.StarExpr); ok {
				typ = pointer.X
			}
			if id, ok := typ.(*ast.Ident); ok && resultTypes[id.Name] {
				needs = true
			}
		}
		// Closed publisher and shared resource helpers release either a scalar
		// timestamp or error, so result-type discovery alone cannot cover them.
		needs = needs || strings.HasPrefix(fn.Name.Name, "checkSearchImage") || fn.Name.Name == "requireSearchImageMemory" ||
			fn.Name.Name == "searchImageTimeMS" || fn.Name.Name == "commandTimeMS" || fn.Name.Name == "requireCommandMemory"
		needs = needs || fn.Name.Name == "acknowledgeSearchPublisher" || fn.Name.Name == "searchImageStageFreshFacts"
		needs = needs || fn.Name.Name == "acknowledgeScanRunLifecycle" || fn.Name.Name == "composeScanLifecycleHistory"
		needs = needs || fn.Name.Name == "newWCReclaimDriver" || fn.Name.Name == "composeWCReclaim" || fn.Name.Name == "checkWCReclaimNativeDisclosure" || fn.Name.Name == "newWCSecurityDriver" || fn.Name.Name == "composeWCSecurity" || fn.Name.Name == "checkWCSecurityNativeDisclosure" || fn.Name.Name == "newWCProgressDriver" || fn.Name.Name == "composeWCProgress" || fn.Name.Name == "initializeWCProgressFixture" || fn.Name.Name == "checkWCProgressNativeDisclosure"
		needs = needs || fn.Name.Name == "newScanDriver" || fn.Name.Name == "keepPreparation" || fn.Name.Name == "prepareWork" || fn.Name.Name == "allocateBirth" || fn.Name.Name == "prepareBirth" || fn.Name.Name == "checkBirthAbsence" || fn.Name.Name == "excludeOldWork"
		needs = needs || fn.Name.Name == "ObserveInventory" || fn.Name.Name == "newScanPreparation"
		needs = needs || fn.Name.Name == "searchFragmentStageFreshFacts"
		needs = needs || fn.Name.Name == "PrepareCreate" || fn.Name.Name == "PrepareClaim" || fn.Name.Name == "Lease" || fn.Name.Name == "retainSourcePreparation" || fn.Name.Name == "retainIssuerPreparation"
		needs = needs || fn.Name.Name == "configureScanIssuer" || fn.Name.Name == "composeScanLifecycleProfiles" || fn.Name.Name == "excludePredecessor" || fn.Name.Name == "checkScanSourceActive"
		if !needs {
			continue
		}
		if len(fn.Body.List) == 1 {
			if ret, ok := fn.Body.List[0].(*ast.ReturnStmt); ok && len(ret.Results) == 2 {
				zero, zok := ret.Results[0].(*ast.CompositeLit)
				err, eok := ret.Results[1].(*ast.Ident)
				if zok && len(zero.Elts) == 0 && eok && err.Name == "ErrNotImplemented" {
					continue
				}
			}
		}
		guarded := false
		for _, s := range fn.Body.List {
			if d, ok := s.(*ast.DeferStmt); ok {
				guarded = guarded || guardedFinishDefer(fn, d)
			}
			// The deferred rule must be installed before ANY reachable return, not
			// merely somewhere in a later branch of the function.
			returns := false
			ast.Inspect(s, func(n ast.Node) bool {
				if _, ok := n.(*ast.FuncLit); ok {
					return false
				}
				if _, ok := n.(*ast.ReturnStmt); ok {
					returns = true
				}
				return true
			})
			if returns && !guarded {
				break
			}
		}
		if !guarded {
			bad = append(bad, fn.Name.Name)
		}
	}
	return bad
}

// Accept only the two straight-line shapes used by real releases: assignment
// directly to named results, or a finish pair immediately projected into ALL
// named results. Identifier-object identity distinguishes actual named returns
// from unrelated or same-spelled shadow variables.
func guardedFinishDefer(fn *ast.FuncDecl, deferred *ast.DeferStmt) bool {
	if fn.Type.Results == nil || len(fn.Type.Results.List) == 0 {
		return false
	}
	literal, ok := deferred.Call.Fun.(*ast.FuncLit)
	if !ok || len(deferred.Call.Args) != 0 || literal.Type.Params.NumFields() != 0 || len(literal.Body.List) == 0 {
		return false
	}
	var named []*ast.Ident
	for _, field := range fn.Type.Results.List {
		if len(field.Names) == 0 {
			return false
		}
		for _, id := range field.Names {
			if id.Name == "_" || id.Obj == nil {
				return false
			}
			named = append(named, id)
		}
	}
	lastType, ok := fn.Type.Results.List[len(fn.Type.Results.List)-1].Type.(*ast.Ident)
	if !ok || lastType.Name != "error" || len(named) == 0 {
		return false
	}
	// An early return inside the deferred closure would skip the finish.
	earlyReturn := false
	for _, stmt := range literal.Body.List {
		ast.Inspect(stmt, func(n ast.Node) bool {
			if _, ok := n.(*ast.FuncLit); ok {
				return false
			}
			if _, ok := n.(*ast.ReturnStmt); ok {
				earlyReturn = true
			}
			return true
		})
	}
	if earlyReturn {
		return false
	}
	last, ok := literal.Body.List[len(literal.Body.List)-1].(*ast.AssignStmt)
	if !ok || last.Tok != token.ASSIGN {
		return false
	}
	if call := finishAssignment(last); call != nil {
		// Error-only native scope checks discard ONLY a literal empty struct, not
		// a payload. Their sole named result must receive the finish error.
		if len(named) == 1 {
			blank, ok := last.Lhs[0].(*ast.Ident)
			if !ok || blank.Name != "_" || resultObject(last.Lhs[1]) != named[0].Obj {
				return false
			}
			zero, ok := call.Args[2].(*ast.CompositeLit)
			if !ok || len(zero.Elts) != 0 {
				return false
			}
			shape, ok := zero.Type.(*ast.StructType)
			return ok && shape.Fields.NumFields() == 0
		}
		return namedFinishTargets(last.Lhs, named)
	}
	if len(named) < 2 || len(literal.Body.List) < 2 || !namedFinishTargets(last.Lhs, named) || len(last.Rhs) != len(named) {
		return false
	}
	pair, ok := literal.Body.List[len(literal.Body.List)-2].(*ast.AssignStmt)
	if !ok || finishAssignment(pair) == nil {
		return false
	}
	value, valueOK := pair.Lhs[0].(*ast.Ident)
	failure, failureOK := pair.Lhs[1].(*ast.Ident)
	if !valueOK || !failureOK || value.Name == "_" || failure.Name == "_" || value.Obj == nil || failure.Obj == nil || value.Obj == failure.Obj {
		return false
	}
	for i, expr := range last.Rhs {
		want := value.Obj
		if i == len(named)-1 {
			want = failure.Obj
		}
		if resultObject(expr) != want {
			return false
		}
	}
	return true
}

func finishAssignment(assign *ast.AssignStmt) *ast.CallExpr {
	if len(assign.Lhs) != 2 || len(assign.Rhs) != 1 {
		return nil
	}
	call, ok := assign.Rhs[0].(*ast.CallExpr)
	if !ok || len(call.Args) != 4 {
		return nil
	}
	name, ok := call.Fun.(*ast.Ident)
	if !ok || name.Name != "finishCommand" {
		return nil
	}
	return call
}

func namedFinishTargets(targets []ast.Expr, named []*ast.Ident) bool {
	if len(targets) != len(named) {
		return false
	}
	for i, target := range targets {
		id, ok := target.(*ast.Ident)
		if !ok || id.Name == "_" || id.Obj != named[i].Obj {
			return false
		}
	}
	return true
}

func resultObject(expr ast.Expr) *ast.Object {
	switch node := expr.(type) {
	case *ast.Ident:
		if node.Name != "_" {
			return node.Obj
		}
	case *ast.SelectorExpr:
		return resultObject(node.X)
	case *ast.ParenExpr:
		return resultObject(node.X)
	}
	return nil
}

func TestEveryCommandReleaseUsesSharedFinish(t *testing.T) {
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
		if bad := unguardedCommandFinishes(file); len(bad) > 0 {
			t.Errorf("%s unguarded releases: %v", name, bad)
		}
	}
	// The physical inventory is deliberate: new source files must participate,
	// not just a closed list of today's dispatchers.
	if len(files) == 0 {
		t.Fatal("empty physical source inventory")
	}
}
func TestCommandReleaseGuardRejectsBypasses(t *testing.T) {
	for _, body := range []string{
		`func newWCProgressDriver(ctx context.Context)(out *WCProgressDriver,err error){return out,nil}`,
		`func progressBypass(ctx context.Context)(out ProgressResult,err error){return out,nil}`,
		`func snapshotBypass(ctx context.Context)(out ProgressSnapshot,err error){return out,nil}`,
		`func commandBypass(ctx context.Context)(out ProgressCommand,err error){return out,nil}`,
		`func newScanDriver(ctx context.Context)(out *ScanDriver,err error){return out,nil}`,
		`func keepPreparation(ctx context.Context)(err error){return nil}`,
		`func prepareWork(ctx context.Context)(out *scanDriverWork,err error){return out,nil}`,
		`func allocateBirth(ctx context.Context)(out *scanBirth,err error){return out,nil}`,
		`func prepareBirth(ctx context.Context)(out *inventoryCreateCommand,err error){return out,nil}`,
		`func checkBirthAbsence(ctx context.Context)(err error){return nil}`,
		`func excludeOldWork(ctx context.Context)(err error){return nil}`,
		`func newScanPreparation(ctx context.Context)(out *scanPreparation,err error){return &scanPreparation{},nil}`,
		`func ObserveInventory(ctx context.Context)(value nativeinventory.Page,err error){return nativeinventory.Page{},nil}`,
		`func fragmentRelease(ctx context.Context)(out searchFragmentStageResult,err error){return searchFragmentStageResult{},nil}`,
		`func searchFragmentStageFreshFacts(ctx context.Context,turn *commandTurn,want searchFragmentStageSnapshot)(out searchFragmentStageSnapshot,err error){return want,nil}`,
		`func lookupScanRunLifecycle(ctx context.Context)(value scanLifecycleReceiptLookup,err error){return scanLifecycleReceiptLookup{},nil}`,
		`func captureScanSource(ctx context.Context)(value scanSourceFacts,err error){return scanSourceFacts{},nil}`,
		`func PrepareCreate(ctx context.Context)(value *ScanRunOperation,err error){return &ScanRunOperation{},nil}`,
		`func arbitraryNewEntry(ctx context.Context)(value *ScanRunResult,err error){return &ScanRunResult{},nil}`,
		`func Snapshot(ctx context.Context)(value ScanRunSnapshot,err error){return ScanRunSnapshot{},nil}`,
		`func acknowledgeScanRunLifecycle(ctx context.Context)(value int64,err error){return 1,nil}`,
		`func composeScanLifecycleHistory(ctx context.Context)(err error){return nil}`,
		`func runScanLifecycleHistory(ctx context.Context)(value scanLifecycleCommandResult,ack AckResult,err error){return value,ack,nil}`,
		`func scanCreateTimeMS(ctx context.Context)(value int64,err error){return 1,nil}`,
		`func scanClaimTimeMS(ctx context.Context)(value int64,err error){return 1,nil}`,
		`func requireScanLifecycleMemory(ctx context.Context)(err error){return nil}`,
		`func applyInventoryCreate(ctx context.Context)(value scanRunLifecycleResult,err error){return value,nil}`,
		`func applyInventoryClaim(ctx context.Context)(value scanRunLifecycleResult,err error){return value,nil}`,
		`func persistScanRunLifecycleResult(ctx context.Context)(value scanRunLifecycleResult,err error){return value,nil}`,
		`func searchImageEngine(ctx context.Context)(out searchpolicy.Engine,err error){return searchpolicy.Engine{Version:"saved"},nil}`,
		`func acknowledgeSearchPublisher(ctx context.Context,turn *commandTurn,q PublisherReceiptRequest,lookup publisherReceiptLookup)(out int64,err error){return 1,nil}`,
		`func searchImageStageFreshFacts(ctx context.Context,turn *commandTurn,want searchImageStageRequest)(out searchImageStageRequest,err error){return want,nil}`,
		`func requireInventoryMemory(ctx context.Context)(err error){return nil}`,
		`func inventoryTimeMS(ctx context.Context)(value int64,err error){return 1,nil}`,
		`func bypass(ctx context.Context)(out InventoryResult,err error){var v InventoryResult;var e error;saved:=InventoryResult{RunID:"saved"};defer func(){v,e=finishCommand(ctx,ctx,out,err);out,err=saved,e}();_=v;return saved,ErrInvalid}`,
		`func bypass(ctx context.Context)(out InventoryResult,err error){var v InventoryResult;var e error;defer func(){v,e=finishCommand(ctx,ctx,out,err);out,err=v,nil}();_=e;return InventoryResult{RunID:"saved"},ErrInvalid}`,
		`func checkInventoryScope(ctx context.Context)(err error){defer func(){_,err=finishCommand(ctx,ctx,InventoryResult{RunID:"saved"},err)}();return nil}`,
		`func bypass(ctx context.Context)(out InventoryResult,err error){defer func(){var out InventoryResult;out,err=finishCommand(ctx,ctx,out,err);_=out}();return InventoryResult{RunID:"saved"},ErrInvalid}`,
		`func bypass(ctx context.Context)(out InventoryResult,err error){var v struct{field,saved InventoryResult};var e error;defer func(){v.field,e=finishCommand(ctx,ctx,out,err);out,err=v.saved,e}();return InventoryResult{RunID:"saved"},ErrInvalid}`,
		`func bypass(ctx context.Context)(InventoryResult,error){defer func(){_,_=finishCommand(ctx,ctx,InventoryResult{},ErrInvalid)}();return InventoryResult{RunID:"saved"},ErrInvalid}`,
		`func bypass(ctx context.Context)(out InventoryResult,err error){defer func(){_,_=finishCommand(ctx,ctx,InventoryResult{},ErrInvalid)}();return InventoryResult{RunID:"saved"},ErrInvalid}`,
		`func bypass(ctx context.Context)(out InventoryResult,err error){defer func(){other,otherErr:=finishCommand(ctx,ctx,out,err);_,_=other,otherErr}();return InventoryResult{RunID:"saved"},ErrInvalid}`,
		`func bypass(ctx context.Context)(out InventoryResult,err error){defer func(){out,err:=finishCommand(ctx,ctx,out,err);_,_=out,err}();return InventoryResult{RunID:"saved"},ErrInvalid}`,
		`func bypass(ctx context.Context)(out InventoryResult,err error){defer func(){v,e:=finishCommand(ctx,ctx,out,err);out,err=v,nil;_=e}();return InventoryResult{RunID:"saved"},ErrInvalid}`,
		`func bypass(ctx context.Context)(InventoryResult,error){return InventoryResult{},nil}`,
		`func bypass(ctx context.Context)(InventoryResult,error){if true{return InventoryResult{},nil};defer func(){finishCommand()}();return InventoryResult{},nil}`,
		`func bypass(ctx context.Context)(InventoryResult,error){defer func(){}();return InventoryResult{},nil}`,
		`func bypass(ctx context.Context)(InventoryResult,error){defer func(){finishCommand()}();return InventoryResult{},nil}`,
		`func searchImageTimeMS(ctx context.Context)(value int64,err error){return 1,nil}`,
		`func commandTimeMS(ctx context.Context)(value int64,err error){return 1,nil}`,
		`func requireCommandMemory(ctx context.Context)(err error){return nil}`,
		`func requireSearchImageMemory(ctx context.Context)(err error){return nil}`,
		`func checkSearchImageTerminal(ctx context.Context)(err error){return nil}`,
	} {
		file, err := parser.ParseFile(token.NewFileSet(), "control.go", "package writer\n"+body, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(unguardedCommandFinishes(file)) != 1 {
			t.Fatal("guard accepted bypass", body)
		}
	}
}

func TestCommandReleaseGuardAcceptsNamedPublication(t *testing.T) {
	for _, body := range []string{
		`func release(ctx context.Context)(out InventoryResult,err error){defer func(){out,err=finishCommand(ctx,ctx,out,err)}();n,err:=work();_=n;return InventoryResult{},err}`,
		`func release(ctx context.Context)(out InventoryResult,err error){defer func(){out,err=finishCommand(ctx,ctx,out,err)}();return InventoryResult{},nil}`,
		`func release(ctx context.Context)(out InventoryResult,ack AckResult,err error){defer func(){v,e:=finishCommand(ctx,ctx,packet{out,ack},err);out,ack,err=v.output,v.ack,e}();return InventoryResult{},AckResult{},nil}`,
		`func checkInventoryScope(ctx context.Context)(err error){defer func(){_,err=finishCommand(ctx,ctx,struct{}{},err)}();return nil}`,
	} {
		file, err := parser.ParseFile(token.NewFileSet(), "positive.go", "package writer\n"+body, 0)
		if err != nil {
			t.Fatal(err)
		}
		if bad := unguardedCommandFinishes(file); len(bad) != 0 {
			t.Fatal("valid named publication refused", body, bad)
		}
	}
}
