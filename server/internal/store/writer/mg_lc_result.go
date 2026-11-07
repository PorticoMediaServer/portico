package writer

import (
	"crypto/sha256"
	"encoding/binary"
)

// Closed versioned result encoding. Native scalar receipt facts, command
// identity and their immutable owner scope all participate; there is no caller
// supplied result hash or JSON authority. This is not a disclosure grant.
func mgLCResultDigest(b mgLCBuild, s mgLCSaved) ResultDigest {
	h := sha256.New()
	text := func(v string) { h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(v)))); h.Write([]byte(v)) }
	integer := func(v int64) { h.Write(binary.BigEndian.AppendUint64(nil, uint64(v))) }
	text("portico.mg.lc.result.v1")
	text(b.generation)
	h.Write(b.incarnation[:])
	text(b.channel)
	integer(b.revision)
	text(b.fence())
	integer(b.history)
	h.Write(b.create[:])
	h.Write(b.birth[:])
	integer(s.command.identity.Sequence)
	h.Write(s.command.identity.OperationID[:])
	h.Write(s.command.request[:])
	text(string(s.kind))
	integer(s.applied)
	integer(s.revision)
	integer(s.ack)
	r := s.result
	integer(r.candidates)
	integer(r.entries)
	integer(r.cursor)
	integer(r.bytes)
	h.Write(r.entryDigest[:])
	integer(r.verified)
	integer(r.verifyAfter)
	integer(r.verifyEnd)
	h.Write(r.verifyDigest[:])
	if r.sealed {
		integer(1)
	} else {
		integer(0)
	}
	text(r.phase)
	text(r.status)
	text(r.lease)
	integer(r.leaseUntil)
	integer(r.published)
	text(r.reclaimFamily)
	text(r.reclaimKey)
	integer(r.reclaimSubkey)
	var out ResultDigest
	copy(out[:], h.Sum(nil))
	return out
}
