package search_test

// RS-19: the search bank fingerprint and Go-toolchain rebuild contract.
//
// The bank's unicode_fingerprint is derived from the Unicode tables selected by
// the running Go build (internal/searchpolicy), so a bank prepared by one
// toolchain compares Stale against another build's compiled profile and
// read.nativeReadFence answers SearchUnavailable. This file pins the operational
// contract around that refusal: stay on Go 1.26.10 until a reviewed rebuild path
// exists, request an owner generation rebuild on the new toolchain, keep the
// accepted bank serving on a compatible engine while the bounded build and
// dual publication catch up, flip only a COMPLETE generation, then reclaim
// natives only after reader pins are released. W/INT own the implementation;
// this file models the merged interfaces and changes no writer lifecycle.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/read"
)

const (
	// contractPinnedGo is the toolchain the bank fingerprint is pinned to. A
	// change needs the reviewed rebuild path documented in
	// tools/search-bank-compatibility.md, not a re-recorded fingerprint.
	contractPinnedGo = "go 1.26.10"

	// contractBuildBatch is a fixture bound, not a production batch size. It
	// only proves that a rebuild is a sequence of bounded owner commands.
	contractBuildBatch = 64
)

var (
	errContractConflict = errors.New("bank contract: generation conflict")
	errContractCapacity = errors.New("bank contract: bounded batch refused")
)

// contractGeneration is the observable subset of one search_generations row
// that the read, publisher and retirement contracts depend on. State mirrors
// state IN('building','active','retired'); phase ends at 'complete'.
type contractGeneration struct {
	id       int64
	profile  searchpolicy.Profile
	state    string
	phase    string
	expected int64
	staged   int64
	pending  int64
	sealed   bool
}

// contractStore is one search_revision row plus its generations and reader
// pins. It is deliberately a model: the writer owns the real statement CAS
// predicates (searchInputPending, searchHeadPendingCAS, searchImageBank,
// searchReclaimBank) and the real reader is read.nativeReadFence.
type contractStore struct {
	active    *contractGeneration
	building  *contractGeneration
	next      int64
	pins      map[int64]int
	reclaimed []int64
}

func newContractStore(active searchpolicy.Profile) *contractStore {
	return &contractStore{
		active: &contractGeneration{id: 1, profile: active, state: "active", phase: "idle"},
		next:   1,
		pins:   map[int64]int{},
	}
}

// contractCompare applies the merged compatibility check. searchpolicy.Compare
// hard-codes the current binary's Compiled(), which is exactly right for the
// running build; the explicit running tuple lets this contract also exercise
// the old build during the dual-publication window. Only the profile-match
// verdict is re-evaluated there; engine attestation and tuple encoding always
// come from the merged check.
func contractCompare(stored, running searchpolicy.Profile, engine searchpolicy.Engine) searchpolicy.Compatibility {
	got := searchpolicy.Compare(stored, engine)
	if running == searchpolicy.Compiled() || (got.Reason != searchpolicy.ProfileMatches && got.Reason != searchpolicy.ProfileMismatch) {
		return got
	}
	if stored == running {
		got.State, got.Reason = searchpolicy.Current, searchpolicy.ProfileMatches
	} else {
		got.State, got.Reason = searchpolicy.Stale, searchpolicy.ProfileMismatch
	}
	return got
}

// read models read.nativeReadFence: a stale or unattested bank is
// SearchUnavailable with no cards, a live generation with outstanding work is
// SearchPending, and only a Current active generation with nothing pending
// answers SearchResults.
func (s *contractStore) read(running searchpolicy.Profile, engine searchpolicy.Engine) (read.SearchDisposition, int64) {
	g := s.active
	if g == nil || g.state != "active" || g.profile == (searchpolicy.Profile{}) {
		return read.SearchUnavailable, 0
	}
	if contractCompare(g.profile, running, engine).State != searchpolicy.Current {
		return read.SearchUnavailable, 0
	}
	if g.pending != 0 {
		return read.SearchPending, g.id
	}
	return read.SearchResults, g.id
}

// beginRebuild requests the owner generation rebuild on the running toolchain.
// It mirrors the merged publisher input: only an attested physical engine with
// the current compiled tuple starts work, and only one generation may be
// building at a time (search_revision.building_generation_id).
func (s *contractStore) beginRebuild(running searchpolicy.Profile, engine searchpolicy.Engine, expected int64) (*contractGeneration, error) {
	if expected < 1 || s.building != nil {
		return nil, errContractConflict
	}
	if contractCompare(running, running, engine).State != searchpolicy.Current {
		return nil, errContractConflict
	}
	s.next++
	g := &contractGeneration{id: s.next, profile: running, state: "building", phase: "schema", expected: expected}
	s.building = g
	return g, nil
}

// stage applies one bounded publisher batch to the building generation. Input
// intents bind to building while it exists (search.publish.cas.input_sequence_building),
// and every CAS carries the exact stored profile tuple, so a re-derived or
// edited tuple is a conflict, not a migration.
func (s *contractStore) stage(g *contractGeneration, running searchpolicy.Profile, subjects int64) error {
	if g == nil || g != s.building || g.state != "building" || g.profile != running {
		return errContractConflict
	}
	if subjects < 1 || subjects > contractBuildBatch || g.staged+subjects > g.expected {
		return errContractCapacity
	}
	g.staged += subjects
	g.pending += subjects
	g.phase = "subjects"
	return nil
}

// seal records that every expected subject's image is staged and verified
// (search_image_seals: staged=verified=expected, sealed=1).
func (s *contractStore) seal(g *contractGeneration, running searchpolicy.Profile) error {
	if g == nil || g != s.building || g.profile != running || g.expected < 1 || g.staged != g.expected {
		return errContractConflict
	}
	g.sealed = true
	g.phase = "seal"
	return nil
}

// publish drains captured subjects in bounded batches. It never publishes a
// card or count from an unsealed generation and reaches complete only when the
// whole expected set is staged and nothing is pending.
func (s *contractStore) publish(g *contractGeneration, running searchpolicy.Profile, subjects int64) error {
	if g == nil || g != s.building || g.profile != running || !g.sealed {
		return errContractConflict
	}
	if subjects < 1 || subjects > contractBuildBatch || subjects > g.pending {
		return errContractCapacity
	}
	g.pending -= subjects
	if g.staged == g.expected && g.pending == 0 {
		g.phase = "complete"
	}
	return nil
}

// flip is the global activation the merged publisher intentionally leaves to
// its owner. It requires a complete, sealed, fully drained generation, the
// activating toolchain's own tuple, and a freshly attested engine; it then
// atomically retires the old generation. There is no profile-only entry point:
// an unbuilt generation cannot become active.
func (s *contractStore) flip(g *contractGeneration, running searchpolicy.Profile, engine searchpolicy.Engine) error {
	if g == nil || g != s.building || g.state != "building" || g.profile != running || g.phase != "complete" || !g.sealed ||
		g.pending != 0 || g.staged != g.expected || g.expected < 1 {
		return errContractConflict
	}
	if contractCompare(running, running, engine).State != searchpolicy.Current {
		return errContractConflict
	}
	old := s.active
	g.state = "active"
	s.active, s.building = g, nil
	if old != nil {
		old.state = "retired"
	}
	return nil
}

func (s *contractStore) pin(g *contractGeneration) {
	if g != nil {
		s.pins[g.id]++
	}
}

func (s *contractStore) release(g *contractGeneration) {
	if g != nil && s.pins[g.id] > 0 {
		s.pins[g.id]--
	}
}

// reclaim releases native rows only for a retired generation with no reader
// pin. Reclaiming the active generation or a pinned reader's generation refuses;
// the caller retries within its own bounded maintenance job.
func (s *contractStore) reclaim(g *contractGeneration) error {
	if g == nil || g.state != "retired" || s.pins[g.id] != 0 {
		return errContractConflict
	}
	delete(s.pins, g.id)
	s.reclaimed = append(s.reclaimed, g.id)
	return nil
}

// contractOtherProfile is a well-formed tuple from a different toolchain. It
// deliberately shares the recipe and SQLite descriptor so the only difference
// is the compiled Unicode fingerprint, like a Go upgrade with the same x/text
// normalization pin.
func contractOtherProfile(t *testing.T) searchpolicy.Profile {
	t.Helper()
	p := searchpolicy.Compiled()
	digest := strings.Repeat("0", 64)
	if searchpolicy.ProfileAlgorithm+":"+digest == p.UnicodeFingerprint {
		digest = strings.Repeat("f", 64)
	}
	p.UnicodeFingerprint = searchpolicy.ProfileAlgorithm + ":" + digest
	if !searchpolicy.StoredWellFormed(p) {
		t.Fatalf("fixture profile is not well formed: %+v", p)
	}
	return p
}

// contractActualEngine reads the real bundled engine through the shared
// connection policy, the same evidence source the publisher samples.
func contractActualEngine(t *testing.T) searchpolicy.Engine {
	t.Helper()
	ctx := context.Background()
	db, _, err := sqliteconn.Open(ctx, filepath.Join(t.TempDir(), "bank-contract.db"), sqliteconn.Config{
		Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal,
	})
	if err != nil {
		t.Fatalf("open fixture engine: %v", err)
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("fixture connection: %v", err)
	}
	defer conn.Close()
	actual, err := sqliteconn.AttestEngine(ctx, conn)
	if err != nil {
		t.Fatalf("attest fixture engine: %v", err)
	}
	return searchpolicy.Engine{Version: actual.Version, SourceID: actual.SourceID}
}

// TestSearchBankUpgradeContractPinnedToolchain keeps the toolchain pin explicit.
// Until the owner rebuild path is reviewed and merged, moving this directive
// would silently change every prepared bank's Unicode fingerprint.
func TestSearchBankUpgradeContractPinnedToolchain(t *testing.T) {
	raw, err := os.ReadFile("../../../go.mod")
	if err != nil {
		t.Fatalf("read server/go.mod: %v", err)
	}
	var directive string
	for _, line := range strings.Split(string(raw), "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "go ") {
			directive = line
			break
		}
	}
	if directive != contractPinnedGo {
		t.Fatalf("server/go.mod = %q, want %q: the bank's Unicode fingerprint follows the Go toolchain; land the reviewed rebuild path in tools/search-bank-compatibility.md before moving this pin", directive, contractPinnedGo)
	}
}

// TestSearchBankUpgradeContractActualEngine is the real compatible/mismatch
// fixture: the bundled engine attests the compiled profile, a different
// toolchain's well-formed tuple is stale, and an unattested engine refuses.
func TestSearchBankUpgradeContractActualEngine(t *testing.T) {
	engine := contractActualEngine(t)
	compiled := searchpolicy.Compiled()
	if got := searchpolicy.Compare(compiled, engine); got != (searchpolicy.Compatibility{State: searchpolicy.Current, Reason: searchpolicy.ProfileMatches, EngineAttested: true}) {
		t.Fatalf("actual engine does not attest the compiled profile: %+v (%+v)", got, engine)
	}
	other := contractOtherProfile(t)
	if got := searchpolicy.Compare(other, engine); got.State != searchpolicy.Stale || got.Reason != searchpolicy.ProfileMismatch || !got.EngineAttested {
		t.Fatalf("a bank from another toolchain = %+v, want stale profile mismatch", got)
	}
	foreign := searchpolicy.Engine{Version: engine.Version, SourceID: engine.SourceID + ";changed"}
	if got := searchpolicy.Compare(compiled, foreign); got.State != searchpolicy.Unavailable || got.Reason != searchpolicy.NativeEngineMismatch || got.EngineAttested {
		t.Fatalf("a non-attested engine = %+v, want unavailable engine mismatch", got)
	}
	store := newContractStore(other)
	if got, id := store.read(compiled, engine); got != read.SearchUnavailable || id != 0 {
		t.Fatalf("reader exposed a bank from another toolchain: %v/%d", got, id)
	}
}

// TestSearchBankUpgradeContractRebuildFlipsOnlyComplete walks the upgrade:
// refuse the old fingerprint, keep the accepted bank serving where its engine
// is compatible, build in bounded dual-publication batches, and flip only the
// complete generation.
func TestSearchBankUpgradeContractRebuildFlipsOnlyComplete(t *testing.T) {
	engine := contractActualEngine(t)
	accepted := searchpolicy.Compiled()
	upgraded := contractOtherProfile(t)
	store := newContractStore(accepted)

	if got, id := store.read(accepted, engine); got != read.SearchResults || id == 0 {
		t.Fatalf("accepted bank on its toolchain = %v/%d", got, id)
	}
	if got, _ := store.read(upgraded, engine); got != read.SearchUnavailable {
		t.Fatalf("old bank on a new toolchain = %v, want SearchUnavailable", got)
	}

	build, err := store.beginRebuild(upgraded, engine, 600)
	if err != nil {
		t.Fatalf("owner rebuild request: %v", err)
	}
	// The accepted bank keeps serving on the compatible engine while the new
	// generation builds; the building generation never appears to readers.
	if got, _ := store.read(accepted, engine); got != read.SearchResults {
		t.Fatalf("accepted bank stopped serving during rebuild: %v", got)
	}
	if got, _ := store.read(upgraded, engine); got != read.SearchUnavailable {
		t.Fatalf("building generation published: %v", got)
	}

	if err := store.stage(build, upgraded, contractBuildBatch+1); !errors.Is(err, errContractCapacity) {
		t.Fatalf("unbounded stage = %v, want capacity refusal", err)
	}
	for left := int64(600); left > 0; {
		n := min(left, int64(contractBuildBatch))
		if err := store.stage(build, upgraded, n); err != nil {
			t.Fatalf("stage %d: %v", n, err)
		}
		left -= n
	}
	if err := store.stage(build, upgraded, 1); !errors.Is(err, errContractCapacity) {
		t.Fatalf("stage past expected = %v, want capacity refusal", err)
	}
	if err := store.stage(build, accepted, 1); !errors.Is(err, errContractConflict) {
		t.Fatalf("stage with another toolchain tuple = %v, want conflict", err)
	}
	if err := store.seal(build, upgraded); err != nil {
		t.Fatalf("seal complete build: %v", err)
	}
	if err := store.publish(build, upgraded, contractBuildBatch); err != nil {
		t.Fatalf("publish batch: %v", err)
	}
	if err := store.flip(build, upgraded, engine); !errors.Is(err, errContractConflict) {
		t.Fatalf("flip with pending subjects = %v, want conflict", err)
	}
	if got, _ := store.read(upgraded, engine); got != read.SearchUnavailable {
		t.Fatalf("partial generation readable: %v", got)
	}
	for left := int64(600 - contractBuildBatch); left > 0; {
		n := min(left, int64(contractBuildBatch))
		if err := store.publish(build, upgraded, n); err != nil {
			t.Fatalf("drain %d: %v", n, err)
		}
		left -= n
	}
	if build.phase != "complete" {
		t.Fatalf("build phase = %q, want complete", build.phase)
	}
	if err := store.flip(build, upgraded, searchpolicy.Engine{Version: engine.Version, SourceID: "changed"}); !errors.Is(err, errContractConflict) {
		t.Fatalf("flip on unattested engine = %v, want conflict", err)
	}
	if err := store.flip(build, upgraded, engine); err != nil {
		t.Fatalf("flip complete generation: %v", err)
	}
	if got, id := store.read(upgraded, engine); got != read.SearchResults || id != build.id {
		t.Fatalf("completed bank after flip = %v/%d", got, id)
	}
	// Downgrade direction: the old toolchain must refuse the newer bank too.
	if got, _ := store.read(accepted, engine); got != read.SearchUnavailable {
		t.Fatalf("new bank on an old toolchain = %v, want SearchUnavailable", got)
	}
}

// TestSearchBankUpgradeContractFingerprintAloneDoesNotPublish proves that an
// unbuilt generation cannot be activated by supplying the current fingerprint.
func TestSearchBankUpgradeContractFingerprintAloneDoesNotPublish(t *testing.T) {
	engine := contractActualEngine(t)
	accepted := searchpolicy.Compiled()
	upgraded := contractOtherProfile(t)

	// A generation created with the new tuple but no staged rows is not a
	// build and cannot be sealed or flipped.
	store := newContractStore(accepted)
	empty, err := store.beginRebuild(upgraded, engine, 4)
	if err != nil {
		t.Fatalf("begin empty rebuild: %v", err)
	}
	if err := store.seal(empty, upgraded); !errors.Is(err, errContractConflict) {
		t.Fatalf("seal without staged rows = %v, want conflict", err)
	}
	if err := store.flip(empty, upgraded, engine); !errors.Is(err, errContractConflict) {
		t.Fatalf("flip without a build = %v, want conflict", err)
	}
	if got, _ := store.read(upgraded, engine); got != read.SearchUnavailable {
		t.Fatalf("unbuilt generation readable: %v", got)
	}

	// The accepted generation is not a staging target: the only supported way
	// to change the stored tuple is a new complete generation, not an edit.
	if err := store.stage(store.active, upgraded, 1); !errors.Is(err, errContractConflict) {
		t.Fatalf("stage into accepted generation = %v, want conflict", err)
	}
	if got, id := store.read(accepted, engine); got != read.SearchResults || id != store.active.id {
		t.Fatalf("accepted generation disturbed = %v/%d", got, id)
	}
}

// TestSearchBankUpgradeContractRestartAndReaderPinnedRetirement covers a
// restart during the build and the reader-pinned retirement of the old native
// generation after the flip.
func TestSearchBankUpgradeContractRestartAndReaderPinnedRetirement(t *testing.T) {
	engine := contractActualEngine(t)
	accepted := searchpolicy.Compiled()
	upgraded := contractOtherProfile(t)
	store := newContractStore(accepted)

	build, err := store.beginRebuild(upgraded, engine, 2)
	if err != nil {
		t.Fatalf("begin rebuild: %v", err)
	}
	if err := store.stage(build, upgraded, 2); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if err := store.seal(build, upgraded); err != nil {
		t.Fatalf("seal: %v", err)
	}

	// Restart from the persisted rows: the accepted bank stays unreadable on
	// the new toolchain, the building generation keeps its exact tuple, and
	// the old toolchain still serves the old bank.
	restarted := &contractStore{
		active: &contractGeneration{id: store.active.id, profile: store.active.profile, state: store.active.state, phase: store.active.phase},
		building: &contractGeneration{id: build.id, profile: build.profile, state: build.state, phase: build.phase,
			expected: build.expected, staged: build.staged, pending: build.pending, sealed: build.sealed},
		next: build.id,
		pins: map[int64]int{},
	}
	if got, _ := restarted.read(upgraded, engine); got != read.SearchUnavailable {
		t.Fatalf("restart exposed the old bank on the new toolchain: %v", got)
	}
	if got, _ := restarted.read(accepted, engine); got != read.SearchResults {
		t.Fatalf("old toolchain lost its accepted bank: %v", got)
	}
	if _, err := restarted.beginRebuild(upgraded, engine, 2); !errors.Is(err, errContractConflict) {
		t.Fatalf("second building generation = %v, want conflict", err)
	}
	if err := restarted.stage(restarted.building, accepted, 1); !errors.Is(err, errContractConflict) {
		t.Fatalf("resume with another tuple = %v, want conflict", err)
	}

	old := restarted.active
	restarted.pin(old)
	if err := restarted.publish(restarted.building, upgraded, 2); err != nil {
		t.Fatalf("drain after restart: %v", err)
	}
	if err := restarted.flip(restarted.building, upgraded, engine); err != nil {
		t.Fatalf("flip after restart: %v", err)
	}
	if err := restarted.reclaim(old); !errors.Is(err, errContractConflict) {
		t.Fatalf("reclaim while a reader is pinned = %v, want conflict", err)
	}
	if err := restarted.reclaim(restarted.active); !errors.Is(err, errContractConflict) {
		t.Fatalf("reclaim the active generation = %v, want conflict", err)
	}
	restarted.release(old)
	if err := restarted.reclaim(old); err != nil {
		t.Fatalf("reclaim after release: %v", err)
	}
	if len(restarted.reclaimed) != 1 || restarted.reclaimed[0] != old.id {
		t.Fatalf("reclaimed = %v, want [%d]", restarted.reclaimed, old.id)
	}

	// A later restart reads the completed generation deterministically.
	again := &contractStore{active: &contractGeneration{id: restarted.active.id, profile: restarted.active.profile, state: "active", phase: "complete"}, pins: map[int64]int{}}
	if got, id := again.read(upgraded, engine); got != read.SearchResults || id != restarted.active.id {
		t.Fatalf("completed generation after restart = %v/%d", got, id)
	}
}
