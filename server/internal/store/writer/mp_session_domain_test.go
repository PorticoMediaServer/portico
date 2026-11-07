package writer

import (
	"context"
	"crypto/sha256"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func mpSessionDomainCatalog(t *testing.T) map[string]stmt.Definition {
	t.Helper()
	d := map[string]stmt.Definition{}
	for _, defs := range [][]stmt.Definition{mpSessionDomainDefinitions(), mpSourcePinsDefinitions(), mpHLSDomainDefinitions()} {
		if _, err := stmt.Describe(defs); err != nil {
			t.Fatal(err)
		}
		for _, v := range defs {
			if strings.Count(v.SQL, "?") != len(v.Params) {
				t.Errorf("%s: SQL placeholders=%d native params=%d", v.ID, strings.Count(v.SQL, "?"), len(v.Params))
			}
			if _, ok := d[string(v.ID)]; ok {
				t.Fatal("duplicate", v.ID)
			}
			d[string(v.ID)] = v
		}
	}
	return d
}
func TestMPSessionDomainDraftCatalog(t *testing.T) { mpSessionDomainCatalog(t) }
func TestMPSessionDomainDraftNativeArity(t *testing.T) {
	defs := mpSessionDomainCatalog(t)
	for _, pattern := range []string{"mp_session_domain*.go", "mp_source_pins*.go", "mp_hls_domain*.go"} {
		files, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range files {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, name, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				c, ok := n.(*ast.CallExpr)
				if !ok || c.Ellipsis.IsValid() {
					return true
				}
				fn, ok := c.Fun.(*ast.Ident)
				if !ok {
					return true
				}
				extra := 3
				switch fn.Name {
				case "mpPrepOne", "mpPrepRow":
				case "mpHLSWriteCount":
					extra = 4
				default:
					return true
				}
				if len(c.Args) < extra {
					t.Error("short SQL call")
					return true
				}
				id, ok := c.Args[2].(*ast.BasicLit)
				if !ok || id.Kind != token.STRING {
					return true
				}
				key, err := strconv.Unquote(id.Value)
				if err != nil {
					t.Fatal(err)
				}
				d, ok := defs[key]
				if !ok {
					t.Errorf("%s unknown native point %s", fset.Position(c.Pos()), key)
					return true
				}
				if got := len(c.Args) - extra; got != len(d.Params) {
					t.Errorf("%s %s: call=%d native=%d", fset.Position(c.Pos()), key, got, len(d.Params))
				}
				return true
			})
		}
	}
}
func mpDomainCreateInput() mpSessionCreateInput {
	raw := "grant-token"
	digest := sha256.Sum256([]byte(raw))
	return mpSessionCreateInput{mediaGeneration: 3, session: "logical", media: "physical", execution: "execution", request: "request", startKey: "key", startDigest: strings.Repeat("a", 64), engine: "native", mode: "direct", presentationURL: "/v1/media/grant-token", grantHash: mpLowerHex(digest[:]), grantToken: raw, expiresAt: "1970-01-01T00:00:09Z", sessionIncarnation: [16]byte{1}, mediaIncarnation: [16]byte{2}, userRoot: strings.Repeat("u", 22), memberIncarnation: strings.Repeat("a", 32), profileLifetime: strings.Repeat("b", 32), item: 1, nowMS: 100, leaseMS: 8000, durationMS: 100000, actor: mpDraftActor(), source: mpDraftSource()}
}
func TestMPSessionDomainDraftInputAndScopedURL(t *testing.T) {
	q := mpDomainCreateInput()
	if !q.valid() {
		t.Fatal("valid create")
	}
	for _, mutate := range []func(*mpSessionCreateInput){func(q *mpSessionCreateInput) { q.grantHash = strings.Repeat("f", 64) }, func(q *mpSessionCreateInput) { q.leaseMS = 9500 }, func(q *mpSessionCreateInput) { q.mode = "arbitrary" }, func(q *mpSessionCreateInput) { q.positionMS = q.durationMS + 1 }, func(q *mpSessionCreateInput) { q.userRoot = "" }, func(q *mpSessionCreateInput) { q.presentationURL = "file:///media/movie.mkv" }} {
		bad := q
		mutate(&bad)
		if bad.valid() {
			t.Fatal("bad create admitted", bad)
		}
	}
	for _, s := range []string{"file:///secret", "https://user:pass@example.com/v1/media/token", "https://origin.example/film.mkv", "//origin.example/v1/media/token", "/v1/media/token#fragment"} {
		if mpPresentationURL(s) {
			t.Fatal("unsafe protocol projection", s)
		}
	}
	for _, s := range []string{"/v1/media/token", "/v1/media/token/master.m3u8", "https://server.example/v1/media/token"} {
		if !mpPresentationURL(s) {
			t.Fatal(s)
		}
	}
}
func TestMPSessionDomainDraftReplacementAndExitCommitments(t *testing.T) {
	q := mpSessionReplacementInput{next: mpDomainCreateInput()}
	d, err := mpSessionReplacementDigest(q)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*mpSessionReplacementInput){func(q *mpSessionReplacementInput) { q.next.source.configurationGeneration++ }, func(q *mpSessionReplacementInput) { q.next.execution = "next-execution" }, func(q *mpSessionReplacementInput) { q.next.queue.custody++ }, func(q *mpSessionReplacementInput) { q.next.mediaIncarnation[0]++ }} {
		v := q
		mutate(&v)
		next, e := mpSessionReplacementDigest(v)
		if e == nil && next == d {
			t.Fatal("unbound replacement")
		}
	}
	f := mpPhysicalFence{execution: "old", generation: 2, nowMS: 100}
	e := mpOccurrenceExit{retirementRequest: "retire", supervisorIncarnation: [16]byte{1}, exitedMS: 90, execution: "old", generation: 2, digest: strings.Repeat("f", 64), cursor: 4, nextCursor: 68, finished: true}
	if !e.valid(f) {
		t.Fatal("bounded exit")
	}
	e.nextCursor++
	if e.valid(f) {
		t.Fatal("unbounded exit")
	}
	e.nextCursor = 5
	e.execution = "new"
	if e.valid(f) {
		t.Fatal("borrowed execution exit")
	}
}
func TestMPSessionDomainDraftPurposeAndFinish(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	turn := &commandTurn{transaction: &transaction{ctx: ctx, descriptor: Descriptor{ID: MPSessionReady}}}
	out, err := applyMPSessionCreate(ctx, turn, mpDomainCreateInput())
	if !errors.Is(err, ErrInvalid) || !errors.Is(err, context.Canceled) || out != (mpSessionEffect{}) {
		t.Fatal(out, err)
	}
	turn.transaction.descriptor.ID = MPSourceInventoryPin
	hls, e := applyMPHLSReserve(ctx, turn, mpPhysicalFence{}, 0, 1)
	if !errors.Is(e, ErrInvalid) || !errors.Is(e, context.Canceled) || hls != (mpHLSEffect{}) {
		t.Fatal(hls, e)
	}
	turn.transaction.descriptor.ID = MPHLSReserve
	pin, e := applyMPSourceInventoryPin(ctx, turn, mpPhysicalFence{})
	if !errors.Is(e, ErrInvalid) || !errors.Is(e, context.Canceled) || pin != (mpSourcePinEffect{}) {
		t.Fatal(pin, e)
	}
}
func TestMPSessionDomainDraftEveryReleaseFinishes(t *testing.T) {
	types := map[string]bool{"mpPhysicalState": true, "mpSessionEffect": true, "mpSourcePinEffect": true, "mpHLSEffect": true, "mpSessionDeliveryProjection": true}
	for _, pattern := range []string{"mp_session_domain*.go", "mp_source_pins*.go", "mp_hls_domain*.go"} {
		paths, _ := filepath.Glob(pattern)
		for _, path := range paths {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil || fn.Type.Results == nil {
					continue
				}
				hasCtx := false
				for _, p := range fn.Type.Params.List {
					if s, ok := p.Type.(*ast.SelectorExpr); ok {
						if x, ok := s.X.(*ast.Ident); ok && x.Name == "context" && s.Sel.Name == "Context" {
							hasCtx = true
						}
					}
				}
				if !hasCtx {
					continue
				}
				need := false
				for _, p := range fn.Type.Results.List {
					if id, ok := p.Type.(*ast.Ident); ok && types[id.Name] {
						need = true
					}
				}
				if !need {
					continue
				}
				guarded := false
				for _, s := range fn.Body.List {
					d, ok := s.(*ast.DeferStmt)
					if !ok {
						continue
					}
					ast.Inspect(d, func(n ast.Node) bool {
						if c, ok := n.(*ast.CallExpr); ok {
							if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "finishCommand" {
								guarded = true
							}
						}
						return true
					})
				}
				if !guarded {
					t.Errorf("unguarded domain release %s:%s", path, fn.Name.Name)
				}
			}
		}
	}
}

func TestMPSessionDomainDraftClosedParentRecipes(t *testing.T) {
	for _, c := range []struct {
		id     CommandID
		recipe mpMediaRecipeKind
	}{{MPSessionCreate, mpRecipeStart}, {MPStartPrepAccept, mpRecipeAcceptedStart}, {MPSessionPresentation, mpRecipePresentation}, {mpCoreQueueAdvancePurpose, mpRecipeQueueAdvance}, {mpCoreNextCommitPurpose, mpRecipeNextCommit}} {
		turn := &commandTurn{transaction: &transaction{descriptor: Descriptor{ID: c.id}}}
		recipe, err := mpMediaParentRecipe(turn)
		if err != nil || recipe != c.recipe {
			t.Fatal(recipe, err)
		}
		for _, leaf := range []CommandID{MPSourcePreparedPin, MPSourceSubtitlePin, MPSourceDiscPin, MPSourceReceiverPin, MPSourceRemoteBind, MPHLSReserve, MPHLSPlan, MPDirectDelivery} {
			if err = mpMediaRecipePurpose(turn, leaf, recipe); err != nil {
				t.Fatal(c.id, leaf, err)
			}
			if err = mpMediaRecipePurpose(turn, leaf, mpRecipeStandalone); !errors.Is(err, ErrInvalid) {
				t.Fatal("borrowed standalone leaf", c.id, leaf, err)
			}
		}
		if err = mpMediaRecipePurpose(turn, MPSessionReady, recipe); !errors.Is(err, ErrInvalid) {
			t.Fatal("uncompiled recipe leaf", err)
		}
	}
	turn := &commandTurn{transaction: &transaction{descriptor: Descriptor{ID: MPSessionReady}}}
	if _, err := mpMediaParentRecipe(turn); !errors.Is(err, ErrInvalid) {
		t.Fatal("unknown parent admitted", err)
	}
}

func TestMPSessionDomainDraftHistoricalOriginMapping(t *testing.T) {
	for _, d := range mpSessionDomainDefinitions() {
		if strings.Contains(d.SQL, "mp_birth_id") || strings.Contains(d.SQL, "mp_start_birth_id") {
			t.Fatal("removed public birth graph", d.ID)
		}
		if d.ID == "mp.session.physical_insert" || d.ID == "mp.session.logical_insert" || d.ID == "mp.session.accepted_preparation" || d.ID == "mp.session.origin_binding" || d.ID == "mp.session.physical_origin_binding" {
			for _, field := range []string{"mp_user_root_id", "mp_owner_member_incarnation", "mp_owner_profile_lifetime"} {
				if !strings.Contains(d.SQL, field) {
					t.Fatal("missing original historical scope", d.ID, field)
				}
			}
		}
	}
	q := mpDomainCreateInput()
	for _, mutate := range []func(*mpSessionCreateInput){func(q *mpSessionCreateInput) { q.userRoot = "short" }, func(q *mpSessionCreateInput) { q.memberIncarnation = "invalid" }, func(q *mpSessionCreateInput) { q.profileLifetime = "" }} {
		bad := q
		mutate(&bad)
		if bad.valid() {
			t.Fatal("invalid historical origin accepted")
		}
	}
}
