package writer

import (
	"context"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	normalizedEvidenceAlgorithm = "portico.normalized-evidence.sha256.v1"
	normalizedSourceAlgorithm   = "portico.normalized-credit-source.sha256.v1"
)

// This is the ONLY temporary data seam to W's one production implementation.
// No SQL handle, turn, callback, authority or proof flag crosses it. It is used
// BEFORE writer admission against a complete captured/charged native snapshot.
// Until W supplies its adapter there is no production implementation/fallback.
type enrichmentSealCodec interface {
	evidenceManifest(EvidenceStagePayload) (normalizedSealManifest, error)
	sourceManifest(normalizedSourceContent) (normalizedSealManifest, error)
}

type normalizedSealCommitment [32]byte // neither RequestDigest nor ResultDigest
type normalizedSealManifest struct {
	algorithm            string
	version, rows, bytes int64
	commitment           normalizedSealCommitment
}

// Exact immutable26-field upstream projection in approved f39cdeae. This is
// inert original native DATA, never a verified-reference capability.
type normalizedEvidenceReceipt struct {
	ownerID                                                                  string
	identity                                                                 CommandIdentity
	request                                                                  RequestDigest
	entityID, version                                                        int64
	evidenceID                                                               string
	subjectID                                                                int64
	parser, provider, kind, providerID, language, region, shape, observation string
	ownerGeneration                                                          int64
	buildID                                                                  string
	buildGeneration, revision                                                int64
	manifest                                                                 normalizedSealManifest
}
type normalizedSourceHeader struct {
	id                         string
	entityID                   int64
	provider, record, observed string
	count                      int64
}
type normalizedSourceRow struct {
	id                                                                                                                                string
	entityID                                                                                                                          int64
	provider, tupleDigest, record, creditID, providerPersonID, identityKey, personToken, targetKind, targetID, name, role, department string
	class                                                                                                                             CreditClass
	rawOrdinal                                                                                                                        int64
	billing                                                                                                                           EvidenceInteger
}
type normalizedSourceMember struct {
	publicationID          string
	entityID               int64
	provider               string
	position               int64
	sourceRowID, reference string
	row                    normalizedSourceRow
}
type normalizedSourceContent struct {
	upstream normalizedEvidenceReceipt
	header   normalizedSourceHeader
	members  []normalizedSourceMember
}

// Data preparation cannot establish native closure/provenance/current authority.
// W independently verifies those facts before entering a seal effect turn.
func prepareEvidenceSealManifest(ctx context.Context, codec enrichmentSealCodec, p EvidenceStagePayload, controls enrichmentControlCapture) (normalizedSealManifest, error) {
	if ctx == nil || ctx.Err() != nil {
		if ctx != nil {
			return normalizedSealManifest{}, ctx.Err()
		}
		return normalizedSealManifest{}, ErrInvalid
	}
	if _, err := preflightEnrichmentControls(controls); err != nil {
		return normalizedSealManifest{}, err
	}
	if codec == nil || !p.valid {
		return normalizedSealManifest{}, ErrResolveRequired
	}
	m, err := codec.evidenceManifest(p)
	if err != nil {
		return normalizedSealManifest{}, err
	}
	if m.algorithm != normalizedEvidenceAlgorithm || m.version != 1 || m.rows != int64(1+len(p.credits)) || m.bytes <= 0 || uint64(m.bytes) > maxCanonicalRequestBytes {
		return normalizedSealManifest{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return normalizedSealManifest{}, err
	}
	m.algorithm = normalizedEvidenceAlgorithm
	return m, nil
}

func prepareSourceSealManifest(ctx context.Context, codec enrichmentSealCodec, input normalizedSourceContent, controls enrichmentControlCapture) (normalizedSourceContent, normalizedSealManifest, error) {
	var zero normalizedSourceContent
	if ctx == nil {
		return zero, normalizedSealManifest{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return zero, normalizedSealManifest{}, err
	}
	if _, err := preflightEnrichmentControls(controls); err != nil {
		return zero, normalizedSealManifest{}, err
	}
	if codec == nil {
		return zero, normalizedSealManifest{}, ErrResolveRequired
	}
	if err := validateNormalizedSource(input); err != nil {
		return zero, normalizedSealManifest{}, err
	}
	// Validation/bounds above precede every copy. Clone all retained strings;
	// short substrings cannot keep uncharged provider/native buffers alive.
	owned := cloneNormalizedSource(input)
	m, err := codec.sourceManifest(owned)
	if err != nil {
		return zero, normalizedSealManifest{}, err
	}
	if m.algorithm != normalizedSourceAlgorithm || m.version != 1 || m.rows != int64(len(owned.members)) || m.bytes <= 0 || uint64(m.bytes) > maxCanonicalRequestBytes {
		return zero, normalizedSealManifest{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return zero, normalizedSealManifest{}, err
	}
	m.algorithm = normalizedSourceAlgorithm
	return owned, m, nil
}

func validateNormalizedSource(s normalizedSourceContent) error {
	h, u := s.header, s.upstream
	if h.entityID <= 0 || h.provider != "tmdb" || !lowerHex(h.id, 32) || h.count != int64(len(s.members)) || len(s.members) > MaxCastCredits+MaxCrewCredits || strings.TrimSpace(h.record) == "" || len(h.record) > 4096 || h.observed == "" ||
		u.ownerID == "" || u.identity.Sequence <= 0 || u.entityID <= 0 || u.version != 1 || u.subjectID <= 0 || u.evidenceID == "" || u.buildID == "" || u.ownerGeneration <= 0 || u.buildGeneration <= 0 || u.revision <= 0 ||
		u.provider != "tmdb" || u.kind != "movie" || u.parser != "tmdb.movie.header-selected-credits.v1" || u.shape != "tmdb.movie.detail+credits.header-selected-credits.v1" || !lowerHex(u.observation, 64) ||
		u.manifest.algorithm != normalizedEvidenceAlgorithm || u.manifest.version != 1 || u.manifest.rows != 1+h.count || u.manifest.bytes <= 0 {
		return invalid("source_manifest_context")
	}
	var originalBytes uint64
	for _, v := range []string{h.id, h.provider, h.record, h.observed, u.ownerID, u.evidenceID, u.parser, u.provider, u.kind, u.providerID, u.language, u.region, u.shape, u.observation, u.buildID, u.manifest.algorithm} {
		if uint64(len(v)) > maxCanonicalRequestBytes-originalBytes {
			return invalid("source_manifest_bytes")
		}
		originalBytes += uint64(len(v))
		if !sealNativeText(v) {
			return invalid("source_manifest_text")
		}
	}
	candidates := make([]ScreenCreditCandidate, len(s.members))
	for i, m := range s.members {
		r := m.row
		if m.publicationID != h.id || m.entityID != h.entityID || m.provider != h.provider || m.position != int64(i) || m.sourceRowID != r.id || r.entityID != h.entityID || r.provider != h.provider || !lowerHex(r.id, 32) || !lowerHex(r.tupleDigest, 64) ||
			(m.reference != "new" && m.reference != "exact" && m.reference != "ambiguous") || r.rawOrdinal < 0 || strings.TrimSpace(r.record) == "" ||
			(r.class == Cast && (!r.billing.Present || r.billing.Value < 0)) || (r.class == Crew && (r.billing.Present || r.billing.Value != 0)) || (r.class != Cast && r.class != Crew) {
			return invalid("source_manifest_member")
		}
		fields := []struct {
			value string
			limit int
		}{{r.record, 4096}, {r.creditID, 1024}, {r.providerPersonID, 1024}, {r.identityKey, 512}, {r.personToken, 256}, {r.targetKind, 64}, {r.targetID, 256}, {r.name, 1200}, {r.role, 2000}, {r.department, 800}}
		for _, f := range fields {
			if len(f.value) > f.limit || uint64(len(f.value)) > maxCanonicalRequestBytes-originalBytes || !sealNativeText(f.value) {
				return invalid("source_manifest_tuple_bytes")
			}
			originalBytes += uint64(len(f.value))
		}
		for j := 0; j < i; j++ {
			if s.members[j].sourceRowID == r.id {
				return invalid("source_manifest_duplicate")
			}
		}
		candidates[i] = ScreenCreditCandidate{r.class, r.billing.Value, r.rawOrdinal, r.creditID, r.role, r.department}
	}
	return ValidateRetainedCreditShape(candidates)
}

func sealNativeText(s string) bool { return utf8.ValidString(s) && strings.IndexByte(s, 0) < 0 }
func lowerHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, b := range []byte(s) {
		if (b < '0' || b > '9') && (b < 'a' || b > 'f') {
			return false
		}
	}
	return true
}

func cloneNormalizedSource(s normalizedSourceContent) normalizedSourceContent {
	h, u := &s.header, &s.upstream
	for _, p := range []*string{&h.id, &h.provider, &h.record, &h.observed, &u.ownerID, &u.evidenceID, &u.parser, &u.provider, &u.kind, &u.providerID, &u.language, &u.region, &u.shape, &u.observation, &u.buildID, &u.manifest.algorithm} {
		*p = strings.Clone(*p)
	}
	s.members = slices.Clone(s.members)
	for i := range s.members {
		m := &s.members[i]
		r := &m.row
		for _, p := range []*string{&m.publicationID, &m.provider, &m.sourceRowID, &m.reference, &r.id, &r.provider, &r.tupleDigest, &r.record, &r.creditID, &r.providerPersonID, &r.identityKey, &r.personToken, &r.targetKind, &r.targetID, &r.name, &r.role, &r.department} {
			*p = strings.Clone(*p)
		}
		if r.class == Cast {
			r.class = Cast
		} else {
			r.class = Crew
		}
	}
	return s
}
