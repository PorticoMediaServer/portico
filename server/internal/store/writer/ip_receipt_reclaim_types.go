package writer

// Private fixed-slot retention protocol. This slot is part of the real command
// owner, never a second cleanup child receipt. A cleanup ACK does not redue a
// completed slot. Source ACK makes only actual child debt due; it refunds zero.
// These snapshots are data, not current disclosure/maintenance/resource grants.
type ipRetentionScope struct {
	Family                                            ipReceiptOwnerKind
	OwnerID, LibraryID                                int64
	Provider, Purpose, PersonOperation, BulkOperation string
}

const ipReceiptBulk ipReceiptOwnerKind = 6

func (s ipRetentionScope) keys() ([]any, error) {
	switch s.Family {
	case ipReceiptPolicy:
		if s.LibraryID <= 0 || !ipPolicyValidText(s.Provider, 128) || s.OwnerID != 0 || s.Purpose != "" || s.PersonOperation != "" || s.BulkOperation != "" {
			return nil, ErrInvalid
		}
		return []any{s.LibraryID, s.Provider}, nil
	case ipReceiptRefresh:
		if !ipPolicyValidText(s.Provider, 128) || (s.Purpose != "metadata" && s.Purpose != "artwork") || s.OwnerID != 0 || s.LibraryID != 0 || s.PersonOperation != "" || s.BulkOperation != "" {
			return nil, ErrInvalid
		}
		return []any{s.Provider, s.Purpose}, nil
	case ipReceiptText, ipReceiptArtwork:
		if s.OwnerID <= 0 || s.LibraryID != 0 || s.Provider != "" || s.Purpose != "" || s.PersonOperation != "" || s.BulkOperation != "" {
			return nil, ErrInvalid
		}
		return []any{s.OwnerID}, nil
	case ipReceiptPerson:
		if !ipPolicyValidText(s.PersonOperation, 512) || s.OwnerID != 0 || s.LibraryID != 0 || s.Provider != "" || s.Purpose != "" || s.BulkOperation != "" {
			return nil, ErrInvalid
		}
		return []any{s.PersonOperation}, nil
	case ipReceiptBulk:
		if !ipPolicyValidText(s.BulkOperation, 512) || s.OwnerID != 0 || s.LibraryID != 0 || s.Provider != "" || s.Purpose != "" || s.PersonOperation != "" {
			return nil, ErrInvalid
		}
		return []any{s.BulkOperation}, nil
	}
	return nil, ErrInvalid
}

type ipReceiptReclaimResult struct {
	Family                                                                             ipReceiptOwnerKind
	CapturedACK, OldCursor, RemovedRows, RemovedPayloadBytes, AfterSequence, AppliedMS int64
	NoNext                                                                             bool
}
type ipReceiptReclaimSlot struct {
	Identity CommandIdentity
	Request  RequestDigest
	Digest   ResultDigest
	Value    ipReceiptReclaimResult
}
type ipReceiptReclaimRequest struct {
	Identity                   CommandIdentity
	Scope                      ipRetentionScope
	CapturedACK, AfterSequence int64
	Previous                   *ipReceiptReclaimSlot
}
type ipReceiptReclaimCommand struct {
	request ipReceiptReclaimRequest
	digest  RequestDigest
}
type ipReceiptReclaimNative struct {
	decision receiptDecision
	value    ipReceiptReclaimResult
	digest   ResultDigest
}

func newIPReceiptReclaimCommand(q ipReceiptReclaimRequest) (ipReceiptReclaimCommand, error) {
	if _, err := q.Scope.keys(); err != nil {
		return ipReceiptReclaimCommand{}, err
	}
	if !ipPolicyValidIdentity(q.Identity) || q.CapturedACK < 0 || q.AfterSequence < 0 || q.AfterSequence > q.CapturedACK || q.Identity.Sequence-q.CapturedACK != 1 {
		return ipReceiptReclaimCommand{}, ErrInvalid
	}
	if q.Previous != nil {
		p := *q.Previous
		if p.Identity.Incarnation != q.Identity.Incarnation || p.Identity.Sequence > q.CapturedACK || p.Request == (RequestDigest{}) || p.Digest == (ResultDigest{}) || p.Value.Family != q.Scope.Family || preflightIPReceiptReclaimResult(p.Value) != nil {
			return ipReceiptReclaimCommand{}, ErrInvalid
		}
	}
	digest, err := canonicalRequestDigest(IPReceiptReclaim, 1, q.Identity, q)
	if err != nil {
		return ipReceiptReclaimCommand{}, err
	}
	if q.Previous != nil {
		previous := *q.Previous
		q.Previous = &previous
	}
	return ipReceiptReclaimCommand{q, digest}, nil
}
func preflightIPReceiptReclaimResult(r ipReceiptReclaimResult) error {
	if r.Family < ipReceiptPolicy || r.Family > ipReceiptBulk || r.CapturedACK < 0 || r.OldCursor < 0 || r.OldCursor > r.CapturedACK || r.RemovedRows < 0 || r.RemovedRows > 12 || r.RemovedPayloadBytes < 0 || r.AfterSequence < r.OldCursor || r.AfterSequence > r.CapturedACK || r.AppliedMS < 0 || r.NoNext && r.AfterSequence != r.CapturedACK || r.RemovedRows == 0 && r.RemovedPayloadBytes != 0 || r.RemovedRows > 0 && r.AfterSequence <= r.OldCursor {
		return ErrInvalid
	}
	return nil
}

// v1 fixed result wire: identity prefix purpose ip_receipt_reclaim, uint16 family,
// six U64 capturedACK/oldCursor/removedRows/removedPayloadBytes/after/appliedMS,
// then noNext bool. No business revision is advanced.
// RemovedPayloadBytes counts only actual deleted TEXT/BLOB payload occurrences;
// it is not a physical-file, page, WAL or source/history debt refund.
func writeIPReceiptReclaimResult(e *requestEncoder, i CommandIdentity, q RequestDigest, v ipReceiptReclaimResult) error {
	writeIPResultIdentity(e, "ip_receipt_reclaim", i, q)
	e.bytes([]byte{0, byte(v.Family)})
	for _, n := range []int64{v.CapturedACK, v.OldCursor, v.RemovedRows, v.RemovedPayloadBytes, v.AfterSequence, v.AppliedMS} {
		e.number(uint64(n))
	}
	e.flag(v.NoNext)
	return e.err
}
