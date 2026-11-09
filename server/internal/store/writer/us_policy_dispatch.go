package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
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

	// usPolicyImageProfiles is the live-profile safety maximum for the caller
	// snapshot. It is well above the product's eight-profile write cap, so it
	// bounds legacy data instead of silently truncating a live profile: a 65th
	// live row is a named refusal, never a dropped row.
	usPolicyImageMaxProfiles int64 = 64
	// The caller snapshot charge covers the retained native row copy and the
	// decoded public DTO copy (2x the preflight octets) plus fixed per-profile
	// slice/cohort overhead and one image-wide header allowance.
	usPolicyImageProfileBytes int64 = 256
	usPolicyImageHeaderBytes  int64 = 1024

	usPolicyChangedEventType = "authority.changed"
)

// Statement inventory for the dispatcher's own reads, the retained-quota
// re-read and the private 541 caller-snapshot loader. None of these opens,
// commits, authenticates, registers a command or mints authority; authority,
// effect and receipt statements stay owned by US-01/US-05/US-06 files.
//
// The two-step shape is deliberate. Every variable (TEXT/BLOB) column first has
// an INTEGER-only octet preflight; the frame grant is charged from those exact
// octets before any TEXT/BLOB Scan, JSON decode, slice or DTO allocation.
const (
	usPolicyOwnerQuota           stmt.ID = "us.policy.owner.quota"
	usPolicyImageAccountOctets   stmt.ID = "us.policy.image.account_octets"
	usPolicyImageFamilyOctets    stmt.ID = "us.policy.image.family_octets"
	usPolicyImagePolicyOctets    stmt.ID = "us.policy.image.policy_octets"
	usPolicyImageHostedOctets    stmt.ID = "us.policy.image.hosted_octets"
	usPolicyImageCustodianOctets stmt.ID = "us.policy.image.custodian_octets"
	usPolicyImageProfilesOctets  stmt.ID = "us.policy.image.profiles_octets"
	usPolicyImageAccount         stmt.ID = "us.policy.image.account"
	usPolicyImageFamily          stmt.ID = "us.policy.image.family"
	usPolicyImagePolicy          stmt.ID = "us.policy.image.policy"
	usPolicyImageFactor          stmt.ID = "us.policy.image.factor"
	usPolicyImageHosted          stmt.ID = "us.policy.image.hosted"
	usPolicyImageCustodian       stmt.ID = "us.policy.image.custodian"
	usPolicyImageProfiles        stmt.ID = "us.policy.image.profiles"
)

type usPolicyInvocation struct {
	request usPolicyReceiptRequest
	input   *usLibraryPolicyCommand
	ack     bool
}

// usPolicyPublicImage is the frozen public shape of a 541 original: the CALLER's
// complete post-effect DirectSnapshot, exactly the identity service's
// directSnapshotTx / EditDirectProfile response. It is a response image captured
// in the SAME frame as the accepted original, never a receipt field. 541 closes
// result_body that way; a replay must never reconstruct it from live source.
//
// 540 has NO public original-response image (the product response is empty/204):
// its saved result and any replay carry no image and no target snapshot DTO.
type usPolicyPublicImage struct {
	// Snapshot is present only for a fresh 541 original.
	Snapshot *USDirectSnapshotImage
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
	ImageBytes   int64
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
	out        usPolicyDispatchResult
	floor      AckResult
	historical bool
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
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	ownerPlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_security_command_owners_1"}, Seeks: []string{"id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{usPolicyPoint("security_command_owners", "sqlite_autoindex_security_command_owners_1", "pk", "id")}}
	accountPlan := stmt.Plan{Hot: true,
		Indexes: []string{"sqlite_autoindex_accounts_1", "sqlite_autoindex_direct_memberships_1"},
		Seeks:   []string{"id=?", "account_id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{
			usPolicyPoint("accounts", "sqlite_autoindex_accounts_1", "pk", "id"),
			usPolicyPoint("direct_memberships", "sqlite_autoindex_direct_memberships_1", "pk", "account_id"),
		}}
	familyPlan := stmt.Plan{Hot: true,
		Indexes: []string{"sqlite_autoindex_authorization_session_families_1", "sqlite_autoindex_direct_profiles_2"},
		Seeks:   []string{"id=?", "account_id=? AND id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{
			usPolicyPoint("authorization_session_families", "sqlite_autoindex_authorization_session_families_1", "pk", "id"),
			usPolicyPoint("direct_profiles", "sqlite_autoindex_direct_profiles_2", "u", "account_id", "id"),
		}}
	policyPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"p", "s"}, Seeks: []string{"rowid=?"}}
	hostedPlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_account_portico_links_1"}, Seeks: []string{"account_id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{usPolicyPoint("account_portico_links", "sqlite_autoindex_account_portico_links_1", "pk", "account_id")}}
	custodianPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"hosted_membership_sync"}, Seeks: []string{"rowid=?"}}
	profilesPlan := stmt.Plan{Hot: true, Indexes: []string{"direct_profiles_account"}, Seeks: []string{"account_id=? AND deleted=?"}}
	defs := []stmt.Definition{
		{ID: usPolicyOwnerQuota, SQL: `SELECT command_incarnation,original_family_id,last_sequence,acknowledged_sequence,reclaimed_sequence,retained_count,retained_variable_bytes,retired_ms
FROM security_command_owners INDEXED BY sqlite_autoindex_security_command_owners_1
WHERE id=? AND ingress='authenticated' AND server_id=? AND issuer_account_id=? AND issuer_member_incarnation=? AND issuer_profile_id=? AND issuer_profile_lifetime=? AND issuer_installation_incarnation=?`,
			Params:  []stmt.Param{text("owner"), text("server"), text("account"), text("member"), text("profile"), text("lifetime"), text("installation")},
			Cohorts: []stmt.Cohort{{Name: "original_scope", Bindings: []any{"security-owner", "server", "issuer", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "issuer-profile", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "cccccccccccccccccccccccccccccccc"}}}, Plan: ownerPlan},
		{ID: usPolicyImageAccountOctets, SQL: `SELECT octet_length(a.username),octet_length(a.profile_id),octet_length(m.role),octet_length(m.allowed_libraries),m.revision,m.disabled
FROM accounts AS a INDEXED BY sqlite_autoindex_accounts_1
CROSS JOIN direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 ON m.account_id=a.id
WHERE a.id=?`,
			Params:  []stmt.Param{text("account")},
			Cohorts: []stmt.Cohort{{Name: "caller", Bindings: []any{"issuer"}}}, Plan: accountPlan},
		{ID: usPolicyImageFamilyOctets, SQL: `SELECT octet_length(f.role),octet_length(f.remember_until),octet_length(f.manage_until),octet_length(p.pin_hash),p.is_primary
FROM authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1
CROSS JOIN direct_profiles AS p INDEXED BY sqlite_autoindex_direct_profiles_2 ON p.account_id=f.account_id
WHERE f.id=? AND f.account_id=? AND p.id=?`,
			Params:  []stmt.Param{text("family"), text("account"), text("profile")},
			Cohorts: []stmt.Cohort{{Name: "caller", Bindings: []any{"issuer-family", "issuer", "issuer-profile"}}}, Plan: familyPlan},
		{ID: usPolicyImagePolicyOctets, SQL: `SELECT octet_length(p.require_two_step),p.pin_remember_days,octet_length(s.sign_in_kind)
FROM identity_sign_in_policy AS p
CROSS JOIN identity_state AS s ON s.singleton=1
WHERE p.singleton=1`,
			Params:  []stmt.Param{},
			Cohorts: []stmt.Cohort{{Name: "single", Bindings: []any{}}}, Plan: policyPlan},
		{ID: usPolicyImageHostedOctets, SQL: `SELECT octet_length(hosted_account_id) FROM account_portico_links INDEXED BY sqlite_autoindex_account_portico_links_1 WHERE account_id=?`,
			Params:  []stmt.Param{text("account")},
			Cohorts: []stmt.Cohort{{Name: "caller", Bindings: []any{"issuer"}}}, Plan: hostedPlan},
		{ID: usPolicyImageCustodianOctets, SQL: `SELECT octet_length(custodian) FROM hosted_membership_sync WHERE singleton=1`,
			Params:  []stmt.Param{},
			Cohorts: []stmt.Cohort{{Name: "single", Bindings: []any{}}}, Plan: custodianPlan},
		{ID: usPolicyImageProfilesOctets, SQL: `SELECT octet_length(p.id),octet_length(p.name),octet_length(p.art),coalesce(octet_length(p.allowed_libraries),0),p.position
FROM direct_profiles AS p INDEXED BY direct_profiles_account
WHERE p.account_id=? AND p.deleted=0 ORDER BY p.position,p.id LIMIT ?`,
			Params:  []stmt.Param{text("account"), integer("row_limit")},
			Cohorts: []stmt.Cohort{{Name: "caller", Bindings: []any{"issuer", int64(65)}}}, Plan: profilesPlan},
		{ID: usPolicyImageAccount, SQL: `SELECT a.username,a.profile_id,m.role,m.revision,m.disabled,m.allowed_libraries
FROM accounts AS a INDEXED BY sqlite_autoindex_accounts_1
CROSS JOIN direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 ON m.account_id=a.id
WHERE a.id=?`,
			Params:  []stmt.Param{text("account")},
			Cohorts: []stmt.Cohort{{Name: "caller", Bindings: []any{"issuer"}}}, Plan: accountPlan},
		{ID: usPolicyImageFamily, SQL: `SELECT f.role,f.remember_until,f.manage_until,length(p.pin_hash),p.is_primary
FROM authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1
CROSS JOIN direct_profiles AS p INDEXED BY sqlite_autoindex_direct_profiles_2 ON p.account_id=f.account_id
WHERE f.id=? AND f.account_id=? AND p.id=?`,
			Params:  []stmt.Param{text("family"), text("account"), text("profile")},
			Cohorts: []stmt.Cohort{{Name: "caller", Bindings: []any{"issuer-family", "issuer", "issuer-profile"}}}, Plan: familyPlan},
		{ID: usPolicyImagePolicy, SQL: `SELECT p.require_two_step,p.pin_remember_days,s.sign_in_kind
FROM identity_sign_in_policy AS p
CROSS JOIN identity_state AS s ON s.singleton=1
WHERE p.singleton=1`,
			Params:  []stmt.Param{},
			Cohorts: []stmt.Cohort{{Name: "single", Bindings: []any{}}}, Plan: policyPlan},
		{ID: usPolicyImageFactor, SQL: `SELECT 1 FROM identity_account_factors WHERE account_id=? AND confirmed=1`,
			Params:  []stmt.Param{text("account")},
			Cohorts: []stmt.Cohort{{Name: "caller", Bindings: []any{"issuer"}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_identity_account_factors_1"}, Seeks: []string{"account_id=?"}}},
		{ID: usPolicyImageHosted, SQL: `SELECT hosted_account_id FROM account_portico_links INDEXED BY sqlite_autoindex_account_portico_links_1 WHERE account_id=?`,
			Params:  []stmt.Param{text("account")},
			Cohorts: []stmt.Cohort{{Name: "caller", Bindings: []any{"issuer"}}}, Plan: hostedPlan},
		{ID: usPolicyImageCustodian, SQL: `SELECT custodian FROM hosted_membership_sync WHERE singleton=1`,
			Params:  []stmt.Param{},
			Cohorts: []stmt.Cohort{{Name: "single", Bindings: []any{}}}, Plan: custodianPlan},
		{ID: usPolicyImageProfiles, SQL: `SELECT p.id,p.name,p.is_primary,p.position,p.art,p.revision,p.pin_revision,length(p.pin_hash),p.allowed_libraries
FROM direct_profiles AS p INDEXED BY direct_profiles_account
WHERE p.account_id=? AND p.deleted=0 ORDER BY p.position,p.id LIMIT ?`,
			Params:  []stmt.Param{text("account"), integer("row_limit")},
			Cohorts: []stmt.Cohort{{Name: "caller", Bindings: []any{"issuer", int64(65)}}}, Plan: profilesPlan},
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
		v, e := finishCommand(ctx, commandLifetime(turn), usPolicyCompletion{out, floor, historical}, err)
		out, floor, historical, err = v.out, v.floor, v.historical, e
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
	identical, authorityChanged, err := usPolicyUnchanged(before, input)
	if err != nil {
		return out, floor, false, err
	}
	// The product compares SORTED COMPACTED sets for its authority event but
	// writes the prepared order: a reorder-only 540 still writes and bumps its
	// revision, and appends NO event.
	v.changed = authorityChanged
	if !identical {
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
	if !identical {
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
	// 540 has no original-response image. 541 freezes the CALLER's complete
	// post-effect snapshot; the loader charges its own preflight before any
	// TEXT/BLOB materialization.
	if v.kind == USProfileLibraries {
		image, imageBytes, e := captureUSPolicyCallerSnapshot(ctx, turn, a, input, v, nowMS)
		if e != nil {
			return out, floor, false, e
		}
		out.Image, out.ImageBytes = usPolicyPublicImage{Snapshot: image}, imageBytes
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
// projection. It reports two distinct product facts: identical (write and
// revision) and authorityChanged (the 540 event). The product publishes an
// authority.changed only when the SORTED COMPACTED library sets differ
// (identity/membership_change.go), while the row always receives the prepared
// order. It is not authority and it never writes.
func usPolicyUnchanged(before sql.NullString, input *usLibraryPolicyCommand) (identical, authorityChanged bool, err error) {
	if input == nil {
		return false, false, ErrInvalid
	}
	if before.Valid != input.policy.Valid {
		return false, true, nil
	}
	if !before.Valid {
		return true, false, nil
	}
	ids, err := decodeUSLibraryIDs(before.String)
	if err != nil {
		return false, false, err
	}
	want := input.request.libraries
	if want == nil {
		want = []string{}
	}
	identical = usPolicySameLibraries(ids, want)
	if input.kind == USMemberLibraries {
		return identical, !usPolicySameLibrarySet(ids, want), nil
	}
	// Profile policy publishes no event; "changed" keeps meaning the saved
	// policy differs from the fresh source (its own next request reads it live).
	return identical, !identical, nil
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

// captureUSPolicyCallerSnapshot freezes the CALLER's complete POST-EFFECT
// DirectSnapshot for a fresh 541 original, exactly the identity service's
// directSnapshotTx shape returned by EditDirectProfile. It is never the edited
// target's MemberProfile and never a member-level 540 image (540 has none).
//
// Authority binding: every key comes from the same-frame rechecked US-01 stamp's
// validated issuer (account/profile/lifetime/family), never the frozen command's
// claimed issuer and never a caller-supplied boolean. The stamp proves the
// current owner session only; this loader derives CanManage, Capabilities,
// ManagePINRequired, TwoStepSetupRequired and CustodyPending from current native
// facts with directSnapshotTx's own rules (including NULL custodian => false).
//
// Resource order: INTEGER-only octet/count preflights over the exact selected
// keys, overflow-checked sum, funded-grant charge, and only then any TEXT/BLOB
// Scan, JSON decode, slice or DTO allocation. The live profile read uses the
// direct_profiles_account index (deleted=0, position,id) with cap+1 refusal, so
// retired rows never consume the cap and a live row is never silently dropped.
func captureUSPolicyCallerSnapshot(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, input *usLibraryPolicyCommand, v usPolicySavedResult, nowMS int64) (out *USDirectSnapshotImage, charged int64, err error) {
	var done struct {
		image   *USDirectSnapshotImage
		charged int64
	}
	defer func() {
		value, e := finishCommand(ctx, commandLifetime(turn), done, err)
		out, charged, err = value.image, value.charged, e
	}()
	if turn == nil || turn.transaction == nil || a == nil || input == nil || v.kind != USProfileLibraries {
		return nil, 0, ErrInvalid
	}
	issuer := a.proof.witness.issuer
	if !usIdentifier(issuer.account) || !usIdentifier(issuer.profile) || !usIdentifier(issuer.family) {
		return nil, 0, ErrInvalid
	}
	// The snapshot is the caller's own account. A cross-account image would be a
	// different product operation and is not admitted here.
	if input.request.account != issuer.account || v.account != issuer.account {
		return nil, 0, ErrDisclosureDenied
	}

	// Integer-only preflight. No variable column is materialized yet.
	var preflight struct {
		account  [4]int64
		family   [3]int64
		policy   [2]int64
		hosted   [2]int64
		profiles int64
		rows     int64
	}
	row, err := usPolicyRow(ctx, turn, usPolicyImageAccountOctets, issuer.account)
	if err != nil {
		return nil, 0, err
	}
	var usernameN, primaryN, roleN, librariesN, revision, disabled sql.NullInt64
	if err = row.Scan(&usernameN, &primaryN, &roleN, &librariesN, &revision, &disabled); err != nil {
		return nil, 0, errors.Join(ErrResolveRequired, err)
	}
	for n, value := range []sql.NullInt64{usernameN, primaryN, roleN, librariesN} {
		if preflight.account[n], err = usPolicyOctet(value); err != nil {
			return nil, 0, err
		}
	}
	if !revision.Valid || revision.Int64 < 1 || !disabled.Valid || disabled.Int64 != 0 {
		return nil, 0, ErrResolveRequired
	}
	row, err = usPolicyRow(ctx, turn, usPolicyImageFamilyOctets, issuer.family, issuer.account, issuer.profile)
	if err != nil {
		return nil, 0, err
	}
	var familyRoleN, rememberN, manageN, pinN, primaryInt sql.NullInt64
	if err = row.Scan(&familyRoleN, &rememberN, &manageN, &pinN, &primaryInt); err != nil {
		return nil, 0, errors.Join(ErrResolveRequired, err)
	}
	for n, value := range []sql.NullInt64{familyRoleN, rememberN, manageN} {
		if preflight.family[n], err = usPolicyOctet(value); err != nil {
			return nil, 0, err
		}
	}
	if !primaryInt.Valid || (primaryInt.Int64 != 0 && primaryInt.Int64 != 1) {
		return nil, 0, ErrResolveRequired
	}
	row, err = usPolicyRow(ctx, turn, usPolicyImagePolicyOctets)
	if err != nil {
		return nil, 0, err
	}
	var twoStepN, rememberDays, kindN sql.NullInt64
	if err = row.Scan(&twoStepN, &rememberDays, &kindN); err != nil {
		return nil, 0, errors.Join(ErrResolveRequired, err)
	}
	if preflight.policy[0], err = usPolicyOctet(twoStepN); err != nil {
		return nil, 0, err
	}
	if preflight.policy[1], err = usPolicyOctet(kindN); err != nil {
		return nil, 0, err
	}
	if !rememberDays.Valid || rememberDays.Int64 < 0 {
		return nil, 0, ErrResolveRequired
	}
	hostedPresent := false
	hostedRow, err := usPolicyRow(ctx, turn, usPolicyImageHostedOctets, issuer.account)
	if err != nil {
		return nil, 0, err
	}
	var hostedN sql.NullInt64
	switch scan := hostedRow.Scan(&hostedN); {
	case errors.Is(scan, sql.ErrNoRows):
		// No Hosted link: both facts are absent, exactly as directSnapshotTx.
	case scan != nil:
		return nil, 0, errors.Join(ErrResolveRequired, scan)
	default:
		if preflight.hosted[0], err = usPolicyOctet(hostedN); err != nil {
			return nil, 0, err
		}
		hostedPresent = true
	}
	if hostedPresent {
		custodianRow, e := usPolicyRow(ctx, turn, usPolicyImageCustodianOctets)
		if e != nil {
			return nil, 0, e
		}
		var custodianN sql.NullInt64
		switch scan := custodianRow.Scan(&custodianN); {
		case errors.Is(scan, sql.ErrNoRows), scan == nil && !custodianN.Valid:
			// No custodian row/fact: NULL custodian, exactly as the product.
		case scan != nil:
			return nil, 0, errors.Join(ErrResolveRequired, scan)
		default:
			if preflight.hosted[1], err = usPolicyOctet(custodianN); err != nil {
				return nil, 0, err
			}
		}
	}
	rows, err := usPolicyRows(ctx, turn, usPolicyImageProfilesOctets, issuer.account, usPolicyImageMaxProfiles+1)
	if err != nil {
		return nil, 0, err
	}
	for rows.Next() {
		var idN, nameN, artN, librariesN, position sql.NullInt64
		if err = rows.Scan(&idN, &nameN, &artN, &librariesN, &position); err != nil {
			_ = rows.Close()
			return nil, 0, err
		}
		preflight.rows++
		if preflight.rows > usPolicyImageMaxProfiles {
			_ = rows.Close()
			return nil, 0, &Refusal{Code: RefusalCapacity}
		}
		for _, value := range []sql.NullInt64{idN, nameN, artN, librariesN} {
			n, e := usPolicyOctet(value)
			if e != nil {
				_ = rows.Close()
				return nil, 0, e
			}
			if preflight.profiles, err = usPolicyOctetsSum(preflight.profiles, n); err != nil {
				_ = rows.Close()
				return nil, 0, err
			}
		}
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, 0, err
	}
	if err = rows.Close(); err != nil {
		return nil, 0, err
	}
	raw := int64(0)
	for _, n := range preflight.account {
		if raw, err = usPolicyOctetsSum(raw, n); err != nil {
			return nil, 0, err
		}
	}
	for _, n := range preflight.family {
		if raw, err = usPolicyOctetsSum(raw, n); err != nil {
			return nil, 0, err
		}
	}
	for _, n := range preflight.policy {
		if raw, err = usPolicyOctetsSum(raw, n); err != nil {
			return nil, 0, err
		}
	}
	for _, n := range preflight.hosted {
		if raw, err = usPolicyOctetsSum(raw, n); err != nil {
			return nil, 0, err
		}
	}
	if raw, err = usPolicyOctetsSum(raw, preflight.profiles); err != nil {
		return nil, 0, err
	}
	// The image also carries three fixed strings that no source column supplies:
	// Authority is the literal "local", and ServerID/Account.ID are the validated
	// issuer's identity Token() values (32 random bytes in base64.RawURLEncoding,
	// so 43 octets each). They are not source-derived, so preflight their exact
	// lengths on the raw side before the comparison; otherwise an ordinary 541
	// would exceed its own source octets and refuse after the effect.
	for _, fixed := range []string{"local", issuer.server, issuer.account} {
		if raw, err = usPolicyOctetsSum(raw, int64(len(fixed))); err != nil {
			return nil, 0, err
		}
	}
	// Both the retained native copy and the decoded DTO copy are resident while
	// the image is built; per-profile slice overhead and the image header are
	// additive. The charge is proved BEFORE any materialization.
	charged = raw
	if charged, err = usPolicyOctetsSum(charged, raw); err != nil {
		return nil, 0, err
	}
	for n := preflight.rows; n > 0; n-- {
		if charged, err = usPolicyOctetsSum(charged, usPolicyImageProfileBytes); err != nil {
			return nil, 0, err
		}
	}
	if charged, err = usPolicyOctetsSum(charged, usPolicyImageHeaderBytes); err != nil {
		return nil, 0, err
	}
	if err = a.budget.check(ctx, turn, charged); err != nil {
		return nil, 0, err
	}
	if err = usPolicyDispatchFund(a, charged); err != nil {
		return nil, 0, err
	}
	done.charged = charged

	// Charged. Only now may variable source become Go memory.
	row, err = usPolicyRow(ctx, turn, usPolicyImageAccount, issuer.account)
	if err != nil {
		return nil, 0, err
	}
	var username, primary, role, libraries string
	if err = row.Scan(&username, &primary, &role, &revision, &disabled, &libraries); err != nil {
		return nil, 0, errors.Join(ErrResolveRequired, err)
	}
	if primary != issuer.profile || (role != "owner" && role != "admin" && role != "member") || !revision.Valid || revision.Int64 < 1 || !disabled.Valid || disabled.Int64 != 0 {
		return nil, 0, ErrResolveRequired
	}
	accountLibraries, err := decodeUSLibraryIDs(libraries)
	if err != nil {
		return nil, 0, err
	}
	if accountLibraries == nil {
		accountLibraries = []string{}
	}

	row, err = usPolicyRow(ctx, turn, usPolicyImageFamily, issuer.family, issuer.account, issuer.profile)
	if err != nil {
		return nil, 0, err
	}
	var familyRole, rememberUntil, manageUntil string
	var pinLength sql.NullInt64
	if err = row.Scan(&familyRole, &rememberUntil, &manageUntil, &pinLength, &primaryInt); err != nil {
		return nil, 0, errors.Join(ErrResolveRequired, err)
	}
	if !usPolicyFamilyRole(familyRole) || !primaryInt.Valid || (primaryInt.Int64 != 0 && primaryInt.Int64 != 1) {
		return nil, 0, ErrResolveRequired
	}
	// directSnapshotTx tests PIN presence (`pin_hash IS NOT NULL`), never a
	// non-empty length: an empty stored hash still protects the profile. The
	// closed projection grammar cannot spell IS NOT NULL, but length() is NULL
	// only for a NULL hash, so NullInt64.Valid is that presence test exactly.
	primaryPIN := pinLength.Valid

	row, err = usPolicyRow(ctx, turn, usPolicyImagePolicy)
	if err != nil {
		return nil, 0, err
	}
	var requireTwoStep, signInKind string
	if err = row.Scan(&requireTwoStep, &rememberDays, &signInKind); err != nil {
		return nil, 0, errors.Join(ErrResolveRequired, err)
	}
	if (requireTwoStep != "nobody" && requireTwoStep != "administrators" && requireTwoStep != "everyone") ||
		(signInKind != "local" && signInKind != "portico") || !rememberDays.Valid || rememberDays.Int64 < 0 {
		return nil, 0, ErrResolveRequired
	}
	// missingRequiredFactorTx, from current native facts only.
	required := usPolicyTwoStepRequired(requireTwoStep, signInKind, role)
	factorConfirmed := false
	if familyRole == "account" && required {
		factor, e := usPolicyRow(ctx, turn, usPolicyImageFactor, issuer.account)
		if e != nil {
			return nil, 0, e
		}
		var present int64
		switch scan := factor.Scan(&present); {
		case scan == nil && present == 1:
			factorConfirmed = true
		case scan == nil, errors.Is(scan, sql.ErrNoRows):
		default:
			return nil, 0, errors.Join(ErrResolveRequired, scan)
		}
	}
	// Under the US-01 stamp this family is always role='owner' and the issuer
	// profile is PIN-free (usAuthorityFamily/usAuthorityPrincipal), so the PIN
	// and account-scope branches below are unreachable through this loader;
	// TestUSPolicyDispatchSnapshotManagement covers the helper directly.
	canManage, twoStep, managePINRequired := usPolicySnapshotManage(familyRole,
		primaryInt.Int64 == 1, primaryPIN, required, factorConfirmed, rememberUntil, manageUntil,
		time.UnixMilli(nowMS).UTC().Format(time.RFC3339))

	hostedAccount := ""
	custodian := sql.NullString{}
	hostedRow, err = usPolicyRow(ctx, turn, usPolicyImageHosted, issuer.account)
	if err != nil {
		return nil, 0, err
	}
	switch scan := hostedRow.Scan(&hostedAccount); {
	case errors.Is(scan, sql.ErrNoRows):
		hostedPresent = false
	case scan != nil:
		return nil, 0, errors.Join(ErrResolveRequired, scan)
	}
	if hostedPresent {
		custodianRow, e := usPolicyRow(ctx, turn, usPolicyImageCustodian)
		if e != nil {
			return nil, 0, e
		}
		switch scan := custodianRow.Scan(&custodian); {
		case errors.Is(scan, sql.ErrNoRows):
		case scan != nil:
			return nil, 0, errors.Join(ErrResolveRequired, scan)
		}
	}
	custodyPending := false
	if hostedPresent && role == "owner" {
		// The product reads custodian only for a Hosted owner. A NULL custodian
		// is NOT pending; an empty or different string is.
		custodyPending = custodian.Valid && custodian.String != hostedAccount
	}

	profiles := make([]USDirectProfileImage, 0, 8)
	rows, err = usPolicyRows(ctx, turn, usPolicyImageProfiles, issuer.account, usPolicyImageMaxProfiles+1)
	if err != nil {
		return nil, 0, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var p USDirectProfileImage
		var pinHash sql.NullInt64
		var libraries sql.NullString
		if err = rows.Scan(&p.ID, &p.Name, &p.Primary, &p.Position, &p.Art, &p.Revision, &p.PINRevision, &pinHash, &libraries); err != nil {
			return nil, 0, err
		}
		if len(profiles) >= int(usPolicyImageMaxProfiles) {
			return nil, 0, &Refusal{Code: RefusalCapacity}
		}
		p.PINRequired = pinHash.Valid
		if libraries.Valid {
			ids, e := decodeUSLibraryIDs(libraries.String)
			if e != nil {
				return nil, 0, e
			}
			p.AllowedLibraries = ids
		}
		if p.ID == v.profile {
			// The edited profile must be the post-effect source this result
			// saved: NULL only for inherit, an explicit list otherwise, in the
			// exact prepared order. A NULL image against a saved explicit list
			// (or the reverse) is a source/result divergence, never an empty list.
			if libraries.Valid == v.inherits {
				return nil, 0, ErrResolveRequired
			}
			if libraries.Valid && !usPolicySameLibraries(p.AllowedLibraries, v.libraries) {
				return nil, 0, ErrResolveRequired
			}
		}
		if p.AllowedLibraries == nil {
			p.AllowedLibraries = []string{}
		}
		profiles = append(profiles, p)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}

	out = &USDirectSnapshotImage{
		TwoStepSetupRequired: twoStep,
		Authority:            "local",
		ServerID:             issuer.server,
		Account: USDirectAccountImage{ID: issuer.account, Username: username, PrimaryProfileID: primary, Role: role,
			Revision: revision.Int64, Disabled: false, AllowedLibraries: accountLibraries,
			DisabledAt: "", DisabledReason: "", HostedAccountID: hostedAccount},
		Profiles:          profiles,
		CanManage:         canManage,
		Capabilities:      usPolicySnapshotCapabilities(canManage, role),
		CustodyPending:    custodyPending,
		RememberProfiles:  rememberDays.Int64 > 0,
		ManagePINRequired: managePINRequired,
	}
	// Frozen carrier octet contract on the fresh image: the image can never cost
	// more variable bytes than the preflighted source it was decoded from.
	imageOctets, e := USDirectSnapshotImageOctets(*out)
	if e != nil || imageOctets > raw {
		return nil, 0, errors.Join(ErrResolveRequired, e)
	}
	done.image = out
	return out, charged, nil
}

// usPolicyTwoStepRequired is directSnapshotTx's requiresFactorTx predicate over
// already-validated native policy facts.
func usPolicyTwoStepRequired(requireTwoStep, signInKind, role string) bool {
	return signInKind == "local" && requireTwoStep != "nobody" &&
		(requireTwoStep == "everyone" || role == "owner" || role == "admin")
}

// usPolicySnapshotManage derives the product's management facts from current
// native facts exactly as directSnapshotTx does. It is pure so the PIN and
// account-scope branches, which the US-01 owner stamp cannot reach in the
// loader, stay directly exercised.
func usPolicySnapshotManage(familyRole string, primary, pinPresent bool, twoStepRequired, factorConfirmed bool,
	rememberUntil, manageUntil, now string) (canManage, twoStep, managePINRequired bool) {
	canManage = primary
	if canManage && familyRole == "account" && pinPresent {
		canManage = false
	}
	twoStep = familyRole == "account" && twoStepRequired && !factorConfirmed
	if twoStep {
		canManage = false
	}
	if canManage && pinPresent {
		// manageLockedTx: remember_until > now AND manage_until <= now. The empty
		// string compares <= now in SQLite, so it must not be guarded away.
		managePINRequired = rememberUntil > now && manageUntil <= now
	}
	return canManage, twoStep, managePINRequired
}

func usPolicySnapshotCapabilities(manage bool, role string) USDirectCapabilitiesImage {
	if !manage {
		return USDirectCapabilitiesImage{}
	}
	admin := role == "owner" || role == "admin"
	return USDirectCapabilitiesImage{ManageAccount: manage, ManagePeople: manage && admin, ManageServer: manage && admin, ManageSignIn: manage && role == "owner"}
}

func usPolicyFamilyRole(role string) bool {
	return role == "account" || role == "owner" || role == "admin" || role == "member"
}

// usPolicyOctet accepts one NOT NULL octet_length integer and refuses a NULL,
// negative or non-integral preflight fact before any materialization.
func usPolicyOctet(value sql.NullInt64) (int64, error) {
	if !value.Valid || value.Int64 < 0 {
		return 0, ErrResolveRequired
	}
	return value.Int64, nil
}

// usPolicyOctetsSum adds two non-negative preflight octets with overflow and
// closed-capacity refusal. It never wraps and never truncates.
func usPolicyOctetsSum(total, add int64) (int64, error) {
	if total < 0 || add < 0 || add > maxCanonicalResultBytes-total {
		return 0, &Refusal{Code: RefusalCapacity}
	}
	return total + add, nil
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

// usPolicySameLibrarySet mirrors identity.membershipLibrarySet: decode, sort and
// compact before comparing. Order and duplicates are not authority changes.
func usPolicySameLibrarySet(a, b []string) bool {
	left := append([]string(nil), a...)
	right := append([]string(nil), b...)
	sort.Strings(left)
	sort.Strings(right)
	left = slices.Compact(left)
	right = slices.Compact(right)
	return slices.Equal(left, right)
}

// runUSPolicyDispatch is the closed test/harness entry. It owns the final
// current disclosure and the FULL physical terminal; the caller supplies only
// the already admitted turn.
func runUSPolicyDispatch(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, inv *usPolicyInvocation) (out usPolicyDispatchResult, floor AckResult, err error) {
	var historical bool
	defer func() {
		v, e := finishCommand(ctx, ctx, usPolicyCompletion{out, floor, historical}, err)
		out, floor, err = v.out, v.floor, e
	}()
	out, floor, historical, err = executeUSPolicyInvocation(ctx, turn, a, inv)
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
	// own lifetime at commit/rollback, so this release uses the live frame
	// lifetime. The deferred final publication rechecks the CALLER's context
	// after physical return, exactly like the shared dispatcher.
	release, releaseErr := finishCommand(ctx, commandLifetime(turn), usPolicyCompletion{out, floor, historical}, nil)
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
		// finishCommand's release rule: no error, including a late cleanup
		// failure or a committed safety overrun, may carry output. Physical truth
		// stays in the private frame; retry must resolve the ORIGINAL.
		if commitErr != nil {
			return usPolicyDispatchResult{}, AckResult{}, commitErr
		}
		if inv.ack {
			return usPolicyDispatchResult{}, floor, nil
		}
		if historical {
			return out, floor, nil
		}
		// Only a submission that actually composed effects is Applied. A bare
		// Resolve of a never-applied command (input == nil) stays Pending, so its
		// zero Value/Digest can never be delivered as an applied original.
		if inv.input != nil && out.Outcome.State == Pending {
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
