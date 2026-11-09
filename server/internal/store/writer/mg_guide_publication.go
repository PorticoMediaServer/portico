package writer

import (
	"context"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Private seal/publication effects on the caller's one admitted writer turn.
// They carry no authority, admission, receipt resolution or COMMIT: the parent
// reauthorizes the source, proves the staging bounds and saves the original
// typed result before committing. Every error, including a late source or
// operation CAS, leaves the WHOLE frame to the caller's rollback.
func mgGuidePublicationStep(t *commandTurn, id stmt.ID) (statementStep, error) {
	if t == nil || t.transaction == nil {
		return statementStep{}, ErrInvalid
	}
	switch id {
	case mgGuideSourceRow, mgGuideSealRow, mgGuidePublishSource, mgGuidePublishNewSource, mgGuidePublishOperation:
	default:
		return statementStep{}, ErrInvalid
	}
	step, ok := t.steps[id]
	if !ok {
		return statementStep{}, ErrInvalid
	}
	return step.statementStep, nil
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

// Seal and publication are NORMAL, policy-verified commands only. A FULL or
// foreign purpose can never borrow the guide seal or a source head flip.
func mgGuidePublicationPurpose(t *commandTurn, purpose CommandID) error {
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != purpose ||
		t.transaction.descriptor.Durability != Normal || !t.transaction.policyVerified ||
		!t.transaction.begun || t.transaction.done {
		return ErrInvalid
	}
	switch purpose {
	case MGGuideSeal, MGGuidePublish:
		return nil
	}
	return ErrInvalid
}

// The turn's applied time for the seal/publication frames. No real-clock
// assertion or latency claim: this is a native observation on the admitted
// frame, never qualification evidence.
func mgGuidePublicationTime(ctx context.Context, t *commandTurn) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil || t.transaction == nil || t.transaction.writer == nil || t.transaction.writer.now == nil {
		return 0, ErrInvalid
	}
	if err = mgGuidePublicationPurpose(t, t.transaction.descriptor.ID); err != nil {
		return 0, err
	}
	if err = t.checkLifetimes(ctx); err != nil {
		return 0, err
	}
	defer t.transaction.guardPanic()
	now := t.transaction.writer.now()
	if now.UnixMilli() < 0 {
		return 0, ErrInvalid
	}
	if err = t.transaction.observeTime(now); err != nil {
		return 0, err
	}
	return now.UnixMilli(), nil
}

// A seal or publication continues the operation's original command incarnation
// on a fresh operation identity, only after the predecessor's exact result was
// acknowledged. A seal requires an unsealed complete staging operation; a
// publication requires the sealed one. Both refuse a cancelled, foreign or
// skipped identity before any native effect.
func mgGuidePublicationNext(t *commandTurn, before mgGuideNative, identity CommandIdentity, sealed bool) error {
	if t == nil || t.transaction == nil || t.transaction.done {
		return ErrInvalid
	}
	p := before.progress
	if before.status != "staging" || p.sealed != sealed ||
		p.sequence == math.MaxInt64 || p.revision == math.MaxInt64 ||
		identity.OperationID == ([16]byte{}) || identity.Incarnation != before.incarnation ||
		identity.Sequence != p.sequence+1 || before.acknowledged != p.sequence {
		return ErrConflict
	}
	return nil
}

// One channel with no programmes is a complete guide; zero channels never is.
// The seal repeats the accepted totals and both digests in its CAS, so a changed
// row, a hole or a replaced header cannot be sealed by counts alone.
func mgSealGuideNative(ctx context.Context, t *commandTurn, before mgGuideNative, identity CommandIdentity) (out mgGuideNativeResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mgGuidePublicationPurpose(t, MGGuideSeal); err != nil {
		return out, err
	}
	if err = mgGuidePublicationNext(t, before, identity, false); err != nil {
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
	p := before.progress
	head, err := mgGuideCurrentHead(ctx, t, p.manifest.source)
	if err != nil {
		return out, err
	}
	if head.revision != p.manifest.expectedRevision || head.generation != p.manifest.previousGeneration {
		return out, ErrConflict
	}
	applied, err := mgGuidePublicationTime(ctx, t)
	if err != nil {
		return out, err
	}
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

// Publish moves the source head to the sealed generation under one point CAS on
// the observed config revision AND the previous published generation; an ordinary
// refresh keeps the config revision while a configuration change advances it. A
// source that is disabled stays disabled, and a first publication creates the
// retained source row with exact native RETURNING equality.
func mgPublishGuideNative(ctx context.Context, t *commandTurn, before mgGuideNative, identity CommandIdentity, refresh bool) (out mgGuideNativeResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mgGuidePublicationPurpose(t, MGGuidePublish); err != nil {
		return out, err
	}
	if err = mgGuidePublicationNext(t, before, identity, true); err != nil {
		return out, err
	}
	actual, err := mgReadGuideNative(ctx, t, before.request)
	if err != nil {
		return out, err
	}
	if actual != before {
		return out, ErrConflict
	}
	p := before.progress
	m := p.manifest
	head, err := mgGuideCurrentHead(ctx, t, m.source)
	if err != nil {
		return out, err
	}
	next, err := planMGGuidePublication(p, head, refresh)
	if err != nil {
		return out, err
	}
	applied, err := mgGuidePublicationTime(ctx, t)
	if err != nil {
		return out, err
	}
	published := mgGuideTimestamp(applied)
	if head.revision == 0 {
		if err = mgCheckGuideCapacity(ctx, t); err != nil {
			return out, err
		}
		if err = mgGuideInsertPublishedSource(ctx, t, next, before.name, before.tuner, published); err != nil {
			return out, err
		}
	} else if err = mgGuidePublicationChange(ctx, t, mgGuidePublishSource, before.name, next.revision, before.tuner, next.generation, published, next.source, head.revision, head.generation, head.state); err != nil {
		return out, err
	}
	if err = mgGuideCheckPublishedSource(ctx, t, next, before.name, before.tuner, published); err != nil {
		return out, err
	}
	if err = mgGuidePublicationChange(ctx, t, mgGuidePublishOperation, identity.Sequence, p.revision+1, next.revision, next.state, published, before.request, before.incarnation[:], p.sequence, before.acknowledged, p.revision, m.source, m.generation, m.expectedRevision, m.previousGeneration); err != nil {
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

// The first publication inserts the retained source row. Every returned column
// must equal the exact bound value; the caller rolls the whole frame back on any
// mismatch, including a later operation CAS failure.
func mgGuideInsertPublishedSource(ctx context.Context, t *commandTurn, head mgGuideHead, name string, tuners int64, published string) error {
	row, err := mgGuidePublicationQuery(ctx, t, mgGuidePublishNewSource, head.source, name, head.revision, head.state, tuners, head.generation, published)
	if err != nil {
		return err
	}
	var source, gotName, state, generation, gotPublished string
	var revision, gotTuners int64
	targets := []any{&source, &gotName, &revision, &state, &gotTuners, &generation, &gotPublished}
	if err = row.Scan(targets...); err != nil {
		return err
	}
	if source != head.source || gotName != name || revision != head.revision || state != head.state ||
		gotTuners != tuners || generation != head.generation || gotPublished != published ||
		!mgGuideText(source, true, 256) || !mgGuideText(generation, true, 256) {
		return ErrConflict
	}
	return nil
}

// The stored source head and its retained display facts must be exactly the
// values this frame wrote: a changed row or a foreign generation is a conflict,
// never a partially published source. The point read uses the source PK only.
func mgGuideCheckPublishedSource(ctx context.Context, t *commandTurn, head mgGuideHead, name string, tuners int64, published string) error {
	row, err := mgGuidePublicationQuery(ctx, t, mgGuideSourceRow, head.source)
	if err != nil {
		return err
	}
	var gotName, state, generation, gotPublished string
	var revision, gotTuners int64
	if err = row.Scan(&gotName, &revision, &state, &gotTuners, &generation, &gotPublished); err != nil {
		return err
	}
	if gotName != name || revision != head.revision || state != head.state ||
		gotTuners != tuners || generation != head.generation || gotPublished != published {
		return ErrConflict
	}
	return nil
}
