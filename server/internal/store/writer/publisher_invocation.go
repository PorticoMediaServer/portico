package writer

import (
	"context"
	"errors"
	"sync/atomic"
	"unicode/utf8"
)

// FIRST is a trusted internal PublisherDriver, never a caller-selected system
// flag or account role. Issuance/opening remains W-owned and is not supplied by
// this protocol layer. Scope identities freeze at issuance; revocation is live.
type publisherScopeKind uint8

const (
	publisherJobScope publisherScopeKind = iota
	publisherReclaimerScope
)

// Distinct fields prevent publisher-job IDs/nonces becoming reclaimer authority.
type publisherReclaimerBinding struct {
	jobID, publicationID int64
	incarnation          [16]byte
}

type publisherScope struct {
	kind                       publisherScopeKind
	reclaimer                  publisherReclaimerBinding
	jobID, libraryID, entityID int64
	libraryKey                 string
	incarnation                [16]byte
	lifetime                   context.Context
	revoked                    atomic.Bool
}

type publisherCall uint8

const (
	publisherStageImage publisherCall = iota + 1
	publisherResolveImage
	publisherAckImage
	publisherStageFragment
	publisherPublishHead
	publisherResolveCommand
	publisherAckCommand
	publisherReclaimFragment
	publisherResolveReclaimer
	publisherAckReclaimer
)

// A bounded typed protocol envelope, NOT authority, a grant, native source
// proof or an execution hook. The dispatcher rechecks current opaque/native
// scope and original history in its SAME turn before any fresh effects.
type publisherInvocation struct {
	disclosure     *PublisherDisclosure
	request        PublisherReceiptRequest
	input          *searchImageStageCommand
	call           publisherCall
	purpose        CommandID
	fragment       *searchFragmentStageCommand
	head           *searchHeadPublishCommand
	reclaim        *searchFragmentReclaimCommand
	reclaimRequest searchReclaimerReceiptRequest
}

// PB's finite receipt adapter returns only shared protocol facts and the exact
// immutable native result. No callback, current-image reconstruction, invented
// result digest or public Applied outcome belongs in this record.
type publisherReceiptLookup struct {
	owner    receiptOwnerFacts
	decision receiptDecision
	result   SearchImageStageResult
}

func currentPublisherPrincipal(w *Writer, c *PublisherDisclosure) error {
	if w == nil || w.effective == nil || c == nil || c.issuer != w || c.scope == nil || c.scope.lifetime == nil || c.scope.revoked.Load() {
		return ErrDisclosureDenied
	}
	// Read revocation again AFTER the independent authority lifetime check.
	// Caller cancellation never replaces this permission check.
	if c.scope.lifetime.Err() != nil || c.scope.revoked.Load() {
		return ErrDisclosureDenied
	}
	return nil
}

func checkPublisherDisclosure(w *Writer, c *PublisherDisclosure, q PublisherReceiptRequest) error {
	if err := currentPublisherPrincipal(w, c); err != nil {
		return err
	}
	s := c.scope
	if s.kind != publisherJobScope || s.reclaimer != (publisherReclaimerBinding{}) {
		return ErrDisclosureDenied
	}
	if q.JobID <= 0 || q.LibraryID <= 0 || q.EntityID <= 0 || q.Receipt.Identity.Sequence <= 0 ||
		s.libraryKey == "" || len(s.libraryKey) > maxCanonicalRequestBytes || !utf8.ValidString(s.libraryKey) ||
		s.jobID != q.JobID || s.libraryID != q.LibraryID || s.entityID != q.EntityID || s.incarnation != q.Receipt.Identity.Incarnation {
		return ErrDisclosureDenied
	}
	return nil
}

func (r *effectiveRuntime) currentCommandDisclosure(w *Writer, cap *EnrichmentDisclosure, request EnrichmentReceiptRequest, scan *inventoryInvocation, publisher *publisherInvocation, invocation wcCommandInvocation) error {
	switch wc := invocation.(type) {
	case *enrichmentInvocation:
		return validateEnrichmentInvocation(r, w, wc)
	case *wcProgressInvocation:
		return checkWCProgressDisclosure(w, wc)
	case *wcSecurityInvocation:
		return checkWCSecurityDisclosure(w, wc)
	case *wcReclaimInvocation:
		return checkWCReclaimDisclosure(w, wc)
	case nil:
	default:
		return ErrInvalid
	}
	if publisher != nil {
		if w == nil || w.effective != r {
			return ErrDisclosureDenied
		}
		if publisher.purpose == SearchFragmentReclaim {
			return checkSearchReclaimerDisclosure(w, publisher.disclosure, publisher.reclaimRequest)
		}
		return checkPublisherDisclosure(w, publisher.disclosure, publisher.request)
	}
	return r.currentRunDisclosure(w, cap, request, scan)
}

func validatePublisherInvocation(ctx context.Context, w *Writer, invocation *publisherInvocation) (out publisherInvocation, err error) {
	defer func() { out, err = finishCommand(ctx, ctx, out, err) }()
	if invocation == nil {
		return publisherInvocation{}, ErrInvalid
	}
	if invocation.purpose != 0 {
		return validateSearchPublisherInvocation(ctx, w, invocation)
	}
	if invocation.fragment != nil || invocation.head != nil || invocation.reclaim != nil || invocation.reclaimRequest != (searchReclaimerReceiptRequest{}) {
		return publisherInvocation{}, ErrInvalid
	}
	q := invocation.request
	var command SearchImageStageCommand
	if invocation.call == publisherStageImage {
		q = PublisherReceiptRequest{}
		command = invocation.input
	}
	verified, err := preparePublisherInvocation(ctx, w, invocation.disclosure, invocation.call, q, command)
	if err != nil {
		return publisherInvocation{}, err
	}
	if verified.request != invocation.request || verified.input != invocation.input {
		return publisherInvocation{}, ErrInvalid
	}
	return verified, nil
}

// Preparation under separately owned ingress custody; no native read, writer
// opening, Try, BEGIN or receipt classification occurs here. Public publisher
// methods remain explicitly unimplemented until PB's reviewed finite adapters,
// native disclosure and full Capture/qualified profile can be composed.
func preparePublisherInvocation(ctx context.Context, w *Writer, disclosure *PublisherDisclosure, call publisherCall, request PublisherReceiptRequest, command SearchImageStageCommand) (out publisherInvocation, err error) {
	defer func() {
		err = errors.Join(err, currentPublisherPrincipal(w, disclosure))
		out, err = finishCommand(ctx, ctx, out, err)
	}()
	if err := currentPublisherPrincipal(w, disclosure); err != nil {
		return publisherInvocation{}, err
	}
	var input *searchImageStageCommand
	switch call {
	case publisherStageImage:
		if request != (PublisherReceiptRequest{}) {
			return publisherInvocation{}, ErrInvalid
		}
		ref, err := SearchImageStageReference(command)
		if err != nil {
			return publisherInvocation{}, err
		}
		input = command.(*searchImageStageCommand) // getter checked the EXACT concrete type
		request = PublisherReceiptRequest{JobID: input.request.owner.id, LibraryID: input.request.source.libraryID,
			EntityID: input.request.source.entityID, Receipt: ref}
	case publisherResolveImage, publisherAckImage:
		if command != nil {
			return publisherInvocation{}, ErrInvalid
		}
	default:
		return publisherInvocation{}, ErrInvalid
	}
	if err := checkPublisherDisclosure(w, disclosure, request); err != nil {
		return publisherInvocation{}, err
	}
	return publisherInvocation{disclosure: disclosure, request: request, input: input, call: call}, nil
}
