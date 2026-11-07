package writer

import (
	"context"
	"crypto/sha256"
	"math"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Effect components, not accepting commands. Parent owns current opaque
// authority, original receipt/floor resolution, actual debt/events and W's sole
// commit/ACK path. MP-001 owns the proposed mp_ columns named by these drafts.
const (
	MPSessionCreate         CommandID = 710
	MPSessionReady          CommandID = 711
	MPSessionTransport      CommandID = 712
	MPSessionRenew          CommandID = 713
	MPSessionPresentation   CommandID = 714
	MPSessionStop           CommandID = 715
	MPSessionExpire         CommandID = 716
	MPSessionAdminTransport CommandID = 717
	MPSessionAccountStop    CommandID = 718
	MPSessionReconcile      CommandID = 719
)

type mpPhysicalFence struct {
	retireRequest                           string // stable current command operation, not the source start key
	media, execution, request               string
	incarnation                             [16]byte
	revision, generation, item, part, nowMS int64
	actor                                   mpPrepActor // target owner DATA; privileged operator remains separate
	source                                  mpPrepSource
	queue                                   mpPrepQueue
}
type mpSessionFence struct {
	physical                                     mpPhysicalFence
	session                                      string
	incarnation                                  [16]byte
	revision, generation, presentationGeneration int64
}
type mpPhysicalState struct {
	exit                                                                                        mpPhysicalExitFacts
	reservedBytes                                                                               int64
	media, execution, request, asset, state, mode, grantHash, grantToken, expiresAt, exitDigest string
	incarnation                                                                                 [16]byte
	revision, generation, item, stopMS, cleanupCursor, expiresMS                                int64
	exited, cleaned, grantRetired, ready                                                        bool
}
type mpSessionEffect struct {
	retiredMedia                                                                                                                                              mpSessionRetireProjection
	physicalExit                                                                                                                                              mpPhysicalExitFacts
	itemPublic                                                                                                                                                [16]byte
	item, part                                                                                                                                                int64
	asset, queue, entry                                                                                                                                       string
	session, media, state, mode, engine, presentationURL, endReason                                                                                           string
	sessionIncarnation, mediaIncarnation                                                                                                                      [16]byte
	revision, generation, presentationGeneration, mediaGeneration, physicalRevision, leaseMS, grantExpiresMS, positionMS, startMS, transportSequence, endedMS int64
	stopExecution                                                                                                                                             string
	stopGeneration, cleanupCursor                                                                                                                             int64
	physicalPending, changed                                                                                                                                  bool
}

// The actual F native retirement/exit tuple. No receipt child is its sole
// live custodian; ACK/reclaim must leave these mutable root facts intact.
type mpPhysicalExitFacts struct {
	retiredGeneration, exitedGeneration, exitedMS int64
	retirementRequest, exitRequest                string
	supervisorIncarnation                         [16]byte
}

func (x mpPhysicalExitFacts) valid(generation int64, pending bool) bool {
	if x.retiredGeneration < 0 || x.retiredGeneration > generation || x.exitedGeneration < 0 || x.exitedGeneration > x.retiredGeneration || x.exitedMS < 0 || (x.retiredGeneration == 0) != (x.retirementRequest == "") {
		return false
	}
	if x.exitedGeneration == 0 {
		if x.exitRequest != "" || x.exitedMS != 0 || x.supervisorIncarnation != ([16]byte{}) {
			return false
		}
	} else {
		if x.exitRequest != x.retirementRequest || x.supervisorIncarnation == ([16]byte{}) || x.exitedMS <= 0 {
			return false
		}
	}
	return pending || (x.exitedGeneration == generation && x.retiredGeneration == generation)
}

type mpSessionRetireProjection struct {
	media, execution, sourceRequest string
	incarnation                     [16]byte
	generation                      int64
	exit                            mpPhysicalExitFacts
	physicalPending                 bool
}

func mpRetireProjection(p mpPhysicalState) mpSessionRetireProjection {
	return mpSessionRetireProjection{media: p.media, execution: p.execution, sourceRequest: p.request, incarnation: p.incarnation, generation: p.generation, exit: p.exit, physicalPending: !p.cleaned}
}
func writeMPSessionRetire(e *requestEncoder, r mpSessionRetireProjection) error {
	if r.media == "" {
		if r != (mpSessionRetireProjection{}) {
			return ErrInvalid
		}
		e.number(0)
		return nil
	}
	if !mpPrepTexts(r.media, r.execution, r.sourceRequest) || r.incarnation == ([16]byte{}) || r.generation <= 0 || !r.exit.valid(r.generation, r.physicalPending) {
		return ErrInvalid
	}
	e.number(1)
	e.text(r.media)
	e.text(r.execution)
	e.text(r.sourceRequest)
	e.bytes(r.incarnation[:])
	e.number(uint64(r.generation))
	writeMPPhysicalExit(e, r.exit)
	if r.physicalPending {
		e.number(1)
	} else {
		e.number(0)
	}
	return e.err
}

func writeMPPhysicalExit(e *requestEncoder, x mpPhysicalExitFacts) {
	e.number(uint64(x.retiredGeneration))
	e.number(uint64(x.exitedGeneration))
	e.number(uint64(x.exitedMS))
	e.text(x.retirementRequest)
	e.text(x.exitRequest)
	e.bytes(x.supervisorIncarnation[:])
}

func (f mpPhysicalFence) valid() bool {
	return mpPrepTexts(f.media, f.execution, f.request) && f.incarnation != ([16]byte{}) && mpPrepRevision(f.revision) && mpPrepRevision(f.generation) && f.item > 0 && f.part >= 0 && f.nowMS > 0 && f.actor.valid(0)
}
func (f mpSessionFence) valid() bool {
	return f.physical.valid() && mpPrepTexts(f.session) && f.incarnation != ([16]byte{}) && mpPrepRevision(f.revision) && mpPrepRevision(f.generation) && mpPrepRevision(f.presentationGeneration)
}

func captureMPPhysical(ctx context.Context, t *commandTurn, f mpPhysicalFence, fresh bool) (out mpPhysicalState, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !f.valid() {
		return out, ErrInvalid
	}
	row, err := mpPrepRow(ctx, t, "mp.session.physical", f.media)
	if err != nil {
		return
	}
	var inc, supervisor []byte
	var exited, cleaned, retired, ready int64
	err = row.Scan(&out.media, &inc, &out.revision, &out.generation, &out.execution, &out.request, &out.item, &out.asset, &out.state, &out.mode, &out.grantHash, &out.grantToken, &out.expiresAt, &out.stopMS, &exited, &cleaned, &out.exitDigest, &out.cleanupCursor, &retired, &out.reservedBytes, &out.exit.retiredGeneration, &out.exit.exitedGeneration, &out.exit.retirementRequest, &supervisor, &out.exit.exitedMS, &out.exit.exitRequest, &ready)
	if err != nil {
		return
	}
	if out.reservedBytes < 0 || len(inc) != 16 || exited < 0 || exited > 1 || cleaned < 0 || cleaned > exited || retired < 0 || retired > 1 {
		return out, ErrResolveRequired
	}
	copy(out.incarnation[:], inc)
	if out.exit.exitedGeneration < 0 || out.exit.retiredGeneration < out.exit.exitedGeneration || out.exit.retiredGeneration > out.generation || (len(supervisor) != 0 && len(supervisor) != 16) {
		return out, ErrResolveRequired
	}
	copy(out.exit.supervisorIncarnation[:], supervisor)
	if exited == 1 && (out.exit.exitedGeneration != out.generation || out.exit.retirementRequest == "" || out.exit.exitRequest != out.exit.retirementRequest || len(supervisor) != 16 || out.exit.exitedMS <= 0) {
		return out, ErrResolveRequired
	}
	expiry, e := time.Parse(time.RFC3339Nano, out.expiresAt)
	if e != nil {
		return out, ErrResolveRequired
	}
	out.expiresMS = expiry.UnixMilli()
	out.exited = exited == 1
	out.cleaned = cleaned == 1
	if ready < 0 || ready > 1 {
		return out, ErrResolveRequired
	}
	out.ready = ready == 1
	out.grantRetired = retired == 1
	if !out.exit.valid(out.generation, !out.cleaned) {
		return out, ErrResolveRequired
	}
	if out.incarnation != f.incarnation || out.revision != f.revision || out.generation != f.generation || out.execution != f.execution || out.request != f.request || out.item != f.item || out.asset != f.source.asset {
		return out, ErrConflict
	}
	// Native immutable occurrence relationship, including original family. Token
	// refresh within the SAME current family is supported; no synthetic principal.
	args := []any{f.media, f.actor.server, f.actor.authority, f.actor.account, f.actor.profile, f.actor.device, f.actor.family}
	args = append(args, f.source.args()...)
	args = append(args, f.queue.args()...)
	args = append(args, f.part)
	if err = mpPrepOne(ctx, t, "mp.session.input_current", args...); err != nil {
		return
	}
	if fresh {
		if !f.actor.valid(f.nowMS) || out.stopMS != 0 || out.expiresMS <= f.nowMS || out.exited || out.grantRetired || out.state == "stopped" || out.state == "ended" || out.state == "failed" {
			return out, ErrConflict
		}
		if err = checkMPPrepActor(ctx, t, f.actor); err != nil {
			return
		}
		if err = checkMPPrepSource(ctx, t, f.source); err != nil {
			return
		}
		if err = checkMPPrepItem(ctx, t, f.item, f.part, f.source); err != nil {
			return
		}
	}
	return
}
func captureMPSession(ctx context.Context, t *commandTurn, f mpSessionFence, fresh bool) (out mpSessionEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !f.valid() {
		return out, ErrInvalid
	}
	p, err := captureMPPhysical(ctx, t, f.physical, fresh)
	if err != nil {
		return out, err
	}
	row, err := mpPrepRow(ctx, t, "mp.session.logical", f.session)
	if err != nil {
		return
	}
	var inc, public []byte
	err = row.Scan(&out.session, &inc, &out.revision, &out.generation, &out.presentationGeneration, &out.media, &out.mediaGeneration, &out.state, &out.positionMS, &out.startMS, &out.transportSequence, &out.leaseMS, &out.endedMS, &out.endReason, &out.engine, &out.presentationURL, &out.item, &out.part, &out.asset, &out.queue, &out.entry, &public)
	if err != nil {
		return
	}
	if len(inc) != 16 || len(public) != 16 {
		return out, ErrResolveRequired
	}
	copy(out.itemPublic[:], public)
	copy(out.sessionIncarnation[:], inc)
	if out.sessionIncarnation != f.incarnation || out.revision != f.revision || out.generation != f.generation || out.presentationGeneration != f.presentationGeneration || out.media != p.media || out.mediaGeneration != p.generation || out.item != p.item || out.asset != p.asset {
		return out, ErrConflict
	}
	if fresh && (out.endedMS != 0 || out.leaseMS <= f.physical.nowMS) {
		return out, ErrConflict
	}
	out.physicalExit = p.exit
	out.mediaIncarnation = p.incarnation
	out.physicalRevision = p.revision
	out.mode = p.mode
	out.grantExpiresMS = p.expiresMS
	out.cleanupCursor = p.cleanupCursor
	out.physicalPending = !p.cleaned
	if p.stopMS != 0 {
		out.retiredMedia = mpRetireProjection(p)
		out.stopExecution = p.execution
		out.stopGeneration = p.generation
	}
	return
}

// A media URL is a server-scoped original URL constructed by the trusted
// protocol adapter, never the remote origin URL or filesystem path. Preserve
// I-03 stable routes; the caller's exact URL is persisted, never reconstructed
// from current mutable state on replay.
func mpPresentationURL(s string) bool {
	if !mpPrepTexts(s) {
		return false
	}
	u, e := url.Parse(s)
	if e != nil || u.User != nil || u.Fragment != "" {
		return false
	}
	if !strings.HasPrefix(u.Path, "/v1/media/") {
		return false
	}
	tail := strings.TrimPrefix(u.Path, "/v1/media/")
	parts := strings.Split(tail, "/")
	if len(parts) == 0 || parts[0] == "" {
		return false
	}
	for _, part := range parts {
		if part == "." || part == ".." {
			return false
		}
	}
	for _, r := range parts[0] {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return false
		}
	}

	if u.IsAbs() {
		return (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && strings.HasPrefix(u.Path, "/")
	}
	return u.Host == "" && strings.HasPrefix(u.Path, "/") && !strings.HasPrefix(s, "//")
}

type mpSessionCreateInput struct {
	itemPublic                                                                                                                 [16]byte
	mediaGeneration                                                                                                            int64
	session, media, execution, request, startKey, startDigest, engine, mode, presentationURL, grantHash, grantToken, expiresAt string
	sessionIncarnation, mediaIncarnation, birthID                                                                              [16]byte
	item, part, nowMS, leaseMS, positionMS, durationMS                                                                         int64
	paused                                                                                                                     bool
	actor                                                                                                                      mpPrepActor
	source                                                                                                                     mpPrepSource
	queue                                                                                                                      mpPrepQueue
}

func (q mpSessionCreateInput) valid() bool {
	digest := sha256.Sum256([]byte(q.grantToken))
	if q.grantHash != mpLowerHex(digest[:]) {
		return false
	}
	expiry, e := time.Parse(time.RFC3339Nano, q.expiresAt)
	if e != nil || q.leaseMS > expiry.UnixMilli() || expiry.UnixMilli() > q.actor.horizonMS {
		return false
	}
	u, _ := url.Parse(q.presentationURL)
	if u == nil || strings.Split(strings.TrimPrefix(u.Path, "/v1/media/"), "/")[0] != q.grantToken {
		return false
	}
	return mpPrepTexts(q.session, q.media, q.execution, q.request, q.startKey, q.grantToken, q.expiresAt) && lowerHex(q.startDigest, 64) && lowerHex(q.grantHash, 64) && mpPresentationURL(q.presentationURL) && (q.engine == "native" || q.engine == "mpv") && (q.mode == "direct" || q.mode == "remote" || q.mode == "hls") && q.sessionIncarnation != ([16]byte{}) && q.mediaIncarnation != ([16]byte{}) && q.birthID != ([16]byte{}) && q.item > 0 && mpPrepRevision(q.mediaGeneration) && q.part >= 0 && q.nowMS > 0 && q.leaseMS > q.nowMS && q.leaseMS <= q.actor.horizonMS && q.positionMS >= 0 && q.durationMS > 0 && q.positionMS <= q.durationMS && q.actor.valid(q.nowMS) && q.source.valid() && q.queue.valid()
}
func applyMPSessionCreate(ctx context.Context, t *commandTurn, q mpSessionCreateInput) (out mpSessionEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPSessionCreate); err != nil {
		return
	}
	return composeMPSessionCreate(ctx, t, q, false)
}

// 703 owns this composition: no invented public710 birth, no command-ID swap.
// Parent first applies preparation acceptance and its original receipt result in
// this SAME private turn; this point rechecks the exact accepted stable IDs.
func composeMPSessionFromPreparation(ctx context.Context, t *commandTurn, q mpSessionCreateInput, preparation string, revision int64) (out mpSessionEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPStartPrepAccept); err != nil {
		return
	}
	if !mpPrepTexts(preparation) || !mpPrepRevision(revision) {
		return out, ErrInvalid
	}
	if err = mpPrepOne(ctx, t, "mp.session.accepted_preparation", preparation, revision, q.session, q.media, q.source.asset, q.actor.device, q.birthID[:], q.execution, q.mediaGeneration); err != nil {
		return
	}
	out, err = composeMPSessionCreate(ctx, t, q, true)
	if err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.session.preparation_transfer", q.media, preparation, q.execution); err != nil {
		return
	}
	return
}
func composeMPSessionCreate(ctx context.Context, t *commandTurn, q mpSessionCreateInput, fromStart bool) (out mpSessionEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	purpose := MPSessionCreate
	if fromStart {
		purpose = MPStartPrepAccept
	}
	if err = mpPrepTurn(t, purpose); err != nil {
		return
	}
	if !q.valid() {
		return out, ErrInvalid
	}
	if err = checkMPPrepActor(ctx, t, q.actor); err != nil {
		return
	}
	if err = checkMPPrepSource(ctx, t, q.source); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.session.item_identity", q.item, q.itemPublic[:]); err != nil {
		return
	}
	if err = checkMPPrepItem(ctx, t, q.item, q.part, q.source); err != nil {
		return
	}
	if err = checkMPPrepQueue(ctx, t, q.queue, q.actor); err != nil {
		return
	}
	var birth, startBirth any
	if fromStart {
		startBirth = q.birthID[:]
	} else {
		birth = q.birthID[:]
	}
	if err = insertMPSessionPhysical(ctx, t, q, birth, startBirth); err != nil {
		return
	}
	if err = insertMPSessionLogical(ctx, t, q, birth, startBirth); err != nil {
		return
	}
	return mpInstalledSessionProjection(q), nil
}

func applyMPSessionReady(ctx context.Context, t *commandTurn, f mpSessionFence) (out mpSessionEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPSessionReady); err != nil {
		return
	}
	if out, err = captureMPSession(ctx, t, f, true); err != nil {
		return
	}
	if out.state != "preparing" {
		return out, ErrConflict
	}
	// The native delivery readiness fact is published by733/737 after real IO.
	row, e := mpPrepRow(ctx, t, "mp.session.ready", f.physical.nowMS, f.physical.nowMS, f.session, f.revision, f.physical.media, f.physical.generation, f.physical.execution)
	if e != nil {
		return out, e
	}
	if err = row.Scan(&out.state); err != nil {
		return
	}
	out.revision++
	out.changed = true
	return
}

type mpTransportAction uint8

const (
	mpPause  mpTransportAction = 1
	mpResume mpTransportAction = 2
	mpSeek   mpTransportAction = 3
)

type mpTransportInput struct {
	action               mpTransportAction
	sequence, positionMS int64
}

func (q mpTransportInput) valid() bool {
	return q.action >= mpPause && q.action <= mpSeek && q.sequence > 0 && q.sequence < math.MaxInt64 && q.positionMS >= 0
}
func applyMPSessionTransport(ctx context.Context, t *commandTurn, f mpSessionFence, q mpTransportInput) (out mpSessionEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPSessionTransport); err != nil {
		return
	}
	return applyMPTransportEffect(ctx, t, f, q, true)
}
func applyMPSessionAdminTransport(ctx context.Context, t *commandTurn, operator mpPrepActor, f mpSessionFence, q mpTransportInput) (out mpSessionEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPSessionAdminTransport); err != nil {
		return
	}
	// Parent must establish CURRENT admin authority for this REAL operator. Do
	// not turn target owner fields into an administrator's fabricated principal.
	if !operator.valid(f.physical.nowMS) {
		return out, ErrInvalid
	}
	if err = checkMPPrepActor(ctx, t, operator); err != nil {
		return
	}
	return applyMPTransportEffect(ctx, t, f, q, false)
}
func applyMPTransportEffect(ctx context.Context, t *commandTurn, f mpSessionFence, q mpTransportInput, owner bool) (out mpSessionEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	kind := MPSessionTransport
	if !owner {
		kind = MPSessionAdminTransport
	}
	if err = mpPrepTurn(t, kind); err != nil {
		return
	}
	if !q.valid() {
		return out, ErrInvalid
	}
	if out, err = captureMPSession(ctx, t, f, owner); err != nil {
		return
	}
	if out.endedMS != 0 || out.leaseMS <= f.physical.nowMS || (out.state != "playing" && out.state != "paused") || q.sequence <= out.transportSequence {
		return out, ErrConflict
	}
	// Admin still revalidates selected source/queue; it does not adopt another
	// source or bypass occurrence fences. Seek is a logical demand; an encoder
	// change must use714 and the old physical-exit boundary.
	if !owner {
		if err = checkMPPrepSource(ctx, t, f.physical.source); err != nil {
			return
		}
		if err = checkMPPrepItem(ctx, t, f.physical.item, f.physical.part, f.physical.source); err != nil {
			return
		}
		if err = checkMPPrepQueue(ctx, t, f.physical.queue, f.physical.actor); err != nil {
			return
		}
	}
	id := stmt.ID("mp.session.pause")
	args := []any{q.sequence, f.physical.nowMS, f.physical.nowMS, f.session, f.revision, f.generation, out.transportSequence}
	switch q.action {
	case mpResume:
		id = "mp.session.resume"
		args = []any{q.sequence, f.physical.nowMS, f.session, f.revision, f.generation, out.transportSequence}
	case mpSeek:
		id = "mp.session.seek"
		args = []any{q.positionMS, q.sequence, f.physical.nowMS, f.session, f.revision, f.generation, out.transportSequence, q.positionMS, f.physical.media}
	}
	if err = mpPrepOne(ctx, t, id, args...); err != nil {
		return
	}
	out.revision++
	out.transportSequence = q.sequence
	out.changed = true
	switch q.action {
	case mpPause:
		out.state = "paused"
	case mpResume:
		out.state = "playing"
	case mpSeek:
		out.positionMS = q.positionMS
	}
	return
}
func applyMPSessionRenew(ctx context.Context, t *commandTurn, f mpSessionFence, untilMS int64) (out mpSessionEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPSessionRenew); err != nil {
		return
	}
	if out, err = captureMPSession(ctx, t, f, true); err != nil {
		return
	}
	if untilMS <= out.leaseMS || untilMS > f.physical.actor.horizonMS || untilMS > out.grantExpiresMS {
		return out, ErrInvalid
	}
	if err = mpPrepOne(ctx, t, "mp.session.renew", untilMS, f.physical.nowMS, f.session, f.revision, f.generation, out.leaseMS); err != nil {
		return
	}
	out.leaseMS = untilMS
	out.revision++
	out.changed = true
	return
}

func applyMPSessionStop(ctx context.Context, t *commandTurn, f mpSessionFence) (out mpSessionEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPSessionStop); err != nil {
		return
	}
	if err = checkMPPrepActor(ctx, t, f.physical.actor); err != nil {
		return
	}
	return stopMPSessionEffect(ctx, t, f, MPSessionStop, "stopped")
}
func applyMPSessionExpire(ctx context.Context, t *commandTurn, f mpSessionFence) (out mpSessionEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPSessionExpire); err != nil {
		return
	}
	return stopMPSessionEffect(ctx, t, f, MPSessionExpire, "expired")
}
func applyMPSessionAccountStop(ctx context.Context, t *commandTurn, operator mpPrepActor, f mpSessionFence) (out mpSessionEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPSessionAccountStop); err != nil {
		return
	}
	if operator.authority != f.physical.actor.authority || operator.account != f.physical.actor.account || !operator.valid(f.physical.nowMS) {
		return out, ErrDisclosureDenied
	}
	if err = checkMPPrepActor(ctx, t, operator); err != nil {
		return
	}
	return stopMPSessionEffect(ctx, t, f, MPSessionAccountStop, "account_stop")
}
func stopMPSessionEffect(ctx context.Context, t *commandTurn, f mpSessionFence, kind CommandID, reason string) (out mpSessionEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	switch kind {
	case MPSessionStop, MPSessionExpire, MPSessionAccountStop, mpCoreQueueAdvancePurpose, mpCoreNextCommitPurpose:
	default:
		return out, ErrInvalid
	}
	if err = mpPrepTurn(t, kind); err != nil {
		return
	}
	if out, err = captureMPSession(ctx, t, f, false); err != nil {
		return
	}
	if out.endedMS != 0 || (kind == MPSessionExpire && out.leaseMS > f.physical.nowMS) {
		return out, ErrConflict
	}
	if out.stopExecution == "" {
		if !mpPrepTexts(f.physical.retireRequest) {
			return out, ErrInvalid
		}
		if err = mpPrepOne(ctx, t, "mp.session.request_stop", f.physical.retireRequest, f.physical.media, f.physical.revision, f.physical.generation, f.physical.execution); err != nil {
			return
		}
		out.physicalRevision++
		out.physicalExit.retiredGeneration = f.physical.generation
		out.physicalExit.retirementRequest = f.physical.retireRequest
	}
	if err = mpPrepOne(ctx, t, "mp.session.stop", f.physical.nowMS, reason, f.physical.nowMS, f.session, f.revision, f.generation, f.physical.media, f.physical.generation); err != nil {
		return
	}
	out.state = "stopped"
	out.endedMS = f.physical.nowMS
	out.endReason = reason
	out.revision++
	out.stopExecution = f.physical.execution
	out.stopGeneration = f.physical.generation
	out.retiredMedia = mpSessionRetireProjection{media: out.media, execution: out.stopExecution, sourceRequest: f.physical.request, incarnation: out.mediaIncarnation, generation: out.mediaGeneration, exit: out.physicalExit, physicalPending: out.physicalPending}
	out.changed = true
	return // No pin, reservation, helper permit or bandwidth release on request.
}

// Exact external observation DATA. Parent must verify supervisor join and the
// bounded cleanup slice under its closed current cleanup-driver capability.
type mpOccurrenceExit struct {
	retirementRequest              string
	supervisorIncarnation          [16]byte
	exitedMS                       int64
	execution, digest              string
	generation, cursor, nextCursor int64
	finished                       bool
}

func (e mpOccurrenceExit) valid(f mpPhysicalFence) bool {
	return mpPrepTexts(e.retirementRequest) && e.supervisorIncarnation != ([16]byte{}) && e.exitedMS > 0 && e.exitedMS <= f.nowMS && e.execution == f.execution && e.generation == f.generation && lowerHex(e.digest, 64) && e.cursor >= 0 && e.nextCursor >= e.cursor && e.nextCursor-e.cursor <= 64
}
func applyMPSessionReconcile(ctx context.Context, t *commandTurn, f mpSessionFence, e mpOccurrenceExit) (out mpSessionEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPSessionReconcile); err != nil {
		return
	}
	if !e.valid(f.physical) {
		return out, ErrInvalid
	}
	if out, err = captureMPSession(ctx, t, f, false); err != nil {
		return
	}
	if out.stopExecution == "" || out.cleanupCursor != e.cursor || e.retirementRequest != out.physicalExit.retirementRequest {
		return out, ErrConflict
	}
	done := int64(0)
	if e.finished {
		if err = mpPrepOne(ctx, t, "mp.session.cleanup_empty", f.physical.media); err != nil {
			return
		}
		done = 1
	}
	if err = mpPrepOne(ctx, t, "mp.session.exit", e.supervisorIncarnation[:], e.exitedMS, e.retirementRequest, e.digest, e.nextCursor, done, f.physical.media, f.physical.revision, f.physical.generation, f.physical.execution, e.cursor, e.retirementRequest, e.supervisorIncarnation[:], e.exitedMS, e.retirementRequest, e.digest); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.session.reconcile_revision", f.physical.nowMS, f.session, f.revision, f.generation); err != nil {
		return
	}
	out.revision++
	out.physicalRevision++
	out.physicalExit.exitedGeneration = f.physical.generation
	out.physicalExit.supervisorIncarnation = e.supervisorIncarnation
	out.physicalExit.exitedMS = e.exitedMS
	out.physicalExit.exitRequest = e.retirementRequest
	out.cleanupCursor = e.nextCursor
	out.physicalPending = !e.finished
	out.retiredMedia.exit = out.physicalExit
	out.retiredMedia.physicalPending = out.physicalPending
	out.changed = true
	return
}

// Explicit typed receipt projection. Parent maps this result into the native
// per-session header/result and <=one retire-media child, preserving the saved
// ORIGINAL scalar result, not a mutable-row reconstruction. This codec neither
// stores a receipt nor grants Applied or commits anything.
func mpSessionDomainResultDigest(h mpPrepReceiptHeader, r mpSessionEffect) (ResultDigest, error) {
	if (h.kind != MPStartPrepAccept && h.kind != mpCoreQueueAdvancePurpose && h.kind != mpCoreNextCommitPurpose && (h.kind < MPSessionCreate || h.kind > MPSessionReconcile)) || !mpPrepTexts(r.session, r.media, r.state, r.mode, r.engine) || !mpPresentationURL(r.presentationURL) || r.sessionIncarnation == ([16]byte{}) || r.mediaIncarnation == ([16]byte{}) || !mpPrepRevision(r.revision) || r.generation <= 0 || r.mediaGeneration <= 0 || r.physicalRevision <= 0 || r.presentationGeneration <= 0 || r.positionMS < 0 || r.startMS < 0 || r.endedMS < 0 || r.cleanupCursor < 0 {
		return ResultDigest{}, ErrInvalid
	}
	e := &requestEncoder{h: sha256.New()}
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("mp_session_domain")
	e.number(uint64(h.kind))
	e.bytes(h.identity.Incarnation[:])
	e.number(uint64(h.identity.Sequence))
	e.bytes(h.identity.OperationID[:])
	e.bytes(h.request[:])
	e.number(uint64(h.appliedMS))
	if !mpPrepHeaderValid(h) {
		return ResultDigest{}, ErrInvalid
	}
	for _, s := range []string{r.session, r.media, r.state, r.mode, r.engine, r.presentationURL, r.endReason, r.stopExecution, r.asset, r.queue, r.entry} {
		e.text(s)
	}
	if !r.physicalExit.valid(r.mediaGeneration, r.physicalPending) {
		return ResultDigest{}, ErrInvalid
	}
	writeMPPhysicalExit(e, r.physicalExit)
	if err := writeMPSessionRetire(e, r.retiredMedia); err != nil {
		return ResultDigest{}, err
	}
	e.bytes(r.itemPublic[:])
	e.bytes(r.sessionIncarnation[:])
	e.bytes(r.mediaIncarnation[:])
	for _, v := range []int64{r.revision, r.generation, r.presentationGeneration, r.mediaGeneration, r.physicalRevision, r.leaseMS, r.grantExpiresMS, r.positionMS, r.startMS, r.transportSequence, r.endedMS, r.stopGeneration, r.cleanupCursor, r.item, r.part} {
		e.number(uint64(v))
	}
	for _, v := range []bool{r.physicalPending, r.changed} {
		if v {
			e.number(1)
		} else {
			e.number(0)
		}
	}
	if e.err != nil {
		return ResultDigest{}, e.err
	}
	var d ResultDigest
	copy(d[:], e.h.Sum(nil))
	return d, nil
}

// This helper composes ONLY media effects under the caller's exact703/710/714
// purpose. It is not an execution entry point for another command ID.
func insertMPSessionPhysical(ctx context.Context, t *commandTurn, q mpSessionCreateInput, birth, startBirth any) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if t == nil || t.transaction == nil {
		return ErrInvalid
	}
	if !q.valid() {
		return ErrInvalid
	}
	switch t.transaction.descriptor.ID {
	case MPStartPrepAccept, MPSessionCreate, MPSessionPresentation, mpCoreQueueAdvancePurpose, mpCoreNextCommitPurpose:
	default:
		return ErrInvalid
	}

	if err = mpPrepOne(ctx, t, "mp.session.physical_insert", q.media, q.actor.sessionHash, q.actor.account, q.actor.profile, q.item, q.source.asset, q.mediaGeneration, q.grantHash, q.grantToken, q.expiresAt, q.request, q.durationMS, q.mode, q.mediaIncarnation[:], q.execution, birth, startBirth); err != nil {
		return err
	}
	args := append([]any{q.media}, q.actor.args()...)
	args = append(args, q.source.args()...)
	args = append(args, q.queue.args()...)
	args = append(args, q.part, q.itemPublic[:])
	if err = mpPrepOne(ctx, t, "mp.session.input_insert", args...); err != nil {
		return err
	}
	// Initial source pins are mandatory SAME-turn effects, not future best effort.
	if err = mpPrepOne(ctx, t, "mp.session.source_insert", q.media, q.source.asset, q.source.size, q.source.modifiedNS, q.source.source, q.source.object, q.source.root, q.source.incarnation); err != nil {
		return err
	}
	if err = mpPrepOne(ctx, t, "mp.session.physical_source_insert", q.media, q.source.source, q.source.incarnation, q.source.configurationGeneration); err != nil {
		return err
	}
	return nil
}

type mpSessionReplacementInput struct{ next mpSessionCreateInput }

func mpSessionReplacementDigest(q mpSessionReplacementInput) (RequestDigest, error) {
	if !q.next.valid() {
		return RequestDigest{}, ErrInvalid
	}
	e := &requestEncoder{h: sha256.New(), path: make(map[uintptr]bool)}
	e.text("mp_session_replacement_v1")
	e.value(reflect.ValueOf(q), 0)
	if e.err != nil {
		return RequestDigest{}, e.err
	}
	var d RequestDigest
	copy(d[:], e.h.Sum(nil))
	return d, nil
}
func checkMPReplacementInput(ctx context.Context, t *commandTurn, f mpSessionFence, q mpSessionReplacementInput) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	n, a := q.next, f.physical.actor
	if !n.valid() || n.session != f.session || n.media == f.physical.media || n.execution == f.physical.execution || n.item != f.physical.item || n.actor.server != a.server || n.actor.authority != a.authority || n.actor.account != a.account || n.actor.profile != a.profile || n.actor.device != a.device || n.actor.family != a.family {
		return ErrInvalid
	}
	if err = checkMPPrepActor(ctx, t, n.actor); err != nil {
		return
	}
	if err = checkMPPrepSource(ctx, t, n.source); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.session.item_identity", n.item, n.itemPublic[:]); err != nil {
		return
	}
	if err = checkMPPrepItem(ctx, t, n.item, n.part, n.source); err != nil {
		return
	}
	return checkMPPrepQueue(ctx, t, n.queue, n.actor)
}
func applyMPSessionPresentationBegin(ctx context.Context, t *commandTurn, f mpSessionFence, q mpSessionReplacementInput) (out mpSessionEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPSessionPresentation); err != nil {
		return
	}
	if err = checkMPReplacementInput(ctx, t, f, q); err != nil {
		return
	}
	if out, err = captureMPSession(ctx, t, f, false); err != nil {
		return
	}
	if !mpPrepTexts(f.physical.retireRequest) || out.endedMS != 0 || out.leaseMS <= f.physical.nowMS || out.stopExecution != "" {
		return out, ErrConflict
	}
	digest, e := mpSessionReplacementDigest(q)
	if e != nil {
		return out, e
	}
	if err = mpPrepOne(ctx, t, "mp.session.replace_begin", f.session, f.physical.media, f.physical.generation, f.generation, f.physical.execution, q.next.media, q.next.execution, q.next.mediaIncarnation[:], digest[:]); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.session.request_stop", f.physical.retireRequest, f.physical.media, f.physical.revision, f.physical.generation, f.physical.execution); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.session.replace_preparing", f.physical.nowMS, f.session, f.revision, f.generation); err != nil {
		return
	}
	out.revision++
	out.physicalRevision++
	out.state = "preparing"
	out.physicalExit.retiredGeneration = f.physical.generation
	out.physicalExit.retirementRequest = f.physical.retireRequest
	out.stopExecution = f.physical.execution
	out.stopGeneration = f.physical.generation
	out.physicalPending = true
	out.retiredMedia = mpSessionRetireProjection{media: out.media, execution: out.stopExecution, sourceRequest: f.physical.request, incarnation: out.mediaIncarnation, generation: out.mediaGeneration, exit: out.physicalExit, physicalPending: true}
	out.changed = true
	return
}
func applyMPSessionPresentationFinish(ctx context.Context, t *commandTurn, f mpSessionFence, q mpSessionReplacementInput) (out mpSessionEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPSessionPresentation); err != nil {
		return
	}
	if err = checkMPReplacementInput(ctx, t, f, q); err != nil {
		return
	}
	if out, err = captureMPSession(ctx, t, f, false); err != nil {
		return
	}
	p, e := captureMPPhysical(ctx, t, f.physical, false)
	if e != nil {
		return out, e
	}
	if out.endedMS != 0 || out.state != "preparing" || !p.exited || !p.cleaned || p.stopMS == 0 {
		return out, ErrConflict
	}
	digest, e := mpSessionReplacementDigest(q)
	if e != nil {
		return out, e
	}
	if err = mpPrepOne(ctx, t, "mp.session.replace_current", f.session, f.physical.media, f.physical.generation, f.generation, f.physical.execution, q.next.media, q.next.execution, q.next.mediaIncarnation[:], digest[:]); err != nil {
		return
	}
	row, e := mpPrepRow(ctx, t, "mp.session.birth_binding", f.session)
	if e != nil {
		return out, e
	}
	var birth, startBirth []byte
	if err = row.Scan(&birth, &startBirth); err != nil {
		return
	}
	if (len(birth) != 0 && len(birth) != 16) || (len(startBirth) != 0 && len(startBirth) != 16) || (len(birth) == 16) == (len(startBirth) == 16) {
		return out, ErrResolveRequired
	}
	var b, sb any
	if len(birth) == 16 {
		if string(birth) != string(q.next.birthID[:]) {
			return out, ErrConflict
		}
		b = birth
	} else {
		if string(startBirth) != string(q.next.birthID[:]) {
			return out, ErrConflict
		}
		sb = startBirth
	}
	// No new native resource row until exact old physical exit AND cleanup are
	// durable. Parent now transfers the independently verified new execution.
	if err = insertMPSessionPhysical(ctx, t, q.next, b, sb); err != nil {
		return
	}
	desired := "playing"
	if q.next.paused {
		desired = "paused"
	}
	if err = mpPrepOne(ctx, t, "mp.session.replace_finish", q.next.media, q.next.mediaGeneration, q.next.item, q.next.source.asset, q.next.part, q.next.queue.id, q.next.queue.entry, q.next.positionMS, q.next.positionMS, q.next.leaseMS, q.next.engine, q.next.presentationURL, desired, q.next.nowMS, f.session, f.revision, f.generation, f.physical.media, f.physical.generation); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.session.replace_installed", f.session, f.physical.media, f.generation, digest[:]); err != nil {
		return
	}
	out.revision++
	out.generation++
	out.presentationGeneration++
	out.retiredMedia = mpRetireProjection(p)
	out.physicalExit = mpPhysicalExitFacts{}
	out.media = q.next.media
	out.mediaIncarnation = q.next.mediaIncarnation
	out.mediaGeneration = q.next.mediaGeneration
	out.physicalRevision = 1
	out.state = "preparing"
	out.mode = q.next.mode
	out.engine = q.next.engine
	out.presentationURL = q.next.presentationURL
	out.itemPublic = q.next.itemPublic
	out.item = q.next.item
	out.part = q.next.part
	out.asset = q.next.source.asset
	out.queue = q.next.queue.id
	out.entry = q.next.queue.entry
	out.positionMS = q.next.positionMS
	out.startMS = q.next.positionMS
	out.leaseMS = q.next.leaseMS
	expiry, _ := time.Parse(time.RFC3339Nano, q.next.expiresAt)
	out.grantExpiresMS = expiry.UnixMilli()
	out.stopExecution = f.physical.execution
	out.stopGeneration = f.physical.generation
	out.physicalPending = true
	out.cleanupCursor = 0
	out.changed = true
	return
}

func insertMPSessionLogical(ctx context.Context, t *commandTurn, q mpSessionCreateInput, birth, startBirth any) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if !q.valid() || t == nil || t.transaction == nil {
		return ErrInvalid
	}
	switch t.transaction.descriptor.ID {
	case MPStartPrepAccept, MPSessionCreate, mpCoreQueueAdvancePurpose, mpCoreNextCommitPurpose:
	default:
		return ErrInvalid
	}

	desired := "playing"
	if q.paused {
		desired = "paused"
	}
	if err = mpPrepOne(ctx, t, "mp.session.logical_insert", q.session, q.actor.device, q.actor.account, q.actor.profile, q.actor.authority, q.startKey, q.startDigest, q.item, q.source.asset, q.part, q.queue.id, q.queue.entry, q.media, q.mediaGeneration, q.positionMS, q.positionMS, q.leaseMS, q.nowMS, q.nowMS, q.sessionIncarnation[:], q.engine, q.presentationURL, desired, birth, startBirth); err != nil {
		return err
	}
	return nil
}

func mpInstalledSessionProjection(q mpSessionCreateInput) mpSessionEffect {
	return mpSessionEffect{itemPublic: q.itemPublic, item: q.item, part: q.part, asset: q.source.asset, queue: q.queue.id, entry: q.queue.entry, session: q.session, media: q.media, sessionIncarnation: q.sessionIncarnation, mediaIncarnation: q.mediaIncarnation, revision: 1, generation: 1, presentationGeneration: 1, mediaGeneration: q.mediaGeneration, physicalRevision: 1, state: "preparing", mode: q.mode, engine: q.engine, presentationURL: q.presentationURL, leaseMS: q.leaseMS, grantExpiresMS: func() int64 { v, _ := time.Parse(time.RFC3339Nano, q.expiresAt); return v.UnixMilli() }(), positionMS: q.positionMS, startMS: q.positionMS, physicalPending: true, changed: true}
}
