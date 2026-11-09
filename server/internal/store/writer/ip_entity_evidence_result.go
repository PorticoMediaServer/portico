package writer

import (
	"errors"
	"unicode/utf8"
)

// ipEntityEvidenceResult is the COMPLETE immutable saved result of one
// nonmovie evidence begin/append. Every field is derived from the original
// request, identity and receipt header, so a replay never reconstructs it from
// mutable build, lease or provider state. It never claims a seal, a complete
// manifest or an accepted source: begin only materializes the header and an
// open build, and append only advances that build's bounded cursor.
type ipEntityEvidenceResult struct {
	Kind, OperationID, EvidenceID, BuildID, Provider, SubjectKind, ProviderID, ParserVersion, Family string
	Lease                                                                                            string
	SubjectID, BuildGeneration, Revision, MaterializedRows, ResultCount, ResultBytes                 int64
	LeaseUntilMS, AppliedMS                                                                          int64
}

const ipEntityEvidenceResultFixedBytes = 4096

func ipEntityEvidenceResultDigest(id CommandIdentity, request RequestDigest, v ipEntityEvidenceResult) (ResultDigest, error) {
	return canonicalResultDigest(id, request, v)
}

func ipEntityEvidenceKind(value string) bool {
	return value == "begin" || value == "append"
}

func preflightIPEntityEvidenceResult(v ipEntityEvidenceResult) error {
	remaining := maxCanonicalResultBytes - ipEntityEvidenceResultFixedBytes
	for _, s := range [...]string{
		v.Kind, v.OperationID, v.EvidenceID, v.BuildID, v.Provider, v.SubjectKind,
		v.ProviderID, v.ParserVersion, v.Family, v.Lease,
	} {
		if len(s) > remaining || !utf8.ValidString(s) {
			return ErrInvalid
		}
		remaining -= len(s)
	}
	if !ipEntityEvidenceKind(v.Kind) || len(v.OperationID) != 32 ||
		!ipEvidenceText(v.EvidenceID, ipEvidenceIDMax) || !ipEvidenceText(v.BuildID, ipEvidenceIDMax) ||
		!ipPolicyProviderCode(v.Provider) || !ipEvidenceSubjectKind(v.SubjectKind) ||
		!ipEvidenceText(v.ProviderID, ipEvidenceIDMax) || !ipEvidenceText(v.ParserVersion, ipEvidenceTextMax) ||
		len(v.Lease) != 32 || v.SubjectID <= 0 || v.BuildGeneration <= 0 || v.Revision <= 0 || v.MaterializedRows < 1 ||
		v.ResultCount < 0 || v.ResultBytes < 0 || v.LeaseUntilMS <= 0 || v.AppliedMS < 0 {
		return ErrInvalid
	}
	switch v.Kind {
	case "begin":
		if v.Family != "" || v.Revision != 1 || v.MaterializedRows != 1 || v.ResultCount != 1 {
			return ErrInvalid
		}
	case "append":
		if ipEvidenceFamilyIndex(v.Family) < 0 || v.ResultCount == 0 || v.MaterializedRows <= 1 {
			return ErrInvalid
		}
	}
	return nil
}

func writeIPEntityEvidenceResult(e *requestEncoder, id CommandIdentity, request RequestDigest, v ipEntityEvidenceResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("ip_entity_evidence")
	e.text(v.Kind)
	e.text("applied")
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(request[:])
	e.text(v.OperationID)
	e.text(v.EvidenceID)
	e.text(v.BuildID)
	e.text(v.Provider)
	e.text(v.SubjectKind)
	e.text(v.ProviderID)
	e.text(v.ParserVersion)
	e.text(v.Family)
	e.text(v.Lease)
	e.number(uint64(v.SubjectID))
	e.number(uint64(v.BuildGeneration))
	e.number(uint64(v.Revision))
	e.number(uint64(v.MaterializedRows))
	e.number(uint64(v.ResultCount))
	e.number(uint64(v.ResultBytes))
	e.number(uint64(v.LeaseUntilMS))
	e.number(uint64(v.AppliedMS))
	if e.err != nil {
		return errors.Join(invalid("ip_entity_evidence_result"), e.err)
	}
	return nil
}
