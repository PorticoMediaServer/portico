package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// IP provider policy is one CAS revision over the retained library/provider
// owner row. It is not a per-user or per-title setting, never fans out into
// catalogue rows and never performs a synchronous library refresh. The saved
// typed receipt retains the exact accepted revision across later owner changes.
type ipPolicyRequest struct {
	libraryID                      int64
	libraryKey, provider           string
	policyIncarnation              [16]byte
	expectedRevision               int64
	enabled                        bool
	priority                       int64
	region, algorithm, refreshMode string
	creditEnabled                  bool
	creditAfterItem                *int64
	creditAfterProvider            string
	consentRevision                int64
}

// One durable sweep request per provider/purpose. It only resets the retained
// RAW cursor to the first entity phase; a later page command owns scheduling.
// The request is immediately discoverable through metadata_refresh_sweep_due
// because it sets job_next_attempt_ms (and therefore the generated job_due_ms)
// to the applied time. Begin never advances generation: IP-04 treats a changed
// generation as stale. epoch_ms is the applied now, not max(previous, now).
// Begin is refused while job_lease_owner is nonzero even if that lease has
// expired; IP-04 must clear the lease before a new request is accepted.
// Artwork never borrows the MusicBrainz metadata eligibility.
type ipRefreshRequest struct {
	provider, purpose                    string
	expectedGeneration, expectedRevision int64
	consentRevision                      int64
}

type ipPolicyCommand struct {
	identity CommandIdentity
	request  ipPolicyRequest
	digest   RequestDigest
	valid    bool
}

type ipRefreshCommand struct {
	identity CommandIdentity
	request  ipRefreshRequest
	digest   RequestDigest
	valid    bool
}

type ipPolicyCommandResult struct {
	Outcome ReceiptOutcome
	Value   ipPolicyResult
	Digest  ResultDigest
}

type ipRefreshCommandResult struct {
	Outcome ReceiptOutcome
	Value   ipRefreshResult
	Digest  ResultDigest
}

func (c *ipPolicyCommand) reference() ReceiptReference {
	return ReceiptReference{Identity: c.identity, RequestDigest: c.digest}
}

func (c *ipRefreshCommand) reference() ReceiptReference {
	return ReceiptReference{Identity: c.identity, RequestDigest: c.digest}
}

func (ipPolicyAuthorization) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "ipPolicyAuthorization")
}

func (ipPolicyCommand) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "ipPolicyCommand")
}

func (ipRefreshCommand) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "ipRefreshCommand")
}

func ipPolicyCommandIdentityValid(identity CommandIdentity) bool {
	return identity.Incarnation != [16]byte{} && identity.OperationID != [16]byte{} && identity.Sequence > 0
}

func ipPolicyRequestShape(in ipPolicyRequest) error {
	if in.libraryID <= 0 || len(in.libraryKey) == 0 || len(in.libraryKey) > 128 || !utf8.ValidString(in.libraryKey) ||
		!ipPolicyProviderCode(in.provider) || in.policyIncarnation == [16]byte{} || in.expectedRevision < 0 ||
		in.priority < 0 || in.priority > 1000 || !ipPolicyRegion(in.region) ||
		len(in.algorithm) == 0 || len(in.algorithm) > 64 || !utf8.ValidString(in.algorithm) ||
		!ipRefreshMode(in.refreshMode) || in.consentRevision <= 0 {
		return ErrInvalid
	}
	if (in.creditAfterItem == nil) != (in.creditAfterProvider == "") {
		return ErrInvalid
	}
	if in.creditAfterItem != nil && (*in.creditAfterItem < 0 || *in.creditAfterItem > 1_000_000) {
		return ErrInvalid
	}
	if len(in.creditAfterProvider) > 128 || !utf8.ValidString(in.creditAfterProvider) {
		return ErrInvalid
	}
	return nil
}

func ipRefreshRequestShape(in ipRefreshRequest) error {
	if !ipPolicyProviderCode(in.provider) || !ipRefreshPurpose(in.purpose) || in.consentRevision <= 0 ||
		in.expectedGeneration < 0 || in.expectedRevision < 0 ||
		(in.expectedGeneration == 0) != (in.expectedRevision == 0) {
		return ErrInvalid
	}
	if in.purpose == "artwork" && in.provider == "musicbrainz" {
		return ErrInvalid
	}
	return nil
}

// Called under W preparation custody BEFORE any writer hold. It performs no SQL
// and creates no durable allocation. Replay can discard this unused candidate.
func newIPPolicyCommand(identity CommandIdentity, in ipPolicyRequest) (*ipPolicyCommand, error) {
	if !ipPolicyCommandIdentityValid(identity) {
		return nil, ErrInvalid
	}
	if err := ipPolicyRequestShape(in); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(IPPolicySet, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &ipPolicyCommand{identity: identity, request: in, digest: digest, valid: true}, nil
}

func newIPRefreshCommand(identity CommandIdentity, in ipRefreshRequest) (*ipRefreshCommand, error) {
	if !ipPolicyCommandIdentityValid(identity) {
		return nil, ErrInvalid
	}
	if err := ipRefreshRequestShape(in); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(IPRefreshBegin, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &ipRefreshCommand{identity: identity, request: in, digest: digest, valid: true}, nil
}

// IP provider policy uses US's exact current-source predicates on a NORMAL
// frame, not its FULL authorization stamp. The value is private, bound to one
// actual turn and mints neither provider-worker nor public command capability.
type ipPolicyBudget struct{ memory uint64 }

func (b ipPolicyBudget) check(ctx context.Context, turn *commandTurn, bytes int64) error {
	if turn == nil || turn.transaction == nil {
		return ErrInvalid
	}
	tx := turn.transaction
	switch tx.descriptor.ID {
	case IPPolicySet, IPRefreshBegin, IPRefreshPage, IPEntityClaim, IPEntityRenew, IPEntityFinish,
		IPEntityEvidenceBegin, IPEntityEvidenceAppend, IPEntitySeal,
		IPTVSetBegin, IPTVSetPage, IPTVSetSeal, IPEntityAccept,
		IPArtworkDiscoveryClaim, IPArtworkDiscoveryPage, IPArtworkDiscoveryFinish,
		IPArtworkMaterialIntent, IPArtworkMaterialInstalled, IPArtworkMaterialPublish,
		IPArtworkSelect, IPArtworkClear:
	default:
		return ErrInvalid
	}
	d, err := Describe(tx.descriptor.ID)
	if err != nil || d != tx.descriptor || !tx.begun || tx.done || !tx.policyVerified {
		return ErrInvalid
	}
	sync := int(sqliteconn.Normal)
	if d.Durability == Full {
		sync = int(sqliteconn.Full)
	}
	if tx.settings.Synchronous != sync {
		return ErrInvalid
	}
	if bytes < 0 || bytes > maxCanonicalResultBytes || uint64(64<<10)+uint64(bytes)*32 > b.memory {
		return &Refusal{Code: RefusalCapacity}
	}
	g, ok := tx.custody.(*governedTurn)
	if !ok || g == nil || g.g == nil || g.a == nil || g.grant == nil {
		return &Refusal{Code: RefusalUnqualified}
	}
	if err := g.check(d.ID, tx.workLimit); err != nil {
		return err
	}
	if g.expected.Need[governor.MemoryBytes] <= 0 || b.memory > uint64(g.expected.Need[governor.MemoryBytes]) {
		return &Refusal{Code: RefusalUnqualified}
	}
	return turn.checkLifetimes(ctx)
}

type ipPolicyAuthorization struct {
	turn     *commandTurn
	proof    usOwnerAuthorityProof
	lifetime context.Context
	until    time.Time
	budget   ipPolicyBudget
}

func authorizeIPPolicyOwner(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, b ipPolicyBudget) (out *ipPolicyAuthorization, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if p == nil || lifetime == nil || lifetime.Err() != nil {
		return nil, ErrDisclosureDenied
	}
	if err = b.check(ctx, turn, 0); err != nil {
		return nil, err
	}
	tx := turn.transaction
	if tx.writer == nil || tx.writer.now == nil {
		return nil, ErrInvalid
	}
	now := tx.writer.now()
	if err = tx.observeTime(now); err != nil {
		return nil, err
	}
	if !p.tokenExpiry.After(now) || !p.horizon.After(now) {
		return nil, ErrDisclosureDenied
	}
	w, i := p.witness, p.witness.issuer
	var nativeBytes, pinGate int64
	row, e := wc008DraftQuery(ctx, turn, usAuthorityFamily, i.tokenDigest, i.family, i.familyGeneration, w.tokenExpiry, i.server, i.account, i.profile, i.epoch, i.authorityGeneration, i.memberIncarnation, i.profileLifetime, w.device, w.horizon, w.remember, w.manage, w.lineage)
	if e != nil {
		return nil, e
	}
	if e = row.Scan(&nativeBytes, &pinGate); e != nil {
		return nil, usAuthorityReadError(e)
	}
	if pinGate != 0 && pinGate != 1 {
		return nil, ErrResolveRequired
	}
	if err = b.check(ctx, turn, nativeBytes); err != nil {
		return nil, err
	}
	protected := false
	row, e = wc008DraftQuery(ctx, turn, usAuthorityPrincipal, i.account, i.epoch, i.memberIncarnation, i.profile, i.profileLifetime)
	if e != nil {
		return nil, e
	}
	var present int64
	e = row.Scan(&present)
	if errors.Is(e, sql.ErrNoRows) {
		if err = usAuthorityPresence(ctx, turn, usAuthorityPrincipalPIN, true, i.account, i.epoch, i.memberIncarnation, i.profile, i.profileLifetime); err != nil {
			return nil, err
		}
		protected = true
	} else if e != nil {
		return nil, e
	} else if present != 1 {
		return nil, ErrResolveRequired
	}
	for _, check := range []struct {
		id      stmt.ID
		args    []any
		present bool
	}{
		{usAuthorityCurrentServer, []any{i.server}, true},
		{usAuthorityState, []any{i.authorityGeneration}, true},
		{usAuthorityDevice, []any{w.device, i.account, w.installationID}, true},
		{usAuthorityInstallation, []any{p.installationHash, i.installation}, true},
		{usAuthorityGrant, []any{i.family}, false},
		{usAuthorityCredential, []any{i.family}, false},
		{usAuthorityResetSession, []any{i.family}, false},
		{usAuthorityWaiting, []any{i.account}, false},
	} {
		if err = usAuthorityPresence(ctx, turn, check.id, check.present, check.args...); err != nil {
			return nil, err
		}
	}
	if err = usAuthorityFenceCheck(ctx, turn, w, "device"); err != nil {
		return nil, err
	}
	until := p.tokenExpiry
	if p.horizon.Before(until) {
		until = p.horizon
	}
	if protected {
		if pinGate != 0 {
			return nil, ErrDisclosureDenied
		}
		if err = usAuthorityFenceCheck(ctx, turn, w, "profile_pin"); err != nil {
			return nil, err
		}
		if w.remember != "" {
			for _, kind := range []string{"profile_remember", "profile_lineage"} {
				if err = usAuthorityFenceCheck(ctx, turn, w, kind); err != nil {
					return nil, err
				}
			}
		}
		if p.remember.After(now) {
			if !p.manage.After(now) {
				return nil, ErrDisclosureDenied
			}
			if p.manage.Before(until) {
				until = p.manage
			}
			if p.remember.Before(until) {
				until = p.remember
			}
		}
	}
	if !until.After(tx.writer.now()) {
		return nil, ErrDisclosureDenied
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return nil, err
	}
	frozen := *p
	out = &ipPolicyAuthorization{turn: turn, proof: frozen, lifetime: lifetime, until: until, budget: b}
	return out, nil
}

// The stamp is bound to one actual turn; it cannot be reused after rollback,
// COMMIT (including unknown COMMIT), on another frame, or after revocation.
func (a *ipPolicyAuthorization) ipPolicyAuthorityRecheck(ctx context.Context, turn *commandTurn) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if a == nil || a.turn != turn || turn == nil || turn.transaction == nil || turn.transaction.done || a.lifetime == nil || a.lifetime.Err() != nil {
		return ErrDisclosureDenied
	}
	_, err = authorizeIPPolicyOwner(ctx, turn, &a.proof, a.lifetime, a.budget)
	return err
}

func ipPolicyLibraryScope(ctx context.Context, turn *commandTurn, libraryID int64, libraryKey string) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipPolicyLibrary, libraryKey, libraryID)
	if err != nil {
		return err
	}
	var actual int64
	if err = row.Scan(&actual); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDisclosureDenied
		}
		return err
	}
	if actual != 1 {
		return ErrDisclosureDenied
	}
	return nil
}

func ipPolicyProviderEnabled(ctx context.Context, turn *commandTurn, provider string) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipPolicyProvider, provider)
	if err != nil {
		return err
	}
	var present int64
	if err = row.Scan(&present); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDisclosureDenied
		}
		return err
	}
	if present != 1 {
		return ErrDisclosureDenied
	}
	return nil
}

// Screen metadata providers use screen_metadata_consent; MusicBrainz uses its
// separate online_lookups/lookup_epoch consent. A provider never borrows the
// other domain's revision.
func ipPolicyProviderConsent(ctx context.Context, turn *commandTurn, provider string, consentRevision int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if consentRevision <= 0 {
		return ErrInvalid
	}
	id := ipPolicyScreenConsent
	if provider == "musicbrainz" {
		id = ipPolicyMusicConsent
	}
	row, err := wc008DraftQuery(ctx, turn, id, consentRevision)
	if err != nil {
		return err
	}
	var present int64
	if err = row.Scan(&present); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDisclosureDenied
		}
		return err
	}
	if present != 1 {
		return ErrDisclosureDenied
	}
	return nil
}

type ipPolicyLookup struct {
	decision          receiptDecision
	owner             receiptOwnerFacts
	policyIncarnation [16]byte
	currentRevision   int64
	create            bool
	value             ipPolicyResult
	digest            ResultDigest
}

type ipRefreshLookup struct {
	decision          receiptDecision
	owner             receiptOwnerFacts
	currentGeneration int64
	currentRevision   int64
	create            bool
	value             ipRefreshResult
	digest            ResultDigest
}

func ipPolicyBool(n int64) (bool, bool) {
	switch n {
	case 0:
		return false, true
	case 1:
		return true, true
	}
	return false, false
}

func ipCloneInt64(p *int64) *int64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func ipPolicyOptionalValues(item *int64, provider string) (any, any) {
	if item == nil || provider == "" {
		return nil, nil
	}
	return *item, provider
}

func ipPolicyResultEqual(a, b ipPolicyResult) bool {
	if a.PolicyIncarnation != b.PolicyIncarnation || a.AcceptedRevision != b.AcceptedRevision || a.AppliedMS != b.AppliedMS ||
		a.ResultCount != b.ResultCount || a.Enabled != b.Enabled || a.CreditEnabled != b.CreditEnabled ||
		a.Priority != b.Priority || a.Region != b.Region || a.Algorithm != b.Algorithm ||
		a.RefreshMode != b.RefreshMode || a.CreditAfterProvider != b.CreditAfterProvider {
		return false
	}
	if (a.CreditAfterItem == nil) != (b.CreditAfterItem == nil) {
		return false
	}
	return a.CreditAfterItem == nil || *a.CreditAfterItem == *b.CreditAfterItem
}

func ipRefreshResultEqual(a, b ipRefreshResult) bool { return a == b }

func scanIPPolicyOwner(row commandRow) (out ipPolicyLookup, err error) {
	var incarnation, policyIncarnation []byte
	var enabled, priority, creditEnabled int64
	var region, algorithm, refreshMode string
	var item sql.NullInt64
	var creditProvider sql.NullString
	err = row.Scan(&incarnation, &out.owner.lastSequence, &out.owner.acknowledgedSequence,
		&policyIncarnation, &out.currentRevision, &enabled, &priority, &region, &algorithm,
		&refreshMode, &creditEnabled, &item, &creditProvider)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	on, ok := ipPolicyBool(enabled)
	creditOn, creditOK := ipPolicyBool(creditEnabled)
	if len(incarnation) != 16 || len(policyIncarnation) != 16 || !ok || !creditOK ||
		out.currentRevision <= 0 || priority < 0 || priority > 1000 || !ipPolicyRegion(region) ||
		len(algorithm) == 0 || len(algorithm) > 64 || !ipRefreshMode(refreshMode) ||
		(item.Valid != creditProvider.Valid) || item.Valid && (item.Int64 < 0 || item.Int64 > 1_000_000) {
		return ipPolicyLookup{}, ErrResolveRequired
	}
	out.owner.exists = true
	out.owner.incarnation = [16]byte(incarnation)
	out.policyIncarnation = [16]byte(policyIncarnation)
	_ = on
	_ = creditOn
	return out, nil
}

func ipPolicyLookupReceipt(ctx context.Context, turn *commandTurn, q ipPolicyRequest, ref ReceiptReference) (out ipPolicyLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IPPolicySet {
		return out, ErrInvalid
	}
	row, err := wc008DraftQuery(ctx, turn, ipPolicyOwner, q.libraryID, q.provider)
	if err != nil {
		return out, err
	}
	out, err = scanIPPolicyOwner(row)
	if err != nil {
		return out, err
	}
	row, err = wc008DraftQuery(ctx, turn, ipPolicyReceipt, q.libraryID, q.provider, ref.Identity.Incarnation[:], ref.Identity.Sequence)
	if err != nil {
		return out, err
	}
	var incarnation, operation, requestDigest, resultDigest, policyIncarnation []byte
	var sequence, appliedMS, acceptedRevision, enabled, priority, creditEnabled int64
	var region, algorithm, refreshMode string
	var item sql.NullInt64
	var creditProvider sql.NullString
	var found receiptFacts
	err = row.Scan(&incarnation, &sequence, &operation, &requestDigest, &appliedMS, &resultDigest,
		&policyIncarnation, &acceptedRevision, &enabled, &priority, &region, &algorithm,
		&refreshMode, &creditEnabled, &item, &creditProvider)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err != nil {
		return out, err
	} else {
		on, ok := ipPolicyBool(enabled)
		creditOn, creditOK := ipPolicyBool(creditEnabled)
		if len(incarnation) != 16 || len(operation) != 16 || len(requestDigest) != 32 || len(resultDigest) != 32 ||
			len(policyIncarnation) != 16 || sequence <= 0 || appliedMS < 0 || acceptedRevision <= 0 || !ok || !creditOK ||
			priority < 0 || priority > 1000 || !ipPolicyRegion(region) || len(algorithm) == 0 || len(algorithm) > 64 ||
			!ipRefreshMode(refreshMode) || (item.Valid != creditProvider.Valid) ||
			item.Valid && (item.Int64 < 0 || item.Int64 > 1_000_000) {
			return out, ErrResolveRequired
		}
		actual := receiptIdentity{command: ref.Identity}
		copy(actual.command.Incarnation[:], incarnation)
		copy(actual.command.OperationID[:], operation)
		copy(actual.digest[:], requestDigest)
		found = receiptFacts{exists: true, identity: actual, kind: IPPolicySet, resultCount: 1}
		copy(found.resultDigest[:], resultDigest)
		out.digest = found.resultDigest
		out.value = ipPolicyResult{PolicyIncarnation: [16]byte(policyIncarnation), AcceptedRevision: acceptedRevision,
			AppliedMS: appliedMS, ResultCount: 1, Enabled: on, Priority: priority, Region: region,
			Algorithm: algorithm, RefreshMode: refreshMode, CreditEnabled: creditOn,
			CreditAfterItem: ipNullableInt64(item), CreditAfterProvider: creditProvider.String}
	}
	if !out.owner.exists {
		if found.exists {
			return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
		}
		if q.expectedRevision == 0 && ref.Identity.Sequence == 1 {
			out.create = true
			out.decision = receiptFresh
			return out, nil
		}
		return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
	}
	out.decision, err = classifyReceipt(receiptIdentity{ref.Identity, ref.RequestDigest}, IPPolicySet, out.owner, found)
	if err != nil {
		return out, err
	}
	if out.decision == receiptReplay {
		computed, e := ipPolicyResultDigest(ref.Identity, ref.RequestDigest, out.value)
		if e != nil || computed != out.digest {
			return out, errors.Join(ErrResolveRequired, e)
		}
		out.digest = computed
	}
	return out, nil
}

func ipNullableInt64(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	value := v.Int64
	return &value
}

func ipRefreshOwnerFacts(ctx context.Context, turn *commandTurn, q ipRefreshRequest) (out ipRefreshLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, ipRefreshOwner, q.provider, q.purpose)
	if err != nil {
		return out, err
	}
	var incarnation []byte
	var epochMS, afterID, pageFrom, pageUntil, pageWaiting, leaseOwner int64
	var phase string
	err = row.Scan(&incarnation, &out.owner.lastSequence, &out.owner.acknowledgedSequence,
		&out.currentGeneration, &out.currentRevision, &epochMS, &phase,
		&afterID, &pageFrom, &pageUntil, &pageWaiting, &leaseOwner)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	waiting, ok := ipPolicyBool(pageWaiting)
	if len(incarnation) != 16 || out.currentGeneration <= 0 || out.currentRevision <= 0 || epochMS < 0 ||
		!(phase == "entities" || phase == "people" || phase == "complete") || afterID < 0 ||
		pageFrom < 0 || pageUntil < pageFrom || !ok || leaseOwner < 0 {
		return ipRefreshLookup{}, ErrResolveRequired
	}
	out.owner.exists = true
	out.owner.incarnation = [16]byte(incarnation)
	_ = waiting
	return out, nil
}

func ipRefreshLookupReceipt(ctx context.Context, turn *commandTurn, q ipRefreshRequest, ref ReceiptReference) (out ipRefreshLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IPRefreshBegin {
		return out, ErrInvalid
	}
	out, err = ipRefreshOwnerFacts(ctx, turn, q)
	if err != nil {
		return out, err
	}
	row, err := wc008DraftQuery(ctx, turn, ipRefreshReceipt, q.provider, q.purpose, ref.Identity.Incarnation[:], ref.Identity.Sequence)
	if err != nil {
		return out, err
	}
	var incarnation, operation, requestDigest, resultDigest []byte
	var sequence, appliedMS, acceptedRevision, generation, epochMS, afterID, scheduledCount, pageWaiting int64
	var kind, phase string
	var found receiptFacts
	err = row.Scan(&incarnation, &sequence, &operation, &requestDigest, &appliedMS, &resultDigest,
		&kind, &acceptedRevision, &generation, &epochMS, &phase, &afterID, &scheduledCount, &pageWaiting)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err != nil {
		return out, err
	} else {
		waiting, ok := ipPolicyBool(pageWaiting)
		if len(incarnation) != 16 || len(operation) != 16 || len(requestDigest) != 32 || len(resultDigest) != 32 ||
			sequence <= 0 || appliedMS < 0 || kind != "request" || acceptedRevision <= 0 || generation <= 0 ||
			epochMS < 0 || phase != "entities" || afterID != 0 || scheduledCount != 0 || !ok || waiting {
			return out, ErrResolveRequired
		}
		actual := receiptIdentity{command: ref.Identity}
		copy(actual.command.Incarnation[:], incarnation)
		copy(actual.command.OperationID[:], operation)
		copy(actual.digest[:], requestDigest)
		found = receiptFacts{exists: true, identity: actual, kind: IPRefreshBegin, resultCount: 0}
		copy(found.resultDigest[:], resultDigest)
		out.digest = found.resultDigest
		out.value = ipRefreshResult{Provider: q.provider, Purpose: q.purpose, Generation: generation,
			AcceptedRevision: acceptedRevision, EpochMS: epochMS, Phase: phase, AppliedMS: appliedMS}
	}
	if !out.owner.exists {
		if found.exists {
			return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
		}
		if q.expectedGeneration == 0 && q.expectedRevision == 0 && ref.Identity.Sequence == 1 {
			out.create = true
			out.decision = receiptFresh
			return out, nil
		}
		return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
	}
	out.decision, err = classifyReceipt(receiptIdentity{ref.Identity, ref.RequestDigest}, IPRefreshBegin, out.owner, found)
	if err != nil {
		return out, err
	}
	if out.decision == receiptReplay {
		computed, e := ipRefreshResultDigest(ref.Identity, ref.RequestDigest, out.value)
		if e != nil || computed != out.digest {
			return out, errors.Join(ErrResolveRequired, e)
		}
		out.digest = computed
	}
	return out, nil
}

// Current permission precedes historical receipt disclosure. The ORIGINAL
// saved result is returned without any fresh policy/consent/lease probe.
func applyIPPolicySet(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, b ipPolicyBudget, c *ipPolicyCommand) (out ipPolicyCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipPolicyCommandResult
	if c == nil || !c.valid || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IPPolicySet {
		return zero, ErrInvalid
	}
	q := c.request
	if err = ipPolicyRequestShape(q); err != nil {
		return zero, err
	}
	actual, err := canonicalRequestDigest(IPPolicySet, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPPolicyOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	lookup, err := ipPolicyLookupReceipt(ctx, turn, q, c.reference())
	if err != nil {
		return zero, err
	}
	out.Outcome.Reference = c.reference()
	switch lookup.decision {
	case receiptRetired:
		out.Outcome.State = ResultRetired
		return out, nil
	case receiptReplay:
		out.Outcome.State = Applied
		out.Outcome.Replayed = true
		out.Value, out.Digest = lookup.value, lookup.digest
		return out, nil
	case receiptFresh:
	default:
		return zero, ErrResolveRequired
	}
	if err = auth.ipPolicyAuthorityRecheck(ctx, turn); err != nil {
		return zero, err
	}
	if err = ipPolicyLibraryScope(ctx, turn, q.libraryID, q.libraryKey); err != nil {
		return zero, err
	}
	if err = ipPolicyProviderEnabled(ctx, turn, q.provider); err != nil {
		return zero, err
	}
	if err = ipPolicyProviderConsent(ctx, turn, q.provider, q.consentRevision); err != nil {
		return zero, err
	}
	now, err := turn.transaction.clockTime()
	if err != nil {
		return zero, err
	}
	appliedMS := now.UnixMilli()
	if appliedMS < 0 {
		return zero, ErrInvalid
	}
	item, provider := ipPolicyOptionalValues(q.creditAfterItem, q.creditAfterProvider)
	var acceptedRevision int64
	if lookup.create {
		if c.identity.Sequence != 1 || q.expectedRevision != 0 {
			return zero, ErrConflict
		}
		enabled := int64(0)
		if q.enabled {
			enabled = 1
		}
		creditEnabled := int64(0)
		if q.creditEnabled {
			creditEnabled = 1
		}
		row, e := wc008DraftQuery(ctx, turn, ipPolicyInsert, q.libraryID, q.provider, q.policyIncarnation[:],
			enabled, q.priority, q.region, q.algorithm, q.refreshMode, creditEnabled, item, provider,
			c.identity.Incarnation[:])
		if e != nil {
			return zero, errors.Join(ErrConflict, e)
		}
		if e = row.Scan(&acceptedRevision); e != nil || acceptedRevision != 1 {
			return zero, errors.Join(ErrResolveRequired, e)
		}
	} else {
		if lookup.policyIncarnation != q.policyIncarnation || lookup.currentRevision != q.expectedRevision ||
			q.expectedRevision == math.MaxInt64 {
			return zero, ErrConflict
		}
		enabled := int64(0)
		if q.enabled {
			enabled = 1
		}
		creditEnabled := int64(0)
		if q.creditEnabled {
			creditEnabled = 1
		}
		row, e := wc008DraftQuery(ctx, turn, ipPolicyUpdate, enabled, q.priority, q.region, q.algorithm,
			q.refreshMode, creditEnabled, item, provider, c.identity.Sequence, q.libraryID, q.provider,
			c.identity.Incarnation[:], q.policyIncarnation[:], q.expectedRevision,
			lookup.owner.lastSequence, lookup.owner.acknowledgedSequence)
		if e != nil {
			return zero, errors.Join(ErrConflict, e)
		}
		if e = row.Scan(&acceptedRevision); e != nil || acceptedRevision != q.expectedRevision+1 {
			return zero, errors.Join(ErrConflict, e)
		}
	}
	value := ipPolicyResult{PolicyIncarnation: q.policyIncarnation, AcceptedRevision: acceptedRevision,
		AppliedMS: appliedMS, ResultCount: 1, Enabled: q.enabled, Priority: q.priority, Region: q.region,
		Algorithm: q.algorithm, RefreshMode: q.refreshMode, CreditEnabled: q.creditEnabled,
		CreditAfterItem: ipCloneInt64(q.creditAfterItem), CreditAfterProvider: q.creditAfterProvider}
	digest, err := ipPolicyResultDigest(c.identity, c.digest, value)
	if err != nil {
		return zero, err
	}
	enabled := int64(0)
	if q.enabled {
		enabled = 1
	}
	creditEnabled := int64(0)
	if q.creditEnabled {
		creditEnabled = 1
	}
	written, err := turn.exec(ctx, turn.steps[ipPolicyRecord].statementStep, q.libraryID, q.provider,
		c.identity.Incarnation[:], c.identity.Sequence, c.identity.OperationID[:], c.digest[:],
		appliedMS, digest[:], q.policyIncarnation[:], acceptedRevision, enabled, q.priority, q.region,
		q.algorithm, q.refreshMode, creditEnabled, item, provider)
	if err != nil {
		return zero, err
	}
	count, err := written.RowsAffected()
	if err != nil || count != 1 {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	saved, err := ipPolicyLookupReceipt(ctx, turn, q, c.reference())
	if err != nil || saved.decision != receiptReplay || saved.digest != digest || !ipPolicyResultEqual(saved.value, value) {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.value, saved.digest, Applied
	return out, nil
}

func applyIPRefreshBegin(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, b ipPolicyBudget, c *ipRefreshCommand) (out ipRefreshCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipRefreshCommandResult
	if c == nil || !c.valid || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IPRefreshBegin {
		return zero, ErrInvalid
	}
	q := c.request
	if err = ipRefreshRequestShape(q); err != nil {
		return zero, err
	}
	actual, err := canonicalRequestDigest(IPRefreshBegin, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPPolicyOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	lookup, err := ipRefreshLookupReceipt(ctx, turn, q, c.reference())
	if err != nil {
		return zero, err
	}
	out.Outcome.Reference = c.reference()
	switch lookup.decision {
	case receiptRetired:
		out.Outcome.State = ResultRetired
		return out, nil
	case receiptReplay:
		out.Outcome.State = Applied
		out.Outcome.Replayed = true
		out.Value, out.Digest = lookup.value, lookup.digest
		return out, nil
	case receiptFresh:
	default:
		return zero, ErrResolveRequired
	}
	if err = auth.ipPolicyAuthorityRecheck(ctx, turn); err != nil {
		return zero, err
	}
	if err = ipPolicyProviderEnabled(ctx, turn, q.provider); err != nil {
		return zero, err
	}
	if err = ipPolicyProviderConsent(ctx, turn, q.provider, q.consentRevision); err != nil {
		return zero, err
	}
	now, err := turn.transaction.clockTime()
	if err != nil {
		return zero, err
	}
	appliedMS := now.UnixMilli()
	if appliedMS < 0 {
		return zero, ErrInvalid
	}
	var generation, revision, epochMS, dueMS int64
	if lookup.create {
		if c.identity.Sequence != 1 || q.expectedGeneration != 0 || q.expectedRevision != 0 {
			return zero, ErrConflict
		}
		row, e := wc008DraftQuery(ctx, turn, ipRefreshInsert, q.provider, q.purpose, appliedMS, appliedMS, c.identity.Incarnation[:])
		if e != nil {
			return zero, errors.Join(ErrConflict, e)
		}
		if e = row.Scan(&revision, &generation, &epochMS, &dueMS); e != nil || revision != 1 || generation != 1 || epochMS != appliedMS || dueMS != appliedMS {
			return zero, errors.Join(ErrResolveRequired, e)
		}
	} else {
		if lookup.currentGeneration != q.expectedGeneration || lookup.currentRevision != q.expectedRevision ||
			q.expectedRevision == math.MaxInt64 {
			return zero, ErrConflict
		}
		row, e := wc008DraftQuery(ctx, turn, ipRefreshUpdate, appliedMS, appliedMS, c.identity.Sequence, q.provider,
			q.purpose, c.identity.Incarnation[:], q.expectedGeneration, q.expectedRevision,
			lookup.owner.lastSequence, lookup.owner.acknowledgedSequence)
		if e != nil {
			return zero, errors.Join(ErrConflict, e)
		}
		if e = row.Scan(&revision, &generation, &epochMS, &dueMS); e != nil || revision != q.expectedRevision+1 ||
			generation != q.expectedGeneration || epochMS != appliedMS || dueMS != appliedMS {
			return zero, errors.Join(ErrConflict, e)
		}
	}
	value := ipRefreshResult{Provider: q.provider, Purpose: q.purpose, Generation: generation,
		AcceptedRevision: revision, EpochMS: epochMS, Phase: "entities", AppliedMS: appliedMS}
	digest, err := ipRefreshResultDigest(c.identity, c.digest, value)
	if err != nil {
		return zero, err
	}
	written, err := turn.exec(ctx, turn.steps[ipRefreshRecord].statementStep, q.provider, q.purpose,
		c.identity.Incarnation[:], c.identity.Sequence, c.identity.OperationID[:], c.digest[:],
		appliedMS, digest[:], revision, generation, epochMS, "entities")
	if err != nil {
		return zero, err
	}
	count, err := written.RowsAffected()
	if err != nil || count != 1 {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	saved, err := ipRefreshLookupReceipt(ctx, turn, q, c.reference())
	if err != nil || saved.decision != receiptReplay || saved.digest != digest || !ipRefreshResultEqual(saved.value, value) {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.value, saved.digest, Applied
	return out, nil
}
