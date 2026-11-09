package writer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// These are the owners' complete registered families, including their authority
// and entry statements. Capture compiles the actual SQL and byte recipes on the
// composed canonical schema; it does not execute a command or enable a caller.
// CHANGE-020 coverage/missing/finish families remain outside this C019 capture.
// This uses the existing WC writer component's unanalyzed profile. The initial
// analyzed attempt is retained as failed evidence: inherited accounts CASCADE
// plans scan identity_pin_lineages in this one-account population. No analyzed
// writer/parent-delete acceptance or FK-plan waiver follows from this control.
func TestC019NativeWriterRegistries(t *testing.T) {
	type fixture func(*testing.T) (*executor, *iaEntryRegistry, *usOwnerAuthorityProof)
	for _, tc := range []struct {
		name, source string
		fixture      fixture
		definitions  func() []stmt.Definition
	}{
		{"configure-library", "ia_configure_library_test.go", iaConfigureLibraryFixture, iaConfigureLibraryDefinitions},
		{"configure-source", "ia_configure_source_test.go", iaConfigureSourceFixture, iaConfigureSourceDefinitions},
		{"episode", "ia_episode_accept_test.go", iaEpisodeAcceptFixture, iaEpisodeDefinitions},
		{"music", "ia_music_accept_test.go", iaMusicAcceptFixture, iaMusicDefinitions},
		{"book", "ia_audiobook_accept_test.go", iaBookAcceptFixture, iaBookDefinitions},
		{"reading", "ia_reading_accept_test.go", func(t *testing.T) (*executor, *iaEntryRegistry, *usOwnerAuthorityProof) {
			return iaReadingAcceptFixture(t, 20)
		}, iaReadingDefinitions},
		{"extra", "ia_extra_accept_test.go", iaExtraAcceptFixture, iaExtraAcceptDefinitions},
		{"membership", "ia_membership_accept_test.go", iaMembershipAcceptFixture, iaMembershipDefinitions},
		{"reconcile", "ia_reconcile_object_test.go", iaReconcileObjectFixture, iaReconcileObjectDefinitions},
		{"refresh", "ia_refresh_test.go", func(t *testing.T) (*executor, *iaEntryRegistry, *usOwnerAuthorityProof) {
			w, r, p, _ := iaRefreshFixture(t)
			return w, r, p
		}, iaRefreshDefinitions},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, _, _ := tc.fixture(t)
			requireC019Fixture(t)
			defs := tc.definitions()
			catalogue, err := stmt.Describe(defs)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := schema.ReadContract("INT-INDEX-POLICY.json")
			if err != nil {
				t.Fatal(err)
			}
			policy, err := plan.ReadIndexPolicy(raw)
			if err != nil {
				t.Fatal(err)
			}
			h := sha256.New()
			for _, name := range []string{tc.source, "ia_command_entry_test.go", "ia_c019_composed_plans_test.go"} {
				b, err := os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				h.Write([]byte(name))
				h.Write([]byte{0})
				h.Write(b)
			}
			conn, err := w.db.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			var captures []plan.Golden
			for _, stats := range []plan.Statistics{plan.Unanalyzed} {
				if stats == plan.Analyzed {
					if _, err := conn.ExecContext(t.Context(), "ANALYZE"); err != nil {
						t.Fatal(err)
					}
				}
				g, err := plan.Capture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: w.config, SHA256: hex.EncodeToString(h.Sum(nil)), Statistics: stats, Implementations: "C019 composed owner registry; compile-only native Capture, no producer, authority or performance qualification; sole #1556 baseline unchanged"}, policy)
				if err != nil {
					t.Fatal(stats, err)
				}
				if len(g.Manifest) != len(defs) {
					t.Fatal("incomplete owner registry", len(g.Manifest), len(defs))
				}
				if _, err := stmt.New(defs, g.Receipt()); err != nil {
					t.Fatal("owner registry receipt", err)
				}
				captures = append(captures, g)
			}
			if dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); dir != "" {
				b, err := json.MarshalIndent(captures, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "c019-writer-"+tc.name+".json"), b, 0644); err != nil {
					t.Fatal(err)
				}
			}
			t.Logf("%d actual owner statements, unanalyzed writer profile; no golden or shared baseline rewritten", len(defs))
		})
	}
}
