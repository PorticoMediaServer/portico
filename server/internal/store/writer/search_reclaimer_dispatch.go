package writer

import (
	"context"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strconv"
	"unicode/utf8"
)

// An independent receipt namespace. PublisherReceiptRequest.JobID is NEVER
// reinterpreted as a reclaimer ID, even when numeric IDs/nonces happen to match.
type searchReclaimerReceiptRequest struct {
	ReclaimerID, PublicationID, LibraryID, EntityID int64
	Receipt                                         ReceiptReference
}

func checkSearchReclaimerDisclosure(w *Writer, c *PublisherDisclosure, q searchReclaimerReceiptRequest) error {
	if err := currentPublisherPrincipal(w, c); err != nil {
		return err
	}
	s := c.scope
	if s.kind != publisherReclaimerScope || s.jobID != 0 || s.incarnation != ([16]byte{}) ||
		s.reclaimer.jobID <= 0 || s.reclaimer.publicationID <= 0 ||
		s.reclaimer.jobID != q.ReclaimerID || s.reclaimer.publicationID != q.PublicationID ||
		s.reclaimer.incarnation != q.Receipt.Identity.Incarnation ||
		q.LibraryID <= 0 || q.EntityID <= 0 || q.Receipt.Identity.Sequence <= 0 ||
		s.libraryID != q.LibraryID || s.entityID != q.EntityID ||
		s.libraryKey == "" || len(s.libraryKey) > maxCanonicalRequestBytes || !utf8.ValidString(s.libraryKey) {
		return ErrDisclosureDenied
	}
	return nil
}

func (r *effectiveRuntime) runSearchReclaimer(ctx context.Context, w *Writer, cap *PublisherDisclosure, q searchReclaimerReceiptRequest, c *searchFragmentReclaimCommand, ack bool) (out searchPublisherCommandResult, acknowledged AckResult, err error) {
	defer func() {
		v, e := finishCommand(ctx, ctx, commandCompletion{output: commandOutput{search: out}, ack: acknowledged}, err)
		out, acknowledged, err = v.output.search, v.ack, e
	}()
	if r == nil || w == nil || w.effective != r {
		return out, acknowledged, ErrInvalid
	}
	v, err := prepareSearchReclaimerCommand(ctx, w, cap, q, c, ack)
	if err != nil {
		return out, acknowledged, err
	}
	result, acknowledged, err := r.runCommand(ctx, w, nil, EnrichmentReceiptRequest{}, nil, ack, nil, &v)
	return result.search, acknowledged, err
}

func prepareSearchReclaimerCommand(ctx context.Context, w *Writer, cap *PublisherDisclosure, q searchReclaimerReceiptRequest, c *searchFragmentReclaimCommand, ack bool) (out publisherInvocation, err error) {
	defer func() {
		err = errors.Join(err, currentPublisherPrincipal(w, cap))
		out, err = finishCommand(ctx, ctx, out, err)
	}()
	if ack && c != nil {
		return out, ErrInvalid
	}
	call := publisherResolveReclaimer
	if ack {
		call = publisherAckReclaimer
	}
	if c != nil {
		if !c.valid {
			return out, ErrInvalid
		}
		s := c.request.snapshot
		expected := searchReclaimerReceiptRequest{s.owner.id, s.owner.publicationID, s.fragment.libraryID, s.fragment.entityID, ReceiptReference{c.identity, c.digest}}
		if expected != q {
			return out, ErrInvalid
		}
		call = publisherReclaimFragment
	}
	if err = checkSearchReclaimerDisclosure(w, cap, q); err != nil {
		return out, err
	}
	return publisherInvocation{disclosure: cap, purpose: SearchFragmentReclaim, call: call, reclaim: c, reclaimRequest: q}, nil
}

func validateSearchReclaimerInvocation(ctx context.Context, w *Writer, v *publisherInvocation) (out publisherInvocation, err error) {
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	if v == nil || v.input != nil || v.fragment != nil || v.head != nil || v.request != (PublisherReceiptRequest{}) || v.purpose != SearchFragmentReclaim {
		return out, ErrInvalid
	}
	out, err = prepareSearchReclaimerCommand(ctx, w, v.disclosure, v.reclaimRequest, v.reclaim, v.call == publisherAckReclaimer)
	if err == nil && out != *v {
		return publisherInvocation{}, ErrInvalid
	}
	return out, err
}

func discloseSearchReclaimer(ctx context.Context, t *commandTurn, v *publisherInvocation) (out receiptOwnerFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if ctx == nil || t == nil || t.transaction == nil || t.transaction.descriptor.ID != SearchFragmentReclaim || v == nil || v.disclosure == nil || v.disclosure.scope == nil {
		return out, ErrInvalid
	}
	q := v.reclaimRequest
	o, err := readSearchReclaimOwner(ctx, t, q.ReclaimerID)
	if err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	// Immutable owner identity survives native image/fragment retirement. Neither
	// phase/lease/source predicates nor a live-publication join may hide history.
	if o.publicationID != q.PublicationID || o.incarnation != q.Receipt.Identity.Incarnation || o.incarnation != v.disclosure.scope.reclaimer.incarnation {
		return out, ErrDisclosureDenied
	}
	out = receiptOwnerFacts{true, o.incarnation, o.lastSequence, o.acknowledgedSequence}
	for _, point := range []struct {
		id   stmt.ID
		args []any
		want int64
	}{
		{effectiveDisclosureLibrary, []any{q.LibraryID, v.disclosure.scope.libraryKey}, q.LibraryID},
		{effectiveDisclosureSubject, []any{q.EntityID, q.LibraryID}, q.EntityID},
	} {
		step, e := searchPublisherStep(t, point.id)
		if e != nil {
			return out, e
		}
		row, e := t.queryRow(ctx, step, point.args...)
		if e != nil {
			return out, e
		}
		var actual int64
		if e = row.Scan(&actual); e != nil || actual != point.want {
			return out, errors.Join(ErrDisclosureDenied, e)
		}
	}
	return out, nil
}

func checkSearchReclaimerFreshScope(ctx context.Context, t *commandTurn, q searchReclaimerReceiptRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if ctx == nil || t == nil || t.transaction == nil || t.transaction.descriptor.ID != SearchFragmentReclaim {
		return ErrInvalid
	}
	step, err := searchPublisherStep(t, searchReclaimPublication)
	if err != nil {
		return err
	}
	row, err := t.queryRow(ctx, step, q.PublicationID)
	if err != nil {
		return err
	}
	var id, entity, library, kind, bank, input int64
	if err = row.Scan(&id, &entity, &library, &kind, &bank, &input); err != nil {
		return errors.Join(ErrDisclosureDenied, err)
	}
	if id != q.PublicationID || entity != q.EntityID || library != q.LibraryID || kind != 1 || bank <= 0 || input <= 0 {
		return ErrDisclosureDenied
	}
	return nil
}

func executeSearchReclaimerBody(ctx context.Context, t *commandTurn, v *publisherInvocation, owner receiptOwnerFacts) (out searchPublisherCommandResult, ack int64, historical bool, err error) {
	defer func() {
		x, e := finishCommand(ctx, commandLifetime(t), struct {
			Out        searchPublisherCommandResult
			Ack        int64
			Historical bool
		}{out, ack, historical}, err)
		out, ack, historical, err = x.Out, x.Ack, x.Historical, e
	}()
	if ctx == nil || t == nil || t.transaction == nil || t.transaction.descriptor.ID != SearchFragmentReclaim || v == nil {
		return out, 0, false, ErrInvalid
	}
	q := v.reclaimRequest
	saved, decision, err := lookupSearchFragmentReclaim(ctx, t, q.ReclaimerID, q.Receipt)
	if err != nil {
		return out, 0, false, err
	}
	if v.call == publisherAckReclaimer {
		ack, err = acknowledgeSearchReclaimer(ctx, t, q, owner, decision, saved)
		return out, ack, true, err
	}
	out.Outcome.Reference = q.Receipt
	switch decision {
	case receiptReplay:
		out.Reclaim = saved
		out.Outcome.State, out.Outcome.Replayed = Applied, true
		return out, 0, true, nil
	case receiptRetired:
		out.Outcome.State = ResultRetired
		return out, 0, true, nil
	case receiptFresh:
		out.Outcome.State = Pending
	default:
		return out, 0, false, ErrResolveRequired
	}
	if v.reclaim != nil {
		if err = checkSearchReclaimerFreshScope(ctx, t, q); err != nil {
			return out, 0, false, err
		}
		out.Reclaim, err = applySearchFragmentReclaim(ctx, t, v.reclaim)
	}
	return out, 0, false, err
}

func searchReclaimerJobKey(q searchReclaimerReceiptRequest) string {
	return "search/reclaim/" + strconv.FormatInt(q.ReclaimerID, 10)
}
