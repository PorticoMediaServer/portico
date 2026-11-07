package writer

import (
	"context"
	"crypto/sha256"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	MPSourceInventoryPin     CommandID = 720
	MPSourcePreparedPin      CommandID = 721
	MPSourceSubtitlePin      CommandID = 722
	MPSourceDiscPin          CommandID = 723
	MPSourceGrantRotate      CommandID = 724
	MPSourceReceiverPin      CommandID = 725
	MPSourceInventoryRelease CommandID = 726
	MPSourceResourceRelease  CommandID = 727
	MPSourceGrantRetire      CommandID = 728
	MPSourceRemoteBind       CommandID = 729
)

type mpPinKind uint8

const (
	mpInventoryPin mpPinKind = 1
	mpPreparedPin  mpPinKind = 2
	mpSubtitlePin  mpPinKind = 3
	mpDiscPin      mpPinKind = 4
	mpReceiverPin  mpPinKind = 5
	mpRemotePin    mpPinKind = 6
	mpGrantPin     mpPinKind = 7
)

type mpSourcePinEffect struct {
	physicalExit                                                                                                                           mpPhysicalExitFacts
	state                                                                                                                                  string
	media, execution, asset, resource, version, source, incarnation, digest, grantHash, grantToken, expiresAt, ownerSession, originVersion string
	mediaIncarnation                                                                                                                       [16]byte
	revision, generation, resourceRevision, size, configurationGeneration, expiresMS                                                       int64
	kind                                                                                                                                   mpPinKind
	acquired, released, physicalPending                                                                                                    bool
}

func mpPinResult(p mpPhysicalState, k mpPinKind) mpSourcePinEffect {
	return mpSourcePinEffect{physicalExit: p.exit, state: p.state, media: p.media, mediaIncarnation: p.incarnation, execution: p.execution, asset: p.asset, revision: p.revision, generation: p.generation, kind: k, physicalPending: !p.cleaned}
}
func bumpMPPin(ctx context.Context, t *commandTurn, f mpPhysicalFence) error {
	return mpPrepOne(ctx, t, "mp.pins.bump", f.media, f.revision, f.generation, f.execution)
}
func applyMPSourceInventoryPin(ctx context.Context, t *commandTurn, f mpPhysicalFence) (out mpSourcePinEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPSourceInventoryPin); err != nil {
		return
	}
	p, err := captureMPPhysical(ctx, t, f, true)
	if err != nil {
		return out, err
	}
	out = mpPinResult(p, mpInventoryPin)
	s := f.source
	if err = mpPrepOne(ctx, t, "mp.session.source_insert", f.media, s.asset, s.size, s.modifiedNS, s.source, s.object, s.root, s.incarnation); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.session.physical_source_insert", f.media, s.source, s.incarnation, s.configurationGeneration); err != nil {
		return
	}
	if err = bumpMPPin(ctx, t, f); err != nil {
		return
	}
	out.revision++
	out.source = s.source
	out.incarnation = s.incarnation
	out.size = s.size
	out.configurationGeneration = s.configurationGeneration
	out.acquired = true
	return
}

type mpPreparedPinInput struct {
	version, digest, sourceRevision, profile, target string
	size                                             int64
}

func applyMPSourcePreparedPin(ctx context.Context, t *commandTurn, f mpPhysicalFence, q mpPreparedPinInput) (out mpSourcePinEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return applyMPSourcePreparedPinEffects(ctx, t, f, q, mpRecipeStandalone)
}
func applyMPSourcePreparedPinEffects(ctx context.Context, t *commandTurn, f mpPhysicalFence, q mpPreparedPinInput, recipe mpMediaRecipeKind) (out mpSourcePinEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpMediaRecipePurpose(t, MPSourcePreparedPin, recipe); err != nil {
		return
	}
	if !mpPrepTexts(q.version, q.profile, q.target) || !lowerHex(q.digest, 64) || !lowerHex(q.sourceRevision, 64) || q.size <= 0 {
		return out, ErrInvalid
	}
	p, err := captureMPPhysical(ctx, t, f, true)
	if err != nil {
		return out, err
	}
	out = mpPinResult(p, mpPreparedPin)
	if err = mpPrepOne(ctx, t, "mp.pins.prepared_source_current", append([]any{q.version}, f.source.args()...)...); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.pins.prepared_insert", f.media, q.version, q.digest, q.size, f.item, f.source.asset, f.source.library, q.sourceRevision, q.profile, q.target); err != nil {
		return
	}
	if err = bumpMPPin(ctx, t, f); err != nil {
		return
	}
	out.revision++
	out.version = q.version
	out.digest = q.digest
	out.size = q.size
	out.acquired = true
	return
}

type mpSubtitlePinInput struct {
	resource, digest, viewerKey string
	revision, size              int64
}

func applyMPSourceSubtitlePin(ctx context.Context, t *commandTurn, f mpPhysicalFence, q mpSubtitlePinInput) (out mpSourcePinEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return applyMPSourceSubtitlePinEffects(ctx, t, f, q, mpRecipeStandalone)
}
func applyMPSourceSubtitlePinEffects(ctx context.Context, t *commandTurn, f mpPhysicalFence, q mpSubtitlePinInput, recipe mpMediaRecipeKind) (out mpSourcePinEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpMediaRecipePurpose(t, MPSourceSubtitlePin, recipe); err != nil {
		return
	}
	viewerDigest := sha256.Sum256([]byte(f.actor.authority + "\x00" + f.actor.account + "\x00" + f.actor.profile + "\x00" + f.actor.server))
	if q.viewerKey != mpLowerHex(viewerDigest[:]) || !mpPrepTexts(q.resource, q.viewerKey) || !lowerHex(q.digest, 64) || !mpPrepRevision(q.revision) || q.size < 0 {
		return out, ErrInvalid
	}
	p, err := captureMPPhysical(ctx, t, f, true)
	if err != nil {
		return out, err
	}
	out = mpPinResult(p, mpSubtitlePin)
	if err = mpPrepOne(ctx, t, "mp.pins.subtitle_insert", f.media, q.resource, q.revision, q.digest, q.size, f.item, f.source.asset, q.viewerKey, f.source.size, f.source.modifiedNS); err != nil {
		return
	}
	if err = bumpMPPin(ctx, t, f); err != nil {
		return
	}
	out.revision++
	out.resource = q.resource
	out.resourceRevision = q.revision
	out.digest = q.digest
	out.size = q.size
	out.acquired = true
	return
}

type mpDiscPinInput struct {
	object, title, sourceRevision, fingerprint, titleFacts, timeline string
	streamRevision, chapterRevision                                  int64
}

func applyMPSourceDiscPin(ctx context.Context, t *commandTurn, f mpPhysicalFence, q mpDiscPinInput) (out mpSourcePinEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return applyMPSourceDiscPinEffects(ctx, t, f, q, mpRecipeStandalone)
}
func applyMPSourceDiscPinEffects(ctx context.Context, t *commandTurn, f mpPhysicalFence, q mpDiscPinInput, recipe mpMediaRecipeKind) (out mpSourcePinEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpMediaRecipePurpose(t, MPSourceDiscPin, recipe); err != nil {
		return
	}
	if !mpPrepTexts(q.object, q.title, q.sourceRevision, q.fingerprint, q.titleFacts, q.timeline) || q.streamRevision <= 0 || q.chapterRevision <= 0 || q.object != f.source.object || q.sourceRevision != f.source.revision {
		return out, ErrInvalid
	}
	p, err := captureMPPhysical(ctx, t, f, true)
	if err != nil {
		return out, err
	}
	out = mpPinResult(p, mpDiscPin)
	if err = mpPrepOne(ctx, t, "mp.pins.disc_insert", f.media, q.title, q.streamRevision, q.chapterRevision, q.titleFacts, q.timeline, q.object, q.sourceRevision, q.fingerprint, f.source.asset, q.title, q.titleFacts); err != nil {
		return
	}
	if err = bumpMPPin(ctx, t, f); err != nil {
		return
	}
	out.revision++
	out.resource = q.title
	out.resourceRevision = q.streamRevision
	out.originVersion = q.titleFacts
	out.digest = q.fingerprint
	out.acquired = true
	return
}

type mpGrantRotation struct{ oldHash, newHash, newToken, expiresAt string }

func applyMPSourceGrantRotate(ctx context.Context, t *commandTurn, f mpPhysicalFence, q mpGrantRotation) (out mpSourcePinEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPSourceGrantRotate); err != nil {
		return
	}
	expiry, e := time.Parse(time.RFC3339Nano, q.expiresAt)
	if e != nil || !lowerHex(q.oldHash, 64) || !lowerHex(q.newHash, 64) || !mpPrepTexts(q.newToken) || q.oldHash == q.newHash || expiry.UnixMilli() <= f.nowMS || expiry.UnixMilli() > f.actor.horizonMS {
		return out, ErrInvalid
	}
	// Token bytes/hash originate from parent's trusted token constructor. Check
	// SHA256 locally without RNG/IO; never save the secret in events or logs.
	expected := sha256.Sum256([]byte(q.newToken))
	if q.newHash != mpLowerHex(expected[:]) {
		return out, ErrInvalid
	}
	p, err := captureMPPhysical(ctx, t, f, true)
	if err != nil {
		return out, err
	}
	out = mpPinResult(p, mpGrantPin)
	if p.grantHash != q.oldHash {
		return out, ErrConflict
	}
	if err = mpPrepOne(ctx, t, "mp.pins.grant_rotate", q.newHash, q.newToken, q.expiresAt, f.media, f.revision, f.generation, f.execution, q.oldHash); err != nil {
		return
	}
	out.revision++
	out.grantHash = q.newHash
	out.grantToken = q.newToken
	out.expiresAt = q.expiresAt
	out.expiresMS = expiry.UnixMilli()
	out.acquired = true
	return
}
func mpLowerHex(b []byte) string {
	const digits = "0123456789abcdef"
	v := make([]byte, len(b)*2)
	for i, c := range b {
		v[2*i] = digits[c>>4]
		v[2*i+1] = digits[c&15]
	}
	return string(v)
}

type mpReceiverPinInput struct {
	ownerSession               string
	ownerGeneration, expiresMS int64
}

func applyMPSourceReceiverPin(ctx context.Context, t *commandTurn, f mpPhysicalFence, q mpReceiverPinInput) (out mpSourcePinEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return applyMPSourceReceiverPinEffects(ctx, t, f, q, mpRecipeStandalone)
}
func applyMPSourceReceiverPinEffects(ctx context.Context, t *commandTurn, f mpPhysicalFence, q mpReceiverPinInput, recipe mpMediaRecipeKind) (out mpSourcePinEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpMediaRecipePurpose(t, MPSourceReceiverPin, recipe); err != nil {
		return
	}
	if !mpPrepTexts(q.ownerSession) || q.ownerGeneration <= 0 || q.expiresMS <= f.nowMS || q.expiresMS > f.actor.horizonMS {
		return out, ErrInvalid
	}
	p, err := captureMPPhysical(ctx, t, f, true)
	if err != nil {
		return out, err
	}
	out = mpPinResult(p, mpReceiverPin)
	if err = mpPrepOne(ctx, t, "mp.pins.receiver_insert", f.media, q.expiresMS, q.ownerSession, q.ownerGeneration, f.actor.account, f.actor.profile, q.expiresMS); err != nil {
		return
	}
	if err = bumpMPPin(ctx, t, f); err != nil {
		return
	}
	out.revision++
	out.ownerSession = q.ownerSession
	out.expiresMS = q.expiresMS
	out.acquired = true
	return
}
func applyMPSourceInventoryRelease(ctx context.Context, t *commandTurn, f mpPhysicalFence) (out mpSourcePinEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPSourceInventoryRelease); err != nil {
		return
	}
	p, err := captureMPPhysical(ctx, t, f, false)
	if err != nil {
		return out, err
	}
	out = mpPinResult(p, mpInventoryPin)
	if !p.exited || p.stopMS == 0 || p.exitDigest == "" {
		return out, ErrConflict
	}
	if err = mpPrepOne(ctx, t, "mp.pins.no_readers", f.media); err != nil {
		return
	}
	// Both exact native pins are removed together ONLY after durable actual exit.
	if err = mpPrepOne(ctx, t, "mp.pins.inventory_delete", f.media, f.source.asset, f.source.size, f.source.modifiedNS, f.source.source, f.source.object); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.pins.physical_delete", f.media, f.source.source, f.source.incarnation, f.source.configurationGeneration); err != nil {
		return
	}
	if err = bumpMPPin(ctx, t, f); err != nil {
		return
	}
	out.revision++
	out.released = true
	out.source = f.source.source
	out.incarnation = f.source.incarnation
	return
}

type mpResourceRelease struct {
	kind                                    mpPinKind
	resource, digest, version, ownerSession string
	revision, size, expiresMS               int64
}

func applyMPSourceResourceRelease(ctx context.Context, t *commandTurn, f mpPhysicalFence, q mpResourceRelease) (out mpSourcePinEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPSourceResourceRelease); err != nil {
		return
	}
	p, err := captureMPPhysical(ctx, t, f, false)
	if err != nil {
		return out, err
	}
	out = mpPinResult(p, q.kind)
	if !p.exited || p.stopMS == 0 || p.exitDigest == "" {
		return out, ErrConflict
	}
	if err = mpPrepOne(ctx, t, "mp.pins.no_readers", f.media); err != nil {
		return
	}
	var id stmt.ID
	var args []any
	switch q.kind {
	case mpPreparedPin:
		if !mpPrepTexts(q.version) || !lowerHex(q.digest, 64) || q.size <= 0 {
			return out, ErrInvalid
		}
		id = "mp.pins.prepared_delete"
		args = []any{f.media, q.version, q.digest, q.size}
	case mpSubtitlePin:
		if !mpPrepTexts(q.resource) || !mpPrepRevision(q.revision) {
			return out, ErrInvalid
		}
		id = "mp.pins.subtitle_delete"
		args = []any{f.media, q.resource, q.revision}
	case mpDiscPin:
		if !mpPrepTexts(q.resource, q.digest) || !mpPrepRevision(q.revision) {
			return out, ErrInvalid
		}
		id = "mp.pins.disc_delete"
		args = []any{f.media, q.resource, q.digest, q.revision}
	case mpReceiverPin:
		if !mpPrepTexts(q.ownerSession) || q.expiresMS <= 0 {
			return out, ErrInvalid
		}
		id = "mp.pins.receiver_delete"
		args = []any{f.media, q.ownerSession, q.expiresMS}
	case mpRemotePin:
		if !mpPrepTexts(q.resource) {
			return out, ErrInvalid
		}
		id = "mp.pins.remote_delete"
		args = []any{f.media, q.resource}
	default:
		return out, ErrInvalid
	}
	if q.kind == mpRemotePin {
		if err = mpPrepOne(ctx, t, "mp.pins.remote_evidence_delete", f.media, q.resource); err != nil {
			return
		}
	}
	if err = mpPrepOne(ctx, t, id, args...); err != nil {
		return
	}
	if err = bumpMPPin(ctx, t, f); err != nil {
		return
	}
	out.revision++
	out.resource = q.resource
	out.resourceRevision = q.revision
	out.digest = q.digest
	out.version = q.version
	out.ownerSession = q.ownerSession
	out.size = q.size
	out.released = true
	return
}
func applyMPSourceGrantRetire(ctx context.Context, t *commandTurn, f mpPhysicalFence, hash string) (out mpSourcePinEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPSourceGrantRetire); err != nil {
		return
	}
	if !mpPrepTexts(f.retireRequest) || !lowerHex(hash, 64) {
		return out, ErrInvalid
	}
	p, err := captureMPPhysical(ctx, t, f, false)
	if err != nil {
		return out, err
	}
	out = mpPinResult(p, mpGrantPin)
	if p.grantHash != hash || p.grantRetired {
		return out, ErrConflict
	}
	if err = mpPrepOne(ctx, t, "mp.pins.grant_retire", f.retireRequest, f.media, f.revision, f.generation, f.execution, hash); err != nil {
		return
	}
	out.revision++
	out.grantHash = hash
	out.state = "stopped"
	out.physicalExit.retiredGeneration = f.generation
	if p.stopMS == 0 {
		out.physicalExit.retirementRequest = f.retireRequest
	}
	out.physicalPending = true
	return // never release pins on revoke
}

// Version-bound remote origin DATA only; actual URL/SSRF/TLS/redirect/extent
// verification happens in the parent's remote adapter before this writer turn.
// No credentials or raw origin URL are exposed in a presentation or event.
type mpRemoteBinding struct {
	originVersion, descriptorVersion, evidence, policyDigest, endpointDigest string
	size                                                                     int64
}

func applyMPSourceRemoteBind(ctx context.Context, t *commandTurn, f mpPhysicalFence, q mpRemoteBinding) (out mpSourcePinEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return applyMPSourceRemoteBindEffects(ctx, t, f, q, mpRecipeStandalone)
}
func applyMPSourceRemoteBindEffects(ctx context.Context, t *commandTurn, f mpPhysicalFence, q mpRemoteBinding, recipe mpMediaRecipeKind) (out mpSourcePinEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpMediaRecipePurpose(t, MPSourceRemoteBind, recipe); err != nil {
		return
	}
	if !mpPrepTexts(q.originVersion, q.descriptorVersion, q.evidence) || !lowerHex(q.policyDigest, 64) || !lowerHex(q.endpointDigest, 64) || q.size <= 0 {
		return out, ErrInvalid
	}
	p, err := captureMPPhysical(ctx, t, f, true)
	if err != nil {
		return out, err
	}
	out = mpPinResult(p, mpRemotePin)
	if p.mode != "remote" && p.mode != "hls" {
		return out, ErrConflict
	}
	if err = mpPrepOne(ctx, t, "mp.pins.remote_insert", f.media, q.originVersion, q.descriptorVersion, q.evidence, q.policyDigest, q.endpointDigest, q.size); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.pins.remote_subtitle_evidence", f.media, q.evidence); err != nil {
		return
	}
	if err = bumpMPPin(ctx, t, f); err != nil {
		return
	}
	out.revision++
	out.originVersion = q.originVersion
	out.size = q.size
	out.acquired = true
	return
}

func mpSourcePinsResultDigest(h mpPrepReceiptHeader, r mpSourcePinEffect) (ResultDigest, error) {
	if h.kind < MPSourceInventoryPin || h.kind > MPSourceRemoteBind || !mpPrepHeaderValid(h) || r.kind < mpInventoryPin || r.kind > mpGrantPin || !mpPrepTexts(r.media, r.execution, r.asset, r.state) || r.mediaIncarnation == ([16]byte{}) || r.revision <= 0 || r.generation <= 0 || r.resourceRevision < 0 || r.size < 0 || r.configurationGeneration < 0 || r.expiresMS < 0 {
		return ResultDigest{}, ErrInvalid
	}
	e := &requestEncoder{h: sha256.New()}
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("mp_source_pins")
	e.number(uint64(h.kind))
	e.bytes(h.identity.Incarnation[:])
	e.number(uint64(h.identity.Sequence))
	e.bytes(h.identity.OperationID[:])
	e.bytes(h.request[:])
	e.number(uint64(h.appliedMS))
	e.bytes(r.mediaIncarnation[:])
	if !r.physicalExit.valid(r.generation, r.physicalPending) {
		return ResultDigest{}, ErrInvalid
	}
	writeMPPhysicalExit(e, r.physicalExit)
	for _, s := range []string{r.media, r.execution, r.asset, r.resource, r.version, r.source, r.incarnation, r.digest, r.grantHash, r.grantToken, r.expiresAt, r.ownerSession, r.originVersion, r.state} {
		if !mpPrepText(s, true) {
			return ResultDigest{}, ErrInvalid
		}
		e.text(s)
	}
	for _, v := range []int64{r.revision, r.generation, r.resourceRevision, r.size, r.configurationGeneration, r.expiresMS, int64(r.kind)} {
		e.number(uint64(v))
	}
	for _, v := range []bool{r.acquired, r.released, r.physicalPending} {
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
