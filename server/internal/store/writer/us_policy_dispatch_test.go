package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

const usPolicyIssuerSecondaryProfile = "issuer-profile-2"

// Synthetic harness only. US-01 still mints the authority stamp after its own
// current-source checks; nothing here constructs a production issuer, registers
// a product command or qualifies a hold. The executed SQL is the real closed
// statement set on the canonical schema.
func usPolicyDispatchFixture(t *testing.T, kind CommandID, libraries []string) (w *executor, path string, r *stmt.Registry, proof *usOwnerAuthorityProof, command *usLibraryPolicyCommand) {
	t.Helper()
	return usPolicyDispatchFixtureIssuer(t, kind, libraries, nil)
}

func usPolicyDispatchFixtureIssuer(t *testing.T, kind CommandID, libraries []string, issuer func(*usPolicyIssuer)) (w *executor, path string, r *stmt.Registry, proof *usOwnerAuthorityProof, command *usLibraryPolicyCommand) {
	t.Helper()
	// The issuer session and its proof come from the reviewed US-01 fixture, so
	// this task adds no new producer of its own for the ordinary family/device.
	w, _, proof = usOwnerAuthorityFixtureIssuer(t, issuer)
	// The writer's own file, not a second clone: an injected physical fault must
	// land in the same database this turn opened.
	if err := w.db.QueryRowContext(t.Context(), `SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	identity, request := usPolicyFixture()
	if issuer != nil {
		issuer(&request.issuer)
	}
	if kind == USProfileLibraries {
		// The profile-manager recipe edits the issuer's OWN account, never a
		// cross-account profile.
		request.account, request.memberIncarnation = request.issuer.account, request.issuer.memberIncarnation
		request.profile, request.profileLifetime = usPolicyIssuerSecondaryProfile, strings.Repeat("d", 32)
	} else {
		request.account, request.memberIncarnation = "target", usPolicyMemberLifetime
	}
	request.libraries = libraries
	var err error
	command, err = prepareUSLibraryPolicy(kind, identity, request)
	if err != nil {
		t.Fatal(err)
	}
	i := request.issuer
	wc008NativeExec(t, w, `INSERT INTO identity_sign_in_policy(singleton) VALUES(1) ON CONFLICT(singleton) DO NOTHING`)
	if kind == USMemberLibraries {
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('target','target',X'','target-profile')`, nil},
			{`INSERT INTO direct_memberships(account_id,incarnation,role,allowed_libraries) VALUES('target',?,'member','["movies"]')`, []any{usPolicyMemberLifetime}},
			{`INSERT INTO direct_profiles(id,account_id,lifetime,name,is_primary,allowed_libraries) VALUES('target-profile','target',?,'Target',1,'[]')`, []any{usPolicyProfileLife}},
			{`INSERT INTO direct_profiles(id,account_id,lifetime,name,is_primary,position,allowed_libraries) VALUES('target-second','target',?,'Second',0,2,NULL)`, []any{strings.Repeat("1", 32)}},
		} {
			if _, err = w.db.ExecContext(t.Context(), q.sql, q.args...); err != nil {
				t.Fatal(q.sql, err)
			}
		}
	} else {
		if _, err = w.db.ExecContext(t.Context(), `INSERT INTO direct_profiles(id,account_id,lifetime,name,is_primary,position,allowed_libraries) VALUES(?,?,?,'Second',0,2,'["music"]')`, usPolicyIssuerSecondaryProfile, i.account, request.profileLifetime); err != nil {
			t.Fatal(err)
		}
	}
	wc008NativeExec(t, w, `INSERT INTO security_command_owners(id,command_incarnation,ingress,server_id,issuer_account_id,issuer_member_incarnation,issuer_profile_id,issuer_profile_lifetime,issuer_installation_incarnation,original_family_id,created_ms) VALUES(?,?,'authenticated',?,?,?,?,?,?,?,0)`,
		request.ownerID, identity.Incarnation[:], i.server, i.account, i.memberIncarnation, i.profile, i.profileLifetime, i.installation, i.family)
	defs := append([]stmt.Definition(nil), usAuthorityDefinitions()...)
	defs = append(defs, usPolicyDefinitions()...)
	defs = append(defs, usPolicyEffectDefinitions()...)
	defs = append(defs, usPolicyReceiptDefinitions()...)
	defs = append(defs, usPolicyDispatchDefinitions()...)
	defs = append(defs, wcAPIEventDefinitions()...)
	r, err = stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	return w, path, r, proof, command
}

func usPolicyDispatchTurn(t *testing.T, w *executor, r *stmt.Registry, proof *usOwnerAuthorityProof, kind CommandID, memory uint64) (*commandTurn, *usOwnerAuthorization, context.CancelFunc) {
	t.Helper()
	if memory == 0 {
		memory = 4 << 20
	}
	recipe := testRecipe()
	recipe.limit = Work{100000, 10_000_000, 100000, 4 << 20, 20 * time.Millisecond}
	tx, err := w.begin(t.Context(), kind, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	var steps []statementStep
	for _, d := range r.Manifest() {
		steps = append(steps, statementStep{d.ID, Work{8, 5000, 8, 4096, 10 * time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, r, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = turn.closeRows() })
	lifetime, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	budget := usOwnerAuthorityBudget{memory: memory, fixture: &usOwnerAuthorityFixturePermit{frame: tx}}
	a, err := authorizeUSOwnerPolicy(t.Context(), turn, proof, lifetime, budget)
	if err != nil {
		t.Fatal(err)
	}
	return turn, a, cancel
}

func usPolicyDispatchInput(t *testing.T, c *usLibraryPolicyCommand) *usPolicyInvocation {
	t.Helper()
	q, err := usPolicyReceiptForCommand(c)
	if err != nil {
		t.Fatal(err)
	}
	return &usPolicyInvocation{request: q, input: c}
}

func usPolicyDispatchQuery(t *testing.T, w *executor, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := w.db.QueryRowContext(t.Context(), q, args...).Scan(&n); err != nil {
		t.Fatal(q, err)
	}
	return n
}

func usPolicyDispatchSource(t *testing.T, w *executor, kind CommandID) (revision int64, libraries sql.NullString, events int64) {
	t.Helper()
	if kind == USMemberLibraries {
		if err := w.db.QueryRowContext(t.Context(), `SELECT revision,allowed_libraries FROM direct_memberships WHERE account_id='target'`).Scan(&revision, &libraries); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := w.db.QueryRowContext(t.Context(), `SELECT revision,allowed_libraries FROM direct_profiles WHERE account_id='issuer' AND id=?`, usPolicyIssuerSecondaryProfile).Scan(&revision, &libraries); err != nil {
			t.Fatal(err)
		}
	}
	events = usPolicyDispatchQuery(t, w, `SELECT count(*) FROM api_events`)
	return revision, libraries, events
}

func usPolicyDispatchReplay(t *testing.T, w *executor, r *stmt.Registry, proof *usOwnerAuthorityProof, kind CommandID, inv *usPolicyInvocation) (usPolicyDispatchResult, AckResult) {
	t.Helper()
	turn, a, _ := usPolicyDispatchTurn(t, w, r, proof, kind, 0)
	out, floor, err := runUSPolicyDispatch(t.Context(), turn, a, inv)
	if err != nil {
		t.Fatal(err)
	}
	return out, floor
}

// Original, then replay/Resolve of the same identity, then the floor-only ACK.
func TestUSPolicyDispatchPhysicalOriginalReplayAndACK(t *testing.T) {
	w, _, r, proof, input := usPolicyDispatchFixture(t, USMemberLibraries, []string{"movies", "music"})
	inv := usPolicyDispatchInput(t, input)
	turn, a, _ := usPolicyDispatchTurn(t, w, r, proof, input.kind, 0)
	out, _, err := runUSPolicyDispatch(t.Context(), turn, a, inv)
	if err != nil {
		t.Fatal(err)
	}
	if out.Outcome.State != Applied || out.Outcome.Replayed || out.Value.revision != 2 || !out.Value.changed || out.Digest == (ResultDigest{}) {
		t.Fatal("original was not the closed accepted result", out)
	}
	if !reflect.DeepEqual(out.Value.libraries, []string{"movies", "music"}) || out.ChargedBytes <= 0 || out.EventID <= 0 || out.OwnerCount != 1 || out.OwnerBytes != out.ChargedBytes {
		t.Fatal("original accounting", out)
	}
	// 540's product response is empty/204: no original-response image at all.
	if out.Image.Snapshot != nil || out.ImageBytes != 0 {
		t.Fatal("540 returned an original-response image", out.Image)
	}
	revision, libraries, events := usPolicyDispatchSource(t, w, USMemberLibraries)
	if revision != 2 || libraries.String != `["movies","music"]` || events != 1 {
		t.Fatal("original source/event effects", revision, libraries, events)
	}
	if n := usPolicyDispatchQuery(t, w, `SELECT count(*) FROM api_events WHERE type='authority.changed' AND audience='authority:local:target' AND resource_kind='account' AND resource_id='self' AND revision='2' AND data=''`); n != 1 {
		t.Fatal("scoped hint", n)
	}
	if usPolicyDispatchQuery(t, w, `SELECT retained_count FROM security_command_owners`); usPolicyDispatchQuery(t, w, `SELECT count(*) FROM security_command_receipts`) != 1 ||
		usPolicyDispatchQuery(t, w, `SELECT count(*) FROM security_result_libraries`) != 2 {
		t.Fatal("receipt cohort", out)
	}

	// Replay: the SAME identity again, now without its preparation. Zero new
	// effects and no source-derived image.
	before := usPolicyDispatchQuery(t, w, `SELECT total_changes()`)
	replay, _ := usPolicyDispatchReplay(t, w, r, proof, input.kind, &usPolicyInvocation{request: inv.request})
	if replay.Outcome.State != Applied || !replay.Outcome.Replayed || replay.Digest != out.Digest || !reflect.DeepEqual(replay.Value, out.Value) {
		t.Fatal("replay did not return the saved original", replay)
	}
	if replay.Image.Snapshot != nil || replay.ImageBytes != 0 || replay.ChargedBytes != 0 || replay.EventID != 0 {
		t.Fatal("replay rebuilt a response image or charged again", replay)
	}
	if after := usPolicyDispatchQuery(t, w, `SELECT total_changes()`); after != before {
		t.Fatal("replay made new effects", before, after)
	}
	// Resolve with the original preparation present is still a replay.
	resolve, _ := usPolicyDispatchReplay(t, w, r, proof, input.kind, inv)
	if resolve.Outcome.State != Applied || !resolve.Outcome.Replayed || !reflect.DeepEqual(resolve.Value, out.Value) {
		t.Fatal("resolve with preparation re-executed", resolve)
	}
	if _, _, events = usPolicyDispatchSource(t, w, USMemberLibraries); events != 1 {
		t.Fatal("resolve appended an event", events)
	}

	// Floor-only ACK: it advances one sequence and refunds nothing.
	turn, a, _ = usPolicyDispatchTurn(t, w, r, proof, input.kind, 0)
	ack, floor, err := runUSPolicyDispatch(t.Context(), turn, a, &usPolicyInvocation{request: inv.request, ack: true})
	if err != nil || floor.AcknowledgedThrough != 1 || !reflect.DeepEqual(ack, usPolicyDispatchResult{}) {
		t.Fatal("ACK", ack, floor, err)
	}
	if usPolicyDispatchQuery(t, w, `SELECT acknowledged_sequence FROM security_command_owners`) != 1 ||
		usPolicyDispatchQuery(t, w, `SELECT retained_count FROM security_command_owners`) != 1 ||
		usPolicyDispatchQuery(t, w, `SELECT retained_variable_bytes FROM security_command_owners`) != out.ChargedBytes {
		t.Fatal("ACK refunded or discarded the retained bytes")
	}
	if usPolicyDispatchQuery(t, w, `SELECT count(*) FROM security_command_receipts`) != 1 || usPolicyDispatchQuery(t, w, `SELECT count(*) FROM security_result_libraries`) != 2 {
		t.Fatal("ACK physically removed the cohort")
	}
	// The floor is idempotent and the original stays retired, never Applied.
	retired, _ := usPolicyDispatchReplay(t, w, r, proof, input.kind, &usPolicyInvocation{request: inv.request})
	if retired.Outcome.State != ResultRetired || retired.Value.ownerID != "" {
		t.Fatal("ACKed original was served as Applied", retired)
	}
	turn, a, _ = usPolicyDispatchTurn(t, w, r, proof, input.kind, 0)
	if _, floor, err = runUSPolicyDispatch(t.Context(), turn, a, &usPolicyInvocation{request: inv.request, ack: true}); err != nil || floor.AcknowledgedThrough != 1 {
		t.Fatal("ACK was not idempotent", floor, err)
	}
}

// A no-change command still saves its original receipt but writes no source,
// bumps no revision and appends NO event.
func TestUSPolicyDispatchNoOpHasNoEventOrRevision(t *testing.T) {
	w, _, r, proof, input := usPolicyDispatchFixture(t, USMemberLibraries, []string{"movies"})
	turn, a, _ := usPolicyDispatchTurn(t, w, r, proof, input.kind, 0)
	out, _, err := runUSPolicyDispatch(t.Context(), turn, a, usPolicyDispatchInput(t, input))
	if err != nil {
		t.Fatal(err)
	}
	if out.Outcome.State != Applied || out.Value.changed || out.Value.revision != 1 || out.EventID != 0 || out.EventBytes != 0 {
		t.Fatal("no-op outcome", out)
	}
	revision, libraries, events := usPolicyDispatchSource(t, w, USMemberLibraries)
	if revision != 1 || libraries.String != `["movies"]` || events != 0 {
		t.Fatal("no-op wrote source or an event", revision, libraries, events)
	}
	if usPolicyDispatchQuery(t, w, `SELECT count(*) FROM security_command_receipts`) != 1 || out.ChargedBytes <= 0 {
		t.Fatal("no-op receipt missing", out)
	}
	// The unchanged original replays exactly.
	replay, _ := usPolicyDispatchReplay(t, w, r, proof, input.kind, &usPolicyInvocation{request: usPolicyDispatchInput(t, input).request})
	if replay.Outcome.State != Applied || !replay.Outcome.Replayed || replay.Value.changed || !reflect.DeepEqual(replay.Value, out.Value) {
		t.Fatal("no-op replay", replay)
	}
}

// A 540 member result never inherits: a nil requested list denies, and the
// saved/replayed cohort is the explicit empty list, not nil.
func TestUSPolicyDispatchMemberNilSavesExplicitEmptyList(t *testing.T) {
	w, _, r, proof, input := usPolicyDispatchFixture(t, USMemberLibraries, nil)
	turn, a, _ := usPolicyDispatchTurn(t, w, r, proof, input.kind, 0)
	out, _, err := runUSPolicyDispatch(t.Context(), turn, a, usPolicyDispatchInput(t, input))
	if err != nil {
		t.Fatal(err)
	}
	if out.Value.inherits || out.Value.libraries == nil || len(out.Value.libraries) != 0 || !out.Value.changed {
		t.Fatal("member deny saved an inherit or nil cohort", out.Value)
	}
	if n := usPolicyDispatchQuery(t, w, `SELECT count(*) FROM direct_memberships WHERE account_id='target' AND allowed_libraries='[]'`); n != 1 {
		t.Fatal("source did not receive the explicit empty list", n)
	}
	replay, _ := usPolicyDispatchReplay(t, w, r, proof, input.kind, &usPolicyInvocation{request: usPolicyDispatchInput(t, input).request})
	if replay.Outcome.State != Applied || replay.Value.inherits || replay.Value.libraries == nil || len(replay.Value.libraries) != 0 {
		t.Fatal("replayed member deny lost its explicit empty list", replay.Value)
	}
}

// 541 is read live on the profile's next request: no event, and its original
// response is the CALLER's complete post-effect DirectSnapshot (never the edited
// target's MemberProfile and never a 540 member snapshot).
func TestUSPolicyDispatchProfilePolicyHasNoEvent(t *testing.T) {
	for _, mode := range []struct {
		name      string
		libraries []string
		inherits  bool
		imageList []string
	}{
		{"explicit", []string{"movies"}, false, []string{"movies"}},
		{"inherit", nil, true, []string{}},
		{"deny", []string{}, false, []string{}},
	} {
		t.Run(mode.name, func(t *testing.T) {
			w, _, r, proof, input := usPolicyDispatchFixture(t, USProfileLibraries, mode.libraries)
			turn, a, _ := usPolicyDispatchTurn(t, w, r, proof, input.kind, 0)
			out, _, err := runUSPolicyDispatch(t.Context(), turn, a, usPolicyDispatchInput(t, input))
			if err != nil {
				t.Fatal(err)
			}
			if out.Outcome.State != Applied || out.Value.revision != 2 || !out.Value.changed || out.Value.inherits != mode.inherits || out.EventID != 0 {
				t.Fatal("profile policy outcome", out)
			}
			if len(out.Value.libraries) != len(mode.libraries) {
				t.Fatal("result libraries", out.Value.libraries)
			}
			if out.Image.Snapshot == nil || out.ImageBytes <= 0 {
				t.Fatal("541 must return the caller snapshot", out.Image)
			}
			image := out.Image.Snapshot
			if image.Authority != "local" || image.ServerID != "server" || image.Account.ID != "issuer" ||
				image.Account.PrimaryProfileID != "issuer-profile" || image.Account.Role != "owner" || image.Account.Revision != 1 ||
				image.Account.Disabled || !reflect.DeepEqual(image.Account.AllowedLibraries, []string{}) ||
				!image.CanManage || image.Capabilities != (USDirectCapabilitiesImage{ManageAccount: true, ManagePeople: true, ManageServer: true, ManageSignIn: true}) ||
				image.TwoStepSetupRequired || image.CustodyPending || !image.RememberProfiles || image.ManagePINRequired {
				t.Fatalf("541 caller snapshot is not the product image: %+v", image)
			}
			if len(image.Profiles) != 2 || image.Profiles[0].ID != "issuer-profile" || !image.Profiles[0].Primary ||
				image.Profiles[1].ID != usPolicyIssuerSecondaryProfile || image.Profiles[1].Primary ||
				image.Profiles[1].Revision != 2 || !reflect.DeepEqual(image.Profiles[1].AllowedLibraries, mode.imageList) {
				t.Fatalf("541 caller profiles: %+v", image.Profiles)
			}
			var events int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM api_events`).Scan(&events); err != nil || events != 0 {
				t.Fatal("profile policy appended an event", events, err)
			}
			if mode.inherits {
				if n := usPolicyDispatchQuery(t, w, `SELECT allowed_libraries IS NULL FROM direct_profiles WHERE id=?`, usPolicyIssuerSecondaryProfile); n != 1 {
					t.Fatal("inherit did not save SQL NULL")
				}
			}
			// A replay returns the retained original and constructs no image.
			turn, a, _ = usPolicyDispatchTurn(t, w, r, proof, input.kind, 0)
			for id := range turn.steps {
				switch id {
				case usPolicyImageAccountOctets, usPolicyImageFamilyOctets, usPolicyImagePolicyOctets, usPolicyImageHostedOctets,
					usPolicyImageCustodianOctets, usPolicyImageProfilesOctets, usPolicyImageAccount, usPolicyImageFamily,
					usPolicyImagePolicy, usPolicyImageFactor, usPolicyImageHosted, usPolicyImageCustodian, usPolicyImageProfiles:
					delete(turn.steps, id)
				}
			}
			replay, _, err := runUSPolicyDispatch(t.Context(), turn, a, &usPolicyInvocation{request: usPolicyDispatchInput(t, input).request})
			if err != nil || replay.Outcome.State != Applied || !replay.Outcome.Replayed || replay.Image.Snapshot != nil ||
				replay.ImageBytes != 0 || !reflect.DeepEqual(replay.Value, out.Value) {
				t.Fatal("replay reconstructed an image", replay, err)
			}
		})
	}
}

// Identity Token() values are 32 random bytes in base64.RawURLEncoding: 43
// octets. Real server/account IDs are that shape, while the image adds
// Authority/ServerID/Account.ID that no source column preflights; an ordinary
// 541 must still capture instead of refusing after the effect.
func TestUSPolicyDispatchTokenSizedSnapshot(t *testing.T) {
	serverID, accountID := strings.Repeat("s", 43), strings.Repeat("a", 43)
	w, _, r, proof, input := usPolicyDispatchFixtureIssuer(t, USProfileLibraries, []string{"movies"}, func(i *usPolicyIssuer) {
		i.server, i.account = serverID, accountID
	})
	turn, a, _ := usPolicyDispatchTurn(t, w, r, proof, input.kind, 0)
	out, _, err := runUSPolicyDispatch(t.Context(), turn, a, usPolicyDispatchInput(t, input))
	if err != nil {
		t.Fatal("volume bound refused a Token-sized identity", err)
	}
	snapshot := out.Image.Snapshot
	if out.Outcome.State != Applied || snapshot == nil || out.ImageBytes <= 0 {
		t.Fatal("Token-sized 541 did not capture", out)
	}
	if snapshot.Authority != "local" || snapshot.ServerID != serverID || snapshot.Account.ID != accountID ||
		snapshot.Account.PrimaryProfileID != "issuer-profile" || !snapshot.CanManage {
		t.Fatalf("Token-sized snapshot: %+v", snapshot)
	}
	if image, e := USDirectSnapshotImageOctets(*snapshot); e != nil || image <= 0 {
		t.Fatal("image octets", image, e)
	}
}

// Under the US-01 owner stamp the family is always role='owner' and the issuer
// profile is PIN-free (usAuthorityFamily requires f.role='owner';
// usAuthorityPrincipal requires p.pin_hash IS NULL). The PIN, account-scope and
// two-step branches therefore cannot be reached through runUSPolicyDispatch;
// exercise the pure product-rule helper directly instead.
func TestUSPolicyDispatchSnapshotManagement(t *testing.T) {
	const now, later, past = "2026-11-08T12:00:00Z", "2026-11-08T12:30:00Z", "2026-11-08T11:00:00Z"
	for _, mode := range []struct {
		name                string
		familyRole          string
		primary, pin        bool
		required, confirmed bool
		remember, manage    string
		canManage, twoStep  bool
		managePIN           bool
	}{
		{"owner_without_pin", "owner", true, false, false, false, "", "", true, false, false},
		{"account_scope_pin_blocks_manage", "account", true, true, false, false, "", "", false, false, false},
		{"pin_window_open", "owner", true, true, false, false, later, later, true, false, false},
		{"pin_manage_lapsed", "owner", true, true, false, false, later, past, true, false, true},
		{"pin_empty_manage_until_is_lapsed", "owner", true, true, false, false, later, "", true, false, true},
		{"pin_remember_expired", "owner", true, true, false, false, past, past, true, false, false},
		{"two_step_missing_factor", "account", true, false, true, false, "", "", false, true, false},
		{"two_step_factor_confirmed", "account", true, false, true, true, "", "", true, false, false},
		{"two_step_not_required", "account", true, false, false, false, "", "", true, false, false},
	} {
		t.Run(mode.name, func(t *testing.T) {
			canManage, twoStep, managePIN := usPolicySnapshotManage(mode.familyRole, mode.primary, mode.pin,
				mode.required, mode.confirmed, mode.remember, mode.manage, now)
			if canManage != mode.canManage || twoStep != mode.twoStep || managePIN != mode.managePIN {
				t.Fatalf("management facts: canManage=%v twoStep=%v managePIN=%v", canManage, twoStep, managePIN)
			}
			want := USDirectCapabilitiesImage{}
			if canManage {
				want = USDirectCapabilitiesImage{ManageAccount: true, ManagePeople: true, ManageServer: true, ManageSignIn: true}
			}
			if caps := usPolicySnapshotCapabilities(canManage, "owner"); caps != want {
				t.Fatalf("capabilities: %+v", caps)
			}
		})
	}
	for _, mode := range []struct {
		name                       string
		requireTwoStep, kind, role string
		required                   bool
	}{
		{"everyone_member", "everyone", "local", "member", true},
		{"administrators_owner", "administrators", "local", "owner", true},
		{"administrators_member", "administrators", "local", "member", false},
		{"nobody_owner", "nobody", "local", "owner", false},
		{"portico_everyone_owner", "everyone", "portico", "owner", false},
	} {
		if got := usPolicyTwoStepRequired(mode.requireTwoStep, mode.kind, mode.role); got != mode.required {
			t.Fatalf("%s: two-step required = %v", mode.name, got)
		}
	}
}

// A stale proof preparation cannot execute: the fresh CAS is bound to the exact
// expected revision.
func TestUSPolicyDispatchStaleRevisionConflicts(t *testing.T) {
	w, _, r, proof, input := usPolicyDispatchFixture(t, USMemberLibraries, []string{"movies", "music"})
	wc008NativeExec(t, w, `UPDATE direct_memberships SET revision=2 WHERE account_id='target'`)
	turn, a, _ := usPolicyDispatchTurn(t, w, r, proof, input.kind, 0)
	out, _, err := runUSPolicyDispatch(t.Context(), turn, a, usPolicyDispatchInput(t, input))
	if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, usPolicyDispatchResult{}) {
		t.Fatal("stale revision", out, err)
	}
	revision, _, events := usPolicyDispatchSource(t, w, USMemberLibraries)
	if revision != 2 || events != 0 || usPolicyDispatchQuery(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 {
		t.Fatal("conflicting original wrote a prefix", revision, events)
	}
}

// Receipt failure after a nonempty prefix rolls the whole domain prefix back.
func TestUSPolicyDispatchEventAndReceiptFailureRollsBack(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remove func(*commandTurn)
	}{
		{"receipt_owner_advance", func(turn *commandTurn) { delete(turn.steps, usPolicyOwnerAdvance) }},
		{"event_insert", func(turn *commandTurn) { delete(turn.steps, wcAPIEventInsert) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, _, r, proof, input := usPolicyDispatchFixture(t, USMemberLibraries, []string{"movies", "music"})
			turn, a, _ := usPolicyDispatchTurn(t, w, r, proof, input.kind, 0)
			tc.remove(turn)
			if out, _, err := runUSPolicyDispatch(t.Context(), turn, a, usPolicyDispatchInput(t, input)); err == nil || !reflect.DeepEqual(out, usPolicyDispatchResult{}) {
				t.Fatal("partial prefix escaped", out, err)
			}
			revision, libraries, events := usPolicyDispatchSource(t, w, USMemberLibraries)
			if revision != 1 || libraries.String != `["movies"]` || events != 0 {
				t.Fatal("source effect survived rollback", revision, libraries)
			}
			if usPolicyDispatchQuery(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 || usPolicyDispatchQuery(t, w, `SELECT count(*) FROM security_result_libraries`) != 0 {
				t.Fatal("receipt survived rollback")
			}
			if usPolicyDispatchQuery(t, w, `SELECT last_sequence FROM security_command_owners`) != 0 || usPolicyDispatchQuery(t, w, `SELECT retained_variable_bytes FROM security_command_owners`) != 0 {
				t.Fatal("owner debt survived rollback")
			}
		})
	}
}

// An uncertain COMMIT is never Applied. The driver boundary is the physical
// truth: the SQLite COMMIT either reached the database (after) or did not
// (before), and in both cases the caller only learns to resolve the ORIGINAL.
func TestUSPolicyDispatchUnknownCommitNeverApplied(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "after"}[after], func(t *testing.T) {
			w, path, r, proof, input := usPolicyDispatchFixture(t, USMemberLibraries, []string{"movies", "music"})
			connector, err := sqliteconn.Connector(path, w.config, sqliteconn.ExistingFile)
			if err != nil {
				t.Fatal(err)
			}
			fault := &faultConnector{Connector: connector, replyError: true, afterCommit: after}
			native := false
			fault.afterNativeCommit = func() { native = true }
			installFaultPool(w, fault)
			turn, a, _ := usPolicyDispatchTurn(t, w, r, proof, input.kind, 0)
			out, floor, err := runUSPolicyDispatch(t.Context(), turn, a, usPolicyDispatchInput(t, input))
			if !errors.Is(err, ErrResolveRequired) || out.Outcome.State == Applied || !reflect.DeepEqual(out, usPolicyDispatchResult{}) || floor.AcknowledgedThrough != 0 {
				t.Fatal("uncertain commit exposed success", out, floor, err)
			}
			if !w.ambiguous || !w.fenced {
				t.Fatal("uncertain writer was not fenced")
			}
			// The injected reply failed BEFORE the native COMMIT in one case and
			// AFTER it in the other; only that physical difference changes.
			if fault.commits.Load() != 1 || native != after {
				t.Fatal("physical COMMIT disposition changed", fault.commits.Load(), native, after)
			}
			// A quarantined writer refuses every further turn: the same original
			// must be resolved, never re-executed blind.
			if _, err := w.begin(t.Context(), input.kind, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
				t.Fatal("fenced writer admitted another turn", err)
			}
		})
	}
}

// A denial at the final release REPLACES the body outcome and rolls everything
// back: the accepted prefix never survives.
func TestUSPolicyDispatchCanceledReleaseDeniesAndRollsBack(t *testing.T) {
	// Canceled preparation: the entry recheck denies before any read.
	w, _, r, proof, input := usPolicyDispatchFixture(t, USMemberLibraries, []string{"movies", "music"})
	turn, a, cancel := usPolicyDispatchTurn(t, w, r, proof, input.kind, 0)
	cancel()
	if out, _, err := runUSPolicyDispatch(t.Context(), turn, a, usPolicyDispatchInput(t, input)); !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, usPolicyDispatchResult{}) {
		t.Fatal("canceled preparation was accepted", out, err)
	}
	revision, libraries, events := usPolicyDispatchSource(t, w, USMemberLibraries)
	if revision != 1 || libraries.String != `["movies"]` || events != 0 {
		t.Fatal("canceled preparation wrote source", revision, libraries, events)
	}

	// A denial that arrives only at the final release. The virtual clock is
	// installed AFTER the turn is minted, so the entry checks stay valid. The
	// probe counts the clock samples of one complete dispatch; the final
	// disclosure is the sixth sample from the end (the terminal's own samples
	// follow it), so expiring the authority there leaves a composed prefix that
	// the release must roll back. The step is small enough to stay inside the
	// runtime safety ceiling, so this is a denial, not an overrun.
	w, _, r, proof, input = usPolicyDispatchFixture(t, USMemberLibraries, []string{"movies", "music"})
	probe, probeAuth, _ := usPolicyDispatchTurn(t, w, r, proof, input.kind, 0)
	probeClock := usPolicyStepClockOn(w, 0, 0)
	if _, _, err := runUSPolicyDispatch(t.Context(), probe, probeAuth, usPolicyDispatchInput(t, input)); err != nil {
		t.Fatal("probe run failed", err)
	}
	total := probeClock.calls
	if total <= 6 {
		t.Fatalf("dispatch sampled too few instants: %d", total)
	}
	w, _, r, proof, input = usPolicyDispatchFixture(t, USMemberLibraries, []string{"movies", "music"})
	deniedTurn, deniedAuth, _ := usPolicyDispatchTurn(t, w, r, proof, input.kind, 0)
	// The captured proof is what every recheck re-runs; shortening it AFTER the
	// entry authorization is the only way to expire it mid-frame.
	deniedAuth.proof.tokenExpiry = w.now().Add(time.Second)
	usPolicyStepClockOn(w, total-6, 2*time.Second)
	out, _, err := runUSPolicyDispatch(t.Context(), deniedTurn, deniedAuth, usPolicyDispatchInput(t, input))
	if !errors.Is(err, ErrDisclosureDenied) || errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, usPolicyDispatchResult{}) {
		t.Fatal("release denial joined or missed", out, err)
	}
	deniedRevision, deniedLibraries, deniedEvents := usPolicyDispatchSource(t, w, USMemberLibraries)
	if deniedRevision != 1 || deniedLibraries.String != `["movies"]` || deniedEvents != 0 {
		t.Fatal("release denial left a source/event prefix", deniedRevision, deniedLibraries)
	}
	if usPolicyDispatchQuery(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 || usPolicyDispatchQuery(t, w, `SELECT last_sequence FROM security_command_owners`) != 0 {
		t.Fatal("release denial left a receipt prefix")
	}
}

// The retained owner quota refuses a fresh result BEFORE any source effect.
func TestUSPolicyDispatchReceiptQuotaRefusesBeforeEffects(t *testing.T) {
	w, _, r, proof, input := usPolicyDispatchFixture(t, USMemberLibraries, []string{"movies", "music"})
	wc008NativeExec(t, w, `UPDATE security_command_owners SET last_sequence=1,acknowledged_sequence=1,retained_count=1,retained_variable_bytes=?`, usPolicyOwnerReceiptBytes-1)
	identity := input.identity
	identity.Sequence = 2
	next, err := prepareUSLibraryPolicy(input.kind, identity, input.request)
	if err != nil {
		t.Fatal(err)
	}
	turn, a, _ := usPolicyDispatchTurn(t, w, r, proof, input.kind, 0)
	nextRef, err := usPolicyReceiptForCommand(next)
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := runUSPolicyDispatch(t.Context(), turn, a, &usPolicyInvocation{request: nextRef, input: next})
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity || !reflect.DeepEqual(out, usPolicyDispatchResult{}) {
		t.Fatal("quota", out, err)
	}
	revision, libraries, events := usPolicyDispatchSource(t, w, USMemberLibraries)
	if revision != 1 || libraries.String != `["movies"]` || events != 0 || usPolicyDispatchQuery(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 {
		t.Fatal("capacity refusal wrote an effect", revision, libraries, events)
	}
}

// The retained COUNT bound is independent of the byte bound: 64 retained
// receipts refuse a 65th before any source effect even with byte headroom.
func TestUSPolicyDispatchReceiptCountQuotaRefusesBeforeEffects(t *testing.T) {
	w, _, r, proof, input := usPolicyDispatchFixture(t, USMemberLibraries, []string{"movies", "music"})
	wc008NativeExec(t, w, `UPDATE security_command_owners SET last_sequence=1,acknowledged_sequence=1,retained_count=64,retained_variable_bytes=1`)
	identity := input.identity
	identity.Sequence = 2
	next, err := prepareUSLibraryPolicy(input.kind, identity, input.request)
	if err != nil {
		t.Fatal(err)
	}
	turn, a, _ := usPolicyDispatchTurn(t, w, r, proof, input.kind, 0)
	nextRef, err := usPolicyReceiptForCommand(next)
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := runUSPolicyDispatch(t.Context(), turn, a, &usPolicyInvocation{request: nextRef, input: next})
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity || !reflect.DeepEqual(out, usPolicyDispatchResult{}) {
		t.Fatal("count quota", out, err)
	}
	revision, libraries, events := usPolicyDispatchSource(t, w, USMemberLibraries)
	if revision != 1 || libraries.String != `["movies"]` || events != 0 || usPolicyDispatchQuery(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 {
		t.Fatal("count refusal wrote an effect", revision, libraries, events)
	}
}

// original_family_id is provenance, not an equality predicate: the scoped owner
// resolves across a changed family string, while a different installation cannot
// read or advance it at all.
func TestUSPolicyDispatchOriginalFamilyAndScope(t *testing.T) {
	w, _, r, proof, input := usPolicyDispatchFixture(t, USMemberLibraries, []string{"movies", "music"})
	turn, a, _ := usPolicyDispatchTurn(t, w, r, proof, input.kind, 0)
	if _, _, err := runUSPolicyDispatch(t.Context(), turn, a, usPolicyDispatchInput(t, input)); err != nil {
		t.Fatal(err)
	}
	// The retained row keeps its original family string; the current session is
	// addressed by server/account/member/profile lifetime/installation only. A
	// family-string equality rule would break this replay.
	wc008NativeExec(t, w, `UPDATE security_command_owners SET original_family_id='original-family'`)
	turn, a, _ = usPolicyDispatchTurn(t, w, r, proof, input.kind, 0)
	out, _, err := runUSPolicyDispatch(t.Context(), turn, a, &usPolicyInvocation{request: usPolicyDispatchInput(t, input).request})
	if err != nil || out.Outcome.State != Applied || !out.Outcome.Replayed {
		t.Fatal("a changed original family string blocked the scoped original", out, err)
	}
	// A different current installation is a different owner scope.
	other := usPolicyOtherIssuer(t, w)
	turn, a, _ = usPolicyDispatchTurn(t, w, r, other, input.kind, 0)
	if out, _, err := runUSPolicyDispatch(t.Context(), turn, a, &usPolicyInvocation{request: usPolicyDispatchInput(t, input).request}); !errors.Is(err, ErrOwnerUnavailable) || !reflect.DeepEqual(out, usPolicyDispatchResult{}) {
		t.Fatal("foreign installation resolved the original", out, err)
	}
}

// A retired owner is not resurrected: writes and ACK refuse, replay stays a
// read-only original.
func TestUSPolicyDispatchRetiredOwner(t *testing.T) {
	w, _, r, proof, input := usPolicyDispatchFixture(t, USMemberLibraries, []string{"movies", "music"})
	inv := usPolicyDispatchInput(t, input)
	turn, a, _ := usPolicyDispatchTurn(t, w, r, proof, input.kind, 0)
	original, _, err := runUSPolicyDispatch(t.Context(), turn, a, inv)
	if err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `UPDATE security_command_owners SET retired_ms=1`)
	// Replay of the retained original stays readable with zero effects.
	replay, _ := usPolicyDispatchReplay(t, w, r, proof, input.kind, &usPolicyInvocation{request: inv.request})
	if replay.Outcome.State != Applied || !replay.Outcome.Replayed || !reflect.DeepEqual(replay.Value, original.Value) {
		t.Fatal("retired owner replay", replay)
	}
	// ACK may not advance a retired owner's floor.
	turn, a, _ = usPolicyDispatchTurn(t, w, r, proof, input.kind, 0)
	if out, _, err := runUSPolicyDispatch(t.Context(), turn, a, &usPolicyInvocation{request: inv.request, ack: true}); !errors.Is(err, ErrOwnerUnavailable) || !reflect.DeepEqual(out, usPolicyDispatchResult{}) {
		t.Fatal("retired owner ACK", out, err)
	}
	// A fresh successor sequence is refused before any effect.
	identity := input.identity
	identity.Sequence = 2
	next, err := prepareUSLibraryPolicy(input.kind, identity, input.request)
	if err != nil {
		t.Fatal(err)
	}
	turn, a, _ = usPolicyDispatchTurn(t, w, r, proof, input.kind, 0)
	if out, _, err := runUSPolicyDispatch(t.Context(), turn, a, usPolicyDispatchInput(t, next)); !errors.Is(err, ErrOwnerUnavailable) || !reflect.DeepEqual(out, usPolicyDispatchResult{}) {
		t.Fatal("retired owner accepted a fresh write", out, err)
	}
	revision, _, events := usPolicyDispatchSource(t, w, USMemberLibraries)
	if revision != 2 || events != 1 || usPolicyDispatchQuery(t, w, `SELECT count(*) FROM security_command_receipts`) != 1 {
		t.Fatal("retired owner was mutated", revision, events)
	}
}

// The dispatcher funds its own authority rechecks out of the parent's grant.
func TestUSPolicyDispatchRecheckFunding(t *testing.T) {
	w, _, r, proof, input := usPolicyDispatchFixture(t, USMemberLibraries, []string{"movies", "music"})
	// One byte short of the declared recheck funding: refused before any read.
	turn, a, _ := usPolicyDispatchTurn(t, w, r, proof, input.kind, uint64(usPolicyDispatchRechecks*usPolicyRecheckBytes)-1)
	out, _, err := runUSPolicyDispatch(t.Context(), turn, a, usPolicyDispatchInput(t, input))
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified || !reflect.DeepEqual(out, usPolicyDispatchResult{}) {
		t.Fatal("unqualified recheck budget", out, err)
	}
	if usPolicyDispatchQuery(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 || usPolicyDispatchSourceRevision(t, w) != 1 {
		t.Fatal("unfunded dispatch wrote an effect")
	}
	// Exactly the declared recheck funding still needs the body and its image,
	// so a budget that covers ONLY the rechecks is refused by the same gate,
	// never silently under-funded.
	turn, a, _ = usPolicyDispatchTurn(t, w, r, proof, input.kind, uint64(usPolicyDispatchRechecks*usPolicyRecheckBytes))
	if out, _, err = runUSPolicyDispatch(t.Context(), turn, a, usPolicyDispatchInput(t, input)); !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified || !reflect.DeepEqual(out, usPolicyDispatchResult{}) {
		t.Fatal("body funding", out, err)
	}
	if usPolicyDispatchQuery(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 || usPolicyDispatchSourceRevision(t, w) != 1 {
		t.Fatal("unfunded body wrote an effect")
	}
	// A fully funded turn completes.
	turn, a, _ = usPolicyDispatchTurn(t, w, r, proof, input.kind, 0)
	if out, _, err = runUSPolicyDispatch(t.Context(), turn, a, usPolicyDispatchInput(t, input)); err != nil || out.Outcome.State != Applied {
		t.Fatal("funded dispatch", out, err)
	}
	if usPolicyDispatchRechecks != 3 {
		t.Fatal("declared recheck count changed without a funding review")
	}
}

func usPolicyDispatchSourceRevision(t *testing.T, w *executor) int64 {
	t.Helper()
	revision, _, _ := usPolicyDispatchSource(t, w, USMemberLibraries)
	return revision
}

// The statement inventory stays closed, hot and plan-captured.
func TestUSPolicyDispatchStatementCapture(t *testing.T) {
	defs := usPolicyDispatchDefinitions()
	if len(defs) != 14 {
		t.Fatal("dispatch statement inventory changed", len(defs))
	}
	for _, d := range defs {
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(d.Cohorts) == 0 {
			t.Fatal("dispatch statement must stay hot, captured and waiver-free", d.ID)
		}
		// The closed octet points are grammar-checked with the octet registry
		// enabled (plan.Capture below); plain LintSQL deliberately has no
		// octet_length. Every other statement is linted here directly.
		if strings.Contains(d.SQL, "octet_length(") {
			continue
		}
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
	}
	w := wc008NativeExecutor(t)
	registry := nativeInventoryFixtureRegistry(t, w, defs, "us_policy_dispatch.go")
	if len(registry.Manifest()) != len(defs) {
		t.Fatal("registry did not bind the complete dispatch inventory")
	}
}

// The dispatch image and result redact cleanly in logs and JSON.
func TestUSPolicyDispatchRedaction(t *testing.T) {
	image := usPolicyPublicImage{Snapshot: &USDirectSnapshotImage{Account: USDirectAccountImage{Username: "secret-user"}}}
	var out usPolicyDispatchResult
	for _, value := range []any{image, out, usPolicyCompletion{}} {
		for _, rendered := range []string{fmtValue(value), fmtGoValue(value)} {
			if strings.Contains(rendered, "secret-user") {
				t.Fatal("dispatch value leaked", rendered)
			}
		}
	}
}

// First-instance review fixes, all in one focused function: a bare Resolve is
// never Applied, a reorder-only 540 writes quietly, a committed overrun carries
// no output, and the 541 caller snapshot follows the product's own rules with a
// charged INTEGER preflight and no retired-row leakage.
func TestUSPolicyDispatchReviewFixes(t *testing.T) {
	t.Run("resolve_never_applied", func(t *testing.T) {
		w, _, r, proof, input := usPolicyDispatchFixture(t, USMemberLibraries, []string{"movies", "music"})
		inv := usPolicyDispatchInput(t, input)
		turn, a, _ := usPolicyDispatchTurn(t, w, r, proof, input.kind, 0)
		// Force the original's prefix to roll back after its source effect.
		delete(turn.steps, wcAPIEventInsert)
		if out, _, err := runUSPolicyDispatch(t.Context(), turn, a, inv); err == nil || !reflect.DeepEqual(out, usPolicyDispatchResult{}) {
			t.Fatal("forced rollback escaped", out, err)
		}
		if usPolicyDispatchQuery(t, w, `SELECT last_sequence FROM security_command_owners`) != 0 ||
			usPolicyDispatchQuery(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 {
			t.Fatal("forced rollback left a prefix")
		}
		// A bare Resolve at last+1 (0+1) is a fresh classification with no input:
		// it may report Pending but must never become Applied or deliver a zero
		// Value/Digest as an applied original.
		turn, a, _ = usPolicyDispatchTurn(t, w, r, proof, input.kind, 0)
		out, floor, err := runUSPolicyDispatch(t.Context(), turn, a, &usPolicyInvocation{request: inv.request})
		if err != nil {
			t.Fatal("bare Resolve failed", err)
		}
		if out.Outcome.State == Applied || out.Value.revision != 0 || out.Digest != (ResultDigest{}) ||
			out.ChargedBytes != 0 || out.EventID != 0 || floor.AcknowledgedThrough != 0 {
			t.Fatal("bare Resolve was delivered as an applied original", out, floor)
		}
		if usPolicyDispatchQuery(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 ||
			usPolicyDispatchQuery(t, w, `SELECT count(*) FROM api_events`) != 0 {
			t.Fatal("bare Resolve wrote a receipt or event")
		}
	})

	t.Run("reorder_writes_without_event", func(t *testing.T) {
		w, _, r, proof, first := usPolicyDispatchFixture(t, USMemberLibraries, []string{"movies", "music"})
		turn, a, _ := usPolicyDispatchTurn(t, w, r, proof, first.kind, 0)
		if _, _, err := runUSPolicyDispatch(t.Context(), turn, a, usPolicyDispatchInput(t, first)); err != nil {
			t.Fatal(err)
		}
		turn, a, _ = usPolicyDispatchTurn(t, w, r, proof, first.kind, 0)
		if _, _, err := runUSPolicyDispatch(t.Context(), turn, a, &usPolicyInvocation{request: usPolicyDispatchInput(t, first).request, ack: true}); err != nil {
			t.Fatal(err)
		}
		identity := first.identity
		identity.Sequence = 2
		identity.OperationID = [16]byte{3}
		request := first.request
		request.expectedRevision = 2
		request.libraries = []string{"music", "movies"}
		next, err := prepareUSLibraryPolicy(first.kind, identity, request)
		if err != nil {
			t.Fatal(err)
		}
		turn, a, _ = usPolicyDispatchTurn(t, w, r, proof, first.kind, 0)
		out, _, err := runUSPolicyDispatch(t.Context(), turn, a, usPolicyDispatchInput(t, next))
		if err != nil {
			t.Fatal(err)
		}
		if out.Outcome.State != Applied || out.Value.changed || out.Value.revision != 3 || out.EventID != 0 {
			t.Fatal("reorder-only 540", out)
		}
		revision, libraries, events := usPolicyDispatchSource(t, w, USMemberLibraries)
		if revision != 3 || libraries.String != `["music","movies"]` || events != 1 {
			t.Fatal("reorder did not write the prepared order quietly", revision, libraries, events)
		}
	})

	t.Run("committed_overrun_zeroes_output", func(t *testing.T) {
		w, path, r, proof, input := usPolicyDispatchFixture(t, USMemberLibraries, []string{"movies", "music"})
		connector, err := sqliteconn.Connector(path, w.config, sqliteconn.ExistingFile)
		if err != nil {
			t.Fatal(err)
		}
		now := w.now()
		fault := &faultConnector{Connector: connector}
		fault.afterNativeCommit = func() { now = now.Add(MaxCommandSafetyHold + time.Millisecond) }
		installFaultPool(w, fault)
		w.now = func() time.Time { return now }
		turn, a, _ := usPolicyDispatchTurn(t, w, r, proof, input.kind, 0)
		out, floor, err := runUSPolicyDispatch(t.Context(), turn, a, usPolicyDispatchInput(t, input))
		if !errors.Is(err, ErrOverrun) || !reflect.DeepEqual(out, usPolicyDispatchResult{}) || floor.AcknowledgedThrough != 0 {
			t.Fatal("committed overrun carried output", out, floor, err)
		}
		// The physical prefix committed; only the caller-visible result is zero.
		// The overrun fence closed the private pool, so read the committed file
		// through a fresh independent reader.
		read := func(query string) int64 {
			cfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
			db, _, openErr := sqliteconn.Open(t.Context(), path, cfg)
			if openErr != nil {
				t.Fatal(openErr)
			}
			defer db.Close()
			var n int64
			if scanErr := db.QueryRowContext(t.Context(), query).Scan(&n); scanErr != nil {
				t.Fatal(scanErr)
			}
			return n
		}
		if read(`SELECT revision FROM direct_memberships WHERE account_id='target'`) != 2 ||
			read(`SELECT count(*) FROM api_events`) != 1 ||
			read(`SELECT count(*) FROM security_command_receipts`) != 1 {
			t.Fatal("overrun did not commit the physical prefix")
		}
	})

	t.Run("caller_snapshot_product_rules", func(t *testing.T) {
		for _, mode := range []struct {
			name           string
			hosted         bool
			custodian      sql.NullString
			custodyPending bool
		}{
			{"no_hosted_link", false, sql.NullString{}, false},
			{"custodian_null", true, sql.NullString{}, false},
			{"custodian_other", true, sql.NullString{String: "custodian-other", Valid: true}, true},
			{"custodian_same", true, sql.NullString{String: "hosted-1", Valid: true}, false},
		} {
			t.Run(mode.name, func(t *testing.T) {
				w, _, r, proof, input := usPolicyDispatchFixture(t, USProfileLibraries, []string{"movies"})
				if mode.hosted {
					wc008NativeExec(t, w, `INSERT INTO account_portico_links(account_id,hosted_account_id) VALUES('issuer','hosted-1')`)
				}
				if mode.custodian.Valid {
					wc008NativeExec(t, w, `INSERT INTO hosted_membership_sync(singleton,custodian) VALUES(1,?)`, mode.custodian.String)
				}
				if mode.name == "custodian_same" {
					wc008NativeExec(t, w, `UPDATE identity_sign_in_policy SET pin_remember_days=0`)
				}
				turn, a, _ := usPolicyDispatchTurn(t, w, r, proof, input.kind, 0)
				out, _, err := runUSPolicyDispatch(t.Context(), turn, a, usPolicyDispatchInput(t, input))
				if err != nil {
					t.Fatal(err)
				}
				snapshot := out.Image.Snapshot
				if snapshot == nil || out.ImageBytes <= 0 {
					t.Fatal("541 snapshot missing", out.Image)
				}
				hostedAccount := ""
				if mode.hosted {
					hostedAccount = "hosted-1"
				}
				if snapshot.CustodyPending != mode.custodyPending || snapshot.Account.HostedAccountID != hostedAccount {
					t.Fatal("custody facts", snapshot.CustodyPending, snapshot.Account.HostedAccountID)
				}
				if mode.name == "custodian_same" && snapshot.RememberProfiles {
					t.Fatal("sign-in policy fact was not read")
				}
				// Product rules: owner main profile, no PIN, so the owner manages
				// everything; no account-scope factor setup; no manage lock.
				if !snapshot.CanManage || snapshot.TwoStepSetupRequired || snapshot.ManagePINRequired ||
					snapshot.Capabilities.ManageSignIn != true || snapshot.Account.Revision != 1 {
					t.Fatalf("product management facts: %+v", snapshot)
				}
			})
		}
	})

	t.Run("snapshot_rejects_inherit_mismatch", func(t *testing.T) {
		w, _, r, proof, inherit := usPolicyDispatchFixture(t, USProfileLibraries, nil)
		// Make the edited profile's source SQL NULL (inherit), then claim an
		// explicit saved list: a NULL image must not be accepted as explicit.
		wc008NativeExec(t, w, `UPDATE direct_profiles SET allowed_libraries=NULL WHERE id=?`, usPolicyIssuerSecondaryProfile)
		turn, a, _ := usPolicyDispatchTurn(t, w, r, proof, inherit.kind, 0)
		explicit := usPolicySavedResult{kind: USProfileLibraries, account: "issuer", profile: usPolicyIssuerSecondaryProfile,
			profileLifetime: inherit.request.profileLifetime, revision: 2, inherits: false, libraries: []string{"movies"}}
		if _, _, err := captureUSPolicyCallerSnapshot(t.Context(), turn, a, inherit, explicit, w.now().UnixMilli()); !errors.Is(err, ErrResolveRequired) {
			t.Fatal("an image NULL against a saved explicit list was accepted", err)
		}
		// The reverse: an explicit source against a saved inherit.
		w, _, r, proof, explicitCommand := usPolicyDispatchFixture(t, USProfileLibraries, []string{"movies"})
		turn, a, _ = usPolicyDispatchTurn(t, w, r, proof, explicitCommand.kind, 0)
		if _, _, err := runUSPolicyDispatch(t.Context(), turn, a, usPolicyDispatchInput(t, explicitCommand)); err != nil {
			t.Fatal(err)
		}
		inheritSaved := usPolicySavedResult{kind: USProfileLibraries, account: "issuer", profile: usPolicyIssuerSecondaryProfile,
			profileLifetime: explicitCommand.request.profileLifetime, revision: 2, inherits: true, libraries: nil}
		turn, a, _ = usPolicyDispatchTurn(t, w, r, proof, explicitCommand.kind, 0)
		if _, _, err := captureUSPolicyCallerSnapshot(t.Context(), turn, a, explicitCommand, inheritSaved, w.now().UnixMilli()); !errors.Is(err, ErrResolveRequired) {
			t.Fatal("an image list against a saved inherit was accepted", err)
		}
	})

	t.Run("retired_profiles_do_not_consume_cap", func(t *testing.T) {
		w, _, r, proof, input := usPolicyDispatchFixture(t, USProfileLibraries, []string{"movies"})
		for i := 0; i < 58; i++ {
			if _, err := w.db.ExecContext(t.Context(), `INSERT INTO direct_profiles(id,account_id,lifetime,name,is_primary,position,deleted) VALUES(?, 'issuer', ?, 'Retired', 0, ?, 1)`,
				fmt.Sprintf("retired-%02d", i), fmt.Sprintf("%032x", i+1), 100+i); err != nil {
				t.Fatal(err)
			}
		}
		turn, a, _ := usPolicyDispatchTurn(t, w, r, proof, input.kind, 0)
		out, _, err := runUSPolicyDispatch(t.Context(), turn, a, usPolicyDispatchInput(t, input))
		if err != nil {
			t.Fatal(err)
		}
		if out.Image.Snapshot == nil || len(out.Image.Snapshot.Profiles) != 2 ||
			out.Image.Snapshot.Profiles[0].ID != "issuer-profile" || out.Image.Snapshot.Profiles[1].ID != usPolicyIssuerSecondaryProfile {
			t.Fatalf("retired rows consumed the live cap: %+v", out.Image.Snapshot)
		}
	})

	t.Run("too_many_live_profiles_refused", func(t *testing.T) {
		w, _, r, proof, input := usPolicyDispatchFixture(t, USProfileLibraries, []string{"movies"})
		for i := 0; i < 63; i++ {
			if _, err := w.db.ExecContext(t.Context(), `INSERT INTO direct_profiles(id,account_id,lifetime,name,is_primary,position) VALUES(?, 'issuer', ?, ?, 0, ?)`,
				fmt.Sprintf("live-%02d", i), fmt.Sprintf("%032x", i+100), fmt.Sprintf("Live %02d", i), 10+i); err != nil {
				t.Fatal(err)
			}
		}
		turn, a, _ := usPolicyDispatchTurn(t, w, r, proof, input.kind, 0)
		out, _, err := runUSPolicyDispatch(t.Context(), turn, a, usPolicyDispatchInput(t, input))
		var refusal *Refusal
		if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity || !reflect.DeepEqual(out, usPolicyDispatchResult{}) {
			t.Fatal("65 live profiles were not refused", out, err)
		}
		if revision, _, events := usPolicyDispatchSource(t, w, USProfileLibraries); revision != 1 || events != 0 {
			t.Fatal("refused image left a source prefix", revision, events)
		}
	})

	t.Run("oversize_libraries_refused_before_materialization", func(t *testing.T) {
		w, _, r, proof, input := usPolicyDispatchFixture(t, USProfileLibraries, []string{"movies"})
		big := `["` + strings.Repeat("a", 200000) + `"]`
		wc008NativeExec(t, w, `UPDATE direct_profiles SET allowed_libraries=? WHERE id='issuer-profile'`, big)
		turn, a, _ := usPolicyDispatchTurn(t, w, r, proof, input.kind, 0)
		// The TEXT materialization points are absent: only the INTEGER preflight
		// may be reached, so the same refusal proves nothing was materialized.
		for _, id := range []stmt.ID{usPolicyImageAccount, usPolicyImageFamily, usPolicyImagePolicy, usPolicyImageFactor, usPolicyImageHosted, usPolicyImageCustodian, usPolicyImageProfiles} {
			delete(turn.steps, id)
		}
		out, _, err := runUSPolicyDispatch(t.Context(), turn, a, usPolicyDispatchInput(t, input))
		var refusal *Refusal
		if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity || !reflect.DeepEqual(out, usPolicyDispatchResult{}) {
			t.Fatal("oversize libraries were not refused before materialization", out, err)
		}
		if revision, _, _ := usPolicyDispatchSource(t, w, USProfileLibraries); revision != 1 {
			t.Fatal("refused image wrote source", revision)
		}
	})
}

// usPolicyStepClock recovers the validated instant, then steps once over a
// bounded delta at a pinned call. It stays inside the 3 s safety ceiling so a
// denied release is a disclosure denial, never an overrun.
type usPolicyStepClock struct {
	base   time.Time
	flipAt int
	calls  int
	delta  time.Duration
}

func (c *usPolicyStepClock) now() time.Time {
	c.calls++
	if c.flipAt > 0 && c.calls >= c.flipAt {
		return c.base.Add(c.delta)
	}
	return c.base
}

func usPolicyStepClockOn(w *executor, flipAt int, delta time.Duration) *usPolicyStepClock {
	c := &usPolicyStepClock{base: w.now(), flipAt: flipAt, delta: delta}
	w.now = c.now
	return c
}

func fmtValue(v any) string   { return fmt.Sprintf("%v", v) }
func fmtGoValue(v any) string { return fmt.Sprintf("%#v", v) }
