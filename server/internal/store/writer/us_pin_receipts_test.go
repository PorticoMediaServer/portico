package writer

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Synthetic harness only. US-01 still mints the authority stamp after its own
// current-source checks; nothing here constructs a production issuer, registers
// a product command or qualifies a hold. The executed SQL is the real closed
// statement set on the canonical schema.
const usPINIssuerSecondary = "issuer-profile-2"

type usPINHarness struct {
	w      *executor
	path   string
	r      *stmt.Registry
	proof  *usOwnerAuthorityProof
	issuer usPolicyIssuer
	owner  string
	member string
	hash   []byte
}

func usPINDispatchFixture(t *testing.T) *usPINHarness {
	t.Helper()
	w, _, proof := usOwnerAuthorityFixture(t)
	// The writer's own file, not a second clone: an injected physical fault must
	// land in the same database this turn opened.
	var path string
	if err := w.db.QueryRowContext(t.Context(), `SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	identity, policy := usPolicyFixture()
	i := policy.issuer
	// A secondary live profile of the caller's own account, for the own-account
	// non-primary target and the default-restriction image.
	wc008NativeExec(t, w, `INSERT INTO direct_profiles(id,account_id,lifetime,name,is_primary,position) VALUES(?,?,?,'Second',0,2)`,
		usPINIssuerSecondary, i.account, strings.Repeat("d", 32))
	wc008NativeExec(t, w, `INSERT INTO security_command_owners(id,command_incarnation,ingress,server_id,issuer_account_id,issuer_member_incarnation,issuer_profile_id,issuer_profile_lifetime,issuer_installation_incarnation,original_family_id,created_ms) VALUES(?,?,'authenticated',?,?,?,?,?,?,?,0)`,
		policy.ownerID, identity.Incarnation[:], i.server, i.account, i.memberIncarnation, i.profile, i.profileLifetime, i.installation, i.family)
	defs := append([]stmt.Definition(nil), usAuthorityDefinitions()...)
	defs = append(defs, usPolicyReceiptDefinitions()...)
	defs = append(defs, usPINEffectDefinitions()...)
	defs = append(defs, usPINFenceDefinitions()...)
	defs = append(defs, usPINReceiptDefinitions()...)
	defs = append(defs, usPINImageDefinitions()...)
	r, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	return &usPINHarness{w: w, path: path, r: r, proof: proof, issuer: i, owner: policy.ownerID,
		member: i.memberIncarnation, hash: []byte(usPINFixtureHash)}
}

// usPINCommand freezes one PIN preparation at the exact target revisions the
// source currently has. Hashing happened outside writer custody; the synthetic
// envelope is only an encoding fixture.
func (h *usPINHarness) command(t *testing.T, kind CommandID, identity CommandIdentity, profile, lifetime string, revision, pinRevision int64) *usPINCommand {
	t.Helper()
	pin := usPINRequest{account: h.issuer.account, profile: profile, profileLifetime: lifetime,
		expectedRevision: revision, expectedPINRevision: pinRevision}
	if kind == usPINSet {
		pin.hash = h.hash
	}
	command, err := prepareUSPINCommand(kind, identity, h.owner, h.issuer, h.member, pin)
	if err != nil {
		t.Fatal(err)
	}
	return command
}

func usPINIdentity(sequence int64) CommandIdentity {
	return CommandIdentity{Incarnation: [16]byte{1}, Sequence: sequence, OperationID: [16]byte{byte(sequence) + 10}}
}

func (h *usPINHarness) input(t *testing.T, command *usPINCommand) *usPINInvocation {
	t.Helper()
	q, err := usPINReceiptForCommand(command)
	if err != nil {
		t.Fatal(err)
	}
	return &usPINInvocation{request: q, input: command}
}

func (h *usPINHarness) dispatch(t *testing.T, kind CommandID, inv *usPINInvocation) (usPINOutcome, error) {
	t.Helper()
	return h.dispatchMemory(t, kind, inv, 0)
}

func (h *usPINHarness) dispatchMemory(t *testing.T, kind CommandID, inv *usPINInvocation, memory uint64) (usPINOutcome, error) {
	t.Helper()
	turn, a, _ := usPolicyDispatchTurn(t, h.w, h.r, h.proof, kind, memory)
	return runUSPINDispatch(t.Context(), turn, a, inv)
}

func usPINQuery(t *testing.T, w *executor, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := w.db.QueryRowContext(t.Context(), q, args...).Scan(&n); err != nil {
		t.Fatal(q, err)
	}
	return n
}

func (h *usPINHarness) source(t *testing.T, profile string) (revision, pinRevision, present int64) {
	t.Helper()
	if err := h.w.db.QueryRowContext(t.Context(), `SELECT revision,pin_revision,pin_hash IS NOT NULL FROM direct_profiles WHERE account_id=? AND id=?`,
		h.issuer.account, profile).Scan(&revision, &pinRevision, &present); err != nil {
		t.Fatal(err)
	}
	return revision, pinRevision, present
}

// usPINImageFromProduct converts the product's independent memberProfileTx
// result into the image DTO so every public field is compared, not a chosen
// subset.
func usPINImageFromProduct(p identity.MemberProfile) USMemberProfileImage {
	out := USMemberProfileImage{
		ID: p.ID, Name: p.Name, Primary: p.Primary, Position: p.Position, Art: p.Art,
		Revision: p.Revision, PINRevision: p.PINRevision, PINRequired: p.PINRequired,
		AllowedLibraries: append([]string{}, p.AllowedLibraries...),
		Restrictions: USMemberProfileRestrictionsImage{
			ProfileID: p.Restrictions.ProfileID, RatingSystem: p.Restrictions.RatingSystem,
			MaximumAgeRating: p.Restrictions.MaximumAgeRating, MaximumAge: p.Restrictions.MaximumAge,
			AllowUnrated: p.Restrictions.AllowUnrated, BlockedLabels: append([]string{}, p.Restrictions.BlockedLabels...),
			AllowDownloads: p.Restrictions.AllowDownloads, AllowLiveTV: p.Restrictions.AllowLiveTV,
			AllowDVR: p.Restrictions.AllowDVR, AllowWatchTogether: p.Restrictions.AllowWatchTogether,
			Revision: p.Restrictions.Revision,
		},
	}
	if p.Avatar != nil {
		out.Avatar = &USMemberProfileAvatarImage{ProfileID: p.Avatar.ProfileID, Version: p.Avatar.Version,
			UpdatedAt: p.Avatar.UpdatedAt, SourceMIME: p.Avatar.SourceMIME, URL: p.Avatar.URL}
	}
	return out
}

// productImage is the independent product read: the merged memberProfileTx
// response for this profile, never the loader's own SQL.
func (h *usPINHarness) productImage(t *testing.T, profile string) USMemberProfileImage {
	t.Helper()
	tx, err := h.w.db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	profiles, err := identity.MemberProfilesTx(t.Context(), tx, h.issuer.account)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range profiles.Items {
		if p.ID == profile {
			return usPINImageFromProduct(p)
		}
	}
	t.Fatalf("product read has no profile %q", profile)
	return USMemberProfileImage{}
}

// usPINImageMismatches compares the ENTIRE MemberProfile field by field. Any
// field the loader fails to populate appears here by name.
func usPINImageMismatches(got, want USMemberProfileImage) []string {
	var out []string
	add := func(name string, g, w any) {
		if !reflect.DeepEqual(g, w) {
			out = append(out, fmt.Sprintf("%s: got %v want %v", name, g, w))
		}
	}
	add("ID", got.ID, want.ID)
	add("Name", got.Name, want.Name)
	add("Primary", got.Primary, want.Primary)
	add("Position", got.Position, want.Position)
	add("Art", got.Art, want.Art)
	add("Revision", got.Revision, want.Revision)
	add("PINRevision", got.PINRevision, want.PINRevision)
	add("PINRequired", got.PINRequired, want.PINRequired)
	add("AllowedLibraries", got.AllowedLibraries, want.AllowedLibraries)
	add("Restrictions.ProfileID", got.Restrictions.ProfileID, want.Restrictions.ProfileID)
	add("Restrictions.RatingSystem", got.Restrictions.RatingSystem, want.Restrictions.RatingSystem)
	add("Restrictions.MaximumAgeRating", got.Restrictions.MaximumAgeRating, want.Restrictions.MaximumAgeRating)
	add("Restrictions.MaximumAge", got.Restrictions.MaximumAge, want.Restrictions.MaximumAge)
	add("Restrictions.AllowUnrated", got.Restrictions.AllowUnrated, want.Restrictions.AllowUnrated)
	add("Restrictions.BlockedLabels", got.Restrictions.BlockedLabels, want.Restrictions.BlockedLabels)
	add("Restrictions.AllowDownloads", got.Restrictions.AllowDownloads, want.Restrictions.AllowDownloads)
	add("Restrictions.AllowLiveTV", got.Restrictions.AllowLiveTV, want.Restrictions.AllowLiveTV)
	add("Restrictions.AllowDVR", got.Restrictions.AllowDVR, want.Restrictions.AllowDVR)
	add("Restrictions.AllowWatchTogether", got.Restrictions.AllowWatchTogether, want.Restrictions.AllowWatchTogether)
	add("Restrictions.Revision", got.Restrictions.Revision, want.Restrictions.Revision)
	if (got.Avatar == nil) != (want.Avatar == nil) {
		out = append(out, fmt.Sprintf("Avatar presence: got %v want %v", got.Avatar != nil, want.Avatar != nil))
	} else if got.Avatar != nil {
		add("Avatar.ProfileID", got.Avatar.ProfileID, want.Avatar.ProfileID)
		add("Avatar.Version", got.Avatar.Version, want.Avatar.Version)
		add("Avatar.UpdatedAt", got.Avatar.UpdatedAt, want.Avatar.UpdatedAt)
		add("Avatar.SourceMIME", got.Avatar.SourceMIME, want.Avatar.SourceMIME)
		add("Avatar.URL", got.Avatar.URL, want.Avatar.URL)
	}
	return out
}

func (h *usPINHarness) assertImageParity(t *testing.T, image *USMemberProfileImage, profile string) {
	t.Helper()
	if image == nil {
		t.Fatalf("no image for %q", profile)
	}
	if mismatches := usPINImageMismatches(*image, h.productImage(t, profile)); len(mismatches) > 0 {
		t.Fatalf("image parity for %q:\n%s", profile, strings.Join(mismatches, "\n"))
	}
}

// usPINDefinitionSQL returns the exact captured SQL for one statement ID.
func usPINDefinitionSQL(t *testing.T, defs []stmt.Definition, id stmt.ID) string {
	t.Helper()
	for _, d := range defs {
		if d.ID == id {
			return d.SQL
		}
	}
	t.Fatalf("missing statement %q", id)
	return ""
}

// usPINOrderConn observes the exact query order without changing results.
type usPINOrderConn struct {
	writerDriverConn
	onQuery func(string)
}

func (c *usPINOrderConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if c.onQuery != nil {
		c.onQuery(query)
	}
	return c.writerDriverConn.QueryContext(ctx, query, args)
}

// Original, then replay/Resolve of the same identity, then the floor-only ACK.
// The acting family starts PIN-gated, so the keep-proof is load-bearing: only
// the US-02 family witness can pass the post-effect current check.
func TestUSPINPhysicalOriginalReplayAndACK(t *testing.T) {
	h := usPINDispatchFixture(t)
	wc008NativeExec(t, h.w, `UPDATE authorization_session_families SET pin_gate=1 WHERE id=?`, h.issuer.family)
	command := h.command(t, usPINSet, usPINIdentity(1), h.issuer.profile, h.issuer.profileLifetime, 1, 1)
	inv := h.input(t, command)
	got, err := h.dispatch(t, usPINSet, inv)
	if err != nil {
		t.Fatal(err)
	}
	if got.Result.Outcome.State != Applied || got.Result.Outcome.Replayed || got.Result.Value.revision != 2 ||
		got.Result.Value.pinRevision != 2 || !got.Result.Value.pinRequired || got.Result.Digest == (ResultDigest{}) {
		t.Fatal("original was not the closed accepted result", got.Result)
	}
	if got.Result.Image.Profile == nil || got.Result.ImageBytes <= 0 || got.Result.ChargedBytes != 204 ||
		got.Result.OwnerCount != 1 || got.Result.OwnerBytes != got.Result.ChargedBytes {
		t.Fatal("original accounting", got.Result)
	}
	// The complete US-04 image is the post-effect source, independently read
	// through the product's own memberProfileTx.
	h.assertImageParity(t, got.Result.Image.Profile, h.issuer.profile)
	if image := got.Result.Image.Profile; image.PINRequired != true || image.Revision != 2 || image.PINRevision != 2 ||
		image.Name != "Issuer" || image.Art != "blue" || image.Primary != true || !image.Restrictions.AllowDVR ||
		image.Restrictions.MaximumAge != -1 || image.Restrictions.Revision != 1 || image.Avatar != nil {
		t.Fatalf("complete image facts: %+v", *image)
	}
	revision, pinRevision, present := h.source(t, h.issuer.profile)
	if revision != 2 || pinRevision != 2 || present != 1 {
		t.Fatal("original source effect", revision, pinRevision, present)
	}
	var stored []byte
	if err := h.w.db.QueryRowContext(t.Context(), `SELECT pin_hash FROM direct_profiles WHERE id=?`, h.issuer.profile).Scan(&stored); err != nil || !reflect.DeepEqual(stored, h.hash) {
		t.Fatal("stored prepared hash mismatch", err)
	}
	if usPINQuery(t, h.w, `SELECT count(*) FROM identity_security_fences WHERE scope_kind='profile_pin'`) != 1 ||
		usPINQuery(t, h.w, `SELECT generation FROM identity_security_fences WHERE scope_kind='profile_pin' AND profile_id=?`, h.issuer.profile) != 1 {
		t.Fatal("fence birth")
	}
	if usPINQuery(t, h.w, `SELECT pin_gate FROM authorization_session_families WHERE id=?`, h.issuer.family) != 0 ||
		usPINQuery(t, h.w, `SELECT confirmed_generation FROM authorization_family_security_fences WHERE family_id=? AND scope_kind='profile_pin'`, h.issuer.family) != 1 {
		t.Fatal("keep-family witness")
	}
	if usPINQuery(t, h.w, `SELECT count(*) FROM security_command_receipts WHERE command_kind=532 AND result_pin_required=1 AND result_changed=1 AND result_revision=2 AND result_pin_revision=2 AND result_count=0 AND policy_inherits IS NULL AND target_account_id='issuer' AND target_profile_id='issuer-profile'`) != 1 {
		t.Fatal("typed receipt row")
	}
	if usPINQuery(t, h.w, `SELECT variable_bytes FROM security_command_receipts`) != got.Result.ChargedBytes ||
		usPINQuery(t, h.w, `SELECT retained_variable_bytes FROM security_command_owners`) != got.Result.ChargedBytes {
		t.Fatal("exact byte debt")
	}
	// 532 appends no api_event in the pinned product.
	if usPINQuery(t, h.w, `SELECT count(*) FROM api_events`) != 0 {
		t.Fatal("PIN change appended an event")
	}

	// Replay: the SAME identity again, now without its preparation and with every
	// image/source statement removed from the turn. Zero new effects and no
	// source-derived image.
	before := usPINQuery(t, h.w, `SELECT total_changes()`)
	turn, a, _ := usPolicyDispatchTurn(t, h.w, h.r, h.proof, usPINSet, 0)
	for id := range turn.steps {
		switch id {
		case usPINImageProfileOctets, usPINImageProfile, usPINImageRestrictionOctets, usPINImageRestriction,
			usPINImageAvatarOctets, usPINImageAvatar, usPINImageDefaultDVR, usPINTargetSelf, usPINSetWrite, usPINClearWrite,
			usPINReceiptInsert, usPolicyOwnerAdvance:
			delete(turn.steps, id)
		}
	}
	replay, err := runUSPINDispatch(t.Context(), turn, a, &usPINInvocation{request: inv.request})
	if err != nil {
		t.Fatal(err)
	}
	if replay.Result.Outcome.State != Applied || !replay.Result.Outcome.Replayed || replay.Result.Digest != got.Result.Digest ||
		!reflect.DeepEqual(replay.Result.Value, got.Result.Value) {
		t.Fatal("replay did not return the saved original", replay.Result)
	}
	if replay.Result.Image.Profile != nil || replay.Result.ImageBytes != 0 || replay.Result.ChargedBytes != 0 {
		t.Fatal("replay rebuilt a response image or charged again", replay.Result)
	}
	if after := usPINQuery(t, h.w, `SELECT total_changes()`); after != before {
		t.Fatal("replay made new effects", before, after)
	}
	// Resolve with the original preparation present is still a replay.
	resolve, err := h.dispatch(t, usPINSet, inv)
	if err != nil || resolve.Result.Outcome.State != Applied || !resolve.Result.Outcome.Replayed ||
		!reflect.DeepEqual(resolve.Result.Value, got.Result.Value) {
		t.Fatal("resolve with preparation re-executed", resolve, err)
	}

	// Floor-only ACK: it advances one sequence and refunds nothing.
	ack, err := h.dispatch(t, usPINSet, &usPINInvocation{request: inv.request, ack: true})
	if err != nil || ack.Floor.AcknowledgedThrough != 1 || !reflect.DeepEqual(ack.Result, usPINDispatchResult{}) {
		t.Fatal("ACK", ack, err)
	}
	if usPINQuery(t, h.w, `SELECT acknowledged_sequence FROM security_command_owners`) != 1 ||
		usPINQuery(t, h.w, `SELECT retained_count FROM security_command_owners`) != 1 ||
		usPINQuery(t, h.w, `SELECT retained_variable_bytes FROM security_command_owners`) != got.Result.ChargedBytes ||
		usPINQuery(t, h.w, `SELECT count(*) FROM security_command_receipts`) != 1 {
		t.Fatal("ACK refunded or removed the retained cohort")
	}
	// The floor is idempotent and the original stays retired, never Applied.
	retired, err := h.dispatch(t, usPINSet, &usPINInvocation{request: inv.request})
	if err != nil || retired.Result.Outcome.State != ResultRetired || retired.Result.Value.owner != "" {
		t.Fatal("ACKed original was served as Applied", retired, err)
	}
	again, err := h.dispatch(t, usPINSet, &usPINInvocation{request: inv.request, ack: true})
	if err != nil || again.Floor.AcknowledgedThrough != 1 {
		t.Fatal("ACK was not idempotent", again, err)
	}
}

// A set and a later clear each bump BOTH revisions, and a clear of an absent PIN
// is still an edit. Clearing preserves the sign-in families.
func TestUSPINSetThenClearBothRevisionBumps(t *testing.T) {
	h := usPINDispatchFixture(t)
	set := h.command(t, usPINSet, usPINIdentity(1), h.issuer.profile, h.issuer.profileLifetime, 1, 1)
	if out, err := h.dispatch(t, usPINSet, h.input(t, set)); err != nil || out.Result.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	if _, err := h.dispatch(t, usPINSet, &usPINInvocation{request: h.input(t, set).request, ack: true}); err != nil {
		t.Fatal(err)
	}
	clear := h.command(t, usPINClear, usPINIdentity(2), h.issuer.profile, h.issuer.profileLifetime, 2, 2)
	got, err := h.dispatch(t, usPINClear, h.input(t, clear))
	if err != nil {
		t.Fatal(err)
	}
	if got.Result.Outcome.State != Applied || got.Result.Value.kind != usPINClear || got.Result.Value.revision != 3 ||
		got.Result.Value.pinRevision != 3 || got.Result.Value.pinRequired || got.Result.Image.Profile == nil ||
		got.Result.Image.Profile.PINRequired {
		t.Fatal("clear result", got.Result)
	}
	revision, pinRevision, present := h.source(t, h.issuer.profile)
	if revision != 3 || pinRevision != 3 || present != 0 {
		t.Fatal("clear source effect", revision, pinRevision, present)
	}
	if usPINQuery(t, h.w, `SELECT generation FROM identity_security_fences WHERE scope_kind='profile_pin' AND profile_id=?`, h.issuer.profile) != 2 ||
		usPINQuery(t, h.w, `SELECT result_pin_required FROM security_command_receipts WHERE command_kind=533`) != 0 {
		t.Fatal("clear fence or typed receipt")
	}
	// Clearing did not sign any family out.
	if usPINQuery(t, h.w, `SELECT count(*) FROM authorization_session_families WHERE revoked<>0`) != 0 {
		t.Fatal("clear revoked a family")
	}

	// Clearing an already absent PIN on the secondary own profile bumps both
	// revisions again, exactly as the API does.
	revision, pinRevision, _ = h.source(t, usPINIssuerSecondary)
	absent := h.command(t, usPINClear, usPINIdentity(3), usPINIssuerSecondary, strings.Repeat("d", 32), revision, pinRevision)
	// The sequence after the primary clear's ACK is the owner-wide floor.
	wc008NativeExec(t, h.w, `UPDATE security_command_owners SET acknowledged_sequence=last_sequence`)
	absentOut, err := h.dispatch(t, usPINClear, h.input(t, absent))
	if err != nil {
		t.Fatal(err)
	}
	if absentOut.Result.Outcome.State != Applied || absentOut.Result.Value.revision != revision+1 ||
		absentOut.Result.Value.pinRevision != pinRevision+1 || absentOut.Result.Value.pinRequired {
		t.Fatal("clear of an absent PIN was not an edit", absentOut.Result)
	}
}

// The image of a secondary profile at stored position 2 is the complete product
// MemberProfile for both a SET and a CLEAR. The old parity fixture only ever
// compared the primary profile at the default position, which hid an unloaded
// position field.
func TestUSPINSecondaryProfileImageParity(t *testing.T) {
	h := usPINDispatchFixture(t)
	lifetime := strings.Repeat("d", 32)
	set := h.command(t, usPINSet, usPINIdentity(1), usPINIssuerSecondary, lifetime, 1, 1)
	got, err := h.dispatch(t, usPINSet, h.input(t, set))
	if err != nil || got.Result.Outcome.State != Applied || got.Result.Image.Profile == nil {
		t.Fatal("secondary set", got, err)
	}
	if image := got.Result.Image.Profile; image.Position != 2 || image.Primary || !image.PINRequired {
		t.Fatalf("secondary set image facts: position=%d primary=%v pin=%v", image.Position, image.Primary, image.PINRequired)
	}
	h.assertImageParity(t, got.Result.Image.Profile, usPINIssuerSecondary)
	if _, err := h.dispatch(t, usPINSet, &usPINInvocation{request: h.input(t, set).request, ack: true}); err != nil {
		t.Fatal(err)
	}
	clear := h.command(t, usPINClear, usPINIdentity(2), usPINIssuerSecondary, lifetime, 2, 2)
	got, err = h.dispatch(t, usPINClear, h.input(t, clear))
	if err != nil || got.Result.Outcome.State != Applied || got.Result.Image.Profile == nil {
		t.Fatal("secondary clear", got, err)
	}
	if image := got.Result.Image.Profile; image.Position != 2 || image.Primary || image.PINRequired {
		t.Fatalf("secondary clear image facts: position=%d primary=%v pin=%v", image.Position, image.Primary, image.PINRequired)
	}
	h.assertImageParity(t, got.Result.Image.Profile, usPINIssuerSecondary)
}

// One scalar fence generation gates every other family of the profile without
// scanning, rewriting or counting them. Only the proof-checked acting family
// receives a witness.
func TestUSPINOtherSessionsGatedWithoutEnumeration(t *testing.T) {
	h := usPINDispatchFixture(t)
	for n := 0; n < 2; n++ {
		wc008NativeExec(t, h.w, `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,pin_gate,command_member_incarnation,command_profile_lifetime,command_incarnation,security_kind) VALUES(?,?,?,?,'local','owner',1,'horizon',1,1,?,?,?,0)`,
			fmt.Sprint("other-family-", n), h.issuer.server, h.issuer.account, h.issuer.profile, h.member, h.issuer.profileLifetime, []byte{byte(n + 1), 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	}
	wc008NativeExec(t, h.w, `UPDATE authorization_session_families SET pin_gate=1`)
	command := h.command(t, usPINSet, usPINIdentity(1), h.issuer.profile, h.issuer.profileLifetime, 1, 1)
	if out, err := h.dispatch(t, usPINSet, h.input(t, command)); err != nil || out.Result.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	if usPINQuery(t, h.w, `SELECT pin_gate FROM authorization_session_families WHERE id=?`, h.issuer.family) != 0 {
		t.Fatal("acting family was not kept")
	}
	if usPINQuery(t, h.w, `SELECT count(*) FROM authorization_session_families WHERE pin_gate=1`) != 2 {
		t.Fatal("other families were enumerated or rewritten")
	}
	// Exactly one witness: the other sessions are gated because their absent
	// witness can never match the advanced generation, not by a rewrite.
	if usPINQuery(t, h.w, `SELECT count(*) FROM authorization_family_security_fences WHERE scope_kind='profile_pin'`) != 1 ||
		usPINQuery(t, h.w, `SELECT count(*) FROM authorization_family_security_fences WHERE scope_kind='profile_pin' AND family_id<>?`, h.issuer.family) != 0 {
		t.Fatal("family witness fan-out")
	}
}

// A stale preparation cannot execute: the fresh CAS is bound to the exact
// expected revisions.
func TestUSPINStaleRevisionConflicts(t *testing.T) {
	h := usPINDispatchFixture(t)
	command := h.command(t, usPINSet, usPINIdentity(1), h.issuer.profile, h.issuer.profileLifetime, 1, 1)
	wc008NativeExec(t, h.w, `UPDATE direct_profiles SET revision=2 WHERE id=?`, h.issuer.profile)
	out, err := h.dispatch(t, usPINSet, h.input(t, command))
	if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, usPINOutcome{}) {
		t.Fatal("stale revision", out, err)
	}
	if revision, _, present := h.source(t, h.issuer.profile); revision != 2 || present != 0 ||
		usPINQuery(t, h.w, `SELECT count(*) FROM identity_security_fences WHERE scope_kind='profile_pin'`) != 0 ||
		usPINQuery(t, h.w, `SELECT count(*) FROM security_command_receipts`) != 0 {
		t.Fatal("conflicting original wrote a prefix", revision)
	}
}

// Receipt failure after a nonempty source+fence prefix rolls the whole domain
// prefix back.
func TestUSPINReceiptFailureRollsBackSourceAndFence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remove func(*commandTurn)
	}{
		{"receipt_insert", func(turn *commandTurn) { delete(turn.steps, usPINReceiptInsert) }},
		{"owner_advance", func(turn *commandTurn) { delete(turn.steps, usPolicyOwnerAdvance) }},
		{"keep_witness", func(turn *commandTurn) { delete(turn.steps, usPINKeepWitness) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := usPINDispatchFixture(t)
			command := h.command(t, usPINSet, usPINIdentity(1), h.issuer.profile, h.issuer.profileLifetime, 1, 1)
			turn, a, _ := usPolicyDispatchTurn(t, h.w, h.r, h.proof, usPINSet, 0)
			tc.remove(turn)
			if out, err := runUSPINDispatch(t.Context(), turn, a, h.input(t, command)); err == nil || !reflect.DeepEqual(out, usPINOutcome{}) {
				t.Fatal("partial prefix escaped", out, err)
			}
			if revision, pinRevision, present := h.source(t, h.issuer.profile); revision != 1 || pinRevision != 1 || present != 0 {
				t.Fatal("source effect survived rollback", revision, pinRevision, present)
			}
			if usPINQuery(t, h.w, `SELECT count(*) FROM identity_security_fences WHERE scope_kind='profile_pin'`) != 0 ||
				usPINQuery(t, h.w, `SELECT count(*) FROM security_command_receipts`) != 0 ||
				usPINQuery(t, h.w, `SELECT last_sequence FROM security_command_owners`) != 0 ||
				usPINQuery(t, h.w, `SELECT retained_variable_bytes FROM security_command_owners`) != 0 {
				t.Fatal("receipt/fence/owner prefix survived rollback")
			}
		})
	}
}

// A fresh result that would exceed the retained quota is refused BEFORE any
// source effect, independently for the count and byte bounds.
func TestUSPINReceiptQuotaRefusesBeforeEffects(t *testing.T) {
	for _, tc := range []struct {
		name   string
		update string
	}{
		{"count", `UPDATE security_command_owners SET last_sequence=1,acknowledged_sequence=1,retained_count=64,retained_variable_bytes=1`},
		{"bytes", `UPDATE security_command_owners SET last_sequence=1,acknowledged_sequence=1,retained_count=1,retained_variable_bytes=?`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := usPINDispatchFixture(t)
			if tc.name == "bytes" {
				wc008NativeExec(t, h.w, tc.update, usPINOwnerReceiptBytes-1)
			} else {
				wc008NativeExec(t, h.w, tc.update)
			}
			command := h.command(t, usPINSet, usPINIdentity(2), h.issuer.profile, h.issuer.profileLifetime, 1, 1)
			out, err := h.dispatch(t, usPINSet, h.input(t, command))
			var refusal *Refusal
			if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity || !reflect.DeepEqual(out, usPINOutcome{}) {
				t.Fatal("quota", out, err)
			}
			if revision, _, present := h.source(t, h.issuer.profile); revision != 1 || present != 0 ||
				usPINQuery(t, h.w, `SELECT count(*) FROM security_command_receipts`) != 0 ||
				usPINQuery(t, h.w, `SELECT count(*) FROM identity_security_fences WHERE scope_kind='profile_pin'`) != 0 {
				t.Fatal("capacity refusal wrote an effect", revision)
			}
		})
	}
}

// The dispatcher funds its own authority rechecks out of the parent's grant.
func TestUSPINDispatchRecheckFunding(t *testing.T) {
	h := usPINDispatchFixture(t)
	command := h.command(t, usPINSet, usPINIdentity(1), h.issuer.profile, h.issuer.profileLifetime, 1, 1)
	out, err := h.dispatchMemory(t, usPINSet, h.input(t, command), uint64(usPINDispatchRechecks*usPINRecheckBytes)-1)
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified || !reflect.DeepEqual(out, usPINOutcome{}) {
		t.Fatal("unqualified recheck budget", out, err)
	}
	if usPINQuery(t, h.w, `SELECT count(*) FROM security_command_receipts`) != 0 {
		t.Fatal("unfunded dispatch wrote a receipt")
	}
}

// A denial at the entry or final release REPLACES the body outcome and rolls
// everything back. The second case cancels AFTER the source effect, fence and
// receipt are applied, so only the final release recheck can deny it.
func TestUSPINCanceledReleaseDeniesAndRollsBack(t *testing.T) {
	t.Run("entry", func(t *testing.T) {
		h := usPINDispatchFixture(t)
		command := h.command(t, usPINSet, usPINIdentity(1), h.issuer.profile, h.issuer.profileLifetime, 1, 1)
		turn, a, cancel := usPolicyDispatchTurn(t, h.w, h.r, h.proof, usPINSet, 0)
		cancel()
		out, err := runUSPINDispatch(t.Context(), turn, a, h.input(t, command))
		if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, usPINOutcome{}) {
			t.Fatal("canceled preparation was accepted", out, err)
		}
		if revision, _, present := h.source(t, h.issuer.profile); revision != 1 || present != 0 ||
			usPINQuery(t, h.w, `SELECT count(*) FROM security_command_receipts`) != 0 {
			t.Fatal("canceled preparation wrote source", revision)
		}
	})
	t.Run("after_effect", func(t *testing.T) {
		h := usPINDispatchFixture(t)
		command := h.command(t, usPINSet, usPINIdentity(1), h.issuer.profile, h.issuer.profileLifetime, 1, 1)
		if err := h.w.db.Close(); err != nil {
			t.Fatal(err)
		}
		connector, err := sqliteconn.Connector(h.path, h.w.config, sqliteconn.ExistingFile)
		if err != nil {
			t.Fatal(err)
		}
		profileSQL := usPINDefinitionSQL(t, usPINImageDefinitions(), usPINImageProfile)
		receiptSQL := usPINDefinitionSQL(t, usPINReceiptDefinitions(), usPINReceiptInsert)
		var effectApplied, canceled bool
		var cancel context.CancelFunc
		h.w.openPool(connector, func(conn driver.Conn) driver.Conn {
			return &usPINOrderConn{writerDriverConn: conn.(writerDriverConn), onQuery: func(query string) {
				if query == receiptSQL {
					effectApplied = true
				}
				if query == profileSQL && effectApplied && !canceled {
					canceled = true
					cancel()
				}
			}}
		})
		turn, a, stop := usPolicyDispatchTurn(t, h.w, h.r, h.proof, usPINSet, 0)
		cancel = stop
		out, err := runUSPINDispatch(t.Context(), turn, a, h.input(t, command))
		if !effectApplied || !canceled {
			t.Fatalf("test did not cancel after the effect: applied=%v canceled=%v", effectApplied, canceled)
		}
		if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, usPINOutcome{}) {
			t.Fatal("release-time cancellation was accepted", out, err)
		}
		if revision, pinRevision, present := h.source(t, h.issuer.profile); revision != 1 || pinRevision != 1 || present != 0 ||
			usPINQuery(t, h.w, `SELECT count(*) FROM security_command_receipts`) != 0 ||
			usPINQuery(t, h.w, `SELECT count(*) FROM identity_security_fences WHERE scope_kind='profile_pin'`) != 0 ||
			usPINQuery(t, h.w, `SELECT last_sequence FROM security_command_owners`) != 0 {
			t.Fatal("release-time denial left the applied prefix", revision, pinRevision, present)
		}
	})
}

// This bounded composition admits only the caller's own account. A target
// membership role is not cross-account authority.
func TestUSPINCrossAccountResetRefused(t *testing.T) {
	h := usPINDispatchFixture(t)
	wc008NativeExec(t, h.w, `INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('target','target',X'','target-profile')`)
	wc008NativeExec(t, h.w, `INSERT INTO direct_memberships(account_id,incarnation,role) VALUES('target','eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee','member')`)
	wc008NativeExec(t, h.w, `INSERT INTO direct_profiles(id,account_id,lifetime,name,is_primary) VALUES('target-profile','target','ffffffffffffffffffffffffffffffff','Target',1)`)
	command := h.command(t, usPINSet, usPINIdentity(1), "target-profile", strings.Repeat("f", 32), 1, 1)
	// prepareUSPINCommand binds the target from the request; retarget its account
	// to the foreign one while keeping the frozen digest.
	command.request.prepared.request.account = "target"
	digest, err := canonicalRequestDigest(usPINSet, 1, command.identity, command.request)
	if err != nil {
		t.Fatal(err)
	}
	command.digest = digest
	out, err := h.dispatch(t, usPINSet, h.input(t, command))
	if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, usPINOutcome{}) {
		t.Fatal("cross-account reset was not refused", out, err)
	}
	if usPINQuery(t, h.w, `SELECT count(*) FROM direct_profiles WHERE account_id='target' AND id='target-profile' AND revision=1 AND pin_hash IS NULL`) != 1 {
		t.Fatal("foreign target was edited")
	}
}

// An uncertain COMMIT is never Applied. The driver boundary is the physical
// truth: the SQLite COMMIT either reached the database or did not, and in both
// cases the caller only learns to resolve the ORIGINAL.
func TestUSPINUnknownCommitNeverApplied(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "after"}[after], func(t *testing.T) {
			h := usPINDispatchFixture(t)
			command := h.command(t, usPINSet, usPINIdentity(1), h.issuer.profile, h.issuer.profileLifetime, 1, 1)
			connector, err := sqliteconn.Connector(h.path, h.w.config, sqliteconn.ExistingFile)
			if err != nil {
				t.Fatal(err)
			}
			fault := &faultConnector{Connector: connector, replyError: true, afterCommit: after}
			native := false
			fault.afterNativeCommit = func() { native = true }
			installFaultPool(h.w, fault)
			out, err := h.dispatch(t, usPINSet, h.input(t, command))
			if !errors.Is(err, ErrResolveRequired) || !reflect.DeepEqual(out, usPINOutcome{}) {
				t.Fatal("uncertain commit exposed success", out, err)
			}
			if !h.w.ambiguous || !h.w.fenced {
				t.Fatal("uncertain writer was not fenced")
			}
			if fault.commits.Load() != 1 || native != after {
				t.Fatal("physical COMMIT disposition changed", fault.commits.Load(), native, after)
			}
			if _, err := h.w.begin(t.Context(), usPINSet, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
				t.Fatal("fenced writer admitted another turn", err)
			}
		})
	}
}

// A committed command that then returns an error (a safety overrun observed
// after the native COMMIT) must carry ZERO output. The physical prefix is
// committed; the caller still only learns to resolve the ORIGINAL.
func TestUSPINCommittedOverrunZeroesOutput(t *testing.T) {
	h := usPINDispatchFixture(t)
	connector, err := sqliteconn.Connector(h.path, h.w.config, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	now := h.w.now()
	fault := &faultConnector{Connector: connector}
	fault.afterNativeCommit = func() { now = now.Add(MaxCommandSafetyHold + time.Millisecond) }
	installFaultPool(h.w, fault)
	h.w.now = func() time.Time { return now }
	command := h.command(t, usPINSet, usPINIdentity(1), h.issuer.profile, h.issuer.profileLifetime, 1, 1)
	out, err := h.dispatch(t, usPINSet, h.input(t, command))
	if !errors.Is(err, ErrOverrun) || !reflect.DeepEqual(out, usPINOutcome{}) {
		t.Fatal("committed overrun carried output", out, err)
	}
	// The overrun fence closed the private pool; read the committed file through
	// a fresh independent reader.
	cfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, _, err := sqliteconn.Open(t.Context(), h.path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	read := func(query string) int64 {
		var n int64
		if scanErr := db.QueryRowContext(t.Context(), query).Scan(&n); scanErr != nil {
			t.Fatal(query, scanErr)
		}
		return n
	}
	if read(`SELECT revision FROM direct_profiles WHERE id='`+h.issuer.profile+`'`) != 2 ||
		read(`SELECT pin_revision FROM direct_profiles WHERE id='`+h.issuer.profile+`'`) != 2 ||
		read(`SELECT count(*) FROM direct_profiles WHERE id='`+h.issuer.profile+`' AND pin_hash IS NOT NULL`) != 1 ||
		read(`SELECT count(*) FROM security_command_receipts`) != 1 ||
		read(`SELECT count(*) FROM identity_security_fences WHERE scope_kind='profile_pin'`) != 1 ||
		read(`SELECT retained_variable_bytes FROM security_command_owners`) <= 0 {
		t.Fatal("committed overrun did not commit the physical prefix")
	}
}

// A retired owner is not resurrected: replay stays a read-only original while
// writes and ACK refuse.
func TestUSPINRetiredOwner(t *testing.T) {
	h := usPINDispatchFixture(t)
	command := h.command(t, usPINSet, usPINIdentity(1), h.issuer.profile, h.issuer.profileLifetime, 1, 1)
	inv := h.input(t, command)
	original, err := h.dispatch(t, usPINSet, inv)
	if err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, h.w, `UPDATE security_command_owners SET retired_ms=1`)
	replay, err := h.dispatch(t, usPINSet, &usPINInvocation{request: inv.request})
	if err != nil || replay.Result.Outcome.State != Applied || !replay.Result.Outcome.Replayed ||
		!reflect.DeepEqual(replay.Result.Value, original.Result.Value) {
		t.Fatal("retired owner replay", replay, err)
	}
	if out, err := h.dispatch(t, usPINSet, &usPINInvocation{request: inv.request, ack: true}); !errors.Is(err, ErrOwnerUnavailable) || !reflect.DeepEqual(out, usPINOutcome{}) {
		t.Fatal("retired owner ACK", out, err)
	}
	next := h.command(t, usPINClear, usPINIdentity(2), h.issuer.profile, h.issuer.profileLifetime, 1, 1)
	wc008NativeExec(t, h.w, `UPDATE security_command_owners SET acknowledged_sequence=last_sequence`)
	if out, err := h.dispatch(t, usPINClear, h.input(t, next)); !errors.Is(err, ErrOwnerUnavailable) || !reflect.DeepEqual(out, usPINOutcome{}) {
		t.Fatal("retired owner accepted a fresh write", out, err)
	}
	if revision, _, present := h.source(t, h.issuer.profile); revision != 2 || present != 1 {
		t.Fatal("retired owner was mutated", revision)
	}
}

// The prepared hash never escapes a result, image, error or formatting path, and
// replay after source drift returns only the retained original.
func TestUSPINHashNeverEscapesAndReplayAfterDrift(t *testing.T) {
	h := usPINDispatchFixture(t)
	command := h.command(t, usPINSet, usPINIdentity(1), h.issuer.profile, h.issuer.profileLifetime, 1, 1)
	inv := h.input(t, command)
	got, err := h.dispatch(t, usPINSet, inv)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{got, got.Result, got.Result.Value, got.Result.Image, err} {
		for _, rendered := range []string{fmt.Sprintf("%v", value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value)} {
			if strings.Contains(rendered, string(h.hash)) || strings.Contains(rendered, "$2a$08$") {
				t.Fatal("prepared hash escaped", rendered)
			}
		}
	}
	// Drift every live source fact the original image named, then replay: the
	// saved result and digest are unchanged and no image is rebuilt. The turn is
	// admitted BEFORE the drift and the drift runs on its own connection, so the
	// dispatch recheck (not turn setup) observes it.
	turn, a, _ := usPolicyDispatchTurn(t, h.w, h.r, h.proof, usPINSet, 0)
	for _, drift := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE direct_profiles SET name='Renamed',revision=revision+1,pin_hash=NULL WHERE id=?`, []any{h.issuer.profile}},
		{`UPDATE direct_memberships SET role='member' WHERE account_id=?`, []any{h.issuer.account}},
		{`UPDATE authorization_session_families SET revoked=1`, nil},
	} {
		if _, err := turn.transaction.conn.ExecContext(turn.transaction.ctx, drift.sql, drift.args...); err != nil {
			t.Fatal(err)
		}
	}
	replay, err := runUSPINDispatch(t.Context(), turn, a, &usPINInvocation{request: inv.request})
	if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(replay, usPINOutcome{}) {
		// Revoking the current family is a current-source denial, not a replay.
		t.Fatal("drifted current authority was not denied", replay, err)
	}
	// The denied turn rolled its drift back; restore current authority but
	// re-apply the original's source facts as drift for the replay.
	wc008NativeExec(t, h.w, `UPDATE direct_memberships SET role='owner' WHERE account_id=?`, h.issuer.account)
	wc008NativeExec(t, h.w, `UPDATE authorization_session_families SET revoked=0, pin_gate=0`)
	wc008NativeExec(t, h.w, `UPDATE direct_profiles SET name='Renamed',revision=revision+1,pin_hash=? WHERE id=?`, h.hash, h.issuer.profile)
	replay, err = h.dispatch(t, usPINSet, &usPINInvocation{request: inv.request})
	if err != nil || replay.Result.Outcome.State != Applied || !replay.Result.Outcome.Replayed ||
		replay.Result.Image.Profile != nil || !reflect.DeepEqual(replay.Result.Value, got.Result.Value) {
		t.Fatal("replay rebuilt from drifted source", replay, err)
	}
}

// A large but legal stored source is refused by the image preflight BEFORE any
// materialization, and the already-composed source+fence+receipt prefix rolls
// back. The exact capacity refusal and the statement order prove the grant
// charge ran before the first TEXT scan: removing it would execute the profile
// read instead of refusing here.
func TestUSPINImagePreflightRefusesBeforeMaterialization(t *testing.T) {
	h := usPINDispatchFixture(t)
	big := strings.Repeat("x", 2<<20)
	wc008NativeExec(t, h.w, `UPDATE direct_profiles SET name=? WHERE id=?`, big, h.issuer.profile)
	if err := h.w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(h.path, h.w.config, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	profileSQL := usPINDefinitionSQL(t, usPINImageDefinitions(), usPINImageProfile)
	var materialized bool
	h.w.openPool(connector, func(conn driver.Conn) driver.Conn {
		return &usPINOrderConn{writerDriverConn: conn.(writerDriverConn), onQuery: func(query string) {
			if query == profileSQL {
				materialized = true
			}
		}}
	})
	command := h.command(t, usPINSet, usPINIdentity(1), h.issuer.profile, h.issuer.profileLifetime, 1, 1)
	turn, a, _ := usPolicyDispatchTurn(t, h.w, h.r, h.proof, usPINSet, 0)
	out, err := runUSPINDispatch(t.Context(), turn, a, h.input(t, command))
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity || !reflect.DeepEqual(out, usPINOutcome{}) {
		t.Fatal("over-large image was not refused by capacity", out, err)
	}
	if materialized {
		t.Fatal("profile TEXT was materialized before the preflight charge refused")
	}
	if revision, _, present := h.source(t, h.issuer.profile); revision != 1 || present != 0 ||
		usPINQuery(t, h.w, `SELECT count(*) FROM identity_security_fences WHERE scope_kind='profile_pin'`) != 0 ||
		usPINQuery(t, h.w, `SELECT count(*) FROM security_command_receipts`) != 0 {
		t.Fatal("refused image left a prefix", revision)
	}
}

// The image includes a stored restriction cohort and an avatar when present,
// with the product's default only when no row exists.
func TestUSPINImageStoredRestrictionsAndAvatar(t *testing.T) {
	h := usPINDispatchFixture(t)
	wc008NativeExec(t, h.w, `INSERT INTO profile_restrictions(profile_id,rating_system,maximum_age_rating,maximum_age,allow_unrated,blocked_labels,allow_downloads,allow_live_tv,allow_dvr,allow_watch_together,revision) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		h.issuer.profile, "mpaa", "PG-13", 13, 0, `["violence","gore"]`, 0, 1, 0, 1, 4)
	wc008NativeExec(t, h.w, `INSERT INTO profile_avatars(profile_id,digest,source_mime,version,updated_at) VALUES(?,?,?,?,?)`,
		h.issuer.profile, "digest", "image/png", 3, "2026-11-08T00:00:00Z")
	command := h.command(t, usPINSet, usPINIdentity(1), h.issuer.profile, h.issuer.profileLifetime, 1, 1)
	got, err := h.dispatch(t, usPINSet, h.input(t, command))
	if err != nil {
		t.Fatal(err)
	}
	image := got.Result.Image.Profile
	h.assertImageParity(t, image, h.issuer.profile)
	if image.Restrictions.RatingSystem != "mpaa" || image.Restrictions.MaximumAge != 13 ||
		image.Restrictions.AllowUnrated || !reflect.DeepEqual(image.Restrictions.BlockedLabels, []string{"violence", "gore"}) ||
		image.Restrictions.AllowDownloads || image.Restrictions.Revision != 4 ||
		image.Avatar == nil || image.Avatar.Version != 3 || image.Avatar.SourceMIME != "image/png" ||
		image.Avatar.URL != usPINProfileURL(h.issuer.account, h.issuer.profile, 3) {
		t.Fatalf("stored image facts: %+v", *image)
	}
}

// The retained PIN receipt is bound to the exact sequence: a later source edit
// is a NEW command, and the same operation cannot be re-stored at another
// sequence.
func TestUSPINOperationAndSequenceBinding(t *testing.T) {
	h := usPINDispatchFixture(t)
	command := h.command(t, usPINSet, usPINIdentity(1), h.issuer.profile, h.issuer.profileLifetime, 1, 1)
	if out, err := h.dispatch(t, usPINSet, h.input(t, command)); err != nil || out.Result.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	// ACK the original so an unacknowledged successor is a separate condition.
	if _, err := h.dispatch(t, usPINSet, &usPINInvocation{request: h.input(t, command).request, ack: true}); err != nil {
		t.Fatal(err)
	}
	// The SAME operation at another retained sequence is a conflict, not an
	// overwrite; the source effect that preceded the save rolls back.
	sameIdentity := usPINIdentity(1)
	sameIdentity.Sequence = 2
	same := h.command(t, usPINSet, sameIdentity, h.issuer.profile, h.issuer.profileLifetime, 2, 2)
	out, err := h.dispatch(t, usPINSet, h.input(t, same))
	if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, usPINOutcome{}) {
		t.Fatal("same operation re-stored", out, err)
	}
	if revision, pinRevision, _ := h.source(t, h.issuer.profile); revision != 2 || pinRevision != 2 {
		t.Fatal("conflicting re-store left a source prefix", revision, pinRevision)
	}
	// A different operation at an unacknowledged successor needs the ACK first.
	wc008NativeExec(t, h.w, `UPDATE security_command_owners SET acknowledged_sequence=0`)
	next := h.command(t, usPINClear, usPINIdentity(2), h.issuer.profile, h.issuer.profileLifetime, 2, 2)
	if out, err := h.dispatch(t, usPINClear, h.input(t, next)); !errors.Is(err, ErrResolveRequired) || !reflect.DeepEqual(out, usPINOutcome{}) {
		t.Fatal("unacknowledged successor accepted", out, err)
	}
}
