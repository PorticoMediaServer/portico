package read

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

// BEGIN_RB_CHANNEL_JOURNAL
func newRBChannelSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	canonical := rbChannelAcceptedFixture(t, "movie")
	f.exec(t, `INSERT INTO lc_channels(id,revision,config_json,enabled,position,name,state) VALUES('channel',1,?,1,0,'Channel','building')`, string(canonical))
	f.exec(t, `INSERT INTO lc_generations(id,channel_id,config_revision,config_json,catalog_fence,seed,status,phase,base_generation,start_ms,end_ms,boundary_ms,cursor_ms,created_ms,command_incarnation,create_operation_id,birth_request_digest,entry_digest,verify_end_ms,verified_entry_digest) VALUES('generation','channel',1,?,'','seed','building','candidates','',0,10000,0,0,1000,x'01010101010101010101010101010101',x'02020202020202020202020202020202',zeroblob(32),zeroblob(32),0,zeroblob(32))`, string(canonical))
	return f
}

// Frozen fixtures were produced by the existing Config -> Normalize -> Validate
// -> json.Marshal path, outside the native test graph (model blob0526e9c9).
func rbChannelAcceptedFixture(t *testing.T, kind string) []byte {
	t.Helper()
	hashes := map[string]string{"movie": "227981f3afb5377c1ce46476d1c841bda750b9d641d668d89a07a626c024bbe6", "episode": "ff642b25e5028294f28aef60c39befe45579d18f70915f346e9954a2425f2a45"}
	want, ok := hashes[kind]
	if !ok {
		t.Fatal("unknown accepted config fixture")
	}
	b, e := os.ReadFile("testdata/rb-channel-config-" + kind + ".json")
	if e != nil {
		t.Fatal(e)
	}
	if fmt.Sprintf("%x", sha256.Sum256(b)) != want {
		t.Fatal("accepted config fixture changed without its canonical producer")
	}
	return b
}

// END_RB_CHANNEL_JOURNAL
func mustRBChannelJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func rbChannelFixtureHash(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("rb_channel_authority_test.go")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(data), "// BEGIN_RB_CHANNEL_JOURNAL\n")
	end := strings.Index(string(data), "// END_RB_CHANNEL_JOURNAL\n")
	if start < 0 || end <= start {
		t.Fatal("channel source journal")
	}
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(browsePlanFixtureHash(t)), data[start:end]...)))
}
func rbChannelFixture(t *testing.T) (*browseFixture, *SharedChannelReader) {
	t.Helper()
	cfg := governor.Config{Now: time.Now, MaxGrantsPerClass: 4}
	cfg.Partitions[governor.Background] = governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 20 << 20}
	f := composeReadFixture(t, newRBChannelSource(t), RBSharedChannelDefinitions(), cfg, false, rbChannelFixtureHash(t))
	r, err := NewSharedChannelReader(f.snapshots, f.authority, "channel-test-db")
	if err != nil {
		t.Fatal(err)
	}
	out := governor.Request{Key: governor.JobKey{Domain: 396, ID: "channel-copy"}, Owner: 1, Class: governor.Background, Need: governor.Amounts{governor.MemoryBytes: 8 << 20}, Deadline: time.Now().Add(time.Hour)}
	d := f.governor.Try(out)
	if d.Grant == nil {
		t.Fatal(d.Refusal)
	}
	f.output = BrowseOutput{Authority: f.authority, Grant: d.Grant, Request: out}
	t.Cleanup(func() {
		f.governor.Cancel(d.Grant)
		if e := f.authority.Retired(d.Grant); e != nil {
			t.Error(e)
		}
	})
	return f, r
}
func rbChannelRequest() governor.Request {
	r := readfixture.ReadRequest()
	r.Class = governor.Background
	return r
}
func rbChannelScopeInput() SharedChannelScopeInput {
	return SharedChannelScopeInput{Channel: "channel", Generation: "generation", Rule: "rule", Library: "movies", ExpectedRevision: 1}
}

func TestRBChannelScopeSurvivesEverySessionExpiry(t *testing.T) {
	synctest.Test(t, testRBChannelScopeSurvivesEverySessionExpiry)
}
func testRBChannelScopeSurvivesEverySessionExpiry(t *testing.T) {
	f, r := rbChannelFixture(t)
	f.exec(t, `DELETE FROM authorization_family_tokens`)
	f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
	f.exec(t, `UPDATE direct_memberships SET disabled=1`)
	scope, info, cost, err := r.OpenSharedChannelScope(t.Context(), rbChannelRequest(), time.Second, f.output, rbChannelScopeInput())
	if err != nil || scope == nil || info.RequiredLibraryCount != 1 || info.RequiredLibraries[0] != "movies" || info.CatalogueLibrary != 1 || info.CreatedMS != 1000 || cost.LookupStatements != 3 || cost.PublicationStatements != 3 {
		t.Fatal("real channel authority depended on a viewer", info, cost, err)
	}
	if _, err = r.RevalidateSharedChannelScope(t.Context(), rbChannelRequest(), time.Second, f.output, scope); err != nil {
		t.Fatal(err)
	}
	other, err := NewSharedChannelReader(f.snapshots, f.authority, "restored-db")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = other.RevalidateSharedChannelScope(t.Context(), rbChannelRequest(), time.Second, f.output, scope); !errors.Is(err, ErrInput) {
		t.Fatal("foreign issuer accepted scope", err)
	}
}
func TestRBChannelScopeCannotWidenOrIgnoreCurrentFacts(t *testing.T) {
	for _, mode := range []string{"library", "rule", "generation", "revision", "disabled", "library-retired", "root", "catalogue", "config"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f, r := rbChannelFixture(t)
				in := rbChannelScopeInput()
				scope, _, _, err := r.OpenSharedChannelScope(t.Context(), rbChannelRequest(), time.Second, f.output, in)
				if err != nil {
					t.Fatal(err)
				}
				switch mode {
				case "library":
					in.Library = "other"
				case "rule":
					in.Rule = "unlisted"
				case "generation":
					in.Generation = "missing"
				case "revision":
					in.ExpectedRevision = 2
				case "disabled":
					f.exec(t, `UPDATE lc_channels SET enabled=0`)
				case "library-retired":
					f.exec(t, `UPDATE catalog_libraries SET retired=1 WHERE id=1`)
				case "root":
					f.exec(t, `UPDATE libraries SET root='/replaced' WHERE id='movies'`)
				case "catalogue":
					f.exec(t, `UPDATE libraries SET data_revision=data_revision+1 WHERE id='movies'`)
				case "config":
					f.exec(t, `UPDATE lc_channels SET config_json=json_set(config_json,'$.name','Changed')`)
				}
				if mode == "catalogue" {
					_, err = r.RevalidateSharedChannelScope(t.Context(), rbChannelRequest(), time.Second, f.output, scope)
				} else {
					var out *SharedChannelScope
					out, _, _, err = r.OpenSharedChannelScope(t.Context(), rbChannelRequest(), time.Second, f.output, in)
					if out != nil {
						t.Fatal("invalid scope escaped")
					}
				}
				if err == nil {
					t.Fatal("current scope change was accepted", mode)
				}
			})
		})
	}
}
func TestRBChannelScopeRejectsCancellationAndForegroundBypass(t *testing.T) {
	synctest.Test(t, testRBChannelScopeRejectsCancellationAndForegroundBypass)
}
func testRBChannelScopeRejectsCancellationAndForegroundBypass(t *testing.T) {
	f, r := rbChannelFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if scope, _, _, err := r.OpenSharedChannelScope(ctx, rbChannelRequest(), time.Second, f.output, rbChannelScopeInput()); scope != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(scope, err)
	}
	fg := rbChannelRequest()
	fg.Class = governor.Foreground
	if scope, _, _, err := r.OpenSharedChannelScope(t.Context(), fg, time.Second, f.output, rbChannelScopeInput()); scope != nil || !errors.Is(err, ErrInput) {
		t.Fatal("foreground bypassed LIVE", scope, err)
	}
}
