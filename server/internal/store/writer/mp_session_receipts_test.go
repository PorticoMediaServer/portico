package writer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func TestMPSessionDomainDraftWorkerPayloadAdmission(t *testing.T) {
	h := mpPrepReceiptHeader{identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, request: RequestDigest{3}, kind: MPSessionReconcile, appliedMS: 100, resultDigest: ResultDigest{4}}
	o := mpMediaWorkerBefore{incarnation: h.identity.Incarnation, revision: 1}
	v := mpWMediaPayload{header: h, version: 1, count: 1, payload: []byte("original W payload")}
	a := mpMediaWorkerAdmission{maxRows: 1, maxBytes: 1000, reclaimDueMS: 200}
	n, err := o.admits("owner", v, a)
	if err != nil || n != int64(5+96+len(v.payload)) {
		t.Fatal(n, err)
	}
	bad := o
	bad.ack = 1
	if _, err = bad.admits("owner", v, a); err == nil {
		t.Fatal("borrowed sequence")
	}
	bad = o
	bad.variableBytes = 1
	if _, err = bad.admits("owner", v, a); err == nil {
		t.Fatal("inconsistent debt")
	}
	a.maxBytes = n - 1
	if _, err = o.admits("owner", v, a); err == nil {
		t.Fatal("unadmitted native bytes")
	}
	a.maxBytes = 100000
	v.payload = make([]byte, 65537)
	if _, err = o.admits("owner", v, a); err == nil {
		t.Fatal("oversize W payload")
	}
}
func TestMPSessionDomainDraftWorkerPayloadCatalogArity(t *testing.T) {
	defs := map[string]stmt.Definition{}
	for _, group := range [][]stmt.Definition{mpSessionWorkerPayloadDefinitions(), mpPhysicalWorkerPayloadDefinitions()} {
		if _, err := stmt.Describe(group); err != nil {
			t.Fatal(err)
		}
		for _, d := range group {
			if strings.Count(d.SQL, "?") != len(d.Params) {
				t.Fatal(d.ID, "arity")
			}
			defs[string(d.ID)] = d
		}
	}
	for _, file := range []string{"mp_session_receipts.go", "mp_physical_receipts.go"} {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(token.NewFileSet(), file, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			fn, ok := c.Fun.(*ast.Ident)
			if !ok || (fn.Name != "mpPrepRow" && fn.Name != "mpPrepOne") {
				return true
			}
			literal, ok := c.Args[2].(*ast.BasicLit)
			if !ok {
				t.Fatal("dynamic statement owner")
			}
			id, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatal(err)
			}
			d, ok := defs[id]
			if !ok || len(c.Args)-3 != len(d.Params) {
				t.Error(file, id, "call/native arity")
			}
			return true
		})
	}
	for _, kind := range []CommandID{MPStartPrepAccept, MPSessionCreate, mpCoreQueueAdvancePurpose, mpCoreNextCommitPurpose} {
		if _, err := storeMPSessionWorkerPayload(t.Context(), nil, "owner", mpMediaWorkerBefore{}, mpMediaWorkerAdmission{}, mpWMediaPayload{header: mpPrepReceiptHeader{kind: kind}}); err == nil {
			t.Fatal("nested birth got independent worker receipt", kind)
		}
	}
}
