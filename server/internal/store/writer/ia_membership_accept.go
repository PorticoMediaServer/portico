package writer

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"math"
	"path"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const iaMembershipAcceptRequestVersion uint16 = 1

const (
	iaMembershipMaxChanges = 64
	iaMembershipMaxChain   = 64
	iaMembershipMaxLinks   = 16
)

// IA-16's second accepting class: explicit membership changes. Its private
// outcome and history carry the exact typed result; no public facade, worker or
// producer is wired, so the accepting body stays uncalled outside this package.
type iaMembershipAcceptOutcome struct {
	decision receiptDecision
	result   iaMembershipResult
	digest   ResultDigest
}
type iaMembershipHistory struct {
	decision receiptDecision
	result   iaMembershipResult
	digest   ResultDigest
}

type iaMembershipSavedHeader struct {
	facts                                               receiptFacts
	acceptedRevision, appliedMS, memberBytes            int64
	sourceID, sourceIncarnation                         string
	objectID, objectRevision, cursorKind                string
	directoryPath, directoryIdentity, directoryRevision []byte
	expectedPayload, nextPayload                        []byte
}

// Prepared observations only. A change names one retained subject by its exact
// caller-prepared source key and the exact current revision the caller observed;
// the writer re-reads that subject and saves the actual retained ID/revision. An
// empty effect is a pure reparent (no asset link change). A removed link is only
// expressible here; a present link is the complete new destination membership.
type iaMembershipChange struct {
	subjectKey      string
	subjectKind     int64
	subjectRevision int64
	parentKey       string // destination key; empty means no reparent
	edition         string
	part            int64
	start           float64
	end             *float64
	cueOrdinal      int64
	boundaryStatus  string
	effect          string // "", "present" or "removed"
}

type iaMembershipAcceptRequest struct {
	scope     inventoryScope
	source    inventorySource
	run       inventoryRun
	directory inventoryDirectory
	files     []inventoryFile // exactly one observed membership file
	complete  bool            // complete cohort assertion, not source proof
	changes   []iaMembershipChange
}

type iaMembershipAcceptCommand struct {
	identity CommandIdentity
	request  iaMembershipAcceptRequest
	digest   RequestDigest
	valid    bool
}

func newIAMembershipAcceptCommand(id CommandIdentity, r iaMembershipAcceptRequest) (*iaMembershipAcceptCommand, error) {
	if len(r.files) != 1 || len(r.changes) == 0 || len(r.changes) > iaMembershipMaxChanges || !iaEpisodeCounts(reflect.ValueOf(r)) {
		return nil, invalid("ia_membership_count")
	}
	size := uint64(len(canonicalRequestCodec) + 4 + 16 + 8 + 16)
	iaEpisodeWireSize(reflect.ValueOf(r), &size)
	if size > maxCanonicalRequestBytes {
		return nil, &PolicyError{Field: "ia_membership_bytes", Limit: maxCanonicalRequestBytes, Cause: ErrCap}
	}
	if !iaEpisodeValidText(reflect.ValueOf(r)) || !utf8.Valid(r.source.resolvedRoot) || bytes.IndexByte(r.source.resolvedRoot, 0) >= 0 {
		return nil, invalid("ia_membership_text")
	}
	if err := validateIAMembershipOriginal(id, r); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(IAAcceptMembership, iaMembershipAcceptRequestVersion, id, r)
	if err != nil {
		return nil, err
	}
	r.files = slices.Clone(r.files)
	r.source.resolvedRoot = bytes.Clone(r.source.resolvedRoot)
	r.changes = slices.Clone(r.changes)
	for i := range r.changes {
		if r.changes[i].end != nil {
			v := *r.changes[i].end
			r.changes[i].end = &v
		}
	}
	for _, s := range iaMembershipTexts(&r) {
		*s = strings.Clone(*s)
	}
	return &iaMembershipAcceptCommand{identity: id, request: r, digest: digest, valid: true}, nil
}

// A membership destination is fixed by the subject kind: season->show,
// episode->season, album->artist, song->album, audiobook file->book. A link
// change names the one observed asset and keeps edition/part/cue/range separate.
func iaMembershipParentKind(subjectKind int64) (int64, bool) {
	switch subjectKind {
	case 3:
		return 2, true
	case 4:
		return 3, true
	case 6:
		return 5, true
	case 7:
		return 6, true
	case 9:
		return 8, true
	default:
		return 0, false
	}
}

// A link change on a reading item (kind 20/21) is inseparable from its
// reading_publications row: the publication's FK cascades from the link, so a
// present link would create an asset link with no publication and a removed link
// would delete the publication this writer never observed. Those kinds stay with
// the reading accepting class.
func iaMembershipLinkKind(subjectKind int64) bool {
	switch subjectKind {
	case 1, 4, 7, 9, 11, 12:
		return true
	default:
		return false
	}
}

func validateIAMembershipOriginal(id CommandIdentity, r iaMembershipAcceptRequest) error {
	s, u, d := r.source, r.run, r.directory
	if id.Sequence <= 0 || id.OperationID == ([16]byte{}) || id.Incarnation == ([16]byte{}) ||
		r.scope.runID == "" || r.scope.libraryKey == "" || r.scope.libraryID <= 0 ||
		s.id == "" || s.incarnation == "" || s.rootIdentity == "" || s.generation <= 0 || len(s.resolvedRoot) == 0 || s.resolvedRoot[0] != '/' ||
		u.revision <= 0 || u.policyRevision <= 0 || u.leaseOwner <= 0 || u.leaseGeneration <= 0 || u.leaseExpiresMS <= 0 || u.lastSequence != id.Sequence-1 || u.acknowledgedSequence != u.lastSequence || u.phase != "inventory" || !inventoryScanMode(u.scanMode) ||
		d.identity == "" || d.revision == "" || d.cursorKind != inventoryLinuxCursor || !inventoryPath(d.path, true) || d.pages < 0 || d.skipped < 0 || d.rawNames != 1 || d.unstable {
		return invalid("ia_membership_original")
	}
	root := string(s.resolvedRoot)
	f := r.files[0]
	if !r.complete || strings.ContainsAny(root, `\`) || path.Clean(root) != root || f.sourceKey == "" || f.objectIdentity == "" || f.revision == "" ||
		f.directory || f.size < 0 || f.part < 0 || f.year < 0 || f.metadataLanguage == "" || !inventoryPath(f.relativePath, false) ||
		path.Dir(f.relativePath) != d.path || strings.ContainsAny(f.assetPath, `\`) || path.Clean(f.assetPath) != f.assetPath || f.assetPath != path.Join(root, f.relativePath) {
		return invalid("ia_membership_file")
	}
	// The distinct-total cap is one row per retained subject: duplicates are
	// refused rather than silently deduplicated or applied twice. A move is two
	// different subjects (old source removed, new destination present).
	//
	// A destination that is itself a changed subject is refused too: the plan
	// reads every destination BEFORE the first write, so a child placed under a
	// season or album reparented in the same request would copy the destination's
	// OLD denormalized parent (show/artist) instead of the accepted one.
	moved := make(map[string]bool, len(r.changes))
	for _, c := range r.changes {
		moved[c.subjectKey] = true
	}
	seen := make(map[string]bool, len(r.changes))
	for _, c := range r.changes {
		if !iaEpisodeKey(c.subjectKey) || c.subjectRevision <= 0 || c.part < 0 ||
			!iaMembershipParentKindAllowed(c) || !iaMembershipLinkAllowed(c) ||
			!iaExtraAcceptText(c.edition, iaExtraAcceptMaxTitleBytes) && c.edition != "" ||
			math.IsNaN(c.start) || math.IsInf(c.start, 0) || c.start < 0 ||
			c.end != nil && (math.IsNaN(*c.end) || math.IsInf(*c.end, 0) || *c.end <= c.start) ||
			c.cueOrdinal < 0 || c.cueOrdinal > 0 && c.subjectKind != 7 && c.subjectKind != 9 {
			return invalid("ia_membership_change")
		}
		if c.parentKey != "" && moved[c.parentKey] {
			return invalid("ia_membership_destination_changed")
		}
		if c.effect != "" && c.effect != "present" && c.effect != "removed" {
			return invalid("ia_membership_effect")
		}
		if c.subjectKind == 4 && c.effect != "" {
			if c.boundaryStatus != "whole_source" && c.boundaryStatus != "unknown_multi_episode" {
				return invalid("ia_membership_boundary")
			}
		} else if c.boundaryStatus != "" {
			return invalid("ia_membership_boundary")
		}
		key := c.subjectKey
		if seen[key] {
			return invalid("ia_membership_distinct")
		}
		seen[key] = true
	}
	return nil
}

// iaMembershipParentKindAllowed reports whether the change's reparent (when
// present) names a subject kind with a fixed destination kind.
func iaMembershipParentKindAllowed(c iaMembershipChange) bool {
	if c.parentKey == "" {
		// A change with neither a destination nor a link effect would be a no-op.
		return c.effect != ""
	}
	if !iaEpisodeKey(c.parentKey) || c.effect == "removed" {
		return false
	}
	_, ok := iaMembershipParentKind(c.subjectKind)
	return ok
}

func iaMembershipLinkAllowed(c iaMembershipChange) bool {
	if c.effect == "" {
		return true
	}
	return iaMembershipLinkKind(c.subjectKind)
}

func (c *iaMembershipAcceptCommand) checkOriginal() error {
	if c == nil || !c.valid {
		return invalid("ia_membership_command")
	}
	checked, err := newIAMembershipAcceptCommand(c.identity, c.request)
	if err != nil || checked.digest != c.digest {
		return ErrConflict
	}
	return nil
}

func iaMembershipTexts(r *iaMembershipAcceptRequest) []*string {
	base := inventoryAcceptRequest{scope: r.scope, source: r.source, run: r.run, directory: r.directory, files: r.files}
	texts := []*string{&r.scope.runID, &r.scope.libraryKey, &r.source.id, &r.source.incarnation, &r.source.rootIdentity, &r.run.phase, &r.run.scanMode, &r.directory.path, &r.directory.identity, &r.directory.revision, &r.directory.expectedCursor, &r.directory.nextCursor, &r.directory.cursorKind}
	texts = append(texts, inventoryRequestTexts(&base)[14:]...)
	for i := range r.changes {
		c := &r.changes[i]
		texts = append(texts, &c.subjectKey, &c.parentKey, &c.edition, &c.boundaryStatus, &c.effect)
	}
	return texts
}

// One governed membership change or original replay. Current owner authority is
// established BEFORE any retained owner/header read; fresh source, directory,
// asset and run witnesses are compared only after the receipt is Fresh.
func acceptIAMembership(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, budget iaEntryBudget, reference ReceiptReference, r iaMembershipAcceptRequest) (out iaMembershipAcceptOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IAAcceptMembership {
		return out, ErrInvalid
	}
	command, err := newIAMembershipAcceptCommand(reference.Identity, r)
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
	owner, header, err := iaMembershipReceiptFacts(ctx, turn, r.scope.runID, reference)
	if err != nil {
		return out, err
	}
	decision, err := classifyReceipt(receiptIdentity{reference.Identity, reference.RequestDigest}, IAAcceptMembership, owner, header.facts)
	if err != nil {
		return out, err
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	switch decision {
	case receiptRetired:
		return iaMembershipAcceptOutcome{decision: receiptRetired}, nil
	case receiptReplay:
		history, err := lookupIAMembership(ctx, turn, authority, command, header)
		if err != nil {
			return out, err
		}
		return iaMembershipAcceptOutcome{decision: history.decision, result: history.result, digest: history.digest}, nil
	case receiptFresh:
		return applyIAMembership(ctx, turn, authority, command, reference)
	default:
		return out, ErrResolveRequired
	}
}

func iaMembershipReceiptFacts(ctx context.Context, turn *commandTurn, runID string, reference ReceiptReference) (owner receiptOwnerFacts, header iaMembershipSavedHeader, err error) {
	row, err := wc008DraftQuery(ctx, turn, iaMembershipOwner, runID)
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

	row, err = wc008DraftQuery(ctx, turn, iaMembershipHeader, runID, reference.Identity.Incarnation[:], reference.Identity.Sequence)
	if err != nil {
		return owner, header, err
	}
	var operation, request, digest []byte
	var kind string
	var resultCount, acceptedRevision, appliedMS, version, memberBytes sql.NullInt64
	var sourceID, sourceIncarnation, objectID, objectRevision, cursorKind sql.NullString
	err = row.Scan(&operation, &request, &kind, &resultCount, &digest, &acceptedRevision, &appliedMS,
		&sourceID, &sourceIncarnation, &version, &memberBytes, &objectID, &objectRevision,
		&cursorKind, &header.directoryPath, &header.directoryIdentity, &header.directoryRevision,
		&header.expectedPayload, &header.nextPayload)
	if errors.Is(err, sql.ErrNoRows) {
		return owner, header, nil
	}
	if err != nil {
		return owner, header, err
	}
	if len(operation) != 16 || len(request) != 32 || len(digest) != 32 {
		return owner, header, ErrResolveRequired
	}
	header.facts.exists = true
	header.facts.identity.command = reference.Identity
	copy(header.facts.identity.command.OperationID[:], operation)
	copy(header.facts.identity.digest[:], request)
	copy(header.facts.resultDigest[:], digest)
	if kind != iaMembershipReceiptKind {
		// A receipt at this exact key belongs to another command purpose. Its IA
		// media projection is intentionally NULL, so it is left undecoded and
		// classifyReceipt answers the kind mismatch with ErrConflict rather than
		// a raw NULL scan error.
		return owner, header, nil
	}
	if !resultCount.Valid || !acceptedRevision.Valid || !appliedMS.Valid || !version.Valid || version.Int64 != 1 ||
		!memberBytes.Valid || !sourceID.Valid || !sourceIncarnation.Valid || !objectID.Valid || !objectRevision.Valid || !cursorKind.Valid {
		return owner, header, ErrResolveRequired
	}
	header.facts.resultCount = resultCount.Int64
	header.acceptedRevision = acceptedRevision.Int64
	header.appliedMS = appliedMS.Int64
	header.memberBytes = memberBytes.Int64
	header.sourceID, header.sourceIncarnation = sourceID.String, sourceIncarnation.String
	header.objectID, header.objectRevision, header.cursorKind = objectID.String, objectRevision.String, cursorKind.String
	header.facts.kind = IAAcceptMembership
	return owner, header, nil
}

// Fresh effects are one complete unit: every retained subject is re-read and its
// destination proven before the first write; no ancestry cycle, no partial
// cohort. Reparents retain the subject ID (revision advances), link changes keep
// edition/part/cue/range separate, and only the new destination membership and
// its destination chronology are written. There is no descendant art/name fanout.
func applyIAMembership(ctx context.Context, turn *commandTurn, authority *iaEntryAuthorization, command *iaMembershipAcceptCommand, reference ReceiptReference) (out iaMembershipAcceptOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	r := command.request
	if turn == nil || turn.transaction == nil || turn.transaction.writer == nil || turn.transaction.writer.now == nil {
		return out, ErrInvalid
	}
	appliedMS := turn.transaction.writer.now().UnixMilli()
	if appliedMS < 0 || appliedMS >= r.run.leaseExpiresMS {
		return out, ErrConflict
	}
	if id, err := wc008DraftInteger(ctx, turn, iaMembershipLibrary, r.scope.libraryID, r.scope.libraryKey); err != nil || id != r.scope.libraryID {
		return out, errors.Join(ErrConflict, err)
	}
	row, err := wc008DraftQuery(ctx, turn, iaMembershipSource, r.source.id, r.scope.libraryKey, r.source.generation, r.source.incarnation, r.source.rootIdentity, string(r.source.resolvedRoot), r.source.followSymlinks)
	if err != nil {
		return out, err
	}
	var sourceID string
	if err = row.Scan(&sourceID); errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if sourceID != r.source.id {
		return out, ErrConflict
	}
	if err = iaMembershipCheckDirectory(ctx, turn, r); err != nil {
		return out, err
	}
	asset, err := iaMembershipCheckAsset(ctx, turn, r)
	if err != nil {
		return out, err
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	planned, err := iaMembershipPlan(ctx, turn, r, asset.id)
	if err != nil {
		return out, err
	}
	for i := range planned {
		if err = iaMembershipApplyChange(ctx, turn, r, asset, &planned[i]); err != nil {
			return out, err
		}
	}
	if err = iaMembershipAdvanceDestinationChronology(ctx, turn, r.scope.libraryID, planned, iaEpisodeAddedAt(appliedMS)); err != nil {
		return out, err
	}
	if err = iaMembershipAdvanceDirectory(ctx, turn, r); err != nil {
		return out, err
	}
	runRevision, err := iaMembershipAdvanceRun(ctx, turn, command.identity, r)
	if err != nil {
		return out, err
	}
	facts, err := iaMembershipFacts(r, planned, asset, runRevision, appliedMS)
	if err != nil {
		return out, err
	}
	if err = preflightIAMediaResult(IAAcceptMembership, command.identity, command.digest, facts); err != nil {
		return out, err
	}
	digest, err := canonicalResultDigest(command.identity, command.digest, iaMembershipResult{facts})
	if err != nil {
		return out, err
	}
	if err = iaMembershipWriteReceipt(ctx, turn, command.identity, command.digest, facts, digest); err != nil {
		return out, err
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, ErrDisclosureDenied
	}
	return iaMembershipAcceptOutcome{decision: receiptFresh, result: iaMembershipResult{facts}, digest: digest}, nil
}

func iaMembershipCheckDirectory(ctx context.Context, turn *commandTurn, r iaMembershipAcceptRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	row, err := wc008DraftQuery(ctx, turn, iaMembershipDirectory, r.scope.runID, r.directory.path, r.directory.identity, r.directory.revision, r.directory.expectedCursor, r.directory.pages, r.directory.skipped)
	if err != nil {
		return err
	}
	var pages, skipped int64
	if err = row.Scan(&pages, &skipped); errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if pages != r.directory.pages || skipped != r.directory.skipped {
		return ErrConflict
	}
	return nil
}

func iaMembershipCheckAsset(ctx context.Context, turn *commandTurn, r iaMembershipAcceptRequest) (out iaExtraAssetFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	f := r.files[0]
	row, err := wc008DraftQuery(ctx, turn, iaMembershipAsset, r.scope.libraryID, f.assetPath, f.size, f.modifiedNS)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out.id, &out.token, &out.infoEpoch, &out.fileSizeRevision); errors.Is(err, sql.ErrNoRows) {
		return iaExtraAssetFacts{}, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if out.id <= 0 || out.token == "" || out.infoEpoch <= 0 || out.fileSizeRevision <= 0 {
		return out, ErrResolveRequired
	}
	return out, nil
}

type iaMembershipEntityFacts struct {
	kind, revision                            int64
	parent, show, season, artist, album, book sql.NullInt64
	retired                                   bool
	incarnation                               [16]byte
}

func iaMembershipReadEntity(ctx context.Context, turn *commandTurn, libraryID, subjectID int64) (value iaMembershipEntityFacts, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	row, err := wc008DraftQuery(ctx, turn, iaMembershipEntity, subjectID, libraryID)
	if err != nil {
		return value, err
	}
	var retired int64
	var incarnation []byte
	if err = row.Scan(&value.kind, &value.parent, &value.show, &value.season, &value.artist, &value.album, &value.book,
		&retired, &value.revision, &incarnation); err != nil {
		return value, errors.Join(ErrConflict, err)
	}
	if len(incarnation) != 16 || value.revision <= 0 {
		return value, ErrResolveRequired
	}
	value.retired = retired != 0
	copy(value.incarnation[:], incarnation)
	return value, nil
}

type iaMembershipLinkFacts struct {
	part, available, cue, revision int64
	start                          float64
	end                            *float64
	edition                        string
	incarnation                    [16]byte
}

func iaMembershipReadLink(ctx context.Context, turn *commandTurn, libraryID, subjectID, assetID int64) (value iaMembershipLinkFacts, found bool, err error) {
	row, err := wc008DraftQuery(ctx, turn, iaMembershipLink, subjectID, assetID, libraryID)
	if err != nil {
		return value, false, err
	}
	var end sql.NullFloat64
	var cue sql.NullInt64
	var incarnation []byte
	if err = row.Scan(&value.part, &value.available, &value.start, &end, &value.edition, &cue, &incarnation, &value.revision); errors.Is(err, sql.ErrNoRows) {
		return value, false, nil
	}
	if err != nil {
		return value, false, err
	}
	if len(incarnation) != 16 || value.revision <= 0 {
		return value, false, ErrResolveRequired
	}
	if end.Valid {
		v := end.Float64
		value.end = &v
	}
	value.cue = cue.Int64
	copy(value.incarnation[:], incarnation)
	return value, true, nil
}

// The plan re-reads every retained subject, proves the exact observed revision,
// resolves each destination and refuses a cycle before the first write.
type iaMembershipPlanned struct {
	change           iaMembershipChange
	subjectID        int64
	old              iaMembershipEntityFacts
	revision         int64
	destination      *iaMembershipEntityFacts
	destinationID    int64
	destinationAdded sql.NullString // moved episode's retained added_at, if any
	link             iaMembershipLinkFacts
	linkFound        bool
	playback         [16]byte
}

// destinationMoved reports a genuinely new child membership: the episode's
// accepted parent changes, so the destination season/show gains content. A
// reparent that names the episode's current season rewrites the same edge and
// is not new content.
func (p iaMembershipPlanned) destinationMoved() bool {
	return p.change.subjectKind == 4 && p.destination != nil &&
		(!p.old.parent.Valid || p.old.parent.Int64 != p.destinationID)
}

func iaMembershipPlan(ctx context.Context, turn *commandTurn, r iaMembershipAcceptRequest, assetID int64) (out []iaMembershipPlanned, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	out = make([]iaMembershipPlanned, len(r.changes))
	for i, c := range r.changes {
		node := iaMembershipPlanned{change: c, revision: c.subjectRevision}
		row, err := wc008DraftQuery(ctx, turn, iaMembershipIdentity, r.scope.libraryID, c.subjectKey)
		if err != nil {
			return out, err
		}
		if err = row.Scan(&node.subjectID); errors.Is(err, sql.ErrNoRows) {
			return out, ErrConflict
		}
		if err != nil {
			return out, err
		}
		entity, err := iaMembershipReadEntity(ctx, turn, r.scope.libraryID, node.subjectID)
		if err != nil {
			return out, err
		}
		if entity.retired || entity.kind != c.subjectKind || entity.revision != c.subjectRevision {
			return out, ErrConflict
		}
		node.old = entity
		if c.parentKey == "" {
			out[i] = node
			continue
		}
		wantKind, _ := iaMembershipParentKind(c.subjectKind)
		row, err = wc008DraftQuery(ctx, turn, iaMembershipIdentity, r.scope.libraryID, c.parentKey)
		if err != nil {
			return out, err
		}
		var parentID int64
		if err = row.Scan(&parentID); errors.Is(err, sql.ErrNoRows) {
			return out, ErrConflict
		}
		if err != nil {
			return out, err
		}
		parent, err := iaMembershipReadEntity(ctx, turn, r.scope.libraryID, parentID)
		if err != nil {
			return out, err
		}
		if parent.retired || parent.kind != wantKind {
			return out, ErrConflict
		}
		if c.subjectKind == 4 && !parent.show.Valid {
			return out, ErrConflict
		}
		// A season reparent rewrites only the season's own show_id. Its episodes
		// keep the old show_id (the schema links neither the columns nor the
		// episode-position index), so a bounded cascade is not yet owned here: any
		// episode child refuses the move instead of leaving stale hierarchy.
		if c.subjectKind == 3 {
			episodes, err := iaMembershipSeasonHasEpisodes(ctx, turn, r.scope.libraryID, node.subjectID)
			if err != nil {
				return out, err
			}
			if episodes {
				return out, ErrConflict
			}
		}
		// An album reparent rewrites only the album's own artist_id. Its songs keep
		// the old artist_id (the schema links neither the columns nor the song
		// index), so any song child refuses a move that changes the album's artist.
		// A move to the artist the album already has changes nothing observable and
		// stays allowed, as does a childless album move.
		if c.subjectKind == 6 && (!entity.artist.Valid || entity.artist.Int64 != parentID) {
			songs, err := iaMembershipAlbumHasSongs(ctx, turn, r.scope.libraryID, node.subjectID)
			if err != nil {
				return out, err
			}
			if songs {
				return out, ErrConflict
			}
		}
		if err = iaMembershipCheckCycle(ctx, turn, r.scope.libraryID, node.subjectID, parentID); err != nil {
			return out, err
		}
		node.destination = &parent
		node.destinationID = parentID
		node.revision = entity.revision + 1
		if node.destinationMoved() {
			row, err = wc008DraftQuery(ctx, turn, iaMembershipEpisodeAdded, node.subjectID, r.scope.libraryID)
			if err != nil {
				return out, err
			}
			if err = row.Scan(&node.destinationAdded); errors.Is(err, sql.ErrNoRows) {
				return out, ErrConflict
			}
			if err != nil {
				return out, err
			}
		}
		out[i] = node
	}
	for i := range out {
		c := out[i].change
		switch c.effect {
		case "present":
			step, ok := turn.steps[iaMembershipLinkList]
			if !ok {
				return out, invalid("ia_membership_link_steps")
			}
			links, err := turn.query(ctx, step.statementStep, out[i].subjectID)
			if err != nil {
				return out, err
			}
			var assets []int64
			readErr := func() error {
				for links.Next() {
					var assetID int64
					if err := links.Scan(&assetID); err != nil {
						return err
					}
					assets = append(assets, assetID)
				}
				return links.Err()
			}()
			if err := errors.Join(readErr, links.Close()); err != nil {
				return out, err
			}
			link, found, err := iaMembershipReadLink(ctx, turn, r.scope.libraryID, out[i].subjectID, assetID)
			if err != nil {
				return out, err
			}
			if found {
				out[i].link, out[i].linkFound = link, true
			} else {
				if len(assets) >= iaMembershipMaxLinks {
					return out, ErrCap
				}
				if _, err = rand.Read(out[i].playback[:]); err != nil {
					return out, err
				}
			}
		case "removed":
			link, found, err := iaMembershipReadLink(ctx, turn, r.scope.libraryID, out[i].subjectID, assetID)
			if err != nil {
				return out, err
			}
			if !found || link.part != c.part || link.start != c.start || !iaMembershipSameEnd(link.end, c.end) || link.edition != c.edition || link.cue != c.cueOrdinal {
				return out, ErrConflict
			}
			out[i].link, out[i].linkFound = link, true
		}
	}
	return out, nil
}

func iaMembershipSameEnd(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// The cycle walk is bounded and same-library. An unreachable parent (foreign
// library or missing row) is a conflict, a chain longer than the bound is a cap.
func iaMembershipCheckCycle(ctx context.Context, turn *commandTurn, libraryID, subjectID, destinationID int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	current := destinationID
	for depth := 0; depth < iaMembershipMaxChain; depth++ {
		if current == subjectID {
			return ErrConflict
		}
		entity, err := iaMembershipReadEntity(ctx, turn, libraryID, current)
		if err != nil {
			return err
		}
		if entity.kind == 11 {
			row, err := wc008DraftQuery(ctx, turn, iaMembershipExtraItem, current)
			if err != nil {
				return err
			}
			var library, kind string
			var owner sql.NullInt64
			if err = row.Scan(&library, &owner, &kind); errors.Is(err, sql.ErrNoRows) {
				return ErrConflict
			}
			if err != nil {
				return err
			}
			if !owner.Valid {
				return nil
			}
			current = owner.Int64
			continue
		}
		if !entity.parent.Valid {
			return nil
		}
		current = entity.parent.Int64
	}
	return ErrCap
}

// A season's episodes are found by both hierarchy columns: parent_id is the
// canonical child edge, season_id is the denormalized column the episode's
// show_id must agree with. Each probe is one bounded pointed read.
func iaMembershipSeasonHasEpisodes(ctx context.Context, turn *commandTurn, libraryID, seasonID int64) (found bool, err error) {
	defer func() { found, err = finishCommand(ctx, commandLifetime(turn), found, err) }()
	for _, id := range []stmt.ID{iaMembershipSeasonChild, iaMembershipSeasonEpisode} {
		step, ok := turn.steps[id]
		if !ok {
			return false, invalid("ia_membership_season_steps")
		}
		rows, err := turn.query(ctx, step.statementStep, seasonID, libraryID)
		if err != nil {
			return false, err
		}
		next := rows.Next()
		if err = errors.Join(rows.Err(), rows.Close()); err != nil {
			return false, err
		}
		if next {
			return true, nil
		}
	}
	return false, nil
}

// An album's songs are found by its canonical child edge. The probe is one
// bounded pointed read on the existing partial album index.
func iaMembershipAlbumHasSongs(ctx context.Context, turn *commandTurn, libraryID, albumID int64) (found bool, err error) {
	defer func() { found, err = finishCommand(ctx, commandLifetime(turn), found, err) }()
	step, ok := turn.steps[iaMembershipAlbumSongs]
	if !ok {
		return false, invalid("ia_membership_album_steps")
	}
	rows, err := turn.query(ctx, step.statementStep, albumID, libraryID)
	if err != nil {
		return false, err
	}
	found = rows.Next()
	return found, errors.Join(rows.Err(), rows.Close())
}

func iaMembershipApplyChange(ctx context.Context, turn *commandTurn, r iaMembershipAcceptRequest, asset iaExtraAssetFacts, p *iaMembershipPlanned) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if p.destination != nil {
		parent := p.old.parent
		show, season, artist, album, book := p.old.show, p.old.season, p.old.artist, p.old.album, p.old.book
		destination := p.destinationID
		parent = sql.NullInt64{Int64: destination, Valid: true}
		switch p.change.subjectKind {
		case 3:
			show = sql.NullInt64{Int64: destination, Valid: true}
		case 4:
			season = sql.NullInt64{Int64: destination, Valid: true}
			show = p.destination.show
		case 6:
			artist = sql.NullInt64{Int64: destination, Valid: true}
		case 7:
			album = sql.NullInt64{Int64: destination, Valid: true}
			// A song's artist_id is a denormalized copy of its album's artist; the
			// destination album always carries one (schema CHECK kind<>6 OR
			// artist_id IS NOT NULL), so both columns follow the new parent.
			artist = p.destination.artist
		case 9:
			book = sql.NullInt64{Int64: destination, Valid: true}
		}
		row, err := wc008DraftQuery(ctx, turn, iaMembershipEntityUpdate, iaMembershipAnyInt(parent), iaMembershipAnyInt(show), iaMembershipAnyInt(season),
			iaMembershipAnyInt(artist), iaMembershipAnyInt(album), iaMembershipAnyInt(book),
			p.subjectID, r.scope.libraryID, p.change.subjectKind, p.old.revision)
		if err != nil {
			return err
		}
		var revision int64
		var incarnation []byte
		if err = row.Scan(&revision, &incarnation); err != nil {
			return errors.Join(ErrConflict, err)
		}
		if revision != p.old.revision+1 || len(incarnation) != 16 {
			return ErrConflict
		}
	}
	switch p.change.effect {
	case "present":
		if p.linkFound {
			linkID, oldLink := iaMembershipLinkWriteArgs(iaMembershipLinkUpdate, iaMembershipLinkUpdateNullEnd, iaMembershipLinkUpdateNullCue, iaMembershipLinkUpdateNullBoth, p.link)
			linkArgs := []any{p.change.part, p.change.start, iaEpisodeNullableReal(p.change.end), p.change.edition, iaMembershipNullableCue(p.change.cueOrdinal),
				p.subjectID, asset.id, r.scope.libraryID, p.link.part, p.link.available, p.link.start}
			err = wc008DraftChanged(ctx, turn, linkID, append(linkArgs, oldLink...)...)
		} else {
			err = wc008DraftChanged(ctx, turn, iaMembershipLinkInsert, iaMembershipNullableCue(p.change.cueOrdinal), p.subjectID, asset.id, p.change.part, int64(1),
				p.change.start, iaEpisodeNullableReal(p.change.end), p.change.edition, p.playback[:], int64(1), r.scope.libraryID)
			if err == nil {
				err = wc008DraftChanged(ctx, turn, iaMembershipContributionInsert, r.scope.libraryID, p.subjectID, asset.id, int64(1), int64(0), r.files[0].size, asset.fileSizeRevision)
			}
		}
		if err != nil {
			return err
		}
		if p.change.subjectKind == 4 {
			return iaMembershipWriteBoundary(ctx, turn, p.subjectID, asset.token, p.change.boundaryStatus)
		}
	case "removed":
		linkID, oldLink := iaMembershipLinkWriteArgs(iaMembershipLinkDelete, iaMembershipLinkDeleteNullEnd, iaMembershipLinkDeleteNullCue, iaMembershipLinkDeleteNullBoth, p.link)
		linkArgs := []any{p.subjectID, asset.id, r.scope.libraryID, p.link.part, p.link.available, p.link.start}
		if err = wc008DraftChanged(ctx, turn, linkID, append(linkArgs, oldLink...)...); err != nil {
			return err
		}
		if err = wc008DraftChanged(ctx, turn, iaMembershipContributionDelete, p.subjectID, asset.id, r.scope.libraryID); err != nil {
			return err
		}
		if p.change.subjectKind == 4 {
			return wc008DraftChanged(ctx, turn, iaMembershipBoundaryDelete, p.subjectID, asset.token, p.change.boundaryStatus)
		}
	}
	return nil
}

// The episode boundary is the exact (item, asset) PK: an existing equal status is
// re-written, a different status is an exact CAS, and a missing row is inserted.
func iaMembershipWriteBoundary(ctx context.Context, turn *commandTurn, subjectID int64, token, status string) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	row, err := wc008DraftQuery(ctx, turn, iaMembershipBoundary, subjectID, token)
	if err != nil {
		return err
	}
	var prior string
	err = row.Scan(&prior)
	if errors.Is(err, sql.ErrNoRows) {
		return wc008DraftChanged(ctx, turn, iaMembershipBoundaryInsert, subjectID, token, status)
	}
	if err != nil {
		return err
	}
	return wc008DraftChanged(ctx, turn, iaMembershipBoundaryUpdate, status, subjectID, token, prior)
}

func iaMembershipAnyInt(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return v.Int64
}

func iaMembershipNullableCue(v int64) any {
	if v <= 0 {
		return nil
	}
	return v
}

// iaMembershipLinkWriteArgs selects the closed nullable link statement whose
// stored end_seconds/cue_ordinal match old, and appends the non-null stored
// values in statement order. The hot grammar has no IS ? form, so each nullable
// combination is its own statement (see iaMembershipStatements).
func iaMembershipLinkWriteArgs(full, nullEnd, nullCue, nullBoth stmt.ID, old iaMembershipLinkFacts) (stmt.ID, []any) {
	id := full
	switch {
	case old.end == nil && old.cue <= 0:
		id = nullBoth
	case old.end == nil:
		id = nullEnd
	case old.cue <= 0:
		id = nullCue
	}
	args := []any{}
	if old.end != nil {
		args = append(args, *old.end)
	}
	args = append(args, old.edition)
	if old.cue > 0 {
		args = append(args, old.cue)
	}
	return id, args
}

// A reparent adds a genuinely new child membership to the destination season and
// its show. Every distinct destination container advances at most once, to the
// moved episode's retained added time (or this unit's accepted instant when the
// retained row carries none), and never backwards. Replay never reaches here.
func iaMembershipAdvanceDestinationChronology(ctx context.Context, turn *commandTurn, libraryID int64, planned []iaMembershipPlanned, acceptedAt string) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	type container struct {
		id, kind int64
		at       string
	}
	var order []container
	index := make(map[[2]int64]int)
	for _, p := range planned {
		if !p.destinationMoved() || !p.destination.show.Valid {
			continue
		}
		at := acceptedAt
		if p.destinationAdded.Valid && p.destinationAdded.String != "" {
			at = p.destinationAdded.String
		}
		for _, c := range []container{{id: p.destinationID, kind: 3, at: at}, {id: p.destination.show.Int64, kind: 2, at: at}} {
			if c.id <= 0 {
				continue
			}
			key := [2]int64{c.id, c.kind}
			if i, ok := index[key]; ok {
				if c.at > order[i].at {
					order[i].at = c.at
				}
				continue
			}
			index[key] = len(order)
			order = append(order, c)
		}
	}
	for _, c := range order {
		if err = iaEpisodeAdvanceChronology(ctx, turn, libraryID, c.id, c.kind, c.at); err != nil {
			return err
		}
	}
	return nil
}

func iaMembershipAdvanceDirectory(ctx context.Context, turn *commandTurn, r iaMembershipAcceptRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if r.directory.pages == math.MaxInt64 {
		return ErrCap
	}
	next := r.directory.pages + 1
	row, err := wc008DraftQuery(ctx, turn, iaMembershipDirectoryCAS, r.directory.nextCursor, next,
		r.scope.runID, r.directory.path, r.directory.identity, r.directory.revision, r.directory.expectedCursor, r.directory.pages, r.directory.skipped)
	if err != nil {
		return err
	}
	var cursor string
	var pages, skipped int64
	if err = row.Scan(&cursor, &pages, &skipped); err != nil {
		return errors.Join(ErrConflict, err)
	}
	if cursor != r.directory.nextCursor || pages != next || skipped != r.directory.skipped {
		return ErrConflict
	}
	return nil
}

func iaMembershipAdvanceRun(ctx context.Context, turn *commandTurn, identity CommandIdentity, r iaMembershipAcceptRequest) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if r.run.revision == math.MaxInt64 || identity.Sequence <= 0 || r.run.lastSequence != identity.Sequence-1 {
		return 0, ErrCap
	}
	row, err := wc008DraftQuery(ctx, turn, iaMembershipRunCAS, r.run.revision+1, identity.Sequence,
		r.scope.runID, identity.Incarnation[:], r.scope.libraryID, r.scope.libraryKey, string(r.source.resolvedRoot), r.source.followSymlinks,
		r.source.id, r.source.generation, r.source.incarnation, r.source.rootIdentity, r.run.policyRevision, r.run.phase, r.run.scanMode,
		r.run.leaseOwner, r.run.leaseGeneration, r.run.leaseExpiresMS, r.run.revision, r.run.lastSequence, r.run.acknowledgedSequence)
	if err != nil {
		return 0, err
	}
	var revision, sequence int64
	if err = row.Scan(&revision, &sequence); err != nil {
		return 0, errors.Join(ErrConflict, err)
	}
	if revision != r.run.revision+1 || sequence != identity.Sequence {
		return 0, ErrConflict
	}
	return revision, nil
}

// The cohort is identity rows (retained subject IDs and their new revision or
// destination parent) followed by the membership rows. Only the new destination
// membership is written; the old source is never re-read or reconstructed.
func iaMembershipFacts(r iaMembershipAcceptRequest, planned []iaMembershipPlanned, asset iaExtraAssetFacts, runRevision, appliedMS int64) (iaMediaResult, error) {
	facts := iaMediaResult{
		runID: r.scope.runID, sourceID: r.source.id, sourceIncarnation: r.source.incarnation,
		acceptedRevision: runRevision, appliedMS: appliedMS,
		objectID: r.files[0].objectIdentity, objectRevision: r.files[0].revision,
		cursorKind:        r.directory.cursorKind,
		directoryPath:     []byte(r.directory.path),
		directoryIdentity: []byte(r.directory.identity), directoryRevision: []byte(r.directory.revision),
		expectedPayload: []byte(r.directory.expectedCursor), nextPayload: []byte(r.directory.nextCursor),
		resultVersion: 1,
	}
	add := func(m iaMediaMember) {
		m.ordinal = int64(len(facts.members))
		facts.members = append(facts.members, m)
	}
	for _, p := range planned {
		incarnation := p.old.incarnation
		m := iaMediaMember{kind: "identity", libraryID: r.scope.libraryID, subjectID: p.subjectID, subjectKind: p.change.subjectKind,
			subjectRevision: p.revision, metadataIncarnation: incarnation}
		if p.destination != nil {
			parentKind, _ := iaMembershipParentKind(p.change.subjectKind)
			m.parentID, m.parentKind = iaEpisodePtr(p.destinationID), iaEpisodePtr(parentKind)
		}
		add(m)
	}
	for _, p := range planned {
		c := p.change
		if c.effect == "" {
			continue
		}
		m := iaMediaMember{kind: "membership", libraryID: r.scope.libraryID, subjectID: p.subjectID, subjectKind: c.subjectKind,
			subjectRevision: p.revision, metadataIncarnation: p.old.incarnation,
			assetID: iaEpisodePtr(asset.id), assetInfoEpoch: iaEpisodePtr(asset.infoEpoch), partIndex: iaEpisodePtr(c.part),
			linkOriginRevision: iaEpisodePtr(p.linkOriginRevision()), linkEffect: iaEpisodePtr(c.effect),
			edition: &c.edition, startSeconds: &c.start, endSeconds: c.end, cueOrdinal: iaMembershipCuePtr(c.cueOrdinal)}
		if p.linkFound && c.effect == "removed" {
			playback := p.link.incarnation
			m.linkOriginIncarnation = &playback
			m.partIndex = iaEpisodePtr(p.link.part)
			m.edition = &p.link.edition
			m.startSeconds = &p.link.start
			m.endSeconds = p.link.end
			m.cueOrdinal = iaMembershipCuePtr(p.link.cue)
		} else if p.linkFound {
			playback := p.link.incarnation
			m.linkOriginIncarnation = &playback
		} else {
			playback := p.playback
			m.linkOriginIncarnation = &playback
		}
		if c.subjectKind == 4 {
			m.episodeBoundaryStatus = &c.boundaryStatus
		}
		add(m)
	}
	facts.resultCount = int64(len(facts.members))
	for _, m := range facts.members {
		n, err := iaMediaMemberRetainedBytes(IAAcceptMembership, facts.runID, m)
		if err != nil {
			return iaMediaResult{}, err
		}
		facts.memberBytes += n
	}
	return facts, nil
}

func (p iaMembershipPlanned) linkOriginRevision() int64 {
	if p.linkFound {
		return p.link.revision
	}
	return 1
}

func iaMembershipCuePtr(v int64) *int64 {
	if v <= 0 {
		return nil
	}
	n := v
	return &n
}

// iaMembershipWriteReceipt stores the typed header and every ordinal member.
func iaMembershipWriteReceipt(ctx context.Context, turn *commandTurn, identity CommandIdentity, request RequestDigest, facts iaMediaResult, digest ResultDigest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if err = wc008DraftChanged(ctx, turn, iaMembershipReceiptInsert, facts.runID, identity.Incarnation[:], identity.Sequence, identity.OperationID[:], request[:],
		iaMembershipReceiptKind, facts.acceptedRevision, facts.appliedMS, facts.sourceID, facts.sourceIncarnation, nil, nil, "", facts.resultCount, digest[:],
		facts.resultVersion, facts.memberBytes, facts.objectID, facts.objectRevision, facts.cursorKind,
		facts.directoryPath, facts.directoryIdentity, facts.directoryRevision, facts.expectedPayload, facts.nextPayload); err != nil {
		return err
	}
	for _, m := range facts.members {
		if err = wc008DraftChanged(ctx, turn, iaMembershipMemberInsert, facts.runID, identity.Incarnation[:], identity.Sequence, iaMembershipReceiptKind,
			m.ordinal, m.kind, m.libraryID, m.subjectID, m.subjectKind, m.subjectRevision, m.metadataIncarnation[:],
			iaEpisodeNullableInt(m.parentID), iaEpisodeNullableInt(m.parentKind), iaEpisodeNullableInt(m.ancestorID), iaEpisodeNullableInt(m.ancestorKind),
			iaEpisodeNullableInt(m.assetID), iaEpisodeNullableInt(m.assetInfoEpoch), iaEpisodeNullableInt(m.partIndex),
			iaEpisodeNullableInt(m.linkOriginRevision), iaEpisodeNullableBlob(m.linkOriginIncarnation),
			iaEpisodeNullableText(m.edition), iaEpisodeNullableText(m.linkEffect), iaEpisodeNullableInt(m.cueOrdinal),
			iaEpisodeNullableReal(m.startSeconds), iaEpisodeNullableReal(m.endSeconds),
			iaEpisodeNullableText(m.episodeBoundaryStatus), iaEpisodeNullableText(m.readingFormat),
			iaEpisodeNullableInt(m.readingPathHead), iaEpisodeNullableInt(m.readingPathSequence), iaEpisodeNullableText(m.extraKind)); err != nil {
			return err
		}
	}
	return nil
}

// Replay decodes only the immutable receipt and its complete typed child cohort.
func lookupIAMembership(ctx context.Context, turn *commandTurn, authority *iaEntryAuthorization, command *iaMembershipAcceptCommand, header iaMembershipSavedHeader) (out iaMembershipHistory, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || authority == nil || command == nil || !command.valid ||
		!header.facts.exists || header.facts.kind != IAAcceptMembership || header.facts.identity != (receiptIdentity{command.identity, command.digest}) ||
		header.facts.resultCount < 1 || header.facts.resultCount > 4096 || header.memberBytes <= 0 ||
		header.acceptedRevision <= 0 || header.appliedMS < 0 || header.sourceID == "" || header.sourceIncarnation == "" ||
		header.objectID == "" || header.objectRevision == "" || len(header.directoryPath) == 0 || len(header.directoryIdentity) == 0 || len(header.directoryRevision) == 0 || len(header.nextPayload) == 0 {
		return out, ErrResolveRequired
	}
	if err = authority.budget.check(ctx, turn, header.memberBytes); err != nil {
		return out, err
	}
	for _, id := range []stmt.ID{iaMembershipMemberBytes, iaMembershipMembers} {
		if _, ok := turn.steps[id]; !ok {
			return out, invalid("ia_membership_member_steps")
		}
	}
	limit := header.facts.resultCount + 1
	rows, err := turn.query(ctx, turn.steps[iaMembershipMemberBytes].statementStep, command.request.scope.runID, command.identity.Incarnation[:], command.identity.Sequence, limit)
	if err != nil {
		return out, err
	}
	var lengths []int64
	readErr := func() error {
		for rows.Next() {
			var ordinal, n int64
			if err := rows.Scan(&ordinal, &n); err != nil {
				return err
			}
			if ordinal != int64(len(lengths)) || n <= 0 || int64(len(lengths)) >= header.facts.resultCount {
				return ErrResolveRequired
			}
			lengths = append(lengths, n)
		}
		return rows.Err()
	}()
	if err := errors.Join(readErr, rows.Close()); err != nil {
		return out, err
	}
	if int64(len(lengths)) != header.facts.resultCount {
		return out, ErrResolveRequired
	}
	var total int64
	for _, n := range lengths {
		if n > math.MaxInt64-total {
			return out, ErrResolveRequired
		}
		total += n
	}
	if total != header.memberBytes {
		return out, ErrResolveRequired
	}
	if err = authority.budget.check(ctx, turn, total); err != nil {
		return out, err
	}
	rows, err = turn.query(ctx, turn.steps[iaMembershipMembers].statementStep, command.request.scope.runID, command.identity.Incarnation[:], command.identity.Sequence, limit)
	if err != nil {
		return out, err
	}
	facts := iaMediaResult{
		runID: command.request.scope.runID, sourceID: header.sourceID, sourceIncarnation: header.sourceIncarnation,
		acceptedRevision: header.acceptedRevision, appliedMS: header.appliedMS,
		objectID: header.objectID, objectRevision: header.objectRevision, cursorKind: header.cursorKind,
		directoryPath: header.directoryPath, directoryIdentity: header.directoryIdentity, directoryRevision: header.directoryRevision,
		expectedPayload: header.expectedPayload, nextPayload: header.nextPayload, resultVersion: 1,
	}
	readErr = func() error {
		for rows.Next() {
			var m iaMediaMember
			var parent, parentKind, ancestor, ancestorKind, asset, epoch, part, linkRevision, cue sql.NullInt64
			var start, end sql.NullFloat64
			var metadata, linkIncarnation []byte
			var edition, effect, boundary, reading, extra sql.NullString
			var readingHead, readingSequence sql.NullInt64
			if err := rows.Scan(&m.ordinal, &m.kind, &m.libraryID, &m.subjectID, &m.subjectKind, &m.subjectRevision, &metadata,
				&parent, &parentKind, &ancestor, &ancestorKind, &asset, &epoch, &part, &linkRevision, &linkIncarnation,
				&edition, &effect, &cue, &start, &end, &boundary, &reading, &readingHead, &readingSequence, &extra); err != nil {
				return err
			}
			if m.ordinal != int64(len(facts.members)) || int64(len(facts.members)) >= header.facts.resultCount || len(metadata) != 16 {
				return ErrResolveRequired
			}
			copy(m.metadataIncarnation[:], metadata)
			m.parentID, m.parentKind = iaEpisodePointer(parent), iaEpisodePointer(parentKind)
			m.ancestorID, m.ancestorKind = iaEpisodePointer(ancestor), iaEpisodePointer(ancestorKind)
			m.assetID, m.assetInfoEpoch, m.partIndex, m.linkOriginRevision = iaEpisodePointer(asset), iaEpisodePointer(epoch), iaEpisodePointer(part), iaEpisodePointer(linkRevision)
			if linkIncarnation != nil {
				var v [16]byte
				if len(linkIncarnation) != 16 {
					return ErrResolveRequired
				}
				copy(v[:], linkIncarnation)
				m.linkOriginIncarnation = &v
			}
			m.edition, m.linkEffect = iaEpisodeTextPointer(edition), iaEpisodeTextPointer(effect)
			m.cueOrdinal = iaEpisodePointer(cue)
			m.startSeconds, m.endSeconds = iaEpisodeRealPointer(start), iaEpisodeRealPointer(end)
			m.episodeBoundaryStatus = iaEpisodeTextPointer(boundary)
			m.readingFormat, m.readingPathHead, m.readingPathSequence = iaEpisodeTextPointer(reading), iaEpisodePointer(readingHead), iaEpisodePointer(readingSequence)
			m.extraKind = iaEpisodeTextPointer(extra)
			n, err := iaMediaMemberRetainedBytes(IAAcceptMembership, facts.runID, m)
			if err != nil || n != lengths[len(facts.members)] {
				return ErrResolveRequired
			}
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
	facts.memberBytes = total
	if err = preflightIAMediaResult(IAAcceptMembership, command.identity, command.digest, facts); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	digest, err := canonicalResultDigest(command.identity, command.digest, iaMembershipResult{facts})
	if err != nil || !equalBytes(digest[:], header.facts.resultDigest[:]) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	return iaMembershipHistory{decision: receiptReplay, result: iaMembershipResult{facts}, digest: digest}, nil
}
