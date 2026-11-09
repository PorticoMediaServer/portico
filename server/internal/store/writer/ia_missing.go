package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"math"
	"path"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const iaMissingRequestVersion uint16 = 1

const iaMissingMaxKeyBytes = 512

// IA-19 marks one bounded missing window and nothing else. A window is the
// direct children of exactly one directory whose complete stable coverage IA-18
// durably verified. The window is keyset-paged by object id and capped at
// iaMissingMaxRows, so a turn changes at most eight rows and never an unbounded
// absence set. Its private outcome and history carry the exact typed result; no
// public facade, worker or producer is wired, so the accepting body stays
// uncalled outside this package.
type iaMissingAcceptOutcome struct {
	decision receiptDecision
	result   iaMissingResult
	digest   ResultDigest
}
type iaMissingHistory struct {
	decision receiptDecision
	result   iaMissingResult
	digest   ResultDigest
}

// Same-turn saved header, never a disclosure capability. The complete typed
// member cohort is decoded only after current authority and the receipt floor.
type iaMissingSavedHeader struct {
	facts                              receiptFacts
	acceptedRevision, appliedMS        int64
	sourceID, sourceIncarnation        string
	beforeKey, afterKey, phase, health string
	authoritative, processed, analyzed int64
	warnings, finishedMS               int64
}

// Prepared witnesses only. The sealed directory is what the caller believes its
// verified window was; the coverage sequence names the exact IA-18 receipt that
// authorized absence there. Neither is admitted from this request: a fresh turn
// re-reads the seal, the receipt and the local frontier before any mark.
type iaMissingRequest struct {
	scope  inventoryScope
	source inventorySource
	run    inventoryRun

	directory        iaMissingSeal
	coverageSequence int64

	// beforeKey is the exclusive object-id cursor inside this directory. The
	// empty cursor is the start of the directory, never an EOF alias.
	beforeKey string

	// pinsResolved is the prepared MP-10 seam. False refuses the window rather
	// than widening it beyond the resolved source pins.
	pinsResolved bool
}

// The exact sealed directory row. Identity, revision, cursor, state and the
// page/skip counters are all compared; an EOF cursor alone never authorizes an
// absence inference.
type iaMissingSeal struct {
	relativePath, identity, revision, cursor, state string
	pages, skipped                                  int64
}

type iaMissingCandidate struct {
	objectID, assetID, revision, relativePath, directoryPath string
	missingSince, lastMissingJob                             string
	observations                                             int64
}

type iaMissingCommand struct {
	identity CommandIdentity
	request  iaMissingRequest
	digest   RequestDigest
	valid    bool
}

func newIAMissingCommand(id CommandIdentity, r iaMissingRequest) (*iaMissingCommand, error) {
	// Counts precede all text traversal, copying and hashing.
	if !iaEpisodeCounts(reflect.ValueOf(r)) {
		return nil, invalid("ia_missing_count")
	}
	size := uint64(len(canonicalRequestCodec) + 4 + 16 + 8 + 16)
	iaEpisodeWireSize(reflect.ValueOf(r), &size)
	if size > maxCanonicalRequestBytes {
		return nil, &PolicyError{Field: "ia_missing_bytes", Limit: maxCanonicalRequestBytes, Cause: ErrCap}
	}
	if !iaEpisodeValidText(reflect.ValueOf(r)) || !utf8.Valid(r.source.resolvedRoot) || bytes.IndexByte(r.source.resolvedRoot, 0) >= 0 {
		return nil, invalid("ia_missing_text")
	}
	if err := validateIAMissingOriginal(id, r); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(IAMarkMissing, iaMissingRequestVersion, id, r)
	if err != nil {
		return nil, err
	}
	r.source.resolvedRoot = bytes.Clone(r.source.resolvedRoot)
	for _, s := range iaMissingTexts(&r) {
		*s = strings.Clone(*s)
	}
	return &iaMissingCommand{identity: id, request: r, digest: digest, valid: true}, nil
}

// The original is one exact prepared turn: exact run/lease witnesses, an exact
// sealed directory and the earlier IA-18 coverage receipt that verified it.
func validateIAMissingOriginal(id CommandIdentity, r iaMissingRequest) error {
	s, u, d := r.source, r.run, r.directory
	if id.Sequence <= 0 || id.OperationID == ([16]byte{}) || id.Incarnation == ([16]byte{}) ||
		r.scope.runID == "" || r.scope.libraryKey == "" || r.scope.libraryID <= 0 ||
		s.id == "" || s.incarnation == "" || s.rootIdentity == "" || s.generation <= 0 || len(s.resolvedRoot) == 0 || s.resolvedRoot[0] != '/' ||
		u.revision <= 0 || u.policyRevision <= 0 || u.leaseOwner <= 0 || u.leaseGeneration <= 0 || u.leaseExpiresMS <= 0 ||
		u.lastSequence != id.Sequence-1 || u.acknowledgedSequence != u.lastSequence || u.phase != "reconciling" || !inventoryScanMode(u.scanMode) {
		return invalid("ia_missing_original")
	}
	root := string(s.resolvedRoot)
	if strings.ContainsAny(root, `\`) || path.Clean(root) != root {
		return invalid("ia_missing_root")
	}
	if r.coverageSequence <= 0 || r.coverageSequence >= id.Sequence {
		return invalid("ia_missing_coverage_sequence")
	}
	if !inventoryPath(d.relativePath, true) || !iaMissingText(d.identity, iaMissingMaxKeyBytes) ||
		!iaMissingText(d.revision, iaMissingMaxKeyBytes) || !iaMissingTextAllowEmpty(d.cursor, iaMissingMaxKeyBytes) ||
		d.state != "done" || d.pages < 0 || d.skipped < 0 || d.skipped > d.pages {
		return invalid("ia_missing_directory")
	}
	if !iaMissingTextAllowEmpty(r.beforeKey, iaMissingMaxKeyBytes) {
		return invalid("ia_missing_cursor")
	}
	return nil
}

func iaMissingText(value string, limit int) bool {
	if len(value) == 0 {
		return false
	}
	return iaMissingTextAllowEmpty(value, limit)
}

func iaMissingTextAllowEmpty(value string, limit int) bool {
	if len(value) > limit || !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func iaMissingTexts(r *iaMissingRequest) []*string {
	return []*string{
		&r.scope.runID, &r.scope.libraryKey, &r.source.id, &r.source.incarnation, &r.source.rootIdentity,
		&r.run.phase, &r.run.scanMode, &r.beforeKey,
		&r.directory.relativePath, &r.directory.identity, &r.directory.revision, &r.directory.cursor, &r.directory.state,
	}
}

func (c *iaMissingCommand) checkOriginal() error {
	if c == nil || !c.valid {
		return invalid("ia_missing_command")
	}
	checked, err := newIAMissingCommand(c.identity, c.request)
	if err != nil || checked.digest != c.digest {
		return ErrConflict
	}
	return nil
}

// One governed missing window or original replay. Current owner authority is
// established BEFORE any retained owner/header read; fresh source, run, seal,
// coverage and frontier witnesses are compared only after the receipt is Fresh.
// A saved result is decoded from the immutable receipt/member cohort, never a
// live object row.
func acceptIAMissing(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, budget iaEntryBudget, reference ReceiptReference, r iaMissingRequest) (out iaMissingAcceptOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IAMarkMissing {
		return out, ErrInvalid
	}
	command, err := newIAMissingCommand(reference.Identity, r)
	if err != nil {
		return out, err
	}
	// The canonical request digest is recomputed by this writer. A caller's
	// mismatched digest is a conflicting original, never a fresh admission.
	if command.digest != reference.RequestDigest {
		return out, ErrConflict
	}
	authority, err := authorizeIAEntryOwner(ctx, turn, p, lifetime, budget)
	if err != nil {
		return out, err
	}
	owner, header, err := iaMissingReceiptFacts(ctx, turn, r.scope.runID, reference)
	if err != nil {
		return out, err
	}
	decision, err := classifyReceipt(receiptIdentity{reference.Identity, reference.RequestDigest}, IAMarkMissing, owner, header.facts)
	if err != nil {
		return out, err
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	switch decision {
	case receiptRetired:
		return iaMissingAcceptOutcome{decision: receiptRetired}, nil
	case receiptReplay:
		history, err := lookupIAMissing(ctx, turn, authority, command, header)
		if err != nil {
			return out, err
		}
		return iaMissingAcceptOutcome{decision: history.decision, result: history.result, digest: history.digest}, nil
	case receiptFresh:
		return applyIAMissing(ctx, turn, authority, command)
	default:
		return out, ErrResolveRequired
	}
}

// Owner and header facts are decoded by finite points scoped to the exact
// original run/incarnation/sequence. No current source/lease/object row filters
// history; a kind mismatch is a conflict, never a fresh admission.
func iaMissingReceiptFacts(ctx context.Context, turn *commandTurn, runID string, reference ReceiptReference) (owner receiptOwnerFacts, header iaMissingSavedHeader, err error) {
	row, err := wc008DraftQuery(ctx, turn, iaMissingOwner, runID)
	if err != nil {
		return owner, header, err
	}
	var incarnation []byte
	err = row.Scan(&incarnation, &owner.lastSequence, &owner.acknowledgedSequence)
	if errors.Is(err, sql.ErrNoRows) {
		return owner, header, nil
	}
	if err != nil {
		return owner, header, err
	}
	if len(incarnation) != 16 {
		return owner, header, ErrResolveRequired
	}
	owner.exists = true
	copy(owner.incarnation[:], incarnation)

	row, err = wc008DraftQuery(ctx, turn, iaMissingHeader, runID, reference.Identity.Incarnation[:], reference.Identity.Sequence)
	if err != nil {
		return owner, header, err
	}
	var operation, request, digest []byte
	var kind string
	err = row.Scan(&operation, &request, &kind, &header.facts.resultCount, &digest, &header.acceptedRevision, &header.appliedMS,
		&header.sourceID, &header.sourceIncarnation, &header.beforeKey, &header.afterKey, &header.phase,
		&header.authoritative, &header.processed, &header.analyzed, &header.warnings, &header.finishedMS, &header.health)
	if errors.Is(err, sql.ErrNoRows) {
		return owner, header, nil
	}
	if err != nil {
		return owner, header, err
	}
	if len(operation) != 16 || len(request) != 32 || len(digest) != 32 || header.finishedMS != 0 {
		return owner, header, ErrResolveRequired
	}
	header.facts.exists = true
	header.facts.identity.command = reference.Identity
	copy(header.facts.identity.command.OperationID[:], operation)
	copy(header.facts.identity.digest[:], request)
	copy(header.facts.resultDigest[:], digest)
	if kind == iaMissingReceiptKind {
		header.facts.kind = IAMarkMissing
	}
	return owner, header, nil
}

// Fresh effects are one complete unit: the exact run chronology advance, at
// most eight exact object marks and the typed receipt/member cohort. No
// catalogue, asset or Basic row is mutated; the durable mark is what a producer
// reads to queue its own work.
func applyIAMissing(ctx context.Context, turn *commandTurn, authority *iaEntryAuthorization, command *iaMissingCommand) (out iaMissingAcceptOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	r := command.request
	if turn == nil || turn.transaction == nil || turn.transaction.writer == nil || turn.transaction.writer.now == nil {
		return out, ErrInvalid
	}
	now := turn.transaction.writer.now()
	appliedMS := now.UnixMilli()
	appliedAt := iaMissingTimestamp(now)
	if appliedMS < 0 || appliedMS >= r.run.leaseExpiresMS {
		return out, ErrConflict
	}
	if id, err := wc008DraftInteger(ctx, turn, iaMissingLibrary, r.scope.libraryID, r.scope.libraryKey); err != nil || id != r.scope.libraryID {
		return out, errors.Join(ErrConflict, err)
	}
	row, err := wc008DraftQuery(ctx, turn, iaMissingSource, r.source.id, r.scope.libraryKey, r.source.generation,
		r.source.incarnation, r.source.rootIdentity, string(r.source.resolvedRoot), r.source.followSymlinks)
	if err != nil {
		return out, err
	}
	var sourceID, health string
	if err = row.Scan(&sourceID, &health); errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if sourceID != r.source.id || health != "ready" {
		return out, ErrConflict
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	// MP-10 is not built: an unresolved source pin refuses absence rather than
	// widening the window.
	if !r.pinsResolved {
		return out, errors.Join(ErrResolveRequired, iaCoveragePinsUnresolved)
	}
	if err = iaMissingCheckSeal(ctx, turn, r); err != nil {
		return out, err
	}
	if err = iaMissingCheckCoverage(ctx, turn, r, command.identity.Incarnation[:]); err != nil {
		return out, err
	}
	if err = iaMissingCheckFrontier(ctx, turn, r); err != nil {
		return out, err
	}
	candidates, err := iaMissingWindow(ctx, turn, r)
	if err != nil {
		return out, err
	}
	// The seal, coverage and frontier reads are current-authority evidence too: a
	// same-turn revocation observed there still precedes every write.
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	runRevision, err := iaMissingAdvanceRun(ctx, turn, command.identity, r)
	if err != nil {
		return out, err
	}
	members, err := iaMissingMarkWindow(ctx, turn, r, candidates, appliedAt)
	if err != nil {
		return out, err
	}
	facts := iaMissingFacts(r, members, runRevision, appliedMS, health)
	if err = preflightIAMissingResult(command.identity, command.digest, facts); err != nil {
		return out, err
	}
	digest, err := canonicalResultDigest(command.identity, command.digest, iaMissingResult{facts})
	if err != nil {
		return out, err
	}
	if err = iaMissingWriteReceipt(ctx, turn, command.identity, command.digest, facts, digest); err != nil {
		return out, err
	}
	// A later same-turn revocation still redacts the fresh result.
	if err = authority.recheck(ctx, turn); err != nil {
		return out, ErrDisclosureDenied
	}
	return iaMissingAcceptOutcome{decision: receiptFresh, result: iaMissingResult{facts}, digest: digest}, nil
}

// The exact directory seal is the CAS: identity, revision, cursor, state and the
// page/skip counters must all still equal the prepared observation. A changed
// directory is unstable, never an empty or absent subtree.
func iaMissingCheckSeal(ctx context.Context, turn *commandTurn, r iaMissingRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	row, err := wc008DraftQuery(ctx, turn, iaMissingSealRow, r.scope.runID, r.directory.relativePath)
	if err != nil {
		return err
	}
	var got iaMissingSeal
	got.relativePath = r.directory.relativePath
	if err = row.Scan(&got.identity, &got.revision, &got.cursor, &got.state, &got.pages, &got.skipped); errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return errors.Join(ErrConflict, err)
	}
	if got != r.directory || got.state != "done" {
		return ErrConflict
	}
	return nil
}

// Only an exact earlier IA-18 coverage receipt whose verified directory, seal
// identity and seal revision still match can authorize this window. A done
// directory without that receipt is an EOF observation, not a verified absence
// proof.
func iaMissingCheckCoverage(ctx context.Context, turn *commandTurn, r iaMissingRequest, incarnation []byte) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	row, err := wc008DraftQuery(ctx, turn, iaMissingCoverage, r.scope.runID, incarnation, r.coverageSequence)
	if err != nil {
		return err
	}
	var kind string
	var directory sql.NullString
	var identity, revision []byte
	var phase, sourceID, sourceIncarnation string
	var authoritative, processed, analyzed, warnings, finished int64
	err = row.Scan(&kind, &directory, &identity, &revision, &phase, &authoritative, &processed, &analyzed, &warnings, &finished, &sourceID, &sourceIncarnation)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrResolveRequired
	}
	if err != nil {
		return err
	}
	if kind != "inventory_verify" || !directory.Valid || directory.String != r.directory.relativePath ||
		string(identity) != r.directory.identity || string(revision) != r.directory.revision ||
		phase != "reconciling" || finished != 0 || (authoritative != 0 && authoritative != 1) ||
		sourceID != r.source.id || sourceIncarnation != r.source.incarnation ||
		processed < 0 || analyzed < 0 || warnings < 0 || analyzed > processed || warnings != processed-analyzed {
		return ErrConflict
	}
	return nil
}

// Durable local frontier evidence: no descendant directory may still be pending
// (partial) or blocked (unreadable), and no protected issue may sit in the
// subtree (refused). The exact directory row itself is sealed above.
func iaMissingCheckFrontier(ctx context.Context, turn *commandTurn, r iaMissingRequest) error {
	low, high := iaMissingBounds(r.directory.relativePath)
	for _, check := range []struct {
		id stmt.ID
	}{
		{iaMissingPending},
		{iaMissingBlocked},
		{iaMissingProtected},
	} {
		found, err := iaMissingExists(ctx, turn, check.id, r.scope.runID, low, high)
		if err != nil {
			return err
		}
		if found {
			return ErrConflict
		}
	}
	return nil
}

func iaMissingExists(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (found bool, err error) {
	defer func() { found, err = finishCommand(ctx, commandLifetime(turn), found, err) }()
	rows, err := turn.query(ctx, turn.steps[id].statementStep, args...)
	if err != nil {
		return false, err
	}
	found = rows.Next()
	return found, errors.Join(rows.Err(), rows.Close())
}

// Half-open native path bounds select only descendants of exactly this
// directory. UTF-8 cannot produce 0xff, so the root window stays complete
// without a LIKE pattern or a temporary sort.
func iaMissingBounds(directory string) (low, high string) {
	if directory == "." {
		return "", "\xff"
	}
	return directory + "/", directory + "0"
}

// The window is the direct children of exactly this verified directory. Objects
// already seen by this run, already missing, retired or outside the directory
// are distinct states and are never re-marked. A listing that skipped entries
// can only confirm an object that was already missing once; it can never create
// a first absence.
func iaMissingWindow(ctx context.Context, turn *commandTurn, r iaMissingRequest) (out []iaMissingCandidate, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	skipped := int64(0)
	if r.directory.skipped > 0 {
		skipped = 1
	}
	rows, err := turn.query(ctx, turn.steps[iaMissingWindowStmt].statementStep,
		r.source.id, r.source.incarnation, r.directory.relativePath, r.beforeKey, r.scope.runID, skipped, int64(iaMissingMaxRows))
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var c iaMissingCandidate
		if err = rows.Scan(&c.objectID, &c.assetID, &c.revision, &c.relativePath, &c.missingSince, &c.observations, &c.lastMissingJob); err != nil {
			return out, err
		}
		c.directoryPath = r.directory.relativePath
		if len(out) == iaMissingMaxRows {
			return out, ErrConflict
		}
		out = append(out, c)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	// A full directory path can be large; the whole changed window stays inside
	// the stated byte bound.
	var total int64
	for _, c := range out {
		n, err := iaMissingCandidateBytes(c)
		if err != nil || n > iaMissingMaxWindowBytes-total {
			return out, ErrCap
		}
		total += n
	}
	return out, nil
}

// Every mark is an exact old-state CAS on the one retained object row. A row
// that changed after the window read refuses the whole unit.
func iaMissingMarkWindow(ctx context.Context, turn *commandTurn, r iaMissingRequest, candidates []iaMissingCandidate, appliedAt string) (out []iaMissingMember, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	for _, c := range candidates {
		since, observations := c.missingSince, c.observations
		if since == "" {
			since = appliedAt
		}
		if c.lastMissingJob != r.scope.runID {
			observations++
		}
		row, err := wc008DraftQuery(ctx, turn, iaMissingMark, since, observations, r.scope.runID,
			c.objectID, r.source.id, c.assetID, r.source.incarnation, c.relativePath, c.revision, c.directoryPath,
			r.scope.runID, c.missingSince, c.observations, c.lastMissingJob)
		if err != nil {
			return out, err
		}
		var id string
		if err = row.Scan(&id); err != nil {
			return out, errors.Join(ErrConflict, err)
		}
		if id != c.objectID {
			return out, ErrConflict
		}
		out = append(out, iaMissingMember{
			ordinal: int64(len(out)), objectID: c.objectID,
			beforeRevision: c.revision, afterRevision: c.revision,
			beforePath: c.relativePath, afterPath: c.relativePath,
		})
	}
	return out, nil
}

// The accept consumes exactly one run sequence and one run revision and records
// that this run's verified absence windows are now authoritative. It leaves the
// ACK floor untouched: only a separate durable ACK retires this original.
func iaMissingAdvanceRun(ctx context.Context, turn *commandTurn, identity CommandIdentity, r iaMissingRequest) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if r.run.revision == math.MaxInt64 || identity.Sequence <= 0 || r.run.lastSequence != identity.Sequence-1 {
		return 0, ErrCap
	}
	row, err := wc008DraftQuery(ctx, turn, iaMissingRunCAS, r.run.revision+1, identity.Sequence,
		r.scope.runID, identity.Incarnation[:], r.scope.libraryID, r.scope.libraryKey, string(r.source.resolvedRoot), r.source.followSymlinks,
		r.source.id, r.source.generation, r.source.incarnation, r.source.rootIdentity, r.run.policyRevision, r.run.scanMode,
		r.run.leaseOwner, r.run.leaseGeneration, r.run.leaseExpiresMS, r.run.revision, r.run.lastSequence, r.run.acknowledgedSequence)
	if err != nil {
		return 0, err
	}
	var revision, sequence, authoritative int64
	if err = row.Scan(&revision, &sequence, &authoritative); err != nil {
		return 0, errors.Join(ErrConflict, err)
	}
	if revision != r.run.revision+1 || sequence != identity.Sequence || authoritative != 1 {
		return 0, ErrConflict
	}
	return revision, nil
}

// One exact bounded window. beforeKey is the incoming exclusive cursor and
// afterKey is the last marked object, or the unchanged cursor for a zero-member
// window.
func iaMissingFacts(r iaMissingRequest, members []iaMissingMember, runRevision, appliedMS int64, health string) iaMissingSaved {
	after := r.beforeKey
	if len(members) > 0 {
		after = members[len(members)-1].objectID
	}
	return iaMissingSaved{
		runID: r.scope.runID, sourceID: r.source.id, sourceIncarnation: r.source.incarnation,
		acceptedRevision: runRevision, appliedMS: appliedMS, phase: "reconciling",
		authoritative: 1, processed: int64(len(members)), analyzed: int64(len(members)), warnings: 0,
		sourceHealth: health, beforeKey: r.beforeKey, afterKey: after,
		resultVersion: 1, resultCount: int64(len(members)), members: members,
	}
}

func iaMissingWriteReceipt(ctx context.Context, turn *commandTurn, identity CommandIdentity, request RequestDigest, facts iaMissingSaved, digest ResultDigest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if err = wc008DraftChanged(ctx, turn, iaMissingReceiptInsert, facts.runID, identity.Incarnation[:], identity.Sequence,
		identity.OperationID[:], request[:], iaMissingReceiptKind, facts.acceptedRevision, facts.appliedMS,
		facts.sourceID, facts.sourceIncarnation, nil, nil, "", facts.resultCount, digest[:],
		facts.beforeKey, facts.afterKey, facts.phase, facts.authoritative, facts.processed, facts.analyzed, facts.warnings, int64(0), facts.sourceHealth); err != nil {
		return err
	}
	for _, m := range facts.members {
		if err = wc008DraftChanged(ctx, turn, iaMissingMemberInsert, facts.runID, identity.Incarnation[:], identity.Sequence,
			iaMissingReceiptKind, m.ordinal, m.objectID, nil,
			m.beforeRevision, m.afterRevision, m.beforePath, m.afterPath, iaMissingDisposition); err != nil {
			return err
		}
	}
	return nil
}

// Replay decodes only the immutable receipt and its complete typed member
// cohort. Current source, lease, seal, coverage and object rows are never
// consulted.
func lookupIAMissing(ctx context.Context, turn *commandTurn, authority *iaEntryAuthorization, command *iaMissingCommand, header iaMissingSavedHeader) (out iaMissingHistory, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || authority == nil || command == nil || !command.valid ||
		!header.facts.exists || header.facts.kind != IAMarkMissing || header.facts.identity != (receiptIdentity{command.identity, command.digest}) ||
		header.facts.resultCount < 0 || header.facts.resultCount > iaMissingMaxRows || header.acceptedRevision <= 0 || header.appliedMS < 0 ||
		header.sourceID == "" || header.sourceIncarnation == "" || header.phase != "reconciling" || header.authoritative != 1 ||
		header.processed < 0 || header.analyzed < 0 || header.warnings < 0 || header.finishedMS != 0 ||
		!iaMissingText(header.health, 64) || !iaMissingTextAllowEmpty(header.beforeKey, iaMissingMaxKeyBytes) ||
		!iaMissingTextAllowEmpty(header.afterKey, iaMissingMaxKeyBytes) {
		return out, ErrResolveRequired
	}
	facts := iaMissingSaved{
		runID: command.request.scope.runID, sourceID: header.sourceID, sourceIncarnation: header.sourceIncarnation,
		acceptedRevision: header.acceptedRevision, appliedMS: header.appliedMS, phase: header.phase,
		authoritative: header.authoritative, processed: header.processed, analyzed: header.analyzed, warnings: header.warnings,
		sourceHealth: header.health, beforeKey: header.beforeKey, afterKey: header.afterKey, resultVersion: 1,
	}
	rows, err := turn.query(ctx, turn.steps[iaMissingMembers].statementStep, facts.runID,
		command.identity.Incarnation[:], command.identity.Sequence, int64(iaMissingMaxRows+1))
	if err != nil {
		return out, err
	}
	var bytes int64
	readErr := func() error {
		for rows.Next() {
			var m iaMissingMember
			if err := rows.Scan(&m.ordinal, &m.objectID, &m.beforeRevision, &m.afterRevision, &m.beforePath, &m.afterPath); err != nil {
				return err
			}
			if m.ordinal != int64(len(facts.members)) || int64(len(facts.members)) >= header.facts.resultCount {
				return ErrResolveRequired
			}
			n, err := iaMissingMemberBytes(facts.runID, m)
			if err != nil || n > math.MaxInt64-bytes {
				return ErrResolveRequired
			}
			bytes += n
			facts.members = append(facts.members, m)
		}
		return rows.Err()
	}()
	if err := errors.Join(readErr, rows.Close()); err != nil {
		return out, err
	}
	if int64(len(facts.members)) != header.facts.resultCount {
		return out, ErrResolveRequired
	}
	facts.resultCount = int64(len(facts.members))
	if err = authority.budget.check(ctx, turn, bytes); err != nil {
		return out, err
	}
	if err = preflightIAMissingResult(command.identity, command.digest, facts); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	digest, err := canonicalResultDigest(command.identity, command.digest, iaMissingResult{facts})
	if err != nil || !equalBytes(digest[:], header.facts.resultDigest[:]) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	return iaMissingHistory{decision: receiptReplay, result: iaMissingResult{facts}, digest: digest}, nil
}

func iaMissingTimestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000000000Z")
}

type iaMissingResult struct{ facts iaMissingSaved }

type iaMissingSaved struct {
	runID, sourceID, sourceIncarnation           string
	acceptedRevision, appliedMS                  int64
	phase, beforeKey, afterKey, sourceHealth     string
	authoritative, processed, analyzed, warnings int64
	resultVersion, resultCount                   int64
	members                                      []iaMissingMember
}

type iaMissingMember struct {
	ordinal                                 int64
	objectID, beforeRevision, afterRevision string
	beforePath, afterPath                   string
}

// The private field walk fixes v1 field order. One missing command persists at
// most one bounded window; a zero-member window persists no child row.
func iaMissingFields(id CommandIdentity, r RequestDigest, v iaMissingSaved, f func(any)) {
	f([]byte(canonicalResultCodec))
	f([]byte{0, 1})
	f(iaMissingReceiptKind)
	f("missing_window")
	f("applied")
	f(id.Incarnation[:])
	f(int64(id.Sequence))
	f(id.OperationID[:])
	f(r[:])
	f(v.runID)
	f(v.sourceID)
	f(v.sourceIncarnation)
	f(v.acceptedRevision)
	f(v.appliedMS)
	f(v.phase)
	f(v.authoritative)
	f(v.processed)
	f(v.analyzed)
	f(v.warnings)
	f(v.sourceHealth)
	f(v.beforeKey)
	f(v.afterKey)
	f(v.resultVersion)
	f(v.resultCount)
	f(int64(len(v.members)))
	for _, m := range v.members {
		f(m.ordinal)
		f(m.objectID)
		f(m.beforeRevision)
		f(m.afterRevision)
		f(m.beforePath)
		f(m.afterPath)
		f(iaMissingDisposition)
	}
}

func preflightIAMissingResult(id CommandIdentity, r RequestDigest, v iaMissingSaved) error {
	if id.Sequence <= 0 || id.Incarnation == ([16]byte{}) || id.OperationID == ([16]byte{}) ||
		v.runID == "" || v.sourceID == "" || v.sourceIncarnation == "" || v.acceptedRevision <= 0 || v.appliedMS < 0 ||
		v.phase != "reconciling" || v.authoritative != 1 ||
		v.processed < 0 || v.analyzed < 0 || v.warnings < 0 || !iaMissingText(v.sourceHealth, 64) ||
		v.resultVersion != 1 || v.resultCount != int64(len(v.members)) || v.resultCount < 0 || v.resultCount > iaMissingMaxRows ||
		v.analyzed != v.resultCount || v.processed != v.resultCount || v.warnings != 0 ||
		!iaMissingTextAllowEmpty(v.beforeKey, iaMissingMaxKeyBytes) || !iaMissingTextAllowEmpty(v.afterKey, iaMissingMaxKeyBytes) {
		return invalid("ia_missing_result_header")
	}
	if v.resultCount > 0 {
		previous := v.beforeKey
		var total int64
		for i, m := range v.members {
			if m.ordinal != int64(i) || !iaMissingText(m.objectID, iaMissingMaxKeyBytes) || m.objectID <= previous ||
				!iaMissingText(m.beforeRevision, iaMissingMaxKeyBytes) || m.beforeRevision != m.afterRevision ||
				!inventoryPath(m.beforePath, false) || m.beforePath != m.afterPath {
				return invalid("ia_missing_result_member")
			}
			previous = m.objectID
			n, err := iaMissingMemberBytes(v.runID, m)
			if err != nil || n > iaMissingMaxWindowBytes-total {
				return invalid("ia_missing_result_bytes")
			}
			total += n
		}
		if v.afterKey != v.members[len(v.members)-1].objectID {
			return invalid("ia_missing_result_after_key")
		}
	} else if v.afterKey != v.beforeKey {
		return invalid("ia_missing_result_cursor")
	}
	remaining := uint64(maxCanonicalResultBytes)
	tooBig := false
	iaMissingFields(id, r, v, func(x any) {
		var n uint64
		switch p := x.(type) {
		case string:
			n = 8 + uint64(len(p))
		case []byte:
			n = uint64(len(p))
		case int64:
			n = 8
		}
		if n > remaining {
			tooBig = true
		} else {
			remaining -= n
		}
	})
	if tooBig {
		return invalid("ia_missing_result_bytes")
	}
	var valid = true
	iaMissingFields(id, r, v, func(x any) {
		if p, ok := x.(string); ok {
			valid = valid && utf8.ValidString(p)
		}
	})
	if !valid {
		return invalid("ia_missing_result_text")
	}
	return nil
}

// Exact generated native scalar charge for one member row, independently of wire
// limits. The child has no stored byte ledger, so replay recomputes it from the
// immutable values it decoded.
func iaMissingMemberBytes(runID string, m iaMissingMember) (int64, error) {
	n := int64(64)
	add := func(v int) bool {
		if uint64(v) > uint64(math.MaxInt64-n) {
			return false
		}
		n += int64(v)
		return true
	}
	for _, s := range []string{runID, iaMissingReceiptKind, m.objectID, m.beforeRevision, m.afterRevision, m.beforePath, m.afterPath, iaMissingDisposition} {
		if !add(len(s)) {
			return 0, invalid("ia_missing_retained_bytes")
		}
	}
	return n, nil
}

func iaMissingCandidateBytes(c iaMissingCandidate) (int64, error) {
	n := int64(0)
	add := func(v int) bool {
		if uint64(v) > uint64(math.MaxInt64-n) {
			return false
		}
		n += int64(v)
		return true
	}
	for _, s := range []string{c.objectID, c.assetID, c.revision, c.relativePath, c.directoryPath, c.missingSince, c.lastMissingJob} {
		if !add(len(s)) {
			return 0, invalid("ia_missing_window_bytes")
		}
	}
	return n, nil
}

// Caller must preflight the complete result before invoking this writer.
func writeIAMissingResult(e *requestEncoder, id CommandIdentity, r RequestDigest, v iaMissingSaved) error {
	iaMissingFields(id, r, v, func(x any) {
		switch p := x.(type) {
		case string:
			e.text(p)
		case []byte:
			e.bytes(p)
		case int64:
			e.number(uint64(p))
		}
	})
	return e.err
}
