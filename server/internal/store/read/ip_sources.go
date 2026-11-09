package read

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// IPSourceInput selects one nonmovie catalogue subject's accepted provider
// source. Provider and Role are the exact lowercase slot tuple; movie (kind 1)
// belongs to EN's FIRST path and is outside this recipe. The read returns
// accepted source facts only: never a pending observation, lease, writer handle
// or provider capability.
type IPSourceInput struct {
	View     BrowseInput
	Entity   int64
	Provider string
	Role     string
}

// IPSourcePolicy is the current provider policy/consent witness. ConsentRevision
// and LookupEpoch are provider-specific native state, never caller booleans.
type IPSourcePolicy struct {
	Enabled, ProviderEnabled, ScreenConfirmed, OnlineLookups bool
	Revision, ScreenRevision, LookupEpoch                    int64
	Incarnation                                              [16]byte
	Algorithm, RefreshMode, Region                           string
}

// IPSourcePublication is the exact accepted publication and applied operation.
type IPSourcePublication struct {
	ID, OperationID, ObservedAt                                    string
	SelectionRevision, Generation, PolicyRevision, ConsentRevision int64
}

// IPSourceEvidence is the exact IP-sealed evidence and its seal receipt.
type IPSourceEvidence struct {
	ID, Parser, Digest, ObservedAt, ReleaseGroupID, PrimaryType string
	Sealed, ProofSealed                                         bool
}

// IPSourceBuild is the evidence build owned by the accepting operation.
type IPSourceBuild struct {
	ID, State, OwnerOperation                 string
	Generation, ContentRevision, ExpectedRows int64
	MaterializedRows, MaterializedCharge      int64
}

// IPSourceFact is one accepted provider source. Present=false is honest absence
// (no accepted head), distinct from a pending or refused source. Accepted facts
// survive later policy/metadata revision advances; only a disabled provider or
// revoked provider-specific consent makes the source ineligible.
type IPSourceFact struct {
	Present                                                          bool
	Entity                                                           BrowseItem
	Provider, Role, SubjectKind, ProviderID, RequestedID, ObservedAt string
	HeadRevision                                                     int64
	Policy                                                           IPSourcePolicy
	Publication                                                      IPSourcePublication
	Evidence                                                         IPSourceEvidence
	Build                                                            IPSourceBuild
}

// IPMaterialInput selects one entity role's private artwork source facts.
type IPMaterialInput struct {
	View   BrowseInput
	Entity int64
	Role   string
}

// IPMaterialRendition is one ready encoded rendition. Digests are opaque.
type IPMaterialRendition struct {
	Digest, MIME, Status string
	Width, Height, Bytes int64
}

// IPMaterialFact is the current artwork source fact for one role. Locked with
// Present=false is an explicit owner clear; matched has a ready source. The
// selection/source revisions and digests are the exact witness for the caller's
// own recheck.
type IPMaterialFact struct {
	Present, Locked                                           bool
	Role, Choice                                              string
	SelectionRevision, SourceRevision, EncoderRevision        int64
	Provider, ImageID, Origin, Attribution                    string
	Locale, EffectiveLanguage, SourceFence, Actor, ObservedAt string
	AcceptedMIME                                              string
	AcceptedWidth, AcceptedHeight, LargeEdge                  int64
	SmallOutlier, LargeOutlier                                bool
	Small, Large                                              IPMaterialRendition
}

type ipReadCalls struct {
	n           int
	publication bool
}

// ipScope keeps GV's snapshot lifecycle while exposing only this closed recipe.
// Exact points are preflighted in the same snapshot before any variable payload.
type ipScope struct {
	base              browseSnapshots
	scope             *governor.ReadScope
	extra             []ipReadCalls
	rootCalls         int
	copied, copyLimit int64
}

func (s *ipScope) NativeRowReservation() int64 { return s.base.NativeRowReservation() }
func (s *ipScope) Read(ctx context.Context, r governor.Request, b time.Duration, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	s.extra = append(s.extra, ipReadCalls{})
	callback := func(scope *governor.ReadScope) error {
		s.scope = scope
		defer func() { s.scope = nil }()
		return fn(scope)
	}
	if pool, ok := s.base.(interface {
		ReadForecasted(context.Context, governor.Request, time.Duration, governor.SnapshotForecast, func(*governor.ReadScope) error) (governor.SnapshotResult, error)
	}); ok {
		return pool.ReadForecasted(ctx, r, b, governor.SnapshotForecast{NativeRowBytes: s.NativeRowReservation(), MaxQueries: uint16(min(uint32(4096), r.Budget.RawRows))}, callback)
	}
	return s.base.Read(ctx, r, b, callback)
}

// point is the exact two-query preflight: matching octet lengths first, then the
// payload with identical bindings in one immutable snapshot.
func (s *ipScope) point(name string, variable []int, args ...any) ([]any, error) {
	row, e := s.scope.QueryExactBytes(stmt.ID("read.ip."+name+"_bytes"), stmt.ID("read.ip."+name), variable, args...)
	s.extra[len(s.extra)-1].n += 2
	if e != nil {
		return nil, e
	}
	s.copied += 64 + int64(len(row))*32
	for _, v := range row {
		switch v := v.(type) {
		case string:
			s.copied += 2 * int64(len(v))
		case []byte:
			s.copied += 3 * int64(len(v))
		}
	}
	if s.copied > s.copyLimit {
		return nil, governor.ErrSnapshotBudget
	}
	return row, nil
}

// sincePublication attributes exact point pairs to the lookup or publication
// physical window without changing the shared counters.
func (s *ipScope) sincePublication(result *BrowseReadResult) {
	for _, calls := range s.extra {
		if calls.publication {
			result.PublicationStatements += calls.n
		} else {
			result.LookupStatements += calls.n
		}
	}
}

// ipVerifyRanges proves a bounded range payload still matches the lengths
// measured immediately before it in the same snapshot. A count or length drift
// is refused, never served truncated or from a column maximum.
func ipVerifyRanges(sizes, rows [][]any, variable []int) error {
	if len(sizes) != len(rows) || len(variable) == 0 {
		return ErrUnqualified
	}
	for i := range rows {
		if len(sizes[i]) != len(variable) {
			return ErrUnqualified
		}
		for n, col := range variable {
			if col < 0 || col >= len(rows[i]) {
				return ErrUnqualified
			}
			switch v := rows[i][col].(type) {
			case nil:
				if sizes[i][n] != nil {
					return ErrUnqualified
				}
			case string:
				length, ok := sizes[i][n].(int64)
				if !ok || length != int64(len(v)) {
					return ErrUnqualified
				}
			case []byte:
				length, ok := sizes[i][n].(int64)
				if !ok || length != int64(len(v)) {
					return ErrUnqualified
				}
			default:
				return ErrUnqualified
			}
		}
	}
	return nil
}

func ipString(v any) (string, error) {
	text, ok := v.(string)
	if !ok || !utf8.ValidString(text) || strings.IndexByte(text, 0) >= 0 || len(text) > 4<<20 {
		return "", ErrUnqualified
	}
	return strings.Clone(text), nil
}
func ipBlob16(v any) ([16]byte, error) {
	b, ok := v.([]byte)
	if !ok || len(b) != 16 {
		return [16]byte{}, ErrUnqualified
	}
	var out [16]byte
	copy(out[:], b)
	return out, nil
}
func ipPositive(v any) (int64, error) {
	n, ok := v.(int64)
	if !ok || n <= 0 {
		return 0, ErrUnqualified
	}
	return n, nil
}
func ipNonNegative(v any) (int64, error) {
	n, ok := v.(int64)
	if !ok || n < 0 {
		return 0, ErrUnqualified
	}
	return n, nil
}

// ipSourcePair is the closed provider/role vocabulary. No alias such as mb,
// album or release-group substitutes for it.
func ipSourcePair(provider, role string) bool {
	switch provider {
	case "tmdb", "tvdb":
		return role == "primary"
	case "musicbrainz":
		return role == "primary" || role == "release_track"
	}
	return false
}

// IPProviderSource is harness-only and requires ConsumeForeground custody. It
// discloses no SQL, connection, writer or provider capability.
func (r *ForegroundReader) IPProviderSource(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IPSourceInput) (IPSourceFact, BrowseReadResult, error) {
	if r == nil {
		return IPSourceFact{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (IPSourceFact, BrowseReadResult, error) {
		in.View.Now = at
		return r.ipProviderSource(ctx, request, budget, output, in)
	})
}

func (r *ForegroundReader) ipProviderSource(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IPSourceInput) (IPSourceFact, BrowseReadResult, error) {
	if r == nil || in.Entity <= 0 || in.View.Library <= 0 || in.View.Kind < 2 || in.View.Kind > 10 || in.View.After != nil || in.View.Descending || in.View.KeepArticles || !ipSourcePair(in.Provider, in.Role) {
		return IPSourceFact{}, BrowseReadResult{}, ErrInput
	}
	s := &ipScope{base: r.snapshots, copyLimit: output.Request.Need[governor.MemoryBytes] / 4}
	var fixed, policy IPSourcePolicy
	var eligible bool
	var headSource []any
	root := func(q browseQuery) ([]any, error) {
		s.rootCalls++
		publication := s.rootCalls%2 == 0
		s.extra[len(s.extra)-1].publication = publication
		if !publication {
			headSource = nil
		}
		row, e := q("read.ip.policy", in.Provider, int64(1), int64(1), in.Entity, in.View.Library, in.View.Kind)
		if errors.Is(e, sql.ErrNoRows) {
			return nil, ErrDenied
		}
		if e != nil {
			return nil, e
		}
		if len(row) != 1 {
			return nil, ErrDenied
		}
		fact, e := ipPolicyFact(row[0], in)
		if e != nil {
			return nil, e
		}
		fixed = fact.policy
		eligible = fact.eligible
		if publication && eligible {
			// The accepted head is re-measured and compared in the fresh
			// publication window; a changed tuple retries instead of serving a
			// mixed snapshot.
			now, e := s.point("head", ipSourceHeadVariable, in.Provider, in.Role, in.Entity, in.View.Library, in.View.Kind)
			if errors.Is(e, sql.ErrNoRows) {
				now = nil
			} else if e != nil {
				return nil, e
			}
			if !equalSource(headSource, now) {
				return nil, ErrStale
			}
		}
		return row[0], nil
	}
	steps := []readStep[IPSourceFact]{
		func(ctx context.Context, q browseQuery, f browseFence, d *IPSourceFact) error {
			if !eligible {
				return ctx.Err()
			}
			row, e := s.point("policy_text", []int{0, 1, 2}, in.View.Library, in.Provider)
			if e != nil {
				return e
			}
			algorithm, e1 := ipString(row[0])
			refresh, e2 := ipString(row[1])
			region, e3 := ipString(row[2])
			if e1 != nil || e2 != nil || e3 != nil {
				return ErrUnqualified
			}
			policy = fixed
			policy.Algorithm, policy.RefreshMode, policy.Region = algorithm, refresh, region
			d.Policy = policy
			return ctx.Err()
		},
		func(ctx context.Context, q browseQuery, f browseFence, d *IPSourceFact) error {
			if !eligible {
				return ctx.Err()
			}
			row, e := s.point("head", ipSourceHeadVariable, in.Provider, in.Role, in.Entity, in.View.Library, in.View.Kind)
			if errors.Is(e, sql.ErrNoRows) {
				headSource = nil
				return ctx.Err()
			}
			if e != nil {
				return e
			}
			fact, e := ipHeadFact(row, in)
			if e != nil {
				return e
			}
			headSource = row
			fact.Policy = policy
			*d = fact
			return ctx.Err()
		},
		func(ctx context.Context, q browseQuery, f browseFence, d *IPSourceFact) error {
			if !d.Present {
				return ctx.Err()
			}
			row, e := s.point("build", ipSourceBuildVariable, d.Evidence.ID)
			if errors.Is(e, sql.ErrNoRows) {
				return ErrUnqualified
			}
			if e != nil {
				return e
			}
			build, e := ipBuildFact(row)
			if e != nil {
				return e
			}
			if build.OwnerOperation != d.Publication.OperationID || d.Publication.PolicyRevision > d.Policy.Revision {
				return ErrUnqualified
			}
			d.Build = build
			return ctx.Err()
		},
	}
	fact, result, e := publishRead(ctx, s, request, budget, output, in.View, steps, root)
	if e == nil && !fact.Present {
		// Absent accepted head carries the current policy witness but no entity
		// copy: there is no accepted variable payload to disclose.
		if policy.Revision == 0 {
			policy = fixed
		}
		fact.Policy = policy
	}
	s.sincePublication(&result)
	return fact, result, e
}

type ipPolicyResult struct {
	policy   IPSourcePolicy
	eligible bool
}

func ipPolicyFact(row []any, in IPSourceInput) (ipPolicyResult, error) {
	if len(row) != 16 {
		return ipPolicyResult{}, ErrUnqualified
	}
	id, e1 := ipPositive(row[0])
	library, e2 := ipPositive(row[1])
	kind, e3 := ipPositive(row[2])
	retired, e4 := ipNonNegative(row[3])
	_, e5 := ipBlob16(row[4])
	metadataRevision, e6 := ipPositive(row[5])
	sourceRevision, e7 := ipPositive(row[6])
	identityRevision, e8 := ipPositive(row[7])
	enabled, e9 := ipNonNegative(row[8])
	policyRevision, e10 := ipPositive(row[9])
	policyIncarnation, e11 := ipBlob16(row[10])
	providerEnabled, e12 := ipNonNegative(row[11])
	confirmed, e13 := ipNonNegative(row[12])
	screenRevision, e14 := ipPositive(row[13])
	online, e15 := ipNonNegative(row[14])
	epoch, e16 := ipPositive(row[15])
	for _, e := range []error{e1, e2, e3, e4, e5, e6, e7, e8, e9, e10, e11, e12, e13, e14, e15, e16} {
		if e != nil {
			return ipPolicyResult{}, ErrUnqualified
		}
	}
	if id != in.Entity || library != in.View.Library || kind != in.View.Kind || retired != 0 || metadataRevision <= 0 || sourceRevision <= 0 || identityRevision <= 0 {
		return ipPolicyResult{}, ErrDenied
	}
	if enabled > 1 || providerEnabled > 1 || confirmed > 1 || online > 1 {
		return ipPolicyResult{}, ErrUnqualified
	}
	p := IPSourcePolicy{Enabled: enabled == 1, ProviderEnabled: providerEnabled == 1, ScreenConfirmed: confirmed == 1, OnlineLookups: online == 1, Revision: policyRevision, ScreenRevision: screenRevision, LookupEpoch: epoch, Incarnation: policyIncarnation}
	elig := p.Enabled && p.ProviderEnabled
	switch in.Provider {
	case "tmdb", "tvdb":
		elig = elig && p.ScreenConfirmed
	case "musicbrainz":
		elig = elig && p.OnlineLookups
	}
	return ipPolicyResult{policy: p, eligible: elig}, nil
}

func ipHeadFact(row []any, in IPSourceInput) (IPSourceFact, error) {
	if len(row) != 44 {
		return IPSourceFact{}, ErrUnqualified
	}
	entity, e1 := ipPositive(row[0])
	publicID, e2 := ipBlob16(row[1])
	title, e3 := ipString(row[2])
	revision, e4 := ipPositive(row[3])
	retired, e5 := ipNonNegative(row[4])
	library, e6 := ipPositive(row[5])
	kind, e7 := ipPositive(row[6])
	provider, e8 := ipString(row[7])
	role, e9 := ipString(row[8])
	subjectKind, e10 := ipString(row[9])
	providerID, e11 := ipString(row[10])
	requestedID, e12 := ipString(row[11])
	headRevision, e13 := ipPositive(row[12])
	headObserved, e14 := ipString(row[13])
	publicationID, e15 := ipString(row[14])
	decision, e16 := ipString(row[15])
	selectionRevision, e17 := ipPositive(row[16])
	generation, e18 := ipPositive(row[17])
	publicationObserved, e19 := ipString(row[18])
	operation, e20 := ipString(row[19])
	evidenceID, e21 := ipString(row[20])
	parser, e22 := ipString(row[21])
	digest, e23 := ipString(row[22])
	evidenceObserved, e24 := ipString(row[23])
	sealed, e25 := ipNonNegative(row[24])
	sealOwner, e26 := ipString(row[25])
	sealSequence, e27 := ipPositive(row[26])
	sealIncarnation, e28 := ipBlob16(row[27])
	releaseGroup, e29 := ipString(row[28])
	primaryType, e30 := ipString(row[29])
	operationIncarnation, e31 := ipBlob16(row[30])
	status, e32 := ipString(row[31])
	operationGeneration, e33 := ipPositive(row[32])
	_, e34 := ipBlob16(row[33])
	policyRevision, e35 := ipPositive(row[34])
	consentRevision, e36 := ipPositive(row[35])
	algorithm, e37 := ipString(row[36])
	refreshMode, e38 := ipString(row[37])
	receiptKind, e39 := ipString(row[38])
	receiptRevision, e40 := ipPositive(row[39])
	appliedMS, e41 := ipNonNegative(row[40])
	receiptSubject, e42 := ipPositive(row[41])
	receiptKindName, e43 := ipString(row[42])
	receiptProviderID, e44 := ipString(row[43])
	for _, e := range []error{e1, e2, e3, e4, e5, e6, e7, e8, e9, e10, e11, e12, e13, e14, e15, e16, e17, e18, e19, e20, e21, e22, e23, e24, e25, e26, e27, e28, e29, e30, e31, e32, e33, e34, e35, e36, e37, e38, e39, e40, e41, e42, e43, e44} {
		if e != nil {
			return IPSourceFact{}, ErrUnqualified
		}
	}
	if entity != in.Entity || library != in.View.Library || kind != in.View.Kind || retired != 0 || provider != in.Provider || role != in.Role {
		return IPSourceFact{}, ErrUnqualified
	}
	switch subjectKind {
	case "series", "season", "episode", "collection", "artist", "release", "recording":
	default:
		return IPSourceFact{}, ErrUnqualified
	}
	if decision != "accepted" || status != "applied" || receiptKind != "evidence_seal" || receiptKindName != subjectKind || receiptProviderID != providerID {
		return IPSourceFact{}, ErrUnqualified
	}
	if sealed != 1 || sealOwner != operation || sealSequence <= 0 || generation != operationGeneration || selectionRevision != headRevision {
		return IPSourceFact{}, ErrUnqualified
	}
	if len(digest) != 64 || consentRevision <= 0 || receiptRevision <= 0 || providerID == "" || publicationID == "" || operation == "" || evidenceID == "" || parser == "" || algorithm == "" || refreshMode == "" {
		return IPSourceFact{}, ErrUnqualified
	}
	if sealIncarnation != operationIncarnation {
		return IPSourceFact{}, ErrUnqualified
	}
	_ = receiptSubject
	_ = appliedMS
	return IPSourceFact{
		Present:      true,
		Entity:       BrowseItem{ID: entity, PublicID: publicID, Title: title, Revision: revision},
		Provider:     provider,
		Role:         role,
		SubjectKind:  subjectKind,
		ProviderID:   providerID,
		RequestedID:  requestedID,
		ObservedAt:   headObserved,
		HeadRevision: headRevision,
		Publication:  IPSourcePublication{ID: publicationID, OperationID: operation, ObservedAt: publicationObserved, SelectionRevision: selectionRevision, Generation: generation, PolicyRevision: policyRevision, ConsentRevision: consentRevision},
		Evidence:     IPSourceEvidence{ID: evidenceID, Parser: parser, Digest: digest, ObservedAt: evidenceObserved, ReleaseGroupID: releaseGroup, PrimaryType: primaryType, Sealed: true, ProofSealed: true},
	}, nil
}

func ipBuildFact(row []any) (IPSourceBuild, error) {
	if len(row) != 9 {
		return IPSourceBuild{}, ErrUnqualified
	}
	id, e1 := ipString(row[0])
	state, e2 := ipString(row[1])
	generation, e3 := ipPositive(row[2])
	contentRevision, e4 := ipPositive(row[3])
	owner, e5 := ipString(row[4])
	e6 := error(nil)
	if row[5] != nil {
		_, e6 = ipNonNegative(row[5])
	}
	expected, e7 := ipPositive(row[6])
	materialized, e8 := ipNonNegative(row[7])
	charge, e9 := ipNonNegative(row[8])
	for _, e := range []error{e1, e2, e3, e4, e5, e6, e7, e8, e9} {
		if e != nil {
			return IPSourceBuild{}, ErrUnqualified
		}
	}
	if id == "" || state == "" || owner == "" {
		return IPSourceBuild{}, ErrUnqualified
	}
	return IPSourceBuild{ID: id, State: state, OwnerOperation: owner, Generation: generation, ContentRevision: contentRevision, ExpectedRows: expected, MaterializedRows: materialized, MaterializedCharge: charge}, nil
}

// ipMaterialRoles is the closed public art role vocabulary. Page-slot aliases
// are resolved by RS; this private read accepts only exact stored roles.
func ipMaterialRoles(role string) bool {
	switch role {
	case "poster", "cover", "portrait", "square", "backdrop", "logo", "still", "banner":
		return true
	}
	return false
}

// IPArtworkMaterial is harness-only and requires ConsumeForeground custody. It
// returns the source/revision/digest facts for RS/RB, never artwork bytes, paths,
// provider URLs or a writer handle.
func (r *ForegroundReader) IPArtworkMaterial(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IPMaterialInput) (IPMaterialFact, BrowseReadResult, error) {
	if r == nil {
		return IPMaterialFact{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (IPMaterialFact, BrowseReadResult, error) {
		in.View.Now = at
		return r.ipArtworkMaterial(ctx, request, budget, output, in)
	})
}

func (r *ForegroundReader) ipArtworkMaterial(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IPMaterialInput) (IPMaterialFact, BrowseReadResult, error) {
	if r == nil || in.Entity <= 0 || in.View.Library <= 0 || in.View.Kind < 1 || in.View.Kind > 10 || in.View.After != nil || in.View.Descending || in.View.KeepArticles || !ipMaterialRoles(in.Role) {
		return IPMaterialFact{}, BrowseReadResult{}, ErrInput
	}
	s := &ipScope{base: r.snapshots, copyLimit: output.Request.Need[governor.MemoryBytes] / 4}
	var locked, matched []any
	root := func(q browseQuery) ([]any, error) {
		s.rootCalls++
		s.extra[len(s.extra)-1].publication = s.rootCalls%2 == 0
		row, e := q("read.ip.material_scope", in.Entity, in.View.Library, in.View.Kind)
		if errors.Is(e, sql.ErrNoRows) {
			return nil, ErrDenied
		}
		if e != nil {
			return nil, e
		}
		if len(row) != 1 {
			return nil, ErrDenied
		}
		return row[0], nil
	}
	read := func(choice string) ([]any, error) {
		row, e := s.point("material", ipMaterialVariable, in.View.Library, in.View.Kind, in.Entity, in.Role, choice)
		if errors.Is(e, sql.ErrNoRows) {
			return nil, nil
		}
		return row, e
	}
	steps := []readStep[IPMaterialFact]{
		func(ctx context.Context, q browseQuery, f browseFence, d *IPMaterialFact) error {
			row, e := read("locked")
			if e != nil {
				return e
			}
			if row != nil && !ipMaterialRow(row, in, "locked") {
				return ErrUnqualified
			}
			locked = row
			return ctx.Err()
		},
		func(ctx context.Context, q browseQuery, f browseFence, d *IPMaterialFact) error {
			row, e := read("matched")
			if e != nil {
				return e
			}
			if row != nil && !ipMaterialRow(row, in, "matched") {
				return ErrUnqualified
			}
			matched = row
			return ctx.Err()
		},
		func(ctx context.Context, q browseQuery, f browseFence, d *IPMaterialFact) error {
			// Re-read both choices in one fresh window: a lock appearing or a
			// selection digest/revision changing between windows must retry.
			nowLocked, e := read("locked")
			if e != nil {
				return e
			}
			nowMatched, e := read("matched")
			if e != nil {
				return e
			}
			if !equalSource(locked, nowLocked) || !equalSource(matched, nowMatched) {
				return ErrStale
			}
			chosen := locked
			if chosen == nil {
				chosen = matched
			}
			if chosen == nil {
				return ctx.Err()
			}
			fact, e := ipMaterialFact(chosen, in)
			if e != nil {
				return e
			}
			*d = fact
			return ctx.Err()
		},
	}
	fact, result, e := publishRead(ctx, s, request, budget, output, in.View, steps, root)
	s.sincePublication(&result)
	return fact, result, e
}

func ipMaterialRow(row []any, in IPMaterialInput, choice string) bool {
	if len(row) != 38 {
		return false
	}
	kind, ok := row[0].(string)
	return ok && kind == "entity" && row[1] == in.Entity && row[2] == in.Role && row[3] == choice
}

func ipMaterialFact(row []any, in IPMaterialInput) (IPMaterialFact, error) {
	role, e1 := ipString(row[2])
	choice, e2 := ipString(row[3])
	revision, e3 := ipPositive(row[5])
	if e1 != nil || e2 != nil || e3 != nil || role != in.Role || (choice != "matched" && choice != "locked") {
		return IPMaterialFact{}, ErrUnqualified
	}
	fact := IPMaterialFact{Role: role, Choice: choice, SelectionRevision: revision, Locked: choice == "locked"}
	if row[4] == nil {
		// A locked choice with a NULL digest is an explicit owner clear, not a
		// pending source. It stops matched fallback and carries no rendition.
		if choice != "locked" {
			return IPMaterialFact{}, ErrUnqualified
		}
		return fact, nil
	}
	digest, e4 := ipString(row[4])
	provider, e5 := ipString(row[6])
	imageID, e6 := ipString(row[7])
	origin, e7 := ipString(row[8])
	attribution, e8 := ipString(row[9])
	locale, e9 := ipString(row[10])
	language, e10 := ipString(row[11])
	fence, e11 := ipString(row[12])
	sourceObject, e12 := ipOptionalString(row[13])
	sourceRevisionText, e13 := ipOptionalString(row[14])
	actor, e14 := ipString(row[15])
	observed, e15 := ipString(row[16])
	mime, e16 := ipString(row[17])
	width, e17 := ipPositive(row[18])
	height, e18 := ipPositive(row[19])
	edge, e19 := ipPositive(row[20])
	smallOutlier, e20 := ipNonNegative(row[21])
	largeOutlier, e21 := ipNonNegative(row[22])
	encoder, e22 := ipPositive(row[23])
	sourceRevision, e23 := ipPositive(row[24])
	retiring, e24 := ipNonNegative(row[25])
	small, se := ipMaterialRendition(row, 26)
	large, le := ipMaterialRendition(row, 32)
	for _, e := range []error{e4, e5, e6, e7, e8, e9, e10, e11, e12, e13, e14, e15, e16, e17, e18, e19, e20, e21, e22, e23, e24, se, le} {
		if e != nil {
			return IPMaterialFact{}, ErrUnqualified
		}
	}
	if len(digest) != 64 || retiring != 0 || fence == "" || actor == "" || observed == "" || (sourceObject == "") != (sourceRevisionText == "") {
		return IPMaterialFact{}, ErrUnqualified
	}
	switch mime {
	case "image/jpeg", "image/png", "image/webp":
	default:
		return IPMaterialFact{}, ErrUnqualified
	}
	fact.Present = true
	fact.Provider, fact.ImageID, fact.Origin, fact.Attribution = provider, imageID, origin, attribution
	fact.Locale, fact.EffectiveLanguage, fact.SourceFence, fact.Actor, fact.ObservedAt = locale, language, fence, actor, observed
	fact.AcceptedMIME, fact.AcceptedWidth, fact.AcceptedHeight, fact.LargeEdge = mime, width, height, edge
	fact.SmallOutlier, fact.LargeOutlier = smallOutlier == 1, largeOutlier == 1
	fact.SourceRevision, fact.EncoderRevision = sourceRevision, encoder
	fact.Small, fact.Large = small, large
	return fact, nil
}

func ipOptionalString(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	return ipString(v)
}

func ipMaterialRendition(row []any, base int) (IPMaterialRendition, error) {
	digest, e1 := ipString(row[base])
	mime, e2 := ipString(row[base+1])
	width, e3 := ipPositive(row[base+2])
	height, e4 := ipPositive(row[base+3])
	bytes, e5 := ipPositive(row[base+4])
	status, e6 := ipString(row[base+5])
	for _, e := range []error{e1, e2, e3, e4, e5, e6} {
		if e != nil {
			return IPMaterialRendition{}, ErrUnqualified
		}
	}
	if len(digest) != 64 || status != "ready" {
		return IPMaterialRendition{}, ErrUnqualified
	}
	switch mime {
	case "image/jpeg", "image/png":
	default:
		return IPMaterialRendition{}, ErrUnqualified
	}
	return IPMaterialRendition{Digest: digest, MIME: mime, Width: width, Height: height, Bytes: bytes, Status: status}, nil
}
