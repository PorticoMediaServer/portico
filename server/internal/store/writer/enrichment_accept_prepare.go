package writer

import (
	"math"
	"reflect"
	"slices"
	"strings"
)

// FIRST publishes a complete primary-provider credit image, never a delta or
// an inferred empty set. Authored/locked and mixed-provider images require the
// separately verified merge planner; they do not silently lose their rows.
func newEnrichAcceptCommand(identity CommandIdentity, begin *effectiveStageBeginCommand, in enrichAcceptRequest) (*enrichAcceptCommand, error) {
	if begin == nil || !begin.valid || begin.sourceReferences == nil || identity.Sequence <= 0 || in.variant != "accept_provider_credits_v1" ||
		in.owner.incarnation != identity.Incarnation || in.owner.lastSequence != in.owner.acknowledgedSequence || identity.Sequence != in.owner.lastSequence+1 || in.owner.lastSequence == math.MaxInt64 || in.owner.lastSequence != begin.identity.Sequence || in.stageSlot < 0 || in.reservedBytes <= 0 ||
		!in.owner.imageID.Valid || in.owner.imageID.Int64 != in.image.id || in.image.id <= 0 || in.image.revision <= 0 || in.entity.ownerLocked != 0 ||
		in.image.id == in.entity.imageID.Int64 || in.owner.entityID != in.entity.id || in.entity.id != begin.request.entity.id ||
		in.image.header != begin.request.header || in.beginIdentity != begin.identity || in.beginRequest != begin.digest ||
		in.beginResult.ProviderOperationID != begin.request.ownerID || in.beginResult.EntityID != in.entity.id || in.beginResult.ImageID != in.image.id ||
		in.beginResult.ImageRevision > in.image.revision || in.beginResult.ImageRevision <= 0 ||
		in.image.header.expectedCount != int64(len(in.rows)) || len(in.rows) > MaxCastCredits+MaxCrewCredits || in.image.header.expectedBytes < 0 ||
		uint64(in.image.header.expectedBytes) > maxCanonicalRequestBytes || in.producer.Publication.Count != int64(len(in.rows)) ||
		in.image.stagedCount != int64(len(in.rows)) || in.image.stagedBytes != in.image.header.expectedBytes ||
		in.entity.incarnation != in.image.header.entityIncarnation || in.entity.sourceRevision != in.image.header.sourceRevision ||
		in.entity.identityRevision != in.image.header.identityRevision || in.entity.metadataRevision != in.image.header.metadataRevision || in.entity.ownerRevision != in.image.header.ownerRevision ||
		in.entity.revision == math.MaxInt64 || in.owner.generation <= 0 || in.producer.Selection.Set.Valid || in.producer.Selection.Publication.Valid || in.producer.Selection.Track.Valid ||
		!effectiveBeginProducerShape(in.producer, in.entity, in.owner, in.image.header) {
		return nil, invalid("enrich_accept_scope")
	}
	br := in.beginResult
	bd, err := effectiveBeginResultDigest(begin.identity, begin.digest, effectiveBeginResult{ProviderOperationID: br.ProviderOperationID, EntityID: br.EntityID, ObservedCatalogRevision: br.ObservedCatalogRevision, AppliedMS: br.AppliedMS, AfterItem: br.AfterItem, ImageID: br.ImageID, ImageRevision: br.ImageRevision})
	if err != nil || bd != br.Digest {
		return nil, ErrConflict
	}
	if in.prior.exists != in.entity.imageID.Valid || (in.prior.exists && (in.prior.owner == "" || in.prior.owner == begin.request.ownerID || in.prior.image != in.entity.imageID.Int64 || in.prior.generation <= 0 || in.prior.pinUnits <= 0 || in.prior.reservedBytes <= 0)) {
		return nil, invalid("enrich_accept_prior")
	}
	if err := preflightEnrichAcceptControls(in, begin); err != nil {
		return nil, err
	}
	// Hash the complete borrowed ORIGINAL before retaining strings or the cohort.
	// The closed codec preflights the entire resource envelope before hashing.
	packet := struct {
		Request       enrichAcceptRequest
		OriginalBegin effectiveNativeSourceRequest
	}{in, effectiveNativeSourceRequest{"begin_native_seal_refs_v1", begin.request, *begin.sourceReferences}}
	digest, err := canonicalRequestDigest(EnrichAccept, 1, identity, packet)
	if err != nil {
		return nil, err
	}
	context := effectiveCreditManifestContext{in.entity.id, in.image.header.declaredInput, in.image.header.sourceManifest, in.image.header.language, int64(len(in.rows)), in.image.header.expectedBytes}
	checkpoint, err := newEffectiveCreditManifestCheckpoint(context, maxCanonicalRequestBytes)
	if err != nil {
		return nil, err
	}
	for _, r := range in.rows {
		if r.ownerEntryID.Valid || r.ownerIncarnation.present || r.ownerRevision.Valid || !r.sourceRowID.Valid || r.sourceStatus != "current" || r.provider != in.owner.provider || r.ordinal < 0 || r.roleID <= 0 || r.departmentID <= 0 || (r.personID.Valid && r.personID.Int64 <= 0) {
			return nil, invalid("enrich_accept_credit")
		}
		checkpoint, err = advanceEffectiveCreditManifest(context, checkpoint, r, maxCanonicalRequestBytes)
		if err != nil {
			return nil, err
		}
	}
	if checkpoint.nextOrdinal != int64(len(in.rows)) || checkpoint.canonicalBytes != in.image.header.expectedBytes || checkpoint.seed != in.image.seed || checkpoint.chain != in.image.chain || checkpoint.chain != in.image.header.expectedManifest {
		return nil, invalid("enrich_accept_manifest")
	}
	original, err := newEffectiveStageBeginFromSeals(begin.identity, begin.request, *begin.sourceReferences)
	if err != nil {
		return nil, err
	}
	if original.digest != begin.digest {
		return nil, ErrConflict
	}
	// Native owner TEXT and nullable TEXT are explicit; producer fields use the
	// existing value-tree ownership helper, never another request codec.
	texts := []*string{&in.variant, &in.owner.provider, &in.owner.phase, &in.owner.status, &in.owner.lease, &in.owner.leaseUntil, &in.owner.algorithm, &in.owner.refreshMode, &in.owner.inputDigest, &in.owner.selectedID, &in.owner.afterItem, &in.owner.queryTitle, &in.owner.queryArtist, &in.owner.queryDigest, &in.owner.hintID, &in.owner.hintProblem, &in.owner.qualification, &in.owner.episodeOrder, &in.owner.setID.String, &in.owner.setSubjectKind.String, &in.owner.setProviderID.String, &in.entity.language, &in.image.header.language, &in.beginResult.ProviderOperationID, &in.beginResult.AfterItem, &in.prior.owner}
	for _, p := range texts {
		*p = strings.Clone(*p)
	}
	var total uint64
	if err = effectiveBeginOwnProducer(reflect.ValueOf(&in.producer).Elem(), true, &total); err != nil {
		return nil, err
	}
	in.rows = slices.Clone(in.rows)
	for i := range in.rows {
		r := &in.rows[i]
		for _, p := range []*string{&r.creditedName, &r.provider, &r.creditID, &r.providerPersonID, &r.sourceRowID.String, &r.ownerEntryID.String, &r.sourceStatus} {
			*p = strings.Clone(*p)
		}
	}
	return &enrichAcceptCommand{identity: identity, digest: digest, originalBegin: original, request: in, valid: true}, nil
}
func (r enrichAcceptRequest) ownerID() string { return r.beginResult.ProviderOperationID }

// Counts borrowed retained-control occurrences before hashing, tuple encoding,
// variable copies or ownership. Credited display names are CONTENT, separately
// bounded by the complete image byte context; their IDs/provenance are controls.
func preflightEnrichAcceptControls(in enrichAcceptRequest, begin *effectiveStageBeginCommand) error {
	var storage [2048]string
	controls := storage[:0]
	tuple := reflect.TypeOf(effectiveCreditTuple{})
	var visit func(reflect.Value) error
	visit = func(v reflect.Value) error {
		switch v.Kind() {
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if v.Type() == tuple && v.Type().Field(i).Name == "creditedName" {
					continue
				}
				if err := visit(v.Field(i)); err != nil {
					return err
				}
			}
		case reflect.Slice:
			if v.Type().Elem() != tuple || v.Len() > MaxCastCredits+MaxCrewCredits {
				return ErrInvalid
			}
			for i := 0; i < v.Len(); i++ {
				if err := visit(v.Index(i)); err != nil {
					return err
				}
			}
		case reflect.String:
			if len(controls) == cap(controls) {
				return ErrCap
			}
			controls = append(controls, v.String())
		case reflect.Array:
			if v.Type().Elem().Kind() != reflect.Uint8 {
				return ErrInvalid
			}
		case reflect.Int64, reflect.Bool, reflect.Float64:
		default:
			return ErrInvalid
		}
		return nil
	}
	for _, v := range []any{in, begin.request} {
		if err := visit(reflect.ValueOf(v)); err != nil {
			return err
		}
	}
	controls = append(controls, begin.sourceReferences.evidence.owner, begin.sourceReferences.source.owner)
	_, err := CheckSealControlStrings(controls)
	return err
}
