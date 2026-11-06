package writer

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
)

const firstLightSourceLabel = "fixture source, NOT production seal proof"
const firstLightFixtureVersion = "WCanonicalTestOnlyEffectiveStageBegin-v1"

type effectiveFirstLightFixture struct {
	writer     *Writer
	disclosure *EnrichmentDisclosure
	command    *effectiveStageBeginCommand
	executor   *executor
	authority  *governor.Authority
	path       string
}

// This constructor exists ONLY in a _test.go file. It accepts no existing file,
// caller SQL, resource profile, grant, authority, seal or production capability.
// The target supplies time.Now; correctness controls may use a virtual clock.
func newEffectiveFirstLightFixture(t *testing.T, clock func() time.Time) *effectiveFirstLightFixture {
	t.Helper()
	w, _, input, identity := effectiveBeginWholeFixture(t)
	// Remove synthetic component history. This fresh source starts at0/0 and
	// original sequence1; no invented prior Applied result is used for admission.
	if _, err := w.db.ExecContext(t.Context(), "DELETE FROM enrichment_command_receipts"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), "UPDATE metadata_provider_operations SET last_command_sequence=0,acknowledged_command_sequence=0 WHERE id=?", input.ownerID); err != nil {
		t.Fatal(err)
	}
	input.owner.lastSequence, input.owner.acknowledgedSequence = 0, 0
	identity.Sequence = 1
	command, err := newEffectiveStageBeginCommand(identity, input)
	if err != nil {
		t.Fatal(err)
	}
	var number int64
	var name, path string
	if err := w.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&number, &name, &path); err != nil {
		t.Fatal(err)
	}
	// Finish setup and close its WAL. Reopen only this newly created, owned file
	// through the shared policy; a read-only startup step creates zero-frame WAL.
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	w.openPool(connector, nil)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer conn.Close()
		if _, err := sqliteconn.Assert(t.Context(), conn, w.config); err != nil {
			t.Fatal(err)
		}
		if err := schema.AssertExisting(t.Context(), conn); err != nil {
			t.Fatal(err)
		}
		var objects int64
		if err := conn.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_schema").Scan(&objects); err != nil || objects == 0 {
			t.Fatal(objects, err)
		}
	}()
	state, err := governor.ReadWAL(path)
	if err != nil || state.Frames != 0 || state.Backfill != 0 || state.ActiveBytes != 0 {
		t.Fatal("fixture is not fresh zero-WAL", state, err)
	}
	w.now = clock
	facade, disclosure, command, _, authority := bindEffectiveFixtureRuntime(t, w, command)
	// The only fixture-permit mint is test-only and binds the complete ORIGINAL
	// request. Flags/digests in a public packet never create this capability.
	disclosure.scope.fixture = &effectiveFixturePermit{issuer: facade.effective, reference: ReceiptReference{command.identity, command.digest}}
	return &effectiveFirstLightFixture{facade, disclosure, command, w, authority, path}
}

func closeEffectiveFirstLightFixture(f *effectiveFirstLightFixture) (bool, error) {
	err := f.executor.close()
	f.executor.physicalFaults.mu.Lock()
	failed := f.executor.physicalFaults.failed
	f.executor.physicalFaults.mu.Unlock()
	stats := f.executor.db.Stats()
	return err == nil && !failed && stats.InUse == 0 && stats.OpenConnections == 0 && f.authority.Usage() == ([4]governor.Amounts{}), err
}

func TestEffectiveFirstLightFixtureUsesRealEffects(t *testing.T) {
	clock := time.Now().Add(time.Hour)
	f := newEffectiveFirstLightFixture(t, func() time.Time { return clock })
	r := effectiveReference(f.command)
	var images int64
	if err := f.executor.db.QueryRowContext(t.Context(), "SELECT count(*) FROM catalog_credit_images").Scan(&images); err != nil || images != 0 {
		t.Fatal("factory wrote BEGIN effects", images, err)
	}
	// The same typed input still refuses if its test-only source permit is absent.
	permit := f.disclosure.scope.fixture
	f.disclosure.scope.fixture = nil
	if _, err := f.writer.StageEffective(t.Context(), f.disclosure, f.command); err != nil {
		var refused *Refusal
		if !errors.As(err, &refused) || refused.Code != RefusalUnqualified {
			t.Fatal("unexpected production source refusal", err)
		}
	} else {
		t.Fatal("production source gate opened")
	}
	f.disclosure.scope.fixture = permit
	changed := f.command.request
	changed.owner.afterItem += "-different"
	other, err := newEffectiveStageBeginCommand(f.command.identity, changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.writer.StageEffective(t.Context(), f.disclosure, other); err != nil {
		var refused *Refusal
		if !errors.As(err, &refused) || refused.Code != RefusalUnqualified {
			t.Fatal("fixture permit was not bound to original full request", err)
		}
	} else {
		t.Fatal("fixture permit lent to a different request")
	}
	result, err := f.writer.StageEffective(t.Context(), f.disclosure, f.command)
	if err != nil || result.Outcome.State != Applied || result.Outcome.Replayed || result.Value.ImageID <= 0 || result.Outcome.Evidence == nil || result.Outcome.Evidence.Commit != CommitSucceeded {
		t.Fatal(result, err)
	}
	if err := f.executor.db.QueryRowContext(t.Context(), "SELECT count(*) FROM catalog_credit_images WHERE id=? AND sealed=0 AND reclaim_state='held'", result.Value.ImageID).Scan(&images); err != nil || images != 1 {
		t.Fatal(images, err)
	}
	resolved, err := f.writer.ResolveEffective(t.Context(), f.disclosure, r)
	if err != nil || resolved.Outcome.State != Applied || !resolved.Outcome.Replayed || resolved.Value != result.Value {
		t.Fatal(resolved, err)
	}
	ack, err := f.writer.AckEffective(t.Context(), f.disclosure, r)
	if err != nil || ack.AcknowledgedThrough != 1 {
		t.Fatal(ack, err)
	}
	if f.authority.Usage()[governor.Background] != (governor.Amounts{}) || f.executor.db.Stats().InUse != 0 {
		t.Fatal("fixture stranded physical custody")
	}
}

// This is an accidental-escape guard, alongside Go's private fields and the
// compiler's exclusion of _test.go from production. Unsafe/linkname/cgo/assembly
// adversarial evasions are outside its scope, as with the shared opener guard.
func firstLightProductionSource(source []byte) error {
	file, err := parser.ParseFile(token.NewFileSet(), "production.go", source, 0)
	if err != nil {
		return err
	}
	allowed := map[token.Pos]bool{}
	for _, decl := range file.Decls {
		group, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range group.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if typeSpec.Name.Name == "effectiveFixturePermit" {
				allowed[typeSpec.Name.Pos()] = true
			}
			if typeSpec.Name.Name != "enrichmentScope" {
				continue
			}
			shape, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range shape.Fields.List {
				pointer, ok := field.Type.(*ast.StarExpr)
				if !ok {
					continue
				}
				id, ok := pointer.X.(*ast.Ident)
				if ok && id.Name == "effectiveFixturePermit" && len(field.Names) == 1 && field.Names[0].Name == "fixture" {
					allowed[id.Pos()] = true
				}
			}
		}
	}
	var violation error
	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.Ident:
			if value.Name == "effectiveFixturePermit" && !allowed[value.Pos()] {
				violation = fmt.Errorf("production fixture permit reference")
				return false
			}
		case *ast.AssignStmt:
			for _, left := range value.Lhs {
				if field, ok := left.(*ast.SelectorExpr); ok && field.Sel.Name == "fixture" {
					violation = fmt.Errorf("production fixture permit assignment")
				}
			}
		case *ast.KeyValueExpr:
			if key, ok := value.Key.(*ast.Ident); ok && key.Name == "fixture" {
				violation = fmt.Errorf("production fixture permit field initialization")
			}
		}
		return violation == nil
	})
	return violation
}

func TestEffectiveFirstLightCannotMintInProduction(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := firstLightProductionSource(source); err != nil {
			t.Fatal(name, err)
		}
	}
	for _, source := range []string{
		"package writer; func mint(){ _ = &effectiveFixturePermit{} }",
		"package writer; func mint(){ _ = new(effectiveFixturePermit) }",
		"package writer; func mint(){ var p effectiveFixturePermit; _ = p }",
		"package writer; func mint(){ scope.fixture = permit }",
		"package writer; var p = enrichmentScope{fixture:permit}",
	} {
		if err := firstLightProductionSource([]byte(source)); err == nil {
			t.Fatal("production mint passed guard", source)
		}
	}
}
