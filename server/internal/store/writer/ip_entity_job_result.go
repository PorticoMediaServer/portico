package writer

import (
	"math"
	"unicode/utf8"
)

// ipEntityJobResult is the COMPLETE immutable saved result of one acquisition
// job command. Every field is derived from the original request, identity and
// receipt header, so a replay never reconstructs it from mutable job, lease or
// provider state. It never claims acquisition success: claim/renew only report
// the admitted owner and its lease, and finish reports the truthful terminal
// outcome without publishing metadata.
type ipEntityJobResult struct {
	Kind, OperationID, Provider, Purpose, SelectedID, Lease, Outcome string
	EntityID, Generation, Revision, LeaseUntilMS, AppliedMS          int64
	ResultCount                                                      int64
}

const ipEntityJobResultFixedBytes = 4096

func ipEntityJobResultDigest(id CommandIdentity, request RequestDigest, v ipEntityJobResult) (ResultDigest, error) {
	return canonicalResultDigest(id, request, v)
}

func ipEntityJobKind(value string) bool {
	return value == "claim" || value == "renew" || value == "finish"
}

func ipEntityJobOutcome(value string) bool {
	return value == "done" || value == "retry"
}

func ipEntityJobHex(value [16]byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 32)
	for n, b := range value {
		out[n*2], out[n*2+1] = digits[b>>4], digits[b&0x0f]
	}
	return string(out)
}

func preflightIPEntityJobResult(v ipEntityJobResult) error {
	remaining := maxCanonicalResultBytes - ipEntityJobResultFixedBytes
	for _, s := range [...]string{v.Kind, v.OperationID, v.Provider, v.Purpose, v.SelectedID, v.Lease, v.Outcome} {
		if len(s) > remaining || !utf8.ValidString(s) {
			return ErrInvalid
		}
		remaining -= len(s)
	}
	if !ipEntityJobKind(v.Kind) || !ipPolicyProviderCode(v.Provider) || v.Purpose != "metadata" ||
		len(v.OperationID) != 32 || len(v.SelectedID) == 0 || len(v.SelectedID) > 128 ||
		v.EntityID <= 0 || v.Generation <= 0 || v.Revision <= 0 || v.AppliedMS < 0 ||
		v.AppliedMS > math.MaxInt64-ipEntityJobLeaseMS || v.ResultCount != 1 {
		return ErrInvalid
	}
	switch v.Kind {
	case "finish":
		if v.Lease != "" || !ipEntityJobOutcome(v.Outcome) || v.LeaseUntilMS != v.AppliedMS {
			return ErrInvalid
		}
	default:
		if len(v.Lease) != 32 || v.Outcome != "" || v.LeaseUntilMS != v.AppliedMS+ipEntityJobLeaseMS {
			return ErrInvalid
		}
	}
	return nil
}

func writeIPEntityJobResult(e *requestEncoder, id CommandIdentity, request RequestDigest, v ipEntityJobResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("ip_entity_job")
	e.text(v.Kind)
	e.text("applied")
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(request[:])
	e.text(v.OperationID)
	e.number(uint64(v.EntityID))
	e.text(v.Provider)
	e.text(v.Purpose)
	e.text(v.SelectedID)
	e.text(v.Lease)
	e.number(uint64(v.Generation))
	e.number(uint64(v.Revision))
	e.number(uint64(v.LeaseUntilMS))
	e.number(uint64(v.AppliedMS))
	e.text(v.Outcome)
	e.number(uint64(v.ResultCount))
	return e.err
}
