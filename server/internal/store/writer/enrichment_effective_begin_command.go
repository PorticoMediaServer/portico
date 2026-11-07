package writer

import (
	"crypto/rand"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"
)

const effectiveBeginPhase = "enrichment-v1"

// This whole value is the PRE-allocation request. The private variant is part
// of the digest too. Generated retirement/image/time/result outputs are not.
type effectiveStageBeginRequest struct {
	variant                                      string
	ownerID, evidenceID, publicationID, objectID string
	owner                                        effectiveBeginOwner
	entity                                       effectiveBeginEntity
	producer                                     effectiveBeginProducer
	header                                       effectiveBeginHeader
}
type effectiveStageBeginCommand struct {
	identity              CommandIdentity
	request               effectiveStageBeginRequest
	digest                RequestDigest
	leaseExpiry           time.Time
	retirementIncarnation [16]byte
	valid                 bool
	// Only the distinct production v2 request owns these exact immutable seal
	// references. Legacy v1 request bytes remain unchanged. References are data,
	// never verified native lineage or permission to execute.
	sourceReferences *effectiveBeginSourceReferences
}

// EffectiveStageBeginResult is historical scalar BEGIN data. Returning it from
// the private handler does not expose Applied: W must validate/commit/physically
// retire the complete command through its sole facade and terminal protocol.
type EffectiveStageBeginResult struct {
	ProviderOperationID                          string
	EntityID, ObservedCatalogRevision, AppliedMS int64
	AfterItem                                    string
	NextOrdinal, ImageID, ImageRevision          int64
	Digest                                       ResultDigest
}

// Called under W preparation custody BEFORE any writer hold. It performs no SQL
// and creates no durable allocation. Replay can discard this unused candidate.
func newEffectiveStageBeginCommand(identity CommandIdentity, in effectiveStageBeginRequest) (*effectiveStageBeginCommand, error) {
	// Preflight the complete retained TEXT envelope before comparing, deriving a
	// manifest seed, cloning, or hashing any potentially large caller value.
	var bytes uint64
	text := []*string{&in.variant, &in.ownerID, &in.evidenceID, &in.publicationID, &in.objectID, &in.owner.provider, &in.owner.phase, &in.owner.status, &in.owner.lease, &in.owner.leaseUntil, &in.owner.algorithm, &in.owner.refreshMode, &in.owner.inputDigest, &in.owner.selectedID, &in.owner.afterItem, &in.entity.language, &in.header.language,
		&in.owner.queryTitle, &in.owner.queryArtist, &in.owner.queryDigest,
		&in.owner.hintID, &in.owner.hintProblem, &in.owner.qualification, &in.owner.episodeOrder,
		&in.owner.setID.String, &in.owner.setSubjectKind.String, &in.owner.setProviderID.String}
	for _, p := range text {
		if uint64(len(*p)) > maxCanonicalRequestBytes-bytes || !utf8.ValidString(*p) {
			return nil, invalid("effective_begin_text")
		}
		bytes += uint64(len(*p))
	}
	if err := effectiveBeginOwnProducer(reflect.ValueOf(&in.producer).Elem(), false, &bytes); err != nil {
		return nil, err
	}
	if identity.Sequence <= 0 || !in.owner.exists || in.variant != "begin" || in.ownerID == "" || in.evidenceID == "" || in.publicationID == "" || in.objectID == "" || in.objectID != in.producer.Object.ID ||
		in.owner.phase != effectiveBeginPhase || in.owner.status != "staged" || in.owner.incarnation != identity.Incarnation || in.owner.imageID.Valid ||
		in.owner.episodeOrder != "" || in.owner.setID.Valid || in.owner.setSubjectKind.Valid || in.owner.setProviderID.Valid ||
		in.owner.lastSequence < 0 || in.owner.lastSequence != in.owner.acknowledgedSequence || identity.Sequence <= in.owner.lastSequence || identity.Sequence-in.owner.lastSequence != 1 ||
		in.entity.id != in.owner.entityID || in.entity.incarnation != in.owner.entityIncarnation || in.entity.sourceRevision != in.owner.sourceRevision || in.entity.identityRevision != in.owner.identityRevision || in.entity.metadataRevision != in.owner.metadataRevision ||
		in.header.entityID != in.entity.id || in.header.entityIncarnation != in.entity.incarnation || in.header.sourceRevision != in.entity.sourceRevision || in.header.identityRevision != in.entity.identityRevision || in.header.metadataRevision != in.entity.metadataRevision || in.header.ownerRevision != in.entity.ownerRevision ||
		in.header.policyIncarnation != in.owner.policyIncarnation || in.header.policyRevision != in.owner.policyRevision || in.header.consentRevision != in.owner.consentRevision || in.header.expectedCount < 0 || in.header.expectedBytes < 0 || (in.header.expectedCount == 0) != (in.header.expectedBytes == 0) ||
		!effectiveBeginProducerShape(in.producer, in.entity, in.owner, in.header) {
		return nil, invalid("effective_begin_request")
	}
	for _, value := range []int64{in.entity.id, in.entity.libraryID, in.entity.revision, in.entity.sourceRevision, in.entity.identityRevision, in.entity.metadataRevision, in.entity.ownerRevision, in.owner.generation, in.owner.policyRevision, in.owner.consentRevision, in.producer.Selection.Revision, in.producer.Subject.Revision, in.producer.Evidence.SubjectID, in.producer.Capture.AssetID, in.producer.Capture.Generation} {
		if value <= 0 {
			return nil, invalid("effective_begin_native_identity")
		}
	}
	if in.owner.provider == "" || in.owner.selectedID == "" || in.producer.Publication.Record == "" ||
		(in.header.expectedCount == 0 && in.header.expectedManifest != effectiveBeginBuildSeed(in.header)) {
		return nil, invalid("effective_begin_declared_shape")
	}
	expiry, err := time.Parse(time.RFC3339Nano, in.owner.leaseUntil)
	if err != nil || in.owner.lease == "" {
		return nil, invalid("effective_begin_lease")
	}
	// Producer fields are all native values/structs with exported scalar fields;
	// fail closed if a future opaque/unowned field is added. No request codec is
	// duplicated: this is only string ownership before the shared full digest.
	for _, p := range text {
		*p = strings.Clone(*p)
	}
	if err := effectiveBeginOwnProducer(reflect.ValueOf(&in.producer).Elem(), true, &bytes); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(EffectiveStage, 1, identity, in)
	if err != nil {
		return nil, err
	}
	c := &effectiveStageBeginCommand{identity: identity, request: in, digest: digest, leaseExpiry: expiry, valid: true}
	if _, err := rand.Read(c.retirementIncarnation[:]); err != nil {
		return nil, err
	}
	return c, nil
}

func effectiveBeginOwnProducer(v reflect.Value, clone bool, total *uint64) error {
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).PkgPath != "" {
				return invalid("effective_begin_private_capture")
			}
			if err := effectiveBeginOwnProducer(v.Field(i), clone, total); err != nil {
				return err
			}
		}
	case reflect.String:
		s := v.String()
		if clone {
			v.SetString(strings.Clone(s))
			return nil
		}
		if uint64(len(s)) > maxCanonicalRequestBytes-*total || !utf8.ValidString(s) {
			return invalid("effective_begin_capture_text")
		}
		*total += uint64(len(s))
	case reflect.Array:
		if v.Type().Elem().Kind() != reflect.Uint8 {
			return invalid("effective_begin_capture_array")
		}
	case reflect.Int64, reflect.Bool, reflect.Float64:
	default:
		return invalid("effective_begin_capture_type")
	}
	return nil
}
