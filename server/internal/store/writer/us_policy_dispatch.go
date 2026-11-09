package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// The FIRST closed accepting entry for the two library-policy purposes. It is
// private test/harness composition: no public Writer method, no product caller
// and no exported constructor. The harness supplies an already begun FULL
// private turn (admission stays with W); this file owns the original/Resolve/ACK
// classification, the receipt and event accounting, the retained quota, the
// frozen public image and the physical terminal.
//
// Settled here, from the US-06 review's open list:
//
//   - Retained receipt quota. An owner retains at most 64 receipts and 1 MiB of
//     variable receipt bytes. A fresh result that would pass either bound is
//     refused with RefusalCapacity BEFORE any source effect, so history stays
//     bounded even though US-26 reclaim does not exist yet. These are exact
//     counters, not a product user cap; reclaim refunds actual bytes.
//   - Event and revision effects. Membership library policy (540) is an
//     authority fact: the live mutation appends exactly ONE "authority.changed"
//     event to the member's authority audience and signs nobody out (see
//     identity/membership_change.go). Profile library policy (541) is read live
//     on the profile's next request, so it appends NO event (see
//     identity/direct_profiles.go EditDirectProfile). A no-change command
//     appends no event and bumps no revision.
//   - Retired owners. A fresh store or an ACK on an owner whose retired_ms is
//     non-zero is refused as owner-unavailable: retirement is a retention
//     boundary and may not be resurrected by a write. A replay whose original
//     is still physically retained stays readable with zero effects.
//   - original_family_id. It is provenance, not an equality predicate. The
//     retained owner is addressed by server/account/member incarnation/profile
//     lifetime/installation, so a RENEWED family of the same installation
//     resolves its original command; a different installation cannot read or
//     advance it. Requiring family-string equality would break every renewal.
//   - Recheck cost. The dispatcher funds its own authority rechecks. Each
//     recheck re-runs the complete US-01 current-source recipe and needs the
//     64 KiB base workspace, so the parent's funded memory must cover the
//     dispatcher's declared recheck count plus the measured body before the
//     dispatcher reads anything. A budget funded for one recheck fewer is
//     refused as unqualified.

const (
	// usPolicyOwnerReceipts and usPolicyOwnerReceiptBytes bound the retained
	// history of ONE security owner. A full 1,000-entry policy is about 62 KiB,
	// so 1 MiB holds a realistic working set of originals while remaining a hard
	// ceiling until US-26 reclaim exists.
	usPolicyOwnerReceipts     int64 = 64
	usPolicyOwnerReceiptBytes int64 = 1 << 20

	// Three dispatcher-owned rechecks: the entry recheck before any owner or
	// payload read, the scoped owner re-read that settles retirement/quota, and
	// the final current disclosure before the terminal.
	usPolicyDispatchRechecks int64 = 3
	usPolicyRecheckBytes     int64 = 64 << 10

	usPolicyChangedEventType = "authority.changed"
)

// Statement inventory for the dispatcher's own reads and the retained-quota
// re-read. None of these opens, commits, authenticates, registers a command or
// mints authority; authority, effect and receipt statements stay owned by
// US-01/US-05/US-06 files.
const (
	usPolicyOwnerQuota  stmt.ID = "us.policy.owner.quota"
	usPolicyImageMember stmt.ID = "us.policy.image.member"
	usPolicyImageList   stmt.ID = "us.policy.image.profiles"
	usPolicyImageState  stmt.ID = "us.policy.image.state"
	usPolicyImagePolicy stmt.ID = "us.policy.image.sign_in_policy"
	usPolicyImageFactor stmt.ID = "us.policy.image.factor"
	usPolicyImageOne    stmt.ID = "us.policy.image.profile"
)

type usPolicyInvocation struct {
	request usPolicyReceiptRequest
	input   *usLibraryPolicyCommand
	ack     bool
}

// usPolicyPublicImage is the frozen public shape of the changed subject, using
// the complete US-03 DirectSnapshot and US-04 MemberProfile images. It is a
// response image captured in the SAME frame as the accepted original, never a
// receipt field: 540/541 close result_body, and a replay must never reconstruct
// an original from live source. The immutable original is usPolicySavedResult.
type usPolicyPublicImage struct {
	// 540: the target membership's own snapshot; the account-level library list
	// is the field this purpose changes.
	Member *USDirectSnapshotImage
	// 541: the edited profile in its complete People shape.
	Profile *USMemberProfileImage
}

func (usPolicyPublicImage) String() string   { return "[us policy public image redacted]" }
func (usPolicyPublicImage) GoString() string { return "[us policy public image redacted]" }
func (usPolicyPublicImage) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte("[us policy public image redacted]"))
}

// usPolicyDispatchResult is the closed result of one dispatch call. Image is
// present only for the fresh original response. Replay returns the saved
// complete primitive result and never a source-derived image.
type usPolicyDispatchResult struct {
	Outcome ReceiptOutcome
	Value   usPolicySavedResult
	Digest  ResultDigest
	Image   usPolicyPublicImage

	// Exact accounting for this command, not a quota claim.
	ChargedBytes int64
	EventID      int64
	EventBytes   int64
	OwnerCount   int64
	OwnerBytes   int64
}

func (usPolicyDispatchResult) String() string   { return "usPolicyDispatchResult{redacted}" }
func (usPolicyDispatchResult) GoString() string { return "usPolicyDispatchResult{redacted}" }
func (usPolicyDispatchResult) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte("usPolicyDispatchResult{redacted}"))
}

type usPolicyCompletion struct {
	out   usPolicyDispatchResult
	floor AckResult
}

func (usPolicyCompletion) String() string   { return "usPolicyCompletion{redacted}" }
func (usPolicyCompletion) GoString() string { return "usPolicyCompletion{redacted}" }
func (usPolicyCompletion) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte("usPolicyCompletion{redacted}"))
}

func usPolicyPoint(table, index, origin string, columns ...string) stmt.AutoIndexBinding {
	b := stmt.AutoIndexBinding{Table: table, Name: index, Origin: origin}
	for _, column := range columns {
		b.Columns = append(b.Columns, stmt.IndexColumn{Name: column, Collation: "BINARY"})
	}
	return b
}

// usPolicyDispatchDefinitions is the dispatcher's own finite statement set.
func usPolicyDispatchDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	ownerPlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_security_command_owners_1"}, Seeks: []string{"id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{usPolicyPoint("security_command_owners", "sqlite_autoindex_security_command_owners_1", "pk", "id")}}
	memberPlan := stmt.Plan{Hot: true,
		Indexes: []string{"sqlite_autoindex_accounts_1", "sqlite_autoindex_direct_memberships_1", "sqlite_autoindex_configuration_1"},
		Seeks:   []string{"id=?", "account_id=?", "key=?"},
		AutoIndexes: []stmt.AutoIndexBinding{
			usPolicyPoint("accounts", "sqlite_autoindex_accounts_1", "pk", "id"),
			usPolicyPoint("direct_memberships", "sqlite_autoindex_direct_memberships_1", "pk", "account_id"),
			usPolicyPoint("configuration", "sqlite_autoindex_configuration_1", "pk", "key"),
		}}
	listPlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_direct_profiles_2"}, Seeks: []string{"account_id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{usPolicyPoint("direct_profiles", "sqlite_autoindex_direct_profiles_2", "u", "account_id", "id")}}
	statePlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"identity_state"}, Seeks: []string{"rowid=?"}}
	policyPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"identity_sign_in_policy"}, Seeks: []string{"rowid=?"}}
	factorPlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_identity_account_factors_1"}, Seeks: []string{"account_id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{usPolicyPoint("identity_account_factors", "sqlite_autoindex_identity_account_factors_1", "pk", "account_id")}}
	onePlan := stmt.Plan{Hot: true,
		Indexes: []string{"sqlite_autoindex_direct_profiles_2", "sqlite_autoindex_direct_memberships_1"},
		Seeks:   []string{"account_id=? AND id=?", "account_id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{
			usPolicyPoint("direct_profiles", "sqlite_autoindex_direct_profiles_2", "u", "account_id", "id"),
			usPolicyPoint("direct_memberships", "sqlite_autoindex_direct_memberships_1", "pk", "account_id"),
		}}
	defs := []stmt.Definition{
		{ID: usPolicyOwnerQuota, SQL: `SELECT command_incarnation,original_family_id,last_sequence,acknowledged_sequence,reclaimed_sequence,retained_count,retained_variable_bytes,retired_ms
FROM security_command_owners INDEXED BY sqlite_autoindex_security_command_owners_1
WHERE id=? AND ingress='authenticated' AND server_id=? AND issuer_account_id=? AND issuer_member_incarnation=? AND issuer_profile_id=? AND issuer_profile_lifetime=? AND issuer_installation_incarnation=?`,
			Params:  []stmt.Param{text("owner"), text("server"), text("account"), text("member"), text("profile"), text("lifetime"), text("installation")},
			Cohorts: []stmt.Cohort{{Name: "original_scope", Bindings: []any{"security-owner", "server", "issuer", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "issuer-profile", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "cccccccccccccccccccccccccccccccc"}}}, Plan: ownerPlan},
		{ID: usPolicyImageMember, SQL: `SELECT a.username,a.profile_id,m.role,m.revision,m.disabled,m.allowed_libraries,m.disabled_ms,m.disabled_reason,coalesce(l.hosted_account_id,''),coalesce(h.custodian,''),c.value
FROM accounts AS a INDEXED BY sqlite_autoindex_accounts_1
CROSS JOIN direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 ON m.account_id=a.id
LEFT JOIN account_portico_links AS l ON l.account_id=a.id
LEFT JOIN hosted_membership_sync AS h ON h.singleton=1
CROSS JOIN configuration AS c INDEXED BY sqlite_autoindex_configuration_1 ON c.key=?
WHERE a.id=?`,
			Params:  []stmt.Param{text("server_key"), text("account")},
			Cohorts: []stmt.Cohort{{Name: "target", Bindings: []any{"id", "us-target"}}}, Plan: memberPlan},
		{ID: usPolicyImageList, SQL: `SELECT id,name,is_primary,position,art,revision,pin_revision,coalesce(length(pin_hash),0),allowed_libraries,deleted
FROM direct_profiles INDEXED BY sqlite_autoindex_direct_profiles_2
WHERE account_id=? ORDER BY id LIMIT ?`,
			Params:  []stmt.Param{text("account"), {Name: "row_limit", Type: stmt.Integer}},
			Cohorts: []stmt.Cohort{{Name: "target", Bindings: []any{"us-target", int64(65)}}}, Plan: listPlan},
		{ID: usPolicyImageState, SQL: `SELECT sign_in_kind FROM identity_state WHERE singleton=1`,
			Params:  []stmt.Param{},
			Cohorts: []stmt.Cohort{{Name: "single", Bindings: []any{}}}, Plan: statePlan},
		{ID: usPolicyImagePolicy, SQL: `SELECT require_two_step,pin_remember_days FROM identity_sign_in_policy WHERE singleton=1`,
			Params:  []stmt.Param{},
			Cohorts: []stmt.Cohort{{Name: "single", Bindings: []any{}}}, Plan: policyPlan},
		{ID: usPolicyImageFactor, SQL: `SELECT account_id FROM identity_account_factors INDEXED BY sqlite_autoindex_identity_account_factors_1 WHERE account_id=? AND confirmed=1`,
			Params:  []stmt.Param{text("account")},
			Cohorts: []stmt.Cohort{{Name: "target", Bindings: []any{"us-target"}}}, Plan: factorPlan},
		{ID: usPolicyImageOne, SQL: `SELECT p.id,p.name,p.is_primary,p.position,p.art,p.revision,p.pin_revision,coalesce(length(p.pin_hash),0),p.allowed_libraries,
 m.role,m.disabled,
 r.rating_system,r.maximum_age_rating,r.maximum_age,r.allow_unrated,r.blocked_labels,r.allow_downloads,r.allow_live_tv,r.allow_dvr,r.allow_watch_together,r.revision,
 p.avatar_version,p.avatar_updated_at,p.avatar_source_mime
FROM direct_profiles AS p INDEXED BY sqlite_autoindex_direct_profiles_2
CROSS JOIN direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 ON m.account_id=p.account_id
LEFT JOIN profile_restrictions AS r ON r.profile_id=p.id
WHERE p.account_id=? AND p.id=? AND p.deleted=0`,
			Params:  []stmt.Param{text("account"), text("profile")},
			Cohorts: []stmt.Cohort{{Name: "target", Bindings: []any{"us-target", "us-profile"}}}, Plan: onePlan},
	}
	for n := range defs {
		defs[n].Kind = stmt.Read
		defs[n].Owner = "store/writer US policy dispatch"
		defs[n].CallSites = []string{"store/writer/us_policy_dispatch.go"}
	}
	return defs
}

// usPolicyDispatchFund charges the dispatcher's declared authority rechecks and
// the measured body bytes against the parent's funded memory. It never opens a
// second ledger: the parent's grant already reserves this memory.
func usPolicyDispatchFund(a *usOwnerAuthorization, body int64) error {
	if a == nil || body < 0 || body > maxCanonicalResultBytes {
		return ErrInvalid
	}
	need := uint64(usPolicyDispatchRechecks)*uint64(usPolicyRecheckBytes) + uint64(body)*32
	if need > a.budget.memory {
		return &Refusal{Code: RefusalUnqualified}
	}
	return nil
}

// usPolicyAppliedMS samples the trusted writer clock once for the immutable
// applied_ms, the event timestamp and the quota decision. No real clock is read
// here and no lease is invented: US purposes declare no command clock, so the
// transaction high-water is the only admissible source.
func usPolicyAppliedMS(turn *commandTurn) (int64, error) {
	if turn == nil || turn.transaction == nil || turn.transaction.writer == nil || turn.transaction.writer.now == nil {
		return 0, ErrInvalid
	}
	tx := turn.transaction
	if tx.done || !tx.begun {
		return 0, ErrClosed
	}
	now := tx.writer.now()
	ms := now.UnixMilli()
	if ms < 0 || !time.UnixMilli(ms).Equal(now.Truncate(time.Millisecond)) {
		return 0, ErrInvalid
	}
	if err := tx.observeTime(now); err != nil {
		return 0, err
	}
	if deadline, ok := tx.ctx.Deadline(); !ok || !now.Before(deadline) {
		return 0, context.DeadlineExceeded
	}
	return ms, nil
}

// usPolicyQuotaFacts is the scoped owner re-read with the exact retained
// counters and the original family provenance.
type usPolicyQuotaFacts struct {
	usPolicyOwnerFacts
	family string
}

func readUSPolicyQuota(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, ownerID string) (out usPolicyQuotaFacts, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), out, err)
		out, err = v, e
	}()
	if !usIdentifier(ownerID) || turn == nil || a == nil {
		return out, ErrInvalid
	}
	// The owner is read under the CURRENT issuer scope, before any payload.
	if err = a.recheck(ctx, turn); err != nil {
		return out, err
	}
	i := usPolicyIssuerFacts(a)
	row, err := usPolicyRow(ctx, turn, usPolicyOwnerQuota, ownerID, i.server, i.account, i.memberIncarnation, i.profile, i.profileLifetime, i.installation)
	if err != nil {
		return out, err
	}
	var inc []byte
	if err = row.Scan(&inc, &out.family, &out.lastSequence, &out.acknowledgedSequence, &out.reclaimed, &out.count, &out.bytes, &out.retiredMS); errors.Is(err, sql.ErrNoRows) {
		return out, nil
	} else if err != nil {
		return out, err
	}
	if len(inc) != 16 || !usIdentifier(out.family) || out.lastSequence < 0 || out.acknowledgedSequence < 0 || out.acknowledgedSequence > out.lastSequence || out.lastSequence-out.acknowledgedSequence > 1 || out.reclaimed < 0 || out.reclaimed > out.acknowledgedSequence || out.count < out.lastSequence-out.acknowledgedSequence || out.bytes < 0 || out.retiredMS < 0 || (out.count == 0) != (out.bytes == 0) {
		return out, ErrResolveRequired
	}
	out.exists = true
	copy(out.incarnation[:], inc)
	return out, turn.checkLifetimes(ctx)
}

// usPolicyQuotaAdmit refuses a fresh result that would pass either retained
// bound BEFORE any source effect. count/bytes are W's exact counters.
func usPolicyQuotaAdmit(owner usPolicyOwnerFacts, charge int64) error {
	if !owner.exists || owner.retiredMS != 0 {
		return ErrOwnerUnavailable
	}
	if charge <= 0 || charge > usPolicyOwnerReceiptBytes {
		return &Refusal{Code: RefusalCapacity}
	}
	if owner.count >= usPolicyOwnerReceipts || owner.bytes > usPolicyOwnerReceiptBytes-charge {
		return &Refusal{Code: RefusalCapacity}
	}
	if owner.lastSequence != owner.acknowledgedSequence {
		// The next sequence requires a durable predecessor ACK; that is an
		// ordinary protocol condition, not a capacity refusal.
		return ErrResolveRequired
	}
	return nil
}

// executeUSPolicyInvocation is the algorithm of the unpublished
// us_policy_composition.go draft, adapted to the merged US-01 names and to
// US-06's receipt adapter. It returns Pending for a fresh effect: only the
// physical terminal may declare Applied.
func executeUSPolicyInvocation(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, inv *usPolicyInvocation) (out usPolicyDispatchResult, floor AckResult, historical bool, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), usPolicyCompletion{out, floor}, err)
		out, floor, err = v.out, v.floor, e
	}()
	if turn == nil || turn.transaction == nil || a == nil || inv == nil {
		return out, floor, false, ErrInvalid
	}
	kind := turn.transaction.descriptor.ID
	q := inv.request
	if q.kind != kind || usPolicyReceiptKind(kind) != nil || q.reference.Identity.Incarnation == ([16]byte{}) || q.reference.Identity.OperationID == ([16]byte{}) || q.reference.Identity.Sequence <= 0 || q.reference.RequestDigest == (RequestDigest{}) {
		return out, floor, false, ErrInvalid
	}
	// An earlier prefix in this same frame may have advanced a source row, so
	// current authority is rechecked before ANY owner or payload read.
	if err = a.recheck(ctx, turn); err != nil {
		return out, floor, false, err
	}
	if err = a.budget.check(ctx, turn, 0); err != nil {
		return out, floor, false, err
	}
	if err = usPolicyDispatchFund(a, 0); err != nil {
		return out, floor, false, err
	}
	if inv.ack {
		if inv.input != nil {
			return out, floor, false, ErrInvalid
		}
		floor.AcknowledgedThrough, err = acknowledgeUSPolicyAfterAuthority(ctx, turn, a, q)
		return out, floor, true, err
	}
	// A submission to apply is admitted against the CURRENT retained owner
	// before classification: retirement may not be resurrected by a write, and
	// the quota is refused before any source effect. A bare Resolve/replay reads
	// the owner through the receipt adapter instead.
	if inv.input != nil {
		quota, e := readUSPolicyQuota(ctx, turn, a, q.ownerID)
		if e != nil {
			return out, floor, false, e
		}
		if quota.exists && quota.retiredMS != 0 && q.reference.Identity.Sequence > quota.lastSequence {
			return out, floor, false, ErrOwnerUnavailable
		}
	}
	lookup, err := lookupUSPolicy(ctx, turn, a, q)
	if err != nil {
		return out, floor, false, err
	}
	out.Outcome.Reference = q.reference
	out.OwnerCount, out.OwnerBytes = lookup.owner.count, lookup.owner.bytes
	switch lookup.decision {
	case receiptRetired:
		out.Outcome.State = ResultRetired
		return out, floor, true, nil
	case receiptReplay:
		out.Outcome.State = Applied
		out.Outcome.Replayed = true
		out.Value, out.Digest = lookup.result, lookup.digest
		return out, floor, true, nil
	case receiptFresh:
		out.Outcome.State = Pending
	default:
		return out, floor, false, ErrResolveRequired
	}
	if inv.input == nil {
		// Preparation/status only: no effect, no receipt, no event.
		return out, floor, false, nil
	}
	input := inv.input
	if input.kind != q.kind || input.identity != q.reference.Identity || input.digest != q.reference.RequestDigest || input.request.ownerID != q.ownerID || input.request.issuer != a.proof.witness.issuer {
		return out, floor, false, ErrConflict
	}
	// A fresh submission must be the exact frozen preparation. The historical
	// lookup above is deliberately independent of a now-stale preparation.
	digest, err := canonicalRequestDigest(input.kind, 1, input.identity, input.request)
	if err != nil || digest != input.digest {
		return out, floor, false, errors.Join(ErrConflict, err)
	}
	if err = a.budget.check(ctx, turn, int64(len(input.policy.String))); err != nil {
		return out, floor, false, err
	}
	if err = validateUSPolicyProjection(input); err != nil {
		return out, floor, false, err
	}
	before, revision, lifetime, err := readUSPolicyFreshTarget(ctx, turn, a, input)
	if err != nil {
		return out, floor, false, err
	}
	nowMS, err := usPolicyAppliedMS(turn)
	if err != nil {
		return out, floor, false, err
	}
	libraries := usPolicySavedLibraries(input)
	v := usPolicySavedResult{kind: q.kind, ownerID: q.ownerID, account: input.request.account, memberIncarnation: input.request.memberIncarnation,
		profile: input.request.profile, profileLifetime: input.request.profileLifetime, revision: revision, appliedMS: nowMS,
		inherits: !input.policy.Valid, libraries: libraries}
	unchanged, err := usPolicyUnchanged(before, input)
	if err != nil {
		return out, floor, false, err
	}
	v.changed = !unchanged
	if !unchanged {
		v.revision++
	}
	// Reserve the complete prospective immutable result BEFORE any source effect.
	charge, err := usPolicyResultVariableBytes(v)
	if err != nil {
		return out, floor, false, err
	}
	if err = usPolicyQuotaAdmit(lookup.owner, charge); err != nil {
		return out, floor, false, err
	}
	if err = usPolicyDispatchFund(a, charge); err != nil {
		return out, floor, false, err
	}
	if !unchanged {
		effect, e := applyUSLibraryPolicy(ctx, turn, input, before, revision)
		if e != nil {
			return out, floor, false, e
		}
		if !effect.changed || effect.after.revision != v.revision || effect.lifetime != lifetime {
			return out, floor, false, ErrResolveRequired
		}
	}
	out.Digest, err = usPolicyResultDigest(q.reference.Identity, q.reference.RequestDigest, v)
	if err != nil {
		return out, floor, false, err
	}
	charged, err := storeUSPolicyResult(ctx, turn, a, q, v, out.Digest)
	if err != nil || charged != charge {
		return out, floor, false, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.ChargedBytes = v, charged
	out.OwnerCount, out.OwnerBytes = lookup.owner.count+1, lookup.owner.bytes+charged
	// One scoped hint, only where the pinned product appends one. 540 is an
	// authority fact; 541 is read live on the profile's next request.
	if v.changed && v.kind == USMemberLibraries {
		event, e := appendWCAPIEvent(ctx, turn, usPolicyChangedEvent(v, nowMS))
		if e != nil {
			return out, floor, false, e
		}
		if event.insertedID <= 0 || event.insertedVariableBytes <= 0 {
			return out, floor, false, ErrResolveRequired
		}
		out.EventID, out.EventBytes = event.insertedID, event.insertedVariableBytes
	}
	out.Image, err = usPolicyCaptureImage(ctx, turn, v)
	if err != nil {
		return out, floor, false, err
	}
	return out, floor, false, turn.checkLifetimes(ctx)
}

// usPolicyChangedEvent builds the one closed hint for a membership-policy
// change. Audience, kind, resource and revision mirror the live product append
// in identity/membership_change.go; the payload stays empty.
func usPolicyChangedEvent(v usPolicySavedResult, atMS int64) wcAPIEvent {
	return wcAPIEvent{audience: apievents.AuthorityAudience("local", v.account), eventType: usPolicyChangedEventType,
		resourceKind: "account", resourceID: "self", revision: strconv.FormatInt(v.revision, 10), atMS: atMS}
}

// readUSPolicyFreshTarget reads the complete same-snapshot target facts behind
// the octet preflight. It never authenticates, and it runs only for a fresh
// original after the receipt verdict.
func readUSPolicyFreshTarget(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, input *usLibraryPolicyCommand) (policy sql.NullString, revision int64, lifetime string, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), struct {
			Policy   sql.NullString
			Revision int64
			Lifetime string
		}{policy, revision, lifetime}, err)
		policy, revision, lifetime, err = v.Policy, v.Revision, v.Lifetime, e
	}()
	if turn == nil || input == nil {
		return policy, 0, "", ErrInvalid
	}
	q := input.request
	id, args := usMemberPolicyOctets, []any{q.account, q.memberIncarnation}
	if input.kind == USProfileLibraries {
		id = usProfilePolicyOctets
		args = append(args, q.profile, q.profileLifetime)
	}
	row, err := usPolicyRow(ctx, turn, id, args...)
	if err != nil {
		return policy, 0, "", err
	}
	var sizes [3]int64
	if input.kind == USMemberLibraries {
		err = row.Scan(&sizes[0], &sizes[1])
	} else {
		err = row.Scan(&sizes[0], &sizes[1], &sizes[2])
	}
	if err != nil {
		return policy, 0, "", errors.Join(ErrConflict, err)
	}
	var total int64
	for _, n := range sizes {
		if n < 0 || n > maxCanonicalRequestBytes-total {
			return policy, 0, "", &Refusal{Code: RefusalCapacity}
		}
		total += n
	}
	if err = a.budget.check(ctx, turn, total); err != nil {
		return policy, 0, "", err
	}
	id = usMemberPolicyFacts
	if input.kind == USProfileLibraries {
		id = usProfilePolicyFacts
	}
	row, err = usPolicyRow(ctx, turn, id, args...)
	if err != nil {
		return policy, 0, "", err
	}
	var role, memberJSON string
	var memberRevision, disabled, deleted, primary int64
	if input.kind == USMemberLibraries {
		err = row.Scan(&role, &revision, &disabled, &memberJSON)
		policy = sql.NullString{String: memberJSON, Valid: true}
		lifetime = q.memberIncarnation
	} else {
		err = row.Scan(&role, &memberRevision, &disabled, &memberJSON, &revision, &deleted, &primary, &policy)
		lifetime = q.profileLifetime
	}
	if err != nil {
		return policy, 0, "", errors.Join(ErrConflict, err)
	}
	if disabled != 0 || deleted != 0 || revision != q.expectedRevision || int64(len(role)) != sizes[0] || int64(len(memberJSON)) != sizes[1] || input.kind == USProfileLibraries && int64(len(policy.String)) != sizes[2] {
		return policy, 0, "", ErrConflict
	}
	if role != "owner" && role != "admin" && role != "member" {
		return policy, 0, "", ErrDisclosureDenied
	}
	// This first recipe is independently issued owner/main-profile authority.
	// A member-level command never edits the owner account; profile policy is
	// the own-account profile-manager primitive, not cross-account power.
	if input.kind == USMemberLibraries && role == "owner" || input.kind == USProfileLibraries && q.account != a.proof.witness.issuer.account {
		return policy, 0, "", ErrDisclosureDenied
	}
	if _, err = decodeUSLibraryIDs(memberJSON); err != nil {
		return policy, 0, "", err
	}
	if policy.Valid {
		if _, err = decodeUSLibraryIDs(policy.String); err != nil {
			return policy, 0, "", err
		}
	}
	return policy, revision, lifetime, nil
}

// usPolicyUnchanged compares the fresh source value with the frozen prepared
// projection. It is not authority and it never writes.
func usPolicyUnchanged(before sql.NullString, input *usLibraryPolicyCommand) (bool, error) {
	if input == nil {
		return false, ErrInvalid
	}
	if before.Valid != input.policy.Valid {
		return false, nil
	}
	if !before.Valid {
		return true, nil
	}
	ids, err := decodeUSLibraryIDs(before.String)
	if err != nil {
		return false, err
	}
	want := input.request.libraries
	if want == nil {
		want = []string{}
	}
	return usPolicySameLibraries(ids, want), nil
}

// The cached SQL projection is not an independent request. A future internal
// composition bug cannot mutate policy JSON while retaining the old digest.
func validateUSPolicyProjection(input *usLibraryPolicyCommand) error {
	if input == nil {
		return ErrInvalid
	}
	wantValid := input.kind == USMemberLibraries || input.request.libraries != nil
	if input.policy.Valid != wantValid {
		return ErrConflict
	}
	if !wantValid {
		if input.policy.String != "" {
			return ErrConflict
		}
		return nil
	}
	ids, err := decodeUSLibraryIDs(input.policy.String)
	if err != nil {
		return errors.Join(ErrConflict, err)
	}
	want := input.request.libraries
	if want == nil {
		want = []string{}
	}
	if !usPolicySameLibraries(ids, want) {
		return ErrConflict
	}
	return nil
}

func usPolicySavedLibraries(input *usLibraryPolicyCommand) []string {
	// Only profile inherit saves SQL NULL. A 540 result is always the explicit
	// list (DDL forces policy_inherits=0), so a member nil denies by saving [].
	if input == nil {
		return nil
	}
	if input.kind == USProfileLibraries && !input.policy.Valid {
		return nil
	}
	out := make([]string, len(input.request.libraries))
	copy(out, input.request.libraries)
	if out == nil {
		out = []string{}
	}
	return out
}

// usPolicyCaptureImage captures the complete public image of the changed
// subject from ONE same-snapshot read after the effect. It never becomes a
// receipt and is never used to reconstruct a replay.
func usPolicyCaptureImage(ctx context.Context, turn *commandTurn, v usPolicySavedResult) (out usPolicyPublicImage, err error) {
	defer func() {
		img, e := finishCommand(ctx, commandLifetime(turn), out, err)
		out, err = img, e
	}()
	switch v.kind {
	case USMemberLibraries:
		image, e := captureUSPolicyMemberImage(ctx, turn, v)
		return usPolicyPublicImage{Member: image}, e
	case USProfileLibraries:
		image, e := captureUSPolicyProfileImage(ctx, turn, v)
		return usPolicyPublicImage{Profile: image}, e
	default:
		return usPolicyPublicImage{}, ErrInvalid
	}
}

func captureUSPolicyMemberImage(ctx context.Context, turn *commandTurn, v usPolicySavedResult) (out *USDirectSnapshotImage, err error) {
	defer func() {
		img, e := finishCommand(ctx, commandLifetime(turn), out, err)
		out, err = img, e
	}()
	row, err := usPolicyRow(ctx, turn, usPolicyImageMember, "id", v.account)
	if err != nil {
		return nil, err
	}
	var username, primary, role, libraries, disabledReason, hosted, custodian, server string
	var revision, disabled, disabledMS int64
	if err = row.Scan(&username, &primary, &role, &revision, &disabled, &libraries, &disabledMS, &disabledReason, &hosted, &custodian, &server); err != nil {
		return nil, errors.Join(ErrResolveRequired, err)
	}
	if revision != v.revision || disabled != 0 || !usIdentifier(primary) || !usIdentifier(server) || (role != "owner" && role != "admin" && role != "member") {
		return nil, ErrResolveRequired
	}
	ids, err := decodeUSLibraryIDs(libraries)
	if err != nil || !usPolicySameLibraries(ids, v.libraries) {
		return nil, ErrResolveRequired
	}
	if ids == nil {
		ids = []string{}
	}
	profiles, err := captureUSPolicyProfiles(ctx, turn, v.account)
	if err != nil {
		return nil, err
	}
	state, err := readUSPolicySignInFacts(ctx, turn, v.account)
	if err != nil {
		return nil, err
	}
	// This is the target membership's OWN snapshot: its primary profile manages
	// its own account unless an unconfirmed required factor blocks management.
	twoStep := state.twoStepRequired(role)
	manage := !twoStep
	return &USDirectSnapshotImage{
		TwoStepSetupRequired: twoStep,
		Authority:            "local",
		ServerID:             server,
		Account: USDirectAccountImage{ID: v.account, Username: username, PrimaryProfileID: primary, Role: role,
			Revision: v.revision, Disabled: false, AllowedLibraries: ids,
			DisabledAt: disabledAt(disabledMS, disabled), DisabledReason: disabledReason, HostedAccountID: hosted},
		Profiles:          profiles,
		CanManage:         manage,
		Capabilities:      usPolicyCapabilities(manage, role),
		CustodyPending:    hosted != "" && role == "owner" && custodian != hosted,
		RememberProfiles:  state.rememberProfiles,
		ManagePINRequired: false, // no session family inside a frozen image
	}, nil
}

func disabledAt(ms, disabled int64) string {
	if disabled == 0 || ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

func usPolicyCapabilities(manage bool, role string) USDirectCapabilitiesImage {
	if !manage {
		return USDirectCapabilitiesImage{}
	}
	admin := role == "owner" || role == "admin"
	return USDirectCapabilitiesImage{ManageAccount: manage, ManagePeople: manage && admin, ManageServer: manage && admin, ManageSignIn: manage && role == "owner"}
}

func captureUSPolicyProfiles(ctx context.Context, turn *commandTurn, account string) (out []USDirectProfileImage, err error) {
	defer func() {
		rows, e := finishCommand(ctx, commandLifetime(turn), out, err)
		out, err = rows, e
	}()
	rows, err := usPolicyRows(ctx, turn, usPolicyImageList, account, int64(65))
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	out = make([]USDirectProfileImage, 0, 8)
	for rows.Next() {
		var p USDirectProfileImage
		var libraries sql.NullString
		var pinHash, deleted int64
		if err = rows.Scan(&p.ID, &p.Name, &p.Primary, &p.Position, &p.Art, &p.Revision, &p.PINRevision, &pinHash, &libraries, &deleted); err != nil {
			return nil, err
		}
		if pinHash < 0 || (deleted != 0 && deleted != 1) {
			return nil, ErrResolveRequired
		}
		if deleted == 1 {
			continue
		}
		p.PINRequired = pinHash > 0
		if libraries.Valid {
			ids, e := decodeUSLibraryIDs(libraries.String)
			if e != nil {
				return nil, e
			}
			p.AllowedLibraries = ids
		}
		if p.AllowedLibraries == nil {
			p.AllowedLibraries = []string{}
		}
		if !usIdentifier(p.ID) || len(out) >= 64 {
			return nil, ErrResolveRequired
		}
		out = append(out, p)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	// The product cohort is position,id order; the index scan is id order, so
	// sorting in Go keeps the hot path free of a SQLite temporary B-tree.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Position != out[j].Position {
			return out[i].Position < out[j].Position
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

type usPolicySignInFacts struct {
	signInKind       string
	requireTwoStep   string
	rememberProfiles bool
	factorConfirmed  bool
}

func (s usPolicySignInFacts) twoStepRequired(role string) bool {
	if s.signInKind != "local" || s.requireTwoStep == "nobody" || s.factorConfirmed {
		return false
	}
	return s.requireTwoStep == "everyone" || role == "owner" || role == "admin"
}

func readUSPolicySignInFacts(ctx context.Context, turn *commandTurn, account string) (out usPolicySignInFacts, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), out, err)
		out, err = v, e
	}()
	row, err := usPolicyRow(ctx, turn, usPolicyImageState)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out.signInKind); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if out.signInKind != "local" && out.signInKind != "portico" {
		return out, ErrResolveRequired
	}
	row, err = usPolicyRow(ctx, turn, usPolicyImagePolicy)
	if err != nil {
		return out, err
	}
	var remember int64
	if err = row.Scan(&out.requireTwoStep, &remember); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if (out.requireTwoStep != "nobody" && out.requireTwoStep != "administrators" && out.requireTwoStep != "everyone") || remember < 0 {
		return out, ErrResolveRequired
	}
	confirm, err := usPolicyRow(ctx, turn, usPolicyImageFactor, account)
	if err != nil {
		return out, err
	}
	var present string
	switch scan := confirm.Scan(&present); {
	case scan == nil:
		out.factorConfirmed = true
	case errors.Is(scan, sql.ErrNoRows):
	default:
		return out, scan
	}
	out.rememberProfiles = remember > 0
	return out, nil
}

type usPolicyRestrictionRow struct {
	ratingSystem, maximumRating sql.NullString
	maximumAge                  sql.NullInt64
	allowUnrated                sql.NullInt64
	labels                      sql.NullString
	allowDownloads              sql.NullInt64
	allowLiveTV                 sql.NullInt64
	allowDVR                    sql.NullInt64
	allowWatchTogether          sql.NullInt64
	revision                    sql.NullInt64
}

func (r usPolicyRestrictionRow) image(profile string, primary bool, role string, disabled int64) (USMemberProfileRestrictionsImage, error) {
	if !r.revision.Valid {
		// identity.DefaultRestrictions: Recordings default on only for the
		// server owner's live primary profile.
		return USMemberProfileRestrictionsImage{ProfileID: profile, MaximumAge: -1, AllowUnrated: true, BlockedLabels: []string{},
			AllowDownloads: true, AllowLiveTV: true, AllowDVR: primary && role == "owner" && disabled == 0, AllowWatchTogether: true, Revision: 1}, nil
	}
	if !r.maximumAge.Valid || !r.allowUnrated.Valid || !r.labels.Valid || !r.allowDownloads.Valid || !r.allowLiveTV.Valid || !r.allowDVR.Valid || !r.allowWatchTogether.Valid || r.revision.Int64 < 1 {
		return USMemberProfileRestrictionsImage{}, ErrResolveRequired
	}
	labels, err := decodeUSLibraryIDs(r.labels.String)
	if err != nil {
		return USMemberProfileRestrictionsImage{}, err
	}
	if labels == nil {
		labels = []string{}
	}
	flag := func(v sql.NullInt64) (bool, error) {
		if v.Int64 != 0 && v.Int64 != 1 {
			return false, ErrResolveRequired
		}
		return v.Int64 == 1, nil
	}
	unrated, err := flag(r.allowUnrated)
	if err != nil {
		return USMemberProfileRestrictionsImage{}, err
	}
	downloads, err := flag(r.allowDownloads)
	if err != nil {
		return USMemberProfileRestrictionsImage{}, err
	}
	live, err := flag(r.allowLiveTV)
	if err != nil {
		return USMemberProfileRestrictionsImage{}, err
	}
	dvr, err := flag(r.allowDVR)
	if err != nil {
		return USMemberProfileRestrictionsImage{}, err
	}
	together, err := flag(r.allowWatchTogether)
	if err != nil {
		return USMemberProfileRestrictionsImage{}, err
	}
	if !r.ratingSystem.Valid || !r.maximumRating.Valid {
		return USMemberProfileRestrictionsImage{}, ErrResolveRequired
	}
	return USMemberProfileRestrictionsImage{ProfileID: profile, RatingSystem: r.ratingSystem.String, MaximumAgeRating: r.maximumRating.String,
		MaximumAge: int(r.maximumAge.Int64), AllowUnrated: unrated, BlockedLabels: labels, AllowDownloads: downloads,
		AllowLiveTV: live, AllowDVR: dvr, AllowWatchTogether: together, Revision: r.revision.Int64}, nil
}

func captureUSPolicyProfileImage(ctx context.Context, turn *commandTurn, v usPolicySavedResult) (out *USMemberProfileImage, err error) {
	defer func() {
		img, e := finishCommand(ctx, commandLifetime(turn), out, err)
		out, err = img, e
	}()
	row, err := usPolicyRow(ctx, turn, usPolicyImageOne, v.account, v.profile)
	if err != nil {
		return nil, err
	}
	var p USMemberProfileImage
	var libraries sql.NullString
	var role string
	var disabled, avatarVersion int64
	var avatarUpdated, avatarMIME sql.NullString
	var restriction usPolicyRestrictionRow
	var pinHash int64
	if err = row.Scan(&p.ID, &p.Name, &p.Primary, &p.Position, &p.Art, &p.Revision, &p.PINRevision, &pinHash, &libraries,
		&role, &disabled, &restriction.ratingSystem, &restriction.maximumRating, &restriction.maximumAge, &restriction.allowUnrated,
		&restriction.labels, &restriction.allowDownloads, &restriction.allowLiveTV, &restriction.allowDVR, &restriction.allowWatchTogether,
		&restriction.revision, &avatarVersion, &avatarUpdated, &avatarMIME); err != nil {
		return nil, errors.Join(ErrResolveRequired, err)
	}
	if p.Revision != v.revision || !usIdentifier(p.ID) || (role != "owner" && role != "admin" && role != "member") || pinHash < 0 {
		return nil, ErrResolveRequired
	}
	p.PINRequired = pinHash > 0
	if libraries.Valid {
		ids, e := decodeUSLibraryIDs(libraries.String)
		if e != nil || !usPolicySameLibraries(ids, v.libraries) {
			return nil, ErrResolveRequired
		}
		p.AllowedLibraries = ids
	}
	if p.AllowedLibraries == nil {
		p.AllowedLibraries = []string{}
	}
	p.Restrictions, err = restriction.image(v.profile, p.Primary, role, disabled)
	if err != nil {
		return nil, err
	}
	if avatarVersion > 0 && avatarUpdated.Valid && avatarMIME.Valid {
		p.Avatar = &USMemberProfileAvatarImage{ProfileID: v.profile, Version: avatarVersion, UpdatedAt: avatarUpdated.String, SourceMIME: avatarMIME.String,
			URL: "/v1/admin/access/members/" + v.account + "/profiles/" + v.profile + "/avatar?v=" + strconv.FormatInt(avatarVersion, 10)}
	}
	return &p, nil
}

func usPolicySameLibraries(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// runUSPolicyDispatch is the closed test/harness entry. It owns the final
// current disclosure and the FULL physical terminal; the caller supplies only
// the already admitted turn.
func runUSPolicyDispatch(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, inv *usPolicyInvocation) (out usPolicyDispatchResult, floor AckResult, err error) {
	out, floor, historical, err := executeUSPolicyInvocation(ctx, turn, a, inv)
	if err != nil {
		return withdrawUSPolicy(turn, err)
	}
	// Final current disclosure before the terminal: a release-time denial
	// REPLACES the body outcome and rolls the whole prefix back.
	if denial := a.recheck(ctx, turn); denial != nil {
		return withdrawUSPolicy(turn, denial)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return withdrawUSPolicy(turn, ctxErr)
	}
	// The release rule runs BEFORE the terminal: the transaction cancels its
	// own lifetime at commit/rollback, so a deferred finish afterwards would
	// misreport a committed command as canceled.
	release, releaseErr := finishCommand(ctx, commandLifetime(turn), usPolicyCompletion{out, floor}, nil)
	if releaseErr != nil {
		return withdrawUSPolicy(turn, releaseErr)
	}
	out, floor = release.out, release.floor
	if rowsErr := turn.closeRows(); rowsErr != nil {
		return withdrawUSPolicy(turn, rowsErr)
	}
	physical, commitErr := turn.transaction.commit()
	switch physical.state {
	case committed:
		if inv.ack {
			return usPolicyDispatchResult{}, floor, nil
		}
		if historical {
			return out, floor, nil
		}
		if out.Outcome.State == Pending {
			out.Outcome.State = Applied
		}
		return out, floor, nil
	case commitUnknown:
		_, _ = turn.transaction.rollback(commitErr)
		return usPolicyDispatchResult{}, AckResult{}, errors.Join(ErrResolveRequired, commitErr)
	default:
		_, _ = turn.transaction.rollback(commitErr)
		if commitErr == nil {
			commitErr = ErrResolveRequired
		}
		return usPolicyDispatchResult{}, AckResult{}, commitErr
	}
}

// withdrawUSPolicy rolls back and returns the zero closed result: no partial
// output, receipt, event or source effect survives any failure.
func withdrawUSPolicy(turn *commandTurn, err error) (usPolicyDispatchResult, AckResult, error) {
	if turn != nil && turn.transaction != nil {
		_, _ = turn.transaction.rollback(err)
	}
	return usPolicyDispatchResult{}, AckResult{}, err
}

// usPolicyReceiptForCommand builds the receipt reference of a frozen
// preparation. It is a comparison value, never proof.
func usPolicyReceiptForCommand(c *usLibraryPolicyCommand) (usPolicyReceiptRequest, error) {
	if c == nil {
		return usPolicyReceiptRequest{}, ErrInvalid
	}
	return usPolicyReceiptRequest{ownerID: c.request.ownerID, kind: c.kind, reference: ReceiptReference{Identity: c.identity, RequestDigest: c.digest}}, nil
}
