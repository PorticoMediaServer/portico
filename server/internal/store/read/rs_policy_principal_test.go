package read

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

const rsTestPrincipalPolicy readPolicyID = 64980

type rsTestPrincipalState struct {
	authority *governor.Authority
	lease     *governor.PreparationLease
}

func (*rsTestPrincipalState) readPolicyState() {}
func init() {
	registerReadPolicy(readPolicyRecipe{id: rsTestPrincipalPolicy, domain: "test-principal-purpose", kinds: []int64{1}, stateful: true, scope: readPrincipalScope, validate: func(_ browseQuery, in BrowseInput, _ browseFence) (string, error) {
		p, ok := in.policy.state.(*rsTestPrincipalState)
		if !ok || p == nil || p.authority == nil || p.lease == nil {
			return "", ErrInput
		}
		return "test-principal", p.authority.CheckPreparation(p.lease)
	}})
}
func TestRSPolicyClosedPrincipalScope(t *testing.T) {
	cfg := readfixture.LiveConfig(time.Now)
	cfg.Preparation = &governor.PreparationProfile{MaxActive: 4, MaxBytes: 64 << 20, MaxJobBytes: 16 << 20, ProtectedBytes: 16 << 20}
	for n := range cfg.Partitions {
		cfg.Partitions[n][governor.MemoryBytes] = 128 << 20
	}
	f := composeReadFixture(t, newBrowseSource(t), browseDefinitions(), cfg, true)
	decision := f.authority.BeginPreparation(t.Context(), governor.PreparationRequest{Key: governor.JobKey{Domain: 74, ID: "test-principal-purpose"}, Owner: 1, ReservedBytes: 8192, Deadline: time.Now().Add(12 * time.Second)})
	if decision.Refusal != nil {
		t.Fatal(decision.Refusal)
	}
	defer func() {
		_ = f.authority.CancelPreparation(decision.Lease)
		if e := f.authority.RetirePreparation(decision.Lease); e != nil {
			t.Error(e)
		}
	}()
	in := f.input
	in.Library = 0
	in.policy = readPolicyBinding{recipe: rsTestPrincipalPolicy, state: &rsTestPrincipalState{f.authority, decision.Lease}}
	read := func(in BrowseInput, sources ...browseSnapshots) (bool, error) {
		var snapshots browseSnapshots = f.snapshots
		if len(sources) != 0 {
			snapshots = sources[0]
		}
		p, _, e := publishRead(t.Context(), snapshots, foregroundRequest(), time.Second, f.output, in, []readStep[bool]{func(_ context.Context, _ browseQuery, scope browseFence, result *bool) error {
			if scope.libraryID != 0 || scope.library != "" || scope.account != "a" || scope.profile != "p" {
				t.Fatal("principal scope invented library authority", scope.libraryID, scope.library)
			}
			*result = true
			return nil
		}})
		return p, e
	}
	f.exec(t, `UPDATE direct_profiles SET allowed_libraries='[]' WHERE id='p'`)
	if ok, e := read(in); e != nil || !ok {
		t.Fatal("global purpose needs a library", ok, e)
	}
	for _, v := range []BrowseInput{func() BrowseInput { x := in; x.Library = 1; return x }(), func() BrowseInput { x := in; x.policy.state = nil; return x }(), {Database: in.Database, TokenHash: in.TokenHash, Kind: 1, Limit: 1, Now: in.Now}} {
		if ok, e := read(v); !errors.Is(e, ErrInput) || ok {
			t.Fatal("unsealed/wrong scope accepted", ok, e)
		}
	}
	if ok, e := read(in, &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() { f.exec(t, `UPDATE authorization_session_families SET revoked=1`) }}); !errors.Is(e, ErrDenied) || ok {
		t.Fatal("global purpose skipped fresh final principal", ok, e)
	}
	if ok, e := read(in); !errors.Is(e, ErrDenied) || ok {
		t.Fatal("global purpose skipped current principal", ok, e)
	}
}

func TestRSPolicyStateBindingIsClosed(t *testing.T) {
	state := &rsTestPrincipalState{}
	for _, in := range []BrowseInput{
		{Kind: 1, policy: readPolicyBinding{state: state}},
		{Kind: 1, policy: readPolicyBinding{recipe: rsTestPrincipalPolicy}},
	} {
		if _, e := resolveReadPolicy(in); !errors.Is(e, ErrInput) {
			t.Fatal("state escaped its registered purpose", e)
		}
	}
}
