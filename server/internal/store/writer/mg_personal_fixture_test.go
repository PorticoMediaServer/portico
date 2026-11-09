package writer

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// The fixture uses the REAL embedded A/B/G+013 schema: no draft overlay, no
// dropped table, no synthetic canonical copy. It adds one live source head, one
// published LC channel/generation, one future programme/occurrence and the
// shared lifetime root + command owner.
type mgPersonalFixtureValues struct {
	owner   mgPersonalOwner
	live    mgPersonalChannel
	library mgPersonalChannel
	item    mgPersonalLibraryItem
	start   time.Time
	root    string
	proof   *usOwnerAuthorityProof
}

func (v mgPersonalFixtureValues) reminder(library bool) mgPersonalReminderRequest {
	r := mgPersonalReminderRequest{owner: v.owner, target: v.live, programme: "live-programme"}
	if library {
		r.target = v.library
		r.programme = "lc-occurrence"
		r.libraryItem = v.item
	}
	return r
}

// mgPersonalFixture builds the reviewed US owner session and adds only this
// task's native rows. The root identity is copied from that same witness, so a
// foreign/forged issuer or a changed root is observable.
func mgPersonalFixture(t *testing.T) (*executor, string, mgPersonalFixtureValues) {
	t.Helper()
	return mgPersonalFixtureWith(t)
}

func mgPersonalFixtureWith(t *testing.T, decorate ...func(driver.Conn) driver.Conn) (*executor, string, mgPersonalFixtureValues) {
	t.Helper()
	w, path, _ := testExecutor(t, decorate...)
	now := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	w.now = func() time.Time { return now }
	proof := usOwnerAuthorityPopulation(t, w, now)
	i := proof.witness.issuer
	incarnation := mgPersonalIncarnation()
	v := mgPersonalFixtureValues{
		owner:   mgPersonalOwner{"local", i.account, i.profile},
		live:    mgPersonalChannel{mgPersonalLive, "personal-live", "live-channel", "live-generation", 7},
		library: mgPersonalChannel{mgPersonalLibrary, "library-channel", "library-channel", "lc-generation", 5},
		item:    mgPersonalLibraryItem{101, "selected-library", "selected-asset", "observed-source"},
		start:   now.Add(24 * time.Hour).Truncate(time.Second),
		root:    "abcdefghijklmnopqrstuv",
		proof:   proof,
	}
	start, end := v.start.Format(time.RFC3339), v.start.Add(time.Hour).Format(time.RFC3339)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO user_mutation_global(singleton) VALUES(1) ON CONFLICT(singleton) DO NOTHING`, nil},
		{`INSERT INTO user_mutation_roots(id,installation_incarnation,account_id,profile_id,account_incarnation,profile_incarnation,family_id,registration_revision,state) VALUES(?,?,?,?,?,?,?,?,?)`,
			[]any{v.root, i.installation, i.account, i.profile, i.memberIncarnation, i.profileLifetime, i.family, int64(1), "active"}},
		{`INSERT INTO user_mutation_command_owners(root_id,command_incarnation) VALUES(?,?)`, []any{v.root, incarnation[:]}},
		{`INSERT INTO live_source_identities(id) VALUES('personal-live')`, nil},
		{`INSERT INTO live_generations(id,source_id,name,tuner_count,channel_count,programme_count,created_at) VALUES('live-generation','personal-live','Guide',1,2,2,'2026-11-07T00:00:00Z'),('replacement-generation','personal-live','Next guide',1,0,0,'2026-11-07T00:00:00Z')`, nil},
		{`INSERT INTO live_sources(id,name,revision,state,tuner_count,active_generation,published_at) VALUES('personal-live','Live',7,'active',1,'live-generation','2026-11-07T00:00:00Z')`, nil},
		{`INSERT INTO live_channel_versions(generation_id,channel_id,provider_key,name,number,group_name,position,locator_sealed,available_start,available_end) VALUES('live-generation','live-channel','provider-1','Channel','1','Group',0,X'01','',''),('live-generation','other-channel','provider-2','Other','2','Group',1,X'01','','')`, nil},
		{`INSERT INTO live_programmes(generation_id,id,channel_id,provider_key,title,start_utc,end_utc,lineage) VALUES('live-generation','live-programme','live-channel','p1','Programme',?,?,'lineage'),('live-generation','other-programme','other-channel','p2','Other',?,?,'other')`, []any{start, end, start, end}},
		{`INSERT INTO lc_channels(id,revision,config_json,enabled,position,name,state) VALUES('library-channel',5,'{}',1,0,'Library channel','healthy')`, nil},
		{`INSERT INTO lc_generations(id,channel_id,config_revision,config_json,catalog_fence,seed,status,phase,base_generation,start_ms,end_ms,boundary_ms,cursor_ms,created_ms,command_incarnation,create_operation_id,birth_request_digest,entry_digest,verify_end_ms,verified_entry_digest) VALUES('lc-generation','library-channel',5,'{}','selected-snapshot','seed','published','published','',?,?,?, ?,1,zeroblob(16),zeroblob(16),zeroblob(32),zeroblob(32),?,zeroblob(32))`,
			[]any{v.start.UnixMilli(), v.start.Add(time.Hour).UnixMilli(), v.start.UnixMilli(), v.start.Add(time.Hour).UnixMilli(), v.start.UnixMilli()}},
		{`UPDATE lc_channels SET active_generation='lc-generation' WHERE id='library-channel'`, nil},
		{`INSERT INTO lc_entries VALUES('lc-generation','lc-occurrence','library-channel',?,?,101,'selected-asset','selected-library','observed-source','Entry','rule','block',125,'')`, []any{v.start.UnixMilli() + 123, v.start.Add(time.Hour).UnixMilli()}},
	} {
		if _, err := w.db.ExecContext(t.Context(), q.sql, q.args...); err != nil {
			t.Fatal(q.sql, err)
		}
	}
	return w, path, v
}

// One frozen command incarnation shared by every fixture command. Individual
// tests advance the sequence; the owner row starts empty and is advanced by the
// adapter in the same turn as the effect.
func mgPersonalIncarnation() [16]byte {
	var out [16]byte
	for n := range out {
		out[n] = byte(n + 1)
	}
	return out
}

func mgPersonalOperation(n byte) [16]byte {
	var out [16]byte
	out[0] = n
	out[15] = 0xa5
	return out
}

func mgPersonalIdentity(sequence int64, operation byte) CommandIdentity {
	return CommandIdentity{Incarnation: mgPersonalIncarnation(), Sequence: sequence, OperationID: mgPersonalOperation(operation)}
}

func mgPersonalDefinitionsUnion() []stmt.Definition {
	defs := mgPersonalRootDefinitions()
	return append(defs, mgPersonalDefinitions()...)
}

func mgPersonalTurn(t *testing.T, w *executor, purpose CommandID) *commandTurn {
	t.Helper()
	defs := mgPersonalDefinitionsUnion()
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	recipe := turnRecipe{
		limit:    Work{1000, 1000000, 1000, 8 << 20, 25 * time.Millisecond},
		setup:    Work{8, 100, 8, 4096, time.Millisecond},
		terminal: Work{8, 100, 8, 4096, time.Millisecond},
	}
	tx, err := w.begin(t.Context(), purpose, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	var steps []statementStep
	for _, d := range defs {
		steps = append(steps, statementStep{d.ID, Work{8, 10000, 32, 65536, time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !tx.done {
			_ = turn.closeRows()
			_, _ = tx.rollback(nil)
		}
	})
	return turn
}

func mgPersonalCommit(t *testing.T, turn *commandTurn) {
	t.Helper()
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
}

// The fake stands in ONLY for the unminted current-session authorization. It
// still observes the real root row and rechecks the exact frozen identity and
// the native session revocation flag, so a stale or forged authority is not
// accepted by the frame under test.
type mgPersonalFakeRoots struct {
	root     string
	identity uoRootIdentity
	witness  *usOwnerAuthorityProof
	viewer   mgPersonalFakeViewer
}

// mgPersonalFakeViewer is the test stand-in for the current viewer policy that
// the future production session owns. A nil field admits every value; that is
// the fixture default, never a production grant. A non-nil map/slice is an
// allow-list, so a test can prove a forbidden channel/rating/item denies before
// the effect. Production has no session at all and therefore always refuses.
type mgPersonalFakeViewer struct {
	channels map[string]bool
	items    map[int64]bool
	ratings  []string
}

type mgPersonalFakeSession struct {
	roots    *mgPersonalFakeRoots
	turn     *commandTurn
	lifetime context.Context
	identity uoRootIdentity
}

func mgPersonalFakeIdentity(witness *usOwnerAuthorityProof) uoRootIdentity {
	i := witness.witness.issuer
	return uoRootIdentity{account: i.account, memberIncarnation: i.memberIncarnation, profile: i.profile, profileLifetime: i.profileLifetime, installation: i.installation, family: i.family}
}

func (f *mgPersonalFakeRoots) authorizeUSCurrentSession(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof, lifetime context.Context) (out mgPersonalRootAuthorization, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if f == nil || turn == nil || witness == nil || lifetime == nil || lifetime.Err() != nil {
		return nil, ErrDisclosureDenied
	}
	frozen, err := mgPersonalFreezeWitness(witness)
	if err != nil {
		return nil, err
	}
	identity := mgPersonalFakeIdentity(frozen)
	if f.witness != nil && frozen.witness.issuer.tokenDigest != f.witness.witness.issuer.tokenDigest {
		return nil, ErrDisclosureDenied
	}
	if identity != f.identity {
		return nil, ErrDisclosureDenied
	}
	if _, err = mgPersonalRootObserve(ctx, turn, f.root, identity); err != nil {
		return nil, err
	}
	return &mgPersonalFakeSession{roots: f, turn: turn, lifetime: lifetime, identity: identity}, nil
}

func (s *mgPersonalFakeSession) mgPersonalRootID() string { return s.roots.root }
func (s *mgPersonalFakeSession) mgPersonalRootIdentity() uoRootIdentity {
	return s.identity
}
func (s *mgPersonalFakeSession) state(ctx context.Context, turn *commandTurn) (out string, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if s == nil || s.roots == nil || s.turn != turn {
		return "", ErrDisclosureDenied
	}
	return mgPersonalRootObserve(ctx, turn, s.roots.root, s.identity)
}
func (s *mgPersonalFakeSession) recheck(ctx context.Context, turn *commandTurn) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if s == nil || s.roots == nil || s.turn != turn || s.lifetime == nil || s.lifetime.Err() != nil || turn == nil || turn.transaction == nil || turn.transaction.done {
		return ErrDisclosureDenied
	}
	var revoked int64
	if err := turn.transaction.conn.QueryRowContext(ctx, `SELECT revoked FROM authorization_session_families WHERE id=?`, s.identity.family).Scan(&revoked); err != nil {
		return ErrDisclosureDenied
	}
	if revoked != 0 {
		return ErrDisclosureDenied
	}
	_, err = mgPersonalRootObserve(ctx, turn, s.roots.root, s.identity)
	return err
}

// admitTarget models the viewer policy the minted session will own: the exact
// current channel membership, the current LC item projection and the content
// rating observed from the native row in this same turn. It refuses with
// ErrDisclosureDenied before any effect, and it never accepts a caller rating.
func (s *mgPersonalFakeSession) admitTarget(ctx context.Context, turn *commandTurn, target mgPersonalAdmissionTarget) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if s == nil || s.roots == nil || s.turn != turn || turn == nil || turn.transaction == nil || !target.valid() {
		return ErrDisclosureDenied
	}
	v := s.roots.viewer
	if v.channels != nil && !v.channels[target.channel] {
		return ErrDisclosureDenied
	}
	if target.kind == mgPersonalLibrary && v.items != nil && !v.items[target.item.item] {
		return ErrDisclosureDenied
	}
	if target.programme == "" || v.ratings == nil {
		return nil
	}
	rating, err := mgPersonalFakeRating(ctx, turn, target)
	if err != nil {
		return err
	}
	for _, allowed := range v.ratings {
		if allowed == rating {
			return nil
		}
	}
	return ErrDisclosureDenied
}

// mgPersonalFakeRating reads the rating from the original native row: the live
// programme metadata facts, or the library item's content-rating attribute.
func mgPersonalFakeRating(ctx context.Context, turn *commandTurn, target mgPersonalAdmissionTarget) (string, error) {
	if turn == nil || turn.transaction == nil {
		return "", ErrDisclosureDenied
	}
	if target.kind == mgPersonalLive {
		var raw string
		err := turn.transaction.conn.QueryRowContext(ctx, `SELECT metadata_facts FROM live_programmes WHERE generation_id=? AND id=? AND has_live_programme_metadata=1`, target.generation, target.programme).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		if err != nil {
			return "", ErrDisclosureDenied
		}
		var facts struct {
			Rating *struct {
				Value string `json:"value"`
			} `json:"rating"`
		}
		if json.Unmarshal([]byte(raw), &facts) != nil {
			return "", ErrDisclosureDenied
		}
		if facts.Rating == nil {
			return "", nil
		}
		return facts.Rating.Value, nil
	}
	var rating sql.NullString
	err := turn.transaction.conn.QueryRowContext(ctx, `SELECT min(a.source_value) FROM catalog_item_attribute_edges AS a JOIN catalog_attribute_terms AS term ON term.id=a.term_id WHERE a.item_id=? AND term.field_id=1`, target.item.item).Scan(&rating)
	if err != nil {
		return "", ErrDisclosureDenied
	}
	if !rating.Valid {
		return "", nil
	}
	return rating.String, nil
}

func mgPersonalFake(t *testing.T, v mgPersonalFixtureValues) *mgPersonalFakeRoots {
	t.Helper()
	return &mgPersonalFakeRoots{root: v.root, identity: mgPersonalFakeIdentity(v.proof), witness: v.proof}
}

func mgPersonalReference(t *testing.T, identity CommandIdentity, digest RequestDigest) ReceiptReference {
	t.Helper()
	return ReceiptReference{Identity: identity, RequestDigest: digest}
}

func mgPersonalPreferenceReference(t *testing.T, id CommandIdentity, r mgPersonalPreferenceRequest) ReceiptReference {
	t.Helper()
	c, err := newMGPersonalPreferenceCommand(id, r)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: id, RequestDigest: c.digest}
}
func mgPersonalReminderReference(t *testing.T, id CommandIdentity, r mgPersonalReminderRequest) ReceiptReference {
	t.Helper()
	c, err := newMGPersonalReminderCommand(id, r)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: id, RequestDigest: c.digest}
}
func mgPersonalDeleteReference(t *testing.T, id CommandIdentity, r mgPersonalDeleteRequest) ReceiptReference {
	t.Helper()
	c, err := newMGPersonalDeleteCommand(id, r)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: id, RequestDigest: c.digest}
}

func mgPersonalReadOnly(t *testing.T, path string) *sql.DB {
	t.Helper()
	cfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal, ReadLimits: sqliteconn.ReadLimits{LengthBytes: 65536, Columns: 64}}
	db, _, err := sqliteconn.Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
