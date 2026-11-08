package writer

import (
	"context"
	"strings"
)

// This private source frame precedes ANY retained owner/header/payload lookup.
// MG-03 supplies the UO receipt/commit adapter, never an auth callback or a raw
// transaction API. A removed source may still disclose an owner's original
// result; fresh effects and new captures must separately pass the live fence.
type mgSourceFrame struct {
	self              *mgSourceFrame
	turn              *commandTurn
	actor             *mgActorAuthorization
	source            string
	revision, removed int64
}
type mgSourceHeadWitness struct {
	source, head, access string
	revision             int64
}
type mgSourceAcquisition struct {
	self    *mgSourceAcquisition
	frame   *mgSourceFrame
	witness mgSourceHeadWitness
}

// Distinct from acquisition. No mint/use exists here: cleanup owners must bind
// their retained claim and MP-05 physical observation in their own closed task.
// In particular a denied viewer cannot be laundered through a cleanup stamp.
type mgSourceMaintenance struct{ turn *commandTurn }

type mgSourceOriginalDenial struct {
	Command      CommandID
	Version      int64
	Revision     int64
	Source, Code string
	AppliedMS    int64
}

func openMGSourceFrame(ctx context.Context, t *commandTurn, p *mgActorProof, life context.Context, source string, b mgAuthorityBudget) (out *mgSourceFrame, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !mpWorkerID(source) || p == nil {
		return nil, ErrInvalid
	}
	// Source security changes require owner authority, independently of whether
	// a provider currently permits server-member viewing.
	switch tID(t) {
	case MGConfigureTuner, MGSetSourceEnabled, MGRetireSource, MGSetSourceViewerAccess:
		if p.kind != mgActorOwner {
			return nil, ErrDisclosureDenied
		}
	}
	actor, err := authorizeMGActor(ctx, t, p, life, b)
	if err != nil {
		return nil, err
	}
	row, err := wc008DraftQuery(ctx, t, mgAuthoritySource, source)
	if err != nil {
		return nil, err
	}
	var revision, removed int64
	if err = row.Scan(&revision, &removed); err != nil {
		return nil, usAuthorityReadError(err)
	}
	if revision <= 0 || (removed != 0 && removed != 1) {
		return nil, ErrResolveRequired
	}
	out = &mgSourceFrame{turn: t, actor: actor, source: strings.Clone(source), revision: revision, removed: removed}
	out.self = out
	return out, nil
}
func tID(t *commandTurn) CommandID {
	if t == nil || t.transaction == nil {
		return 0
	}
	return t.transaction.descriptor.ID
}
func (f *mgSourceFrame) recheck(ctx context.Context, t *commandTurn) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if f == nil || f.self != f || f.turn != t {
		return ErrDisclosureDenied
	}
	return f.actor.recheck(ctx, t)
}

// Saved denial is a value, never authority or Applied/COMMIT evidence. MG-03
// stores it through the same closed result codec as success, preserving the
// original applied time on replay. Current actor recheck still precedes replay.
func originalMGSourceDenial(ctx context.Context, f *mgSourceFrame) (out mgSourceOriginalDenial, err error) {
	defer func() { out, err = finishCommand(ctx, mgSourceFrameLifetime(f), out, err) }()
	if f == nil {
		return out, ErrInvalid
	}
	if err = f.recheck(ctx, f.turn); err != nil {
		return out, err
	}
	row, err := wc008DraftQuery(ctx, f.turn, mgAuthoritySource, f.source)
	if err != nil {
		return out, err
	}
	var revision, removed int64
	if err = row.Scan(&revision, &removed); err != nil {
		return out, usAuthorityReadError(err)
	}
	if revision != f.revision || removed != 1 || f.removed != 1 {
		return out, ErrConflict
	}
	now, err := mgSourceTime(ctx, f.turn)
	if err != nil {
		return out, err
	}
	return mgSourceOriginalDenial{Command: tID(f.turn), Version: 1, Revision: revision, Source: f.source, Code: "source_retired", AppliedMS: now}, nil
}
func authorizeMGSourceAcquisition(ctx context.Context, f *mgSourceFrame, w mgSourceHeadWitness) (out *mgSourceAcquisition, err error) {
	defer func() { out, err = finishCommand(ctx, mgSourceFrameLifetime(f), out, err) }()
	if f == nil || !mpWorkerID(w.source) || !mpWorkerID(w.head) || w.revision <= 0 || w.source != f.source || (w.access != "owner-only" && w.access != "server-members") {
		return nil, ErrInvalid
	}
	switch tID(f.turn) {
	case 814, 815, 820, 830, 831, 832, 833, 835:
	default:
		return nil, ErrInvalid
	}
	if err = f.recheck(ctx, f.turn); err != nil {
		return nil, err
	}
	if f.actor.proof.kind != mgActorOwner && w.access != "server-members" {
		return nil, ErrDisclosureDenied
	}
	if err = usAuthorityPresence(ctx, f.turn, mgAuthorityPolicy, true, w.source, w.access); err != nil {
		return nil, err
	}
	if err = usAuthorityPresence(ctx, f.turn, mgAuthorityHead, true, w.source, w.revision, w.head); err != nil {
		return nil, err
	}
	out = &mgSourceAcquisition{frame: f, witness: w}
	out.self = out
	return out, nil
}
func (a *mgSourceAcquisition) recheck(ctx context.Context, t *commandTurn) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if a == nil || a.self != a || a.frame == nil || a.frame.turn != t {
		return ErrDisclosureDenied
	}
	_, err = authorizeMGSourceAcquisition(ctx, a.frame, a.witness)
	return err
}

// Fresh-only source CAS. Retained result disclosure deliberately does not use
// the historical request's head/revision, so replay survives a guide refresh.
func (f *mgSourceFrame) requireFreshHead(ctx context.Context, w mgSourceHeadWitness) (err error) {
	defer func() { _, err = finishCommand(ctx, mgSourceFrameLifetime(f), struct{}{}, err) }()
	if f == nil || w.source != f.source || !mpWorkerID(w.head) || w.revision <= 0 {
		return ErrInvalid
	}
	if err = f.recheck(ctx, f.turn); err != nil {
		return err
	}
	row, err := wc008DraftQuery(ctx, f.turn, mgAuthoritySource, f.source)
	if err != nil {
		return err
	}
	var revision, removed int64
	if err = row.Scan(&revision, &removed); err != nil {
		return usAuthorityReadError(err)
	}
	if removed != 0 {
		return ErrDisclosureDenied
	}
	return usAuthorityPresence(ctx, f.turn, mgAuthorityHead, true, w.source, w.revision, w.head)
}

func mgSourceFrameLifetime(f *mgSourceFrame) context.Context {
	if f == nil {
		return nil
	}
	return commandLifetime(f.turn)
}

// The only source-frame disclosure adapter for this closed saved variant.
// Current authorization precedes decoding; historical CAS/head is not replay
// eligibility. MG-03 owns bounded native payload fetch and the accepting COMMIT.
func (f *mgSourceFrame) discloseOriginalDenial(ctx context.Context, id CommandIdentity, q RequestDigest, payload []byte, digest ResultDigest) (out mgSourceOriginalDenial, err error) {
	defer func() { out, err = finishCommand(ctx, mgSourceFrameLifetime(f), out, err) }()
	if f == nil {
		return out, ErrDisclosureDenied
	}
	if err = f.recheck(ctx, f.turn); err != nil {
		return out, err
	}
	if len(payload) > mgSourceDenialCap {
		return out, ErrResolveRequired
	}
	if err = f.actor.budget.check(ctx, f.turn, int64(len(payload))); err != nil {
		return out, err
	}
	out, err = decodeMGSourceDenial(id, q, payload, digest)
	if err != nil {
		return out, err
	}
	if out.Source != f.source || out.Command != tID(f.turn) {
		return out, ErrResolveRequired
	}
	return out, nil
}
