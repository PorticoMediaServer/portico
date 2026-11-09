package writer

import (
	"math"
	"unicode/utf8"
)

// ipPersonResult is the COMPLETE immutable saved result of one person provider
// lifecycle command (claim/renew/finish) or person evidence BEGIN. Every field
// is derived from the original request, command identity and receipt header, so
// a replay never reads mutable person, lease, build or policy state. It never
// claims person acceptance or publication.
type ipPersonResult struct {
	Kind, OperationID, Provider, ProviderID, Lease, Outcome       string
	EvidenceID, BuildID, SubjectKind, ParserVersion               string
	PersonID, Generation, Revision, LeaseGeneration, LeaseUntilMS int64
	AppliedMS, BuildGeneration, MaterializedRows, ResultBytes     int64
	ResultCount                                                   int64
	Commitment                                                    [32]byte
}

const ipPersonResultFixedBytes = 4096

func ipPersonResultDigest(id CommandIdentity, request RequestDigest, v ipPersonResult) (ResultDigest, error) {
	return canonicalResultDigest(id, request, v)
}

func ipPersonKind(value string) bool {
	return value == "claim" || value == "renew" || value == "finish" || value == "begin"
}

func ipPersonFinishOutcome(value string) bool {
	return value == "done" || value == "retry"
}

func ipPersonHex(value [16]byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 32)
	for n, b := range value {
		out[n*2], out[n*2+1] = digits[b>>4], digits[b&0x0f]
	}
	return string(out)
}

func preflightIPPersonResult(v ipPersonResult) error {
	remaining := maxCanonicalResultBytes - ipPersonResultFixedBytes
	for _, s := range [...]string{v.Kind, v.OperationID, v.Provider, v.ProviderID, v.Lease,
		v.Outcome, v.EvidenceID, v.BuildID, v.SubjectKind, v.ParserVersion} {
		if len(s) > remaining || !utf8.ValidString(s) {
			return ErrInvalid
		}
		remaining -= len(s)
	}
	if !ipPersonKind(v.Kind) || !ipPolicyProviderCode(v.Provider) || len(v.OperationID) != 32 ||
		v.PersonID <= 0 || v.Generation <= 0 || v.Revision <= 0 || v.LeaseGeneration <= 0 ||
		v.AppliedMS < 0 || v.AppliedMS > math.MaxInt64-ipPersonLeaseMS || v.ResultCount != 1 {
		return ErrInvalid
	}
	switch v.Kind {
	case "claim":
		if len(v.ProviderID) == 0 || len(v.ProviderID) > ipPersonProviderIDMax || len(v.Lease) != 32 ||
			v.Outcome != "" || v.EvidenceID != "" || v.BuildID != "" || v.SubjectKind != "" ||
			v.ParserVersion != "" || v.BuildGeneration != 0 || v.MaterializedRows != 0 || v.ResultBytes != 0 ||
			v.Generation != 1 || v.Revision != 1 || v.LeaseGeneration != 1 ||
			v.LeaseUntilMS != v.AppliedMS+ipPersonLeaseMS || v.Commitment != [32]byte{} {
			return ErrInvalid
		}
	case "renew":
		if len(v.ProviderID) == 0 || len(v.ProviderID) > ipPersonProviderIDMax || len(v.Lease) != 32 ||
			v.Outcome != "" || v.EvidenceID != "" || v.BuildID != "" || v.SubjectKind != "" ||
			v.ParserVersion != "" || v.BuildGeneration != 0 || v.MaterializedRows != 0 || v.ResultBytes != 0 ||
			v.Revision < 2 || v.LeaseGeneration < 2 ||
			v.LeaseUntilMS != v.AppliedMS+ipPersonLeaseMS || v.Commitment != [32]byte{} {
			return ErrInvalid
		}
	case "finish":
		if len(v.ProviderID) == 0 || len(v.ProviderID) > ipPersonProviderIDMax || v.Lease != "" ||
			!ipPersonFinishOutcome(v.Outcome) || v.EvidenceID != "" || v.BuildID != "" ||
			v.SubjectKind != "" || v.ParserVersion != "" || v.BuildGeneration != 0 ||
			v.MaterializedRows != 0 || v.ResultBytes != 0 || v.Revision < 2 ||
			v.LeaseUntilMS != v.AppliedMS || v.Commitment != [32]byte{} {
			return ErrInvalid
		}
	default:
		if len(v.ProviderID) == 0 || len(v.ProviderID) > ipPersonProviderIDMax || len(v.Lease) != 32 ||
			v.Outcome != "" || v.EvidenceID == "" || v.BuildID == "" || v.SubjectKind != "person" ||
			v.ParserVersion == "" || v.BuildGeneration <= 0 || v.MaterializedRows != 1 ||
			v.ResultBytes <= 0 || v.Revision < 2 || v.LeaseUntilMS <= v.AppliedMS ||
			v.Commitment == [32]byte{} {
			return ErrInvalid
		}
	}
	return nil
}

func writeIPPersonResult(e *requestEncoder, id CommandIdentity, request RequestDigest, v ipPersonResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("ip_person")
	e.text(v.Kind)
	e.text("applied")
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(request[:])
	e.text(v.OperationID)
	e.number(uint64(v.PersonID))
	e.text(v.Provider)
	e.text(v.ProviderID)
	e.text(v.Lease)
	e.number(uint64(v.Generation))
	e.number(uint64(v.Revision))
	e.number(uint64(v.LeaseGeneration))
	e.number(uint64(v.LeaseUntilMS))
	e.number(uint64(v.AppliedMS))
	e.text(v.Outcome)
	e.text(v.EvidenceID)
	e.text(v.BuildID)
	e.text(v.SubjectKind)
	e.text(v.ParserVersion)
	e.number(uint64(v.BuildGeneration))
	e.number(uint64(v.MaterializedRows))
	e.number(uint64(v.ResultBytes))
	e.bytes(v.Commitment[:])
	e.number(uint64(v.ResultCount))
	return e.err
}
