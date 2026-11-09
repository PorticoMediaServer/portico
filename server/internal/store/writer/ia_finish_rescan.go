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

const iaFinishRescanRequestVersion uint16 = 1

// IA-19 finishes a rescan only after every durable frontier and coverage
// obligation closes. Its outcome and warning counter are derived from the
// durable run, directory and object rows, never from an empty optional queue and
// never from a caller claim: the prepared outcome must equal the derived one.
// Its private outcome and history carry the exact typed result; no public
// facade, worker or producer is wired, so the accepting body stays uncalled
// outside this package.
type iaFinishRescanAcceptOutcome struct {
	decision receiptDecision
	result   iaFinishRescanResult
	digest   ResultDigest
}
type iaFinishRescanHistory struct {
	decision receiptDecision
	result   iaFinishRescanResult
	digest   ResultDigest
}

// Same-turn saved header, never a disclosure capability.
type iaFinishRescanSavedHeader struct {
	facts                              receiptFacts
	acceptedRevision, appliedMS        int64
	sourceID, sourceIncarnation        string
	beforeKey, afterKey, phase, health string
	authoritative, processed, analyzed int64
	warnings, finishedMS               int64
}

// Prepared witnesses only. The outcome is what the caller believes the durable
// frontier proves; the fresh turn re-derives it from the run row, the sealed
// root, the directory frontier and every unseen retained object.
type iaFinishRescanRequest struct {
	scope  inventoryScope
	source inventorySource
	run    inventoryRun

	outcome      string
	pinsResolved bool
}

type iaFinishRescanCommand struct {
	identity CommandIdentity
	request  iaFinishRescanRequest
	digest   RequestDigest
	valid    bool
}

func newIAFinishRescanCommand(id CommandIdentity, r iaFinishRescanRequest) (*iaFinishRescanCommand, error) {
	if !iaEpisodeCounts(reflect.ValueOf(r)) {
		return nil, invalid("ia_finish_rescan_count")
	}
	size := uint64(len(canonicalRequestCodec) + 4 + 16 + 8 + 16)
	iaEpisodeWireSize(reflect.ValueOf(r), &size)
	if size > maxCanonicalRequestBytes {
		return nil, &PolicyError{Field: "ia_finish_rescan_bytes", Limit: maxCanonicalRequestBytes, Cause: ErrCap}
	}
	if !iaEpisodeValidText(reflect.ValueOf(r)) || !utf8.Valid(r.source.resolvedRoot) || bytes.IndexByte(r.source.resolvedRoot, 0) >= 0 {
		return nil, invalid("ia_finish_rescan_text")
	}
	if err := validateIAFinishRescanOriginal(id, r); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(IAFinishRescan, iaFinishRescanRequestVersion, id, r)
	if err != nil {
		return nil, err
	}
	r.source.resolvedRoot = bytes.Clone(r.source.resolvedRoot)
	for _, s := range iaFinishRescanTexts(&r) {
		*s = strings.Clone(*s)
	}
	return &iaFinishRescanCommand{identity: id, request: r, digest: digest, valid: true}, nil
}

func validateIAFinishRescanOriginal(id CommandIdentity, r iaFinishRescanRequest) error {
	s, u := r.source, r.run
	if id.Sequence <= 0 || id.OperationID == ([16]byte{}) || id.Incarnation == ([16]byte{}) ||
		r.scope.runID == "" || r.scope.libraryKey == "" || r.scope.libraryID <= 0 ||
		s.id == "" || s.incarnation == "" || s.rootIdentity == "" || s.generation <= 0 || len(s.resolvedRoot) == 0 || s.resolvedRoot[0] != '/' ||
		u.revision <= 0 || u.policyRevision <= 0 || u.leaseOwner <= 0 || u.leaseGeneration <= 0 || u.leaseExpiresMS <= 0 ||
		u.lastSequence != id.Sequence-1 || u.acknowledgedSequence != u.lastSequence || u.phase != "reconciling" || !inventoryScanMode(u.scanMode) {
		return invalid("ia_finish_rescan_original")
	}
	root := string(s.resolvedRoot)
	if strings.ContainsAny(root, `\`) || path.Clean(root) != root {
		return invalid("ia_finish_rescan_root")
	}
	if r.outcome != iaFinishComplete && r.outcome != iaFinishWithWarnings {
		return invalid("ia_finish_rescan_outcome")
	}
	return nil
}

func iaFinishRescanText(value string, limit int) bool {
	if len(value) == 0 || len(value) > limit || !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func iaFinishRescanTexts(r *iaFinishRescanRequest) []*string {
	return []*string{
		&r.scope.runID, &r.scope.libraryKey, &r.source.id, &r.source.incarnation, &r.source.rootIdentity,
		&r.run.phase, &r.run.scanMode, &r.outcome,
	}
}

func (c *iaFinishRescanCommand) checkOriginal() error {
	if c == nil || !c.valid {
		return invalid("ia_finish_rescan_command")
	}
	checked, err := newIAFinishRescanCommand(c.identity, c.request)
	if err != nil || checked.digest != c.digest {
		return ErrConflict
	}
	return nil
}

// One governed finish or original replay. Current owner authority is
// established BEFORE any retained owner/header read; fresh source, run, root and
// frontier witnesses are compared only after the receipt is Fresh. A saved
// result is decoded from the immutable receipt, never a live row.
func acceptIAFinishRescan(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, budget iaEntryBudget, reference ReceiptReference, r iaFinishRescanRequest) (out iaFinishRescanAcceptOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IAFinishRescan {
		return out, ErrInvalid
	}
	command, err := newIAFinishRescanCommand(reference.Identity, r)
	if err != nil {
		return out, err
	}
	if command.digest != reference.RequestDigest {
		return out, ErrConflict
	}
	authority, err := authorizeIAEntryOwner(ctx, turn, p, lifetime, budget)
	if err != nil {
		return out, err
	}
	owner, header, err := iaFinishRescanReceiptFacts(ctx, turn, r.scope.runID, reference)
	if err != nil {
		return out, err
	}
	decision, err := classifyReceipt(receiptIdentity{reference.Identity, reference.RequestDigest}, IAFinishRescan, owner, header.facts)
	if err != nil {
		return out, err
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	switch decision {
	case receiptRetired:
		return iaFinishRescanAcceptOutcome{decision: receiptRetired}, nil
	case receiptReplay:
		history, err := lookupIAFinishRescan(ctx, turn, authority, command, header)
		if err != nil {
			return out, err
		}
		return iaFinishRescanAcceptOutcome{decision: history.decision, result: history.result, digest: history.digest}, nil
	case receiptFresh:
		return applyIAFinishRescan(ctx, turn, authority, command)
	default:
		return out, ErrResolveRequired
	}
}

func iaFinishRescanReceiptFacts(ctx context.Context, turn *commandTurn, runID string, reference ReceiptReference) (owner receiptOwnerFacts, header iaFinishRescanSavedHeader, err error) {
	row, err := wc008DraftQuery(ctx, turn, iaFinishOwner, runID)
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

	row, err = wc008DraftQuery(ctx, turn, iaFinishHeader, runID, reference.Identity.Incarnation[:], reference.Identity.Sequence)
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
	if len(operation) != 16 || len(request) != 32 || len(digest) != 32 || header.finishedMS != header.appliedMS {
		return owner, header, ErrResolveRequired
	}
	header.facts.exists = true
	header.facts.identity.command = reference.Identity
	copy(header.facts.identity.command.OperationID[:], operation)
	copy(header.facts.identity.digest[:], request)
	copy(header.facts.resultDigest[:], digest)
	if kind == iaFinishReceiptKind {
		header.facts.kind = IAFinishRescan
	}
	return owner, header, nil
}

// Fresh effects are one complete unit: one exact run CAS, the derived warning
// counter and the typed receipt. No object, directory or catalogue row is
// mutated at finish.
func applyIAFinishRescan(ctx context.Context, turn *commandTurn, authority *iaEntryAuthorization, command *iaFinishRescanCommand) (out iaFinishRescanAcceptOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	r := command.request
	if turn == nil || turn.transaction == nil || turn.transaction.writer == nil || turn.transaction.writer.now == nil {
		return out, ErrInvalid
	}
	now := turn.transaction.writer.now()
	appliedMS := now.UnixMilli()
	// The receipt persists only milliseconds; the completion marker is derived
	// from that exact durable value so a replay rebuilds the identical result.
	finishedAt := iaFinishTimestamp(time.UnixMilli(appliedMS))
	if appliedMS < 0 || appliedMS >= r.run.leaseExpiresMS {
		return out, ErrConflict
	}
	if id, err := wc008DraftInteger(ctx, turn, iaFinishLibrary, r.scope.libraryID, r.scope.libraryKey); err != nil || id != r.scope.libraryID {
		return out, errors.Join(ErrConflict, err)
	}
	row, err := wc008DraftQuery(ctx, turn, iaFinishSource, r.source.id, r.scope.libraryKey, r.source.generation,
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
	// MP-10 is not built: an unresolved source pin refuses the convergence claim
	// rather than widening it.
	if !r.pinsResolved {
		return out, errors.Join(ErrResolveRequired, iaCoveragePinsUnresolved)
	}
	run, err := iaFinishCheckRun(ctx, turn, r, command.identity)
	if err != nil {
		return out, err
	}
	if err = iaFinishCheckRoot(ctx, turn, r); err != nil {
		return out, err
	}
	blocked, err := iaFinishCheckFrontier(ctx, turn, r)
	if err != nil {
		return out, err
	}
	// All frontier reads above are current-authority evidence too.
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	if blocked > math.MaxInt64-run.warnings {
		return out, ErrCap
	}
	facts := iaFinishRescanFacts(r, run, blocked, appliedMS, finishedAt, health)
	if facts.phase != r.outcome {
		return out, ErrConflict
	}
	runRevision, err := iaFinishAdvanceRun(ctx, turn, command.identity, r, facts, run)
	if err != nil {
		return out, err
	}
	facts.acceptedRevision = runRevision
	if err = preflightIAFinishRescanResult(command.identity, command.digest, facts); err != nil {
		return out, err
	}
	digest, err := canonicalResultDigest(command.identity, command.digest, iaFinishRescanResult{facts})
	if err != nil {
		return out, err
	}
	if err = iaFinishWriteReceipt(ctx, turn, command.identity, command.digest, facts, digest); err != nil {
		return out, err
	}
	// A later same-turn revocation still redacts the fresh result.
	if err = authority.recheck(ctx, turn); err != nil {
		return out, ErrDisclosureDenied
	}
	return iaFinishRescanAcceptOutcome{decision: receiptFresh, result: iaFinishRescanResult{facts}, digest: digest}, nil
}

// The durable run row fixes the counters the finish may report. A run that has
// not closed traversal, is not reconciling, has already finished or is not the
// prepared exact revision refuses the whole unit.
type iaFinishRunState struct {
	revision, lastSequence, acknowledgedSequence  int64
	authoritative, discovered, analyzed, warnings int64
	phase, completedAt, finishedAt                string
}

func iaFinishCheckRun(ctx context.Context, turn *commandTurn, r iaFinishRescanRequest, identity CommandIdentity) (out iaFinishRunState, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, iaFinishRunStateStmt, r.scope.runID, identity.Incarnation[:])
	if err != nil {
		return out, err
	}
	err = row.Scan(&out.revision, &out.lastSequence, &out.acknowledgedSequence, &out.phase, &out.authoritative,
		&out.discovered, &out.analyzed, &out.warnings, &out.completedAt, &out.finishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if out.phase != "reconciling" || (out.authoritative != 0 && out.authoritative != 1) ||
		out.discovered < 0 || out.analyzed < 0 || out.analyzed > out.discovered || out.warnings < 0 ||
		out.revision != r.run.revision || out.lastSequence != r.run.lastSequence ||
		out.acknowledgedSequence != r.run.acknowledgedSequence || out.finishedAt != "" ||
		!iaFinishRescanText(out.completedAt, 64) {
		return out, ErrConflict
	}
	return out, nil
}

// The root directory must be sealed. An unreadable root is never a completed
// rescan: it is abandoned, not finished.
func iaFinishCheckRoot(ctx context.Context, turn *commandTurn, r iaFinishRescanRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	row, err := wc008DraftQuery(ctx, turn, iaFinishRoot, r.scope.runID)
	if err != nil {
		return err
	}
	var state, identity, revision string
	if err = row.Scan(&state, &identity, &revision); errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return errors.Join(ErrConflict, err)
	}
	if state != "done" || identity == "" || revision == "" {
		return ErrConflict
	}
	return nil
}

// Durable frontier obligations: no directory may still be pending, and every
// unseen available object must be either a required absence obligation or
// inside an exempt subtree whose absence was never claimed. Blocked
// (unreadable), protected (refused/unstable) and skipped directories all keep
// their objects available and unproven: their subtrees are exempt, exactly like
// a protected path. The exempt lists and the unseen frontier are paged by
// keyset until exhausted, so their COUNT can never wedge the run; only more
// than iaFinishMaxRows genuinely unseen non-exempt objects refuse (ErrCap).
//
// The returned count is the exact blocked-subtree warning charge that the scan
// writer did not already add to inventory_runs.warnings. The legacy and writer
// scan paths insert a protect+warning issue for an unreadable directory
// (server/internal/ingestion/coverage.go excludeDirectory,
// server/internal/catalog/inventory_issues.go InventoryIssueTx), so adding
// every blocked row here would double count; only blocked rows without any
// warning-charging issue at their exact path are charged.
func iaFinishCheckFrontier(ctx context.Context, turn *commandTurn, r iaFinishRescanRequest) (blockedCount int64, err error) {
	pending, err := iaFinishExists(ctx, turn, iaFinishPending, r.scope.runID)
	if err != nil {
		return 0, err
	}
	if pending {
		return 0, ErrConflict
	}
	blocked, err := iaFinishPathPages(ctx, turn, iaFinishBlocked, r.scope.runID)
	if err != nil {
		return 0, err
	}
	protected, err := iaFinishPathPages(ctx, turn, iaFinishProtected, r.scope.runID)
	if err != nil {
		return 0, err
	}
	skipped, err := iaFinishPathPages(ctx, turn, iaFinishSkipped, r.scope.runID)
	if err != nil {
		return 0, err
	}
	if _, err = iaFinishUnseenObligation(ctx, turn, r, iaFinishExempt(blocked, protected, skipped)); err != nil {
		return 0, err
	}
	uncharged, err := iaFinishPathPages(ctx, turn, iaFinishBlockedCharged, r.scope.runID)
	if err != nil {
		return 0, err
	}
	return int64(len(uncharged)), nil
}

// iaFinishExempt is the closed prefix test shared by every exempt source. An
// empty or root prefix protects the whole source (matching the scan writer's
// unprotectedObjectSQL); otherwise a path is exempt when it equals a prefix or
// sits below it. Keys are compared exactly and by path segment, never by bare
// string prefix, so "Locked" cannot exempt "Locked2".
type iaFinishExemptPaths struct {
	whole bool
	paths map[string]struct{}
}

func iaFinishExempt(groups ...[]string) iaFinishExemptPaths {
	size := 0
	for _, group := range groups {
		size += len(group)
	}
	out := iaFinishExemptPaths{paths: make(map[string]struct{}, size)}
	for _, group := range groups {
		for _, prefix := range group {
			prefix = strings.TrimSuffix(prefix, "/")
			if prefix == "" || prefix == "." {
				out.whole = true
				continue
			}
			out.paths[prefix] = struct{}{}
		}
	}
	return out
}

func (e iaFinishExemptPaths) covers(path string) bool {
	if e.whole {
		return true
	}
	if _, ok := e.paths[path]; ok {
		return true
	}
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			if _, ok := e.paths[path[:i]]; ok {
				return true
			}
		}
	}
	return false
}

// iaFinishUnseenObligation pages the absence frontier and counts only objects
// outside every exempt subtree. A page that fills is followed by its keyset
// successor, so many exempt unseen objects finish; the ninth genuinely unseen
// object still refuses.
func iaFinishUnseenObligation(ctx context.Context, turn *commandTurn, r iaFinishRescanRequest, exempt iaFinishExemptPaths) (genuine int64, err error) {
	defer func() { genuine, err = finishCommand(ctx, commandLifetime(turn), genuine, err) }()
	after := ""
	for {
		rows, err := turn.query(ctx, turn.steps[iaFinishUnseen].statementStep,
			r.source.id, after, r.source.incarnation, r.scope.runID, int64(iaFinishPageRows))
		if err != nil {
			return 0, err
		}
		read := 0
		for rows.Next() {
			var id, path string
			if err = rows.Scan(&id, &path); err != nil {
				return 0, errors.Join(err, rows.Close())
			}
			read++
			after = id
			if exempt.covers(path) {
				continue
			}
			genuine++
			if genuine > iaFinishMaxRows {
				return 0, errors.Join(ErrCap, rows.Close())
			}
		}
		if err = errors.Join(rows.Err(), rows.Close()); err != nil {
			return 0, err
		}
		if read < iaFinishPageRows {
			return genuine, nil
		}
	}
}

// iaFinishPathPages reads one keyset-ordered exempt frontier to exhaustion,
// iaFinishPageRows at a time. Exhaustion is exact: a short page is the end, and
// a full page is followed by its successor key, so the caller accounts for
// every exempt row instead of a prefix of them.
func iaFinishPathPages(ctx context.Context, turn *commandTurn, id stmt.ID, runID string) (out []string, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	after := ""
	for {
		page, err := iaFinishRows(ctx, turn, id, runID, after, int64(iaFinishPageRows))
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) < iaFinishPageRows {
			return out, nil
		}
		after = page[len(page)-1]
	}
}

func iaFinishExists(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (found bool, err error) {
	defer func() { found, err = finishCommand(ctx, commandLifetime(turn), found, err) }()
	rows, err := turn.query(ctx, turn.steps[id].statementStep, args...)
	if err != nil {
		return false, err
	}
	found = rows.Next()
	return found, errors.Join(rows.Err(), rows.Close())
}

func iaFinishRows(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (out []string, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	rows, err := turn.query(ctx, turn.steps[id].statementStep, args...)
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			return out, err
		}
		out = append(out, value)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	return out, nil
}

// The finish consumes exactly one run sequence and one run revision and moves
// the run to its derived terminal phase. It leaves the ACK/result floor
// untouched: only a separate durable ACK retires this original.
func iaFinishAdvanceRun(ctx context.Context, turn *commandTurn, identity CommandIdentity, r iaFinishRescanRequest, facts iaFinishRescanSaved, run iaFinishRunState) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if run.revision == math.MaxInt64 || identity.Sequence <= 0 || run.lastSequence != identity.Sequence-1 {
		return 0, ErrCap
	}
	row, err := wc008DraftQuery(ctx, turn, iaFinishRunCAS, facts.phase, facts.finishedAt, facts.warnings, run.revision+1, identity.Sequence,
		r.scope.runID, identity.Incarnation[:], r.scope.libraryID, r.scope.libraryKey, string(r.source.resolvedRoot), r.source.followSymlinks,
		r.source.id, r.source.generation, r.source.incarnation, r.source.rootIdentity, r.run.policyRevision, r.run.scanMode,
		r.run.leaseOwner, r.run.leaseGeneration, r.run.leaseExpiresMS, r.run.revision, r.run.lastSequence, r.run.acknowledgedSequence,
		run.discovered, run.analyzed, run.warnings)
	if err != nil {
		return 0, err
	}
	var revision, sequence int64
	var phase string
	if err = row.Scan(&revision, &sequence, &phase); err != nil {
		return 0, errors.Join(ErrConflict, err)
	}
	if revision != run.revision+1 || sequence != identity.Sequence || phase != facts.phase {
		return 0, ErrConflict
	}
	return revision, nil
}

// Truthful counters only: discovered/analyzed come from the durable run row,
// warnings add the exact durable blocked-subtree count. The phase follows from
// the warning count, never from the caller's prepared outcome.
func iaFinishRescanFacts(r iaFinishRescanRequest, run iaFinishRunState, blocked, appliedMS int64, finishedAt, health string) iaFinishRescanSaved {
	warnings := run.warnings + blocked
	phase := iaFinishComplete
	if warnings > 0 {
		phase = iaFinishWithWarnings
	}
	return iaFinishRescanSaved{
		runID: r.scope.runID, sourceID: r.source.id, sourceIncarnation: r.source.incarnation,
		appliedMS: appliedMS, phase: phase, finishedAt: finishedAt,
		authoritative: 1, processed: run.discovered, analyzed: run.analyzed, warnings: warnings,
		finishedMS: appliedMS, sourceHealth: health,
		beforeKey: iaFinishRootPath, afterKey: iaFinishRootPath, resultVersion: 1,
	}
}

func iaFinishWriteReceipt(ctx context.Context, turn *commandTurn, identity CommandIdentity, request RequestDigest, facts iaFinishRescanSaved, digest ResultDigest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	return wc008DraftChanged(ctx, turn, iaFinishReceiptInsert, facts.runID, identity.Incarnation[:], identity.Sequence,
		identity.OperationID[:], request[:], iaFinishReceiptKind, facts.acceptedRevision, facts.appliedMS,
		facts.sourceID, facts.sourceIncarnation, nil, nil, "", int64(0), digest[:],
		facts.beforeKey, facts.afterKey, facts.phase, facts.authoritative, facts.processed, facts.analyzed, facts.warnings, facts.finishedMS, facts.sourceHealth)
}

// Replay decodes only the immutable receipt. Current source, run, directory and
// object rows are never consulted.
func lookupIAFinishRescan(ctx context.Context, turn *commandTurn, authority *iaEntryAuthorization, command *iaFinishRescanCommand, header iaFinishRescanSavedHeader) (out iaFinishRescanHistory, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || authority == nil || command == nil || !command.valid ||
		!header.facts.exists || header.facts.kind != IAFinishRescan || header.facts.identity != (receiptIdentity{command.identity, command.digest}) ||
		header.facts.resultCount != 0 || header.acceptedRevision <= 0 || header.appliedMS < 0 ||
		header.sourceID == "" || header.sourceIncarnation == "" || header.authoritative != 1 ||
		(header.phase != iaFinishComplete && header.phase != iaFinishWithWarnings) ||
		header.beforeKey != iaFinishRootPath || header.afterKey != iaFinishRootPath ||
		header.processed < 0 || header.analyzed < 0 || header.analyzed > header.processed || header.warnings < 0 ||
		header.finishedMS != header.appliedMS || (header.warnings == 0) != (header.phase == iaFinishComplete) ||
		!iaFinishRescanText(header.health, 64) {
		return out, ErrResolveRequired
	}
	facts := iaFinishRescanSaved{
		runID: command.request.scope.runID, sourceID: header.sourceID, sourceIncarnation: header.sourceIncarnation,
		acceptedRevision: header.acceptedRevision, appliedMS: header.appliedMS, phase: header.phase,
		finishedAt:    iaFinishTimestamp(time.UnixMilli(header.appliedMS)),
		authoritative: header.authoritative, processed: header.processed, analyzed: header.analyzed, warnings: header.warnings,
		finishedMS: header.finishedMS, sourceHealth: header.health,
		beforeKey: header.beforeKey, afterKey: header.afterKey, resultVersion: 1,
	}
	bytes := int64(len(facts.sourceID) + len(facts.sourceIncarnation) + len(facts.phase) + len(facts.sourceHealth) + len(facts.runID))
	if err = authority.budget.check(ctx, turn, bytes); err != nil {
		return out, err
	}
	if err = preflightIAFinishRescanResult(command.identity, command.digest, facts); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	digest, err := canonicalResultDigest(command.identity, command.digest, iaFinishRescanResult{facts})
	if err != nil || !equalBytes(digest[:], header.facts.resultDigest[:]) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	return iaFinishRescanHistory{decision: receiptReplay, result: iaFinishRescanResult{facts}, digest: digest}, nil
}

func iaFinishTimestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000000000Z")
}

type iaFinishRescanResult struct{ facts iaFinishRescanSaved }

type iaFinishRescanSaved struct {
	runID, sourceID, sourceIncarnation     string
	acceptedRevision, appliedMS            int64
	phase, finishedAt, beforeKey, afterKey string
	sourceHealth                           string
	authoritative, processed, analyzed     int64
	warnings, finishedMS, resultVersion    int64
}

// The private field walk fixes v1 field order. Finish persists no child cohort;
// result_count is always zero.
func iaFinishRescanFields(id CommandIdentity, r RequestDigest, v iaFinishRescanSaved, f func(any)) {
	f([]byte(canonicalResultCodec))
	f([]byte{0, 1})
	f(iaFinishReceiptKind)
	f("finish_rescan")
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
	f(v.finishedAt)
	f(v.authoritative)
	f(v.processed)
	f(v.analyzed)
	f(v.warnings)
	f(v.finishedMS)
	f(v.sourceHealth)
	f(v.beforeKey)
	f(v.afterKey)
	f(v.resultVersion)
	f(int64(0))
}

func preflightIAFinishRescanResult(id CommandIdentity, r RequestDigest, v iaFinishRescanSaved) error {
	if id.Sequence <= 0 || id.Incarnation == ([16]byte{}) || id.OperationID == ([16]byte{}) ||
		v.runID == "" || v.sourceID == "" || v.sourceIncarnation == "" || v.acceptedRevision <= 0 || v.appliedMS < 0 ||
		(v.phase != iaFinishComplete && v.phase != iaFinishWithWarnings) || v.authoritative != 1 ||
		v.processed < 0 || v.analyzed < 0 || v.analyzed > v.processed || v.warnings < 0 ||
		v.finishedMS != v.appliedMS || (v.warnings == 0) != (v.phase == iaFinishComplete) ||
		!iaFinishRescanText(v.finishedAt, 64) || !iaFinishRescanText(v.sourceHealth, 64) ||
		v.beforeKey != iaFinishRootPath || v.afterKey != iaFinishRootPath || v.resultVersion != 1 {
		return invalid("ia_finish_rescan_result_header")
	}
	remaining := uint64(maxCanonicalResultBytes)
	tooBig := false
	iaFinishRescanFields(id, r, v, func(x any) {
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
		return invalid("ia_finish_rescan_result_bytes")
	}
	var valid = true
	iaFinishRescanFields(id, r, v, func(x any) {
		if p, ok := x.(string); ok {
			valid = valid && utf8.ValidString(p)
		}
	})
	if !valid {
		return invalid("ia_finish_rescan_result_text")
	}
	return nil
}

// Caller must preflight the complete result before invoking this writer.
func writeIAFinishRescanResult(e *requestEncoder, id CommandIdentity, r RequestDigest, v iaFinishRescanSaved) error {
	iaFinishRescanFields(id, r, v, func(x any) {
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
