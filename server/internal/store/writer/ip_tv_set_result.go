package writer

import (
	"errors"
	"unicode/utf8"
)

// ipTVSetResult is the COMPLETE immutable saved result of one IP TV publication
// set command (begin/page/seal). Every field is derived from the original
// request and its exact page/member manifest, so a replay never reconstructs it
// from mutable set, page, owner or lease state. It pins the provider set
// identity and its complete ordered member digest; it never accepts a source,
// writes a catalogue identity or clears an undeclared child.
type ipTVSetResult struct {
	Kind, OperationID, SetID, Provider, SubjectKind, ProviderID, EpisodeOrder, Lease string
	EvidenceID, BuildID                                                              string
	EntityID, SourceRevision, HierarchyRevision, SelectionRevision                   int64
	MemberCount                                                                      int64
	MemberDigest                                                                     [32]byte
	Page, NextPage                                                                   int64
	NextPagePresent                                                                  bool
	PageRows, PageBytes                                                              int64
	PageDigest                                                                       [32]byte
	Revision, OwnerGeneration, BuildGeneration, LeaseUntilMS, AppliedMS              int64
	SealVersion, SealRows, SealBytes                                                 int64
	Commitment                                                                       [32]byte
}

const ipTVSetResultFixedBytes = 4096

func ipTVSetResultDigest(id CommandIdentity, request RequestDigest, v ipTVSetResult) (ResultDigest, error) {
	return canonicalResultDigest(id, request, v)
}

func ipTVSetResultKind(value string) bool {
	return value == "begin" || value == "page" || value == "seal"
}

func ipTVSetDigestSet(value [32]byte) bool {
	var zero [32]byte
	return value != zero
}

func preflightIPTVSetResult(v ipTVSetResult) error {
	remaining := maxCanonicalResultBytes - ipTVSetResultFixedBytes
	for _, s := range [...]string{
		v.Kind, v.OperationID, v.SetID, v.Provider, v.SubjectKind, v.ProviderID,
		v.EpisodeOrder, v.Lease, v.EvidenceID, v.BuildID,
	} {
		if len(s) > remaining || !utf8.ValidString(s) {
			return ErrInvalid
		}
		remaining -= len(s)
	}
	if !ipTVSetResultKind(v.Kind) || len(v.OperationID) != 32 ||
		!ipTVSetText(v.SetID, ipTVSetIDMax) || len(v.SetID) == 0 ||
		!ipPolicyProviderCode(v.Provider) || !ipTVSetSubjectKind(v.SubjectKind) ||
		!ipTVSetText(v.ProviderID, ipTVSetIDMax) || len(v.ProviderID) == 0 ||
		!ipTVSetOrder(v.EpisodeOrder) || len(v.Lease) != 32 ||
		v.EntityID <= 0 || v.SourceRevision <= 0 || v.HierarchyRevision < 0 ||
		v.SelectionRevision < 0 || v.MemberCount <= 0 || v.MemberCount > ipTVSetMembersMax ||
		v.Revision <= 0 || v.OwnerGeneration <= 0 || v.LeaseUntilMS <= 0 || v.AppliedMS < 0 {
		return ErrInvalid
	}
	switch v.Kind {
	case "begin":
		if !ipTVSetText(v.EvidenceID, ipTVSetIDMax) || len(v.EvidenceID) == 0 ||
			!ipTVSetText(v.BuildID, ipTVSetIDMax) || len(v.BuildID) == 0 ||
			v.BuildGeneration <= 0 || !ipTVSetDigestSet(v.MemberDigest) ||
			v.Revision != 1 || v.Page != 0 || v.NextPagePresent || v.NextPage != 0 ||
			v.PageRows != 0 || v.PageBytes != 0 || !ipTVSetZeroDigest(v.PageDigest) ||
			v.SealVersion != 0 || v.SealRows != 0 || v.SealBytes != 0 ||
			!ipTVSetZeroDigest(v.Commitment) {
			return ErrInvalid
		}
	case "page":
		if v.EvidenceID != "" || v.BuildID != "" || v.BuildGeneration != 0 ||
			!ipTVSetZeroDigest(v.MemberDigest) || v.Revision <= 1 ||
			v.Page < 0 || v.Page >= ipTVSetPagesMax ||
			(v.NextPagePresent && v.NextPage != v.Page+1) ||
			v.PageRows <= 0 || v.PageRows > ipTVSetPageMax || v.PageBytes <= 0 ||
			!ipTVSetDigestSet(v.PageDigest) || v.SealVersion != 0 || v.SealRows != 0 ||
			v.SealBytes != 0 || !ipTVSetZeroDigest(v.Commitment) {
			return ErrInvalid
		}
	case "seal":
		if !ipTVSetText(v.EvidenceID, ipTVSetIDMax) || len(v.EvidenceID) == 0 ||
			!ipTVSetText(v.BuildID, ipTVSetIDMax) || len(v.BuildID) == 0 ||
			v.BuildGeneration <= 0 || !ipTVSetDigestSet(v.MemberDigest) ||
			v.Revision <= 1 || v.Page != 0 || v.NextPagePresent || v.NextPage != 0 ||
			v.PageRows != 0 || v.PageBytes != 0 || !ipTVSetZeroDigest(v.PageDigest) ||
			v.SealVersion != ipTVSetVersion || v.SealRows != v.MemberCount ||
			v.SealBytes <= 0 || !ipTVSetDigestSet(v.Commitment) {
			return ErrInvalid
		}
	}
	return nil
}

func ipTVSetZeroDigest(value [32]byte) bool {
	return value == [32]byte{}
}

func writeIPTVSetResult(e *requestEncoder, id CommandIdentity, request RequestDigest, v ipTVSetResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("ip_tv_set")
	e.text(v.Kind)
	e.text("applied")
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(request[:])
	e.text(v.OperationID)
	e.text(v.SetID)
	e.text(v.Provider)
	e.text(v.SubjectKind)
	e.text(v.ProviderID)
	e.text(v.EpisodeOrder)
	e.text(v.Lease)
	e.text(v.EvidenceID)
	e.text(v.BuildID)
	e.number(uint64(v.EntityID))
	e.number(uint64(v.SourceRevision))
	e.number(uint64(v.HierarchyRevision))
	e.number(uint64(v.SelectionRevision))
	e.number(uint64(v.MemberCount))
	e.bytes(v.MemberDigest[:])
	e.number(uint64(v.Page))
	e.number(uint64(v.NextPage))
	e.flag(v.NextPagePresent)
	e.number(uint64(v.PageRows))
	e.number(uint64(v.PageBytes))
	e.bytes(v.PageDigest[:])
	e.number(uint64(v.Revision))
	e.number(uint64(v.OwnerGeneration))
	e.number(uint64(v.BuildGeneration))
	e.number(uint64(v.LeaseUntilMS))
	e.number(uint64(v.AppliedMS))
	e.number(uint64(v.SealVersion))
	e.number(uint64(v.SealRows))
	e.number(uint64(v.SealBytes))
	e.bytes(v.Commitment[:])
	if e.err != nil {
		return errors.Join(invalid("ip_tv_set_result"), e.err)
	}
	return nil
}
