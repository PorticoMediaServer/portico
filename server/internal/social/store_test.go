package social

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestPairingCodeAlphabetExcludesConfusableGlyphs(t *testing.T) {
	// A pairing code is read off a television and typed on a phone, so I, L, O, 0
	// and 1 must not be in it.
	for _, bad := range []rune{'I', 'L', 'O', '0', '1'} {
		for _, c := range codeAlphabet {
			if c == bad {
				t.Fatalf("confusable glyph %q is in the code alphabet", bad)
			}
		}
	}
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		code, e := newCode(8)
		if e != nil {
			t.Fatal(e)
		}
		if len(code) != 8 {
			t.Fatalf("code length %d", len(code))
		}
		for _, c := range code {
			found := false
			for _, allowed := range codeAlphabet {
				if c == allowed {
					found = true
				}
			}
			if !found {
				t.Fatalf("code %q used a character outside the alphabet", code)
			}
		}
		seen[code] = true
	}
	// 31^8 is large enough that 500 draws should not collide; a generator that
	// repeats is a generator that is not random.
	if len(seen) != 500 {
		t.Fatalf("only %d distinct codes in 500 draws", len(seen))
	}
}

func TestNormalizeCodeAcceptsHowPeopleType(t *testing.T) {
	for _, input := range []string{"km7q x2tr", "KM7Q-X2TR", "km7qx2tr"} {
		got, ok := normalizeCode(input)
		if !ok || got != "KM7QX2TR" {
			t.Fatalf("normalizeCode(%q) = %q,%v", input, got, ok)
		}
	}
	for _, input := range []string{"", "ab", "KM7QX2TR!", "KM7QX2TR0", "KM7QX2TRKM7QX2TRKM"} {
		if _, ok := normalizeCode(input); ok {
			t.Fatalf("normalizeCode accepted %q", input)
		}
	}
}

func TestRateLimitWindowIsDurableAndRolls(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "social.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	now := time.Now()
	store := New(db)
	store.Now = func() time.Time { return now }
	ctx := context.Background()
	attempt := func() error {
		gated, e := store.begin(ctx)
		if e != nil {
			return e
		}
		defer gated.Rollback()
		if e = store.limit(ctx, gated.Tx(), "bucket", 3, time.Hour); e != nil {
			return e
		}
		return gated.Commit()
	}
	for i := 0; i < 3; i++ {
		if e = attempt(); e != nil {
			t.Fatalf("attempt %d refused: %v", i, e)
		}
	}
	e = attempt()
	fault, ok := e.(*Fault)
	if !ok || fault.Code != "rate_limited" || fault.Status != 429 || fault.RetryAfter <= 0 {
		t.Fatalf("fourth attempt was not rate limited: %v", e)
	}
	// The window is stored, so a restart does not hand out a fresh allowance.
	restarted := New(db)
	restarted.Now = func() time.Time { return now }
	gated, e := restarted.begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	e = restarted.limit(ctx, gated.Tx(), "bucket", 3, time.Hour)
	gated.Rollback()
	if f, ok := e.(*Fault); !ok || f.Code != "rate_limited" {
		t.Fatalf("a restart reset the window: %v", e)
	}
	// The window rolls once it has passed.
	now = now.Add(time.Hour + time.Second)
	if e = attempt(); e != nil {
		t.Fatalf("window did not roll: %v", e)
	}
}

func TestExtrapolateOnlyAdvancesWhilePlaying(t *testing.T) {
	now := time.Now()
	store := New(nil)
	store.Now = func() time.Time { return now }
	base := record{itemID: "item", state: "playing", positionUS: 1_000_000, positionUpdatedMS: now.UnixMilli(), rateNum: 1, rateDen: 1}
	now = now.Add(2 * time.Second)
	if got := store.extrapolate(base); got != 3_000_000 {
		t.Fatalf("playing extrapolation = %d", got)
	}
	paused := base
	paused.state = "paused"
	if got := store.extrapolate(paused); got != 1_000_000 {
		t.Fatalf("paused extrapolation moved: %d", got)
	}
	// A reconnecting group whose playback continues keeps extrapolating; that is
	// what makes the pause boundary anchor at the right position.
	reconnecting := base
	reconnecting.state = "host-reconnecting-playing"
	if got := store.extrapolate(reconnecting); got != 3_000_000 {
		t.Fatalf("reconnecting extrapolation = %d", got)
	}
	// Double rate advances twice as fast; a group with no item has no clock.
	fast := base
	fast.rateNum = 2
	if got := store.extrapolate(fast); got != 5_000_000 {
		t.Fatalf("rate was ignored: %d", got)
	}
	empty := base
	empty.itemID = ""
	if got := store.extrapolate(empty); got != 0 {
		t.Fatalf("empty group had a clock: %d", got)
	}
}

// Watch Together is chatty by design: a heartbeat every ten seconds per member,
// a group read after most events, a queue read on queue events, a hand-off poll
// every second or two while one is open. Every one of those used to open a gated
// write transaction, so a six-person group idling in front of a paused film was
// a steady stream of transactions through the server's single writer — the same
// writer a sign-in and a playback control queue behind.
//
// This asserts the reads cost the gate nothing. It counts acquisitions rather
// than inspecting the code, because the property that matters is the one the
// gate sees.
func TestSocialReadsDoNotTakeTheWriteGate(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "social.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	now := time.Now()
	store := New(db)
	store.Now = func() time.Time { return now }
	ctx := context.Background()
	p := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "acct", ProfileID: "prof", ServerID: "server", Role: "owner"}, Hash: "hash", Epoch: 1}

	id := seedGroup(t, store, p)

	before := dbwork.WriteGate().Stats().Acquired
	for i := 0; i < 5; i++ {
		if _, e = store.ReadGroup(ctx, p, id); e != nil {
			t.Fatal(e)
		}
		if _, e = store.ListGroups(ctx, p); e != nil {
			t.Fatal(e)
		}
		if _, e = store.ReadQueue(ctx, p, id); e != nil {
			t.Fatal(e)
		}
		if _, _, e = store.EventsSince(ctx, p, id, 0); e != nil {
			t.Fatal(e)
		}
		if _, e = store.ListReceivers(ctx, p); e != nil {
			t.Fatal(e)
		}
	}
	if taken := dbwork.WriteGate().Stats().Acquired - before; taken != 0 {
		t.Fatalf("twenty-five reads took the write gate %d times", taken)
	}
}

// Expiry on read has to keep working: a member looking at a group whose host has
// gone away must see that, whether or not anybody has written since. The read
// computes it; the write records it.
func TestAReadShowsAnExpiredHostWithoutWritingIt(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "social.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	now := time.Now()
	store := New(db)
	store.Now = func() time.Time { return now }
	ctx := context.Background()
	p := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "acct", ProfileID: "prof", ServerID: "server", Role: "owner"}, Hash: "hash", Epoch: 1}
	id := seedGroup(t, store, p)

	// Past the grace period, but not past the pause boundary.
	now = now.Add(time.Duration(HostGraceSeconds+1) * time.Second)
	before := dbwork.WriteGate().Stats().Acquired
	read, e := store.ReadGroup(ctx, p, id)
	if e != nil {
		t.Fatal(e)
	}
	if !isReconnecting(read.Group.State) {
		t.Fatalf("a host past the grace period read as %q", read.Group.State)
	}
	if taken := dbwork.WriteGate().Stats().Acquired - before; taken != 0 {
		t.Fatalf("the read wrote the expiry it computed (%d gate acquisitions)", taken)
	}
	// And the row is genuinely untouched: the UPDATE belongs to the next writer.
	var stored string
	if e = db.QueryRow(`SELECT state FROM social_groups WHERE id=?`, id).Scan(&stored); e != nil {
		t.Fatal(e)
	}
	if isReconnecting(stored) {
		t.Fatalf("the read persisted the expiry: stored state %q", stored)
	}
	// The sweep is what records it, and it takes the gate exactly once for the
	// one group that is due.
	before = dbwork.WriteGate().Stats().Acquired
	if e = store.Sweep(ctx); e != nil {
		t.Fatal(e)
	}
	if taken := dbwork.WriteGate().Stats().Acquired - before; taken != 1 {
		t.Fatalf("a sweep with one due group took the gate %d times", taken)
	}
	if e = db.QueryRow(`SELECT state FROM social_groups WHERE id=?`, id).Scan(&stored); e != nil {
		t.Fatal(e)
	}
	if !isReconnecting(stored) {
		t.Fatalf("the sweep did not record the expiry: stored state %q", stored)
	}
	// A second sweep has nothing to do and must write nothing at all.
	before = dbwork.WriteGate().Stats().Acquired
	if e = store.Sweep(ctx); e != nil {
		t.Fatal(e)
	}
	if taken := dbwork.WriteGate().Stats().Acquired - before; taken != 0 {
		t.Fatalf("a sweep with nothing due took the gate %d times", taken)
	}
}

// One sweeper for the process, not one per open stream.
func TestTheSweeperIsHeldOnceForTheWholeProcess(t *testing.T) {
	previous := SweepInterval
	SweepInterval = 10 * time.Millisecond
	defer func() { SweepInterval = previous }()
	db, e := persistence.Open(filepath.Join(t.TempDir(), "social.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	store := New(db)
	if store.sweeperRunning() {
		t.Fatal("a sweeper was running before anybody held it")
	}
	first := store.HoldSweeper()
	second := store.HoldSweeper()
	third := store.HoldSweeper()
	if !store.sweeperRunning() {
		t.Fatal("holding did not start the sweeper")
	}
	first()
	// Releasing twice must not drop somebody else's hold.
	first()
	second()
	if !store.sweeperRunning() {
		t.Fatal("the sweeper stopped while a stream still held it")
	}
	third()
	if store.sweeperRunning() {
		t.Fatal("the last release did not stop the sweeper")
	}
}

// A live group keeps its host timeline advancing with nobody watching: the
// sweeper runs while any live group exists, not only while a stream holds it,
// and it stops itself when the last live group ends.
func TestLiveGroupsHoldSweeperWithoutStreams(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "social.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	now := time.Now()
	store := New(db)
	store.Now = func() time.Time { return now }
	ctx := context.Background()
	p := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "acct", ProfileID: "prof", ServerID: "server", Role: "owner"}, Hash: "hash", Epoch: 1}

	created, e := store.CreateGroup(ctx, p, "dev-test", CreateGroupRequest{ProtocolVersion: Protocol, Name: "Film night", DisplayName: "Host", HostAuthority: "host-only"})
	if e != nil {
		t.Fatal(e)
	}
	id := created.Group.ID
	if !store.sweeperRunning() {
		t.Fatal("creating a live group did not start the sweeper with no stream held")
	}
	// Past the pause boundary the group pauses with nobody watching.
	now = now.Add(time.Duration(HostPauseSeconds+1) * time.Second)
	if e = store.Sweep(ctx); e != nil {
		t.Fatal(e)
	}
	var stored string
	if e = db.QueryRow(`SELECT state FROM social_groups WHERE id=?`, id).Scan(&stored); e != nil {
		t.Fatal(e)
	}
	if stored != "host-reconnecting-paused" {
		t.Fatalf("pause boundary did not fire without a stream: stored state %q", stored)
	}
	if !store.sweeperRunning() {
		t.Fatal("the sweeper stopped while a live group remained")
	}
	// Past the end boundary it ends, and the sweeper stops itself.
	now = now.Add(time.Duration(HostEndSeconds+1) * time.Second)
	if e = store.Sweep(ctx); e != nil {
		t.Fatal(e)
	}
	if e = db.QueryRow(`SELECT state FROM social_groups WHERE id=?`, id).Scan(&stored); e != nil {
		t.Fatal(e)
	}
	if stored != "ended" {
		t.Fatalf("end boundary did not fire without a stream: stored state %q", stored)
	}
	if store.sweeperRunning() {
		t.Fatal("the sweeper kept running after the last live group ended")
	}
}

// A restart resumes the timeline: a live group that survives the restart still
// advances with no stream open.
func TestSweeperResumesAfterRestart(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "social.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	now := time.Now()
	store := New(db)
	store.Now = func() time.Time { return now }
	ctx := context.Background()
	p := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "acct", ProfileID: "prof", ServerID: "server", Role: "owner"}, Hash: "hash", Epoch: 1}
	id := seedGroup(t, store, p)
	// A fresh Store over the same database, as after a restart, starts the
	// sweeper when a live group exists.
	restarted := New(db)
	restarted.Now = func() time.Time { return now }
	if restarted.sweeperRunning() {
		t.Fatal("a sweeper was running before resume")
	}
	if e = restarted.ResumeSweeperIfLive(ctx); e != nil {
		t.Fatal(e)
	}
	if !restarted.sweeperRunning() {
		t.Fatal("resume did not start the sweeper for a live group")
	}
	// With no live group it starts nothing and runs no query loop.
	if _, e = db.Exec(`UPDATE social_groups SET state='ended' WHERE id=?`, id); e != nil {
		t.Fatal(e)
	}
	stopped := New(db)
	stopped.Now = func() time.Time { return now }
	if e = stopped.ResumeSweeperIfLive(ctx); e != nil {
		t.Fatal(e)
	}
	if stopped.sweeperRunning() {
		t.Fatal("resume started a sweeper with no live group")
	}
	// Release the resumed hold so the test leaves no goroutine behind: ending
	// the group lets the next sweep stop it.
	now = now.Add(time.Duration(HostEndSeconds+1) * time.Second)
	if e = restarted.Sweep(ctx); e != nil {
		t.Fatal(e)
	}
	if restarted.sweeperRunning() {
		t.Fatal("the resumed sweeper did not stop after its group ended")
	}
}

// seedGroup writes one live group with the caller as its host. CreateGroup needs
// the playback control plane to bind a command lane, which has nothing to do
// with what these tests are about.
func seedGroup(t *testing.T, store *Store, p identity.Principal) string {
	t.Helper()
	now := store.ms()
	id, member := "grp_seeded", "mem_seeded"
	if _, e := store.DB.Exec(`INSERT INTO social_groups(id,name,authority,account_id,profile_id,host_member_id,host_authority,state,pause_after_seconds,end_after_seconds,host_seen_ms,position_updated_ms,created_at_ms,updated_at_ms) VALUES(?,?,?,?,?,?,'host-only','playing',?,?,?,?,?,?)`,
		id, "Film night", p.Authority, p.AccountID, p.ProfileID, member, HostPauseSeconds, HostEndSeconds, now, now, now, now); e != nil {
		t.Fatal(e)
	}
	if _, e := store.DB.Exec(`INSERT INTO social_group_members(id,group_id,authority,account_id,profile_id,display_name,role,state,readiness,readiness_at_ms,joined_at_ms) VALUES(?,?,?,?,?,?,'host','joined','ready',?,?)`,
		member, id, p.Authority, p.AccountID, p.ProfileID, "Host", now, now); e != nil {
		t.Fatal(e)
	}
	return id
}
