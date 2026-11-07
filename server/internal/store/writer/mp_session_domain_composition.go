package writer

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Parent owns the actual descriptors. These closed private purposes never
// register IDs, mutate a frame descriptor or execute a nested public command.
const (
	mpCoreQueueAdvancePurpose CommandID = 760
	mpCoreNextCommitPurpose   CommandID = 762
)

type mpMediaRecipeKind uint8

const (
	mpRecipeStandalone    mpMediaRecipeKind = 1
	mpRecipeStart         mpMediaRecipeKind = 2
	mpRecipeAcceptedStart mpMediaRecipeKind = 3
	mpRecipePresentation  mpMediaRecipeKind = 4
	mpRecipeQueueAdvance  mpMediaRecipeKind = 5
	mpRecipeNextCommit    mpMediaRecipeKind = 6
)

func mpMediaRecipePurpose(t *commandTurn, leaf CommandID, r mpMediaRecipeKind) error {
	switch leaf {
	case MPSourcePreparedPin, MPSourceSubtitlePin, MPSourceDiscPin, MPSourceReceiverPin, MPSourceRemoteBind, MPHLSReserve, MPHLSPlan, MPDirectDelivery:
	default:
		return ErrInvalid
	}
	switch r {
	case mpRecipeStandalone:
		return mpPrepTurn(t, leaf)
	case mpRecipeStart:
		return mpPrepTurn(t, MPSessionCreate)
	case mpRecipeAcceptedStart:
		return mpPrepTurn(t, MPStartPrepAccept)
	case mpRecipePresentation:
		return mpPrepTurn(t, MPSessionPresentation)
	case mpRecipeQueueAdvance:
		return mpPrepTurn(t, mpCoreQueueAdvancePurpose)
	case mpRecipeNextCommit:
		return mpPrepTurn(t, mpCoreNextCommitPurpose)
	}
	return ErrInvalid
}
func mpMediaParentRecipe(t *commandTurn) (mpMediaRecipeKind, error) {
	if t == nil || t.transaction == nil {
		return 0, ErrInvalid
	}
	switch t.transaction.descriptor.ID {
	case MPSessionCreate:
		return mpRecipeStart, nil
	case MPStartPrepAccept:
		return mpRecipeAcceptedStart, nil
	case MPSessionPresentation:
		return mpRecipePresentation, nil
	case mpCoreQueueAdvancePurpose:
		return mpRecipeQueueAdvance, nil
	case mpCoreNextCommitPurpose:
		return mpRecipeNextCommit, nil
	}
	return 0, ErrInvalid
}

// Fixed typed recipe, not a runtime command list/callback/response JSON. Each
// optional selected pin is at most ONE; larger manifest cohorts remain staged.
type mpSessionDeliveryRecipe struct {
	prepared         mpPreparedPinInput
	subtitle         mpSubtitlePinInput
	disc             mpDiscPinInput
	receiver         mpReceiverPinInput
	remote           mpRemoteBinding
	direct           mpDirectDeliveryInput
	hls              mpHLSPlanInput
	reservationBytes int64
}
type mpSessionDeliveryProjection struct {
	prepared, subtitle, disc, receiver, remote mpSourcePinEffect
	delivery, reservation                      mpHLSEffect
	physicalRevision                           int64
}

func composeMPSessionDelivery(ctx context.Context, t *commandTurn, f mpPhysicalFence, q mpSessionDeliveryRecipe) (out mpSessionDeliveryProjection, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	recipe, err := mpMediaParentRecipe(t)
	if err != nil {
		return out, err
	}
	if q.prepared.version != "" {
		out.prepared, err = applyMPSourcePreparedPinEffects(ctx, t, f, q.prepared, recipe)
		if err != nil {
			return
		}
		f.revision = out.prepared.revision
	}
	if q.subtitle.resource != "" {
		out.subtitle, err = applyMPSourceSubtitlePinEffects(ctx, t, f, q.subtitle, recipe)
		if err != nil {
			return
		}
		f.revision = out.subtitle.revision
	}
	if q.disc.object != "" {
		out.disc, err = applyMPSourceDiscPinEffects(ctx, t, f, q.disc, recipe)
		if err != nil {
			return
		}
		f.revision = out.disc.revision
	}
	if q.receiver.ownerSession != "" {
		out.receiver, err = applyMPSourceReceiverPinEffects(ctx, t, f, q.receiver, recipe)
		if err != nil {
			return
		}
		f.revision = out.receiver.revision
	}
	if q.remote.originVersion != "" {
		out.remote, err = applyMPSourceRemoteBindEffects(ctx, t, f, q.remote, recipe)
		if err != nil {
			return
		}
		f.revision = out.remote.revision
	}
	if q.hls.digest != "" {
		if q.direct.sourceVersion != "" || q.reservationBytes <= 0 {
			return out, ErrInvalid
		}
		out.reservation, err = applyMPHLSReserveEffects(ctx, t, f, 0, q.reservationBytes, recipe)
		if err != nil {
			return
		}
		f.revision = out.reservation.revision
		out.delivery, err = applyMPHLSPlanEffects(ctx, t, f, q.hls, recipe)
		if err != nil {
			return
		}
		f.revision = out.delivery.revision
	} else {
		if q.reservationBytes != 0 || q.direct.sourceVersion == "" {
			return out, ErrInvalid
		}
		out.delivery, err = applyMPDirectDeliveryEffects(ctx, t, f, q.direct, recipe)
		if err != nil {
			return
		}
		f.revision = out.delivery.revision
	}
	out.physicalRevision = f.revision
	return
}

// Actual-root origin is inherited from the captured predecessor's typed public
// historical original UO root; neither760 nor762 invents a public710 birth. Parent has
// already applied its bounded queue/next selection CAS in this SAME turn.
type mpQueueSessionInstall struct {
	next                   mpSessionCreateInput
	predecessor            mpSessionFence
	queueRevision, custody int64
	nextToken              string
}

func mpQueueInstallOrigin(ctx context.Context, t *commandTurn, q mpQueueSessionInstall) (old mpSessionEffect, err error) {
	defer func() { old, err = finishCommand(ctx, commandLifetime(t), old, err) }()
	if t == nil || t.transaction == nil || (t.transaction.descriptor.ID != mpCoreQueueAdvancePurpose && t.transaction.descriptor.ID != mpCoreNextCommitPurpose) {
		return old, ErrInvalid
	}
	if !q.next.valid() || q.next.session == q.predecessor.session || q.next.queue.id == "" || q.next.queue.revision != q.queueRevision || q.next.queue.custody != q.custody {
		return old, ErrInvalid
	}
	old, err = captureMPSession(ctx, t, q.predecessor, false)
	if err != nil {
		return
	}
	// Prepared media may have private buffering custody; publication of its NEW
	// logical authority waits for exact predecessor physical exit, never cancel.
	if old.physicalExit.exitedGeneration != old.mediaGeneration || old.physicalPending {
		return old, ErrConflict
	}
	a, b := q.next.actor, q.predecessor.physical.actor
	if a.server != b.server || a.authority != b.authority || a.account != b.account || a.profile != b.profile || a.device != b.device || a.family != b.family {
		return old, ErrDisclosureDenied
	}
	if err = checkMPPrepActor(ctx, t, a); err != nil {
		return
	}
	if err = checkMPPrepSource(ctx, t, q.next.source); err != nil {
		return
	}
	if err = checkMPPrepItem(ctx, t, q.next.item, q.next.part, q.next.source); err != nil {
		return
	}
	if err = checkMPPrepQueue(ctx, t, q.next.queue, a); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.session.queue_install_current", q.next.queue.id, q.queueRevision, q.custody, q.next.queue.entry, q.predecessor.session, q.next.session); err != nil {
		return
	}
	err = mpPrepOne(ctx, t, "mp.session.origin_binding", q.predecessor.session, q.next.userRoot, q.next.memberIncarnation, q.next.profileLifetime)
	return
}
func composeMPSessionFromQueueAdvance(ctx context.Context, t *commandTurn, q mpQueueSessionInstall) (out mpSessionEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, mpCoreQueueAdvancePurpose); err != nil {
		return
	}
	old, err := mpQueueInstallOrigin(ctx, t, q)
	if err != nil {
		return out, err
	}
	if err = mpPrepOne(ctx, t, "mp.session.item_identity", q.next.item, q.next.itemPublic[:]); err != nil {
		return
	}
	if err = insertMPSessionPhysical(ctx, t, q.next); err != nil {
		return
	}
	if err = insertMPSessionLogical(ctx, t, q.next); err != nil {
		return
	}
	if old.endedMS == 0 {
		if err = mpPrepOne(ctx, t, "mp.session.stop", q.next.nowMS, "queue_advanced", q.next.nowMS, q.predecessor.session, old.revision, old.generation, q.predecessor.physical.media, q.predecessor.physical.generation); err != nil {
			return
		}
	}
	out = mpInstalledSessionProjection(q.next)
	out.retiredMedia = old.retiredMedia
	return
}
func composeMPSessionFromNextCommit(ctx context.Context, t *commandTurn, q mpQueueSessionInstall, prepared mpPhysicalFence) (out mpSessionEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, mpCoreNextCommitPurpose); err != nil {
		return
	}
	if !mpPrepTexts(q.nextToken) {
		return out, ErrInvalid
	}
	old, err := mpQueueInstallOrigin(ctx, t, q)
	if err != nil {
		return out, err
	}
	if err = mpPrepOne(ctx, t, "mp.session.next_install_current", q.nextToken, q.next.queue.id, q.predecessor.session, q.predecessor.generation, q.next.media, q.next.mediaGeneration, q.next.item, q.next.queue.entry, q.next.actor.family, q.next.actor.device, q.next.nowMS); err != nil {
		return
	}
	p, err := captureMPPhysical(ctx, t, prepared, true)
	if err != nil {
		return out, err
	}
	if p.media != q.next.media || p.incarnation != q.next.mediaIncarnation || p.execution != q.next.execution || p.generation != q.next.mediaGeneration || p.item != q.next.item || prepared.source != q.next.source || p.grantHash != q.next.grantHash || p.grantToken != q.next.grantToken || p.expiresAt != q.next.expiresAt {
		return out, ErrConflict
	}
	if err = mpPrepOne(ctx, t, "mp.session.item_identity", q.next.item, q.next.itemPublic[:]); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.session.physical_origin_binding", q.next.media, q.next.userRoot, q.next.memberIncarnation, q.next.profileLifetime); err != nil {
		return
	}
	// Private-to-public authority transfer of the SAME resource, not new physical
	// custody and not a post-cancel release. Actual predecessor has already exited.
	if err = mpPrepOne(ctx, t, "mp.session.next_private_transfer", q.next.media, q.predecessor.physical.media, q.next.nowMS); err != nil {
		return
	}
	if err = insertMPSessionLogical(ctx, t, q.next); err != nil {
		return
	}
	if old.endedMS == 0 {
		if err = mpPrepOne(ctx, t, "mp.session.stop", q.next.nowMS, "next_committed", q.next.nowMS, q.predecessor.session, old.revision, old.generation, q.predecessor.physical.media, q.predecessor.physical.generation); err != nil {
			return
		}
	}
	out = mpInstalledSessionProjection(q.next)
	out.physicalRevision = p.revision
	if p.ready {
		row, e := mpPrepRow(ctx, t, "mp.session.next_ready", q.next.nowMS, q.next.nowMS, q.next.session, q.next.media, q.next.mediaGeneration)
		if e != nil {
			return out, e
		}
		if err = row.Scan(&out.state); err != nil {
			return
		}
		out.revision++
	}
	out.retiredMedia = old.retiredMedia
	return
}
func mpQueueSessionCompositionDefinitions() []stmt.Definition {
	d := []stmt.Definition{
		mpPrepDefinition("mp.session.next_ready", `UPDATE playback_v1_sessions SET state=mp_desired_state,revision=revision+1,paused_since_ms=CASE WHEN mp_desired_state='paused' THEN ? ELSE 0 END,updated_ms=? WHERE id=? AND media_session_id=? AND media_generation=? AND revision=1 AND ended_ms=0 AND state='preparing' RETURNING state`, "iissi", stmt.Write),
		mpPrepDefinition("mp.session.queue_install_current", `SELECT 1 FROM queues_v1 WHERE id=? AND revision=? AND custody_generation=? AND current_entry_id=? AND (session_id=? OR session_id=?)`, "siisss", stmt.Read),
		mpPrepDefinition("mp.session.next_install_current", `SELECT 1 FROM playback_v1_prepared WHERE token=? AND queue_id=? AND session_id=? AND session_generation=? AND media_session_id=? AND mp_media_generation=? AND item_id=? AND entry_id=? AND mp_family_id=? AND device_id=? AND state='committed' AND expires_ms>?`, "sssisiisssi", stmt.Read),
		mpPrepDefinition("mp.session.next_private_transfer", `DELETE FROM playback_private_presentations WHERE session_id=? AND owner_session_id=? AND expires_ms>? RETURNING 1`, "ssi", stmt.Write),
	}
	for i := range d {
		d[i].CallSites = []string{"store/writer/mp_session_domain_composition.go"}
	}
	return d
}

// Parent760/762 stop staging shares the exact caller turn and receipt owner.
// It records intent now; installation above waits for durable actual exit.
func composeMPQueueSessionRetire(ctx context.Context, t *commandTurn, f mpSessionFence) (out mpSessionEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil || t.transaction == nil || !mpPrepTexts(f.physical.retireRequest) {
		return out, ErrInvalid
	}
	switch t.transaction.descriptor.ID {
	case mpCoreQueueAdvancePurpose, mpCoreNextCommitPurpose:
	default:
		return out, ErrInvalid
	}
	if err = checkMPPrepActor(ctx, t, f.physical.actor); err != nil {
		return
	}
	if out, err = captureMPSession(ctx, t, f, false); err != nil {
		return
	}
	if out.endedMS != 0 || out.stopExecution != "" {
		return out, ErrConflict
	}
	if err = mpPrepOne(ctx, t, "mp.session.request_stop", f.physical.retireRequest, f.physical.media, f.physical.revision, f.physical.generation, f.physical.execution); err != nil {
		return
	}
	out.physicalRevision++
	out.physicalExit.retiredGeneration = f.physical.generation
	out.physicalExit.retirementRequest = f.physical.retireRequest
	out.stopExecution = f.physical.execution
	out.stopGeneration = f.physical.generation
	out.physicalPending = true
	out.changed = true
	out.retiredMedia = mpSessionRetireProjection{item: out.item, asset: out.asset, media: out.media, execution: out.stopExecution, sourceRequest: f.physical.request, incarnation: out.mediaIncarnation, generation: out.mediaGeneration, exit: out.physicalExit, physicalPending: true}
	return
}
