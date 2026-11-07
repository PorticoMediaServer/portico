package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// No callback/SQL handle or writer opening escapes. W must resolve ORIGINAL
// history and current opaque actor/source authority before these private effects
// and save the exact typed receipt/event before commit. Every error, including a
// late source/operation CAS or cleanup failure, requires whole-frame rollback.
func mgGuidePublicationStep(t *commandTurn, id stmt.ID) (statementStep, error) {
	if t == nil || t.transaction == nil {
		return statementStep{}, ErrInvalid
	}
	switch id {
	case mgGuideRead, mgGuideSource, mgGuideIdentity, mgGuideSealRow, mgGuidePublishSource, mgGuidePublishNewSource, mgGuidePublishOperation:
	default:
		return statementStep{}, ErrInvalid
	}
	s, ok := t.steps[id]
	if !ok {
		return statementStep{}, ErrInvalid
	}
	return s.statementStep, nil
}
func mgGuidePublicationQuery(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (commandRow, error) {
	step, err := mgGuidePublicationStep(t, id)
	if err != nil {
		return commandRow{}, err
	}
	return t.queryRow(ctx, step, args...)
}
func mgGuidePublicationChange(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) error {
	step, err := mgGuidePublicationStep(t, id)
	if err != nil {
		return err
	}
	result, err := t.exec(ctx, step, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return t.checkLifetimes(ctx)
}
func mgReadGuideNative(ctx context.Context, t *commandTurn, request string) (out mgGuideNative, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if request == "" {
		return out, ErrInvalid
	}
	row, err := mgGuidePublicationQuery(ctx, t, mgGuideRead, request)
	if err != nil {
		return out, err
	}
	var inc, op, birth, manifest, chain []byte
	var sealed int64
	p := &out.progress
	m := &p.manifest
	err = row.Scan(&out.request, &out.input, &m.source, &m.generation, &m.expectedRevision, &m.previousGeneration, &m.expected[2], &inc, &op, &birth, &p.sequence, &out.acknowledged, &p.revision, &p.staged[0], &p.staged[1], &p.staged[2], &p.bytes, &manifest, &chain, &sealed, &out.status, &out.resultRevision, &out.resultState, &out.publishedAt, &out.name, &out.tuner, &m.expected[0], &m.expected[1], &out.createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if len(inc) != 16 || len(op) != 16 || len(birth) != 32 || len(manifest) != 32 || len(chain) != 32 || (sealed != 0 && sealed != 1) {
		return out, ErrInvalid
	}
	copy(out.incarnation[:], inc)
	copy(out.createOperation[:], op)
	copy(out.birthDigest[:], birth)
	copy(m.expectedChain[:], manifest)
	copy(p.chain[:], chain)
	p.sealed = sealed == 1
	if !validMGGuideProgress(*p) || out.acknowledged < 0 || out.acknowledged > p.sequence || out.name == "" || out.tuner < 0 || out.tuner > 256 {
		return out, ErrInvalid
	}
	return out, nil
}
func mgGuideCurrentHead(ctx context.Context, t *commandTurn, source string) (out mgGuideHead, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	row, err := mgGuidePublicationQuery(ctx, t, mgGuideIdentity, source)
	if err != nil {
		return out, err
	}
	var removed int64
	if err = row.Scan(&removed); err != nil {
		return out, err
	}
	if removed != 0 {
		return out, ErrDisclosureDenied
	}
	out.source = source
	row, err = mgGuidePublicationQuery(ctx, t, mgGuideSource, source)
	if err != nil {
		return out, err
	}
	err = row.Scan(&out.revision, &out.state, &out.generation)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return out, err
}
func mgGuideNativeTime(ctx context.Context, t *commandTurn) (int64, error) {
	if t == nil || t.transaction == nil || t.transaction.writer == nil || t.transaction.writer.now == nil {
		return 0, ErrInvalid
	}
	if err := t.checkLifetimes(ctx); err != nil {
		return 0, err
	}
	defer t.transaction.guardPanic()
	now := t.transaction.writer.now()
	if now.UnixMilli() < 0 {
		return 0, ErrInvalid
	}
	if err := t.transaction.observeTime(now); err != nil {
		return 0, err
	}
	return now.UnixMilli(), nil
}
func mgGuideNextNative(t *commandTurn, before mgGuideNative, identity CommandIdentity) error {
	if t == nil || t.transaction == nil || before.status != "staging" || before.progress.sequence == math.MaxInt64 || before.progress.revision == math.MaxInt64 ||
		identity.OperationID == ([16]byte{}) || identity.Incarnation != before.incarnation || identity.Sequence != before.progress.sequence+1 || before.acknowledged != before.progress.sequence {
		return ErrConflict
	}
	return nil
}
func mgSealGuideNative(ctx context.Context, t *commandTurn, before mgGuideNative, identity CommandIdentity) (out mgGuideNativeResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mgGuideNextNative(t, before, identity); err != nil {
		return out, err
	}
	actual, err := mgReadGuideNative(ctx, t, before.request)
	if err != nil {
		return out, err
	}
	if actual != before {
		return out, ErrConflict
	}
	next, err := planMGGuideSeal(before.progress, identity.Sequence)
	if err != nil {
		return out, err
	}
	head, err := mgGuideCurrentHead(ctx, t, before.progress.manifest.source)
	if err != nil {
		return out, err
	}
	if head.revision != before.progress.manifest.expectedRevision || head.generation != before.progress.manifest.previousGeneration {
		return out, ErrConflict
	}
	applied, err := mgGuideNativeTime(ctx, t)
	if err != nil {
		return out, err
	}
	p := before.progress
	if err = mgGuidePublicationChange(ctx, t, mgGuideSealRow, next.sequence, next.revision, before.request, before.incarnation[:], p.sequence, before.acknowledged, p.revision, p.staged[0], p.staged[1], p.staged[2], p.bytes, p.manifest.expectedChain[:], p.chain[:]); err != nil {
		return out, err
	}
	want := before
	want.progress = next
	post, err := mgReadGuideNative(ctx, t, before.request)
	if err != nil {
		return out, err
	}
	if post != want {
		return out, ErrConflict
	}
	return mgGuideNativeResult{post, head, applied}, nil
}
func mgPublishGuideNative(ctx context.Context, t *commandTurn, before mgGuideNative, identity CommandIdentity, refresh bool) (out mgGuideNativeResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mgGuideNextNative(t, before, identity); err != nil {
		return out, err
	}
	actual, err := mgReadGuideNative(ctx, t, before.request)
	if err != nil {
		return out, err
	}
	if actual != before {
		return out, ErrConflict
	}
	head, err := mgGuideCurrentHead(ctx, t, before.progress.manifest.source)
	if err != nil {
		return out, err
	}
	next, err := planMGGuidePublication(before.progress, head, refresh)
	if err != nil {
		return out, err
	}
	applied, err := mgGuideNativeTime(ctx, t)
	if err != nil {
		return out, err
	}
	published := time.UnixMilli(applied).UTC().Format("2006-01-02T15:04:05.000Z")
	if head.revision == 0 {
		err = mgGuidePublicationChange(ctx, t, mgGuidePublishNewSource, next.source, before.name, next.revision, next.state, before.tuner, next.generation, published)
	} else {
		err = mgGuidePublicationChange(ctx, t, mgGuidePublishSource, before.name, next.revision, before.tuner, next.generation, published, next.source, head.revision, head.generation, head.state)
	}
	if err != nil {
		return out, err
	}
	p := before.progress
	m := p.manifest
	err = mgGuidePublicationChange(ctx, t, mgGuidePublishOperation, identity.Sequence, p.revision+1, next.revision, next.state, published, before.request, before.incarnation[:], p.sequence, before.acknowledged, p.revision, m.source, m.generation, m.expectedRevision, m.previousGeneration)
	if err != nil {
		return out, err
	}
	want := before
	want.progress.sequence = identity.Sequence
	want.progress.revision++
	want.status = "published"
	want.resultRevision = next.revision
	want.resultState = next.state
	want.publishedAt = published
	post, err := mgReadGuideNative(ctx, t, before.request)
	if err != nil {
		return out, err
	}
	if post != want {
		return out, ErrConflict
	}
	postHead, err := mgGuideCurrentHead(ctx, t, m.source)
	if err != nil {
		return out, err
	}
	if postHead != next {
		return out, ErrConflict
	}
	return mgGuideNativeResult{post, postHead, applied}, nil
}
