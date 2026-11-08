package writer

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
)

// Closed canonical logical-row encoding: one family byte, then length-prefixed
// UTF-8 TEXT and big-endian 64-bit native scalars (IEEE bits for weight). This
// measures accepted logical bytes, never SQLite page/WAL or memory admission.
func mgLCPutText(dst []byte, s string) []byte {
	dst = binary.BigEndian.AppendUint64(dst, uint64(len(s)))
	return append(dst, s...)
}
func mgLCInitialDigest(b mgLCBuild) [32]byte {
	p := []byte("portico-mg-lc-entries-v2\x00")
	for _, s := range []string{b.channel, b.generation, b.base, b.config, b.seed, b.fence()} {
		p = mgLCPutText(p, s)
	}
	for _, n := range []int64{b.revision, b.start, b.end, b.history} {
		p = binary.BigEndian.AppendUint64(p, uint64(n))
	}
	p = append(p, b.incarnation[:]...)
	p = append(p, b.create[:]...)
	p = append(p, b.birth[:]...)
	return sha256.Sum256(p)
}
func mgLCCandidateBytes(generation string, c mgLCCandidate) []byte {
	p := []byte{1}
	for _, s := range []string{generation, c.rule, c.library, c.asset, c.source, c.title, c.sort} {
		p = mgLCPutText(p, s)
	}
	for _, n := range []int64{c.item, c.ordinal, c.duration, c.show, c.season, c.episode} {
		p = binary.BigEndian.AppendUint64(p, uint64(n))
	}
	return binary.BigEndian.AppendUint64(p, math.Float64bits(c.weight))
}

type mgLCEntry struct {
	generation, occurrence, channel            string
	start, end, item                           int64
	asset, library, source, title, rule, block string
	offset                                     int64
	slate                                      string
}

func (e mgLCEntry) canonicalBytes() []byte {
	p := []byte{2}
	for _, s := range []string{e.generation, e.occurrence, e.channel, e.asset, e.library, e.source, e.title, e.rule, e.block, e.slate} {
		p = mgLCPutText(p, s)
	}
	for _, n := range []int64{e.start, e.end, e.item, e.offset} {
		p = binary.BigEndian.AppendUint64(p, uint64(n))
	}
	return p
}
func mgLCNextDigest(previous [32]byte, e mgLCEntry) [32]byte {
	h := sha256.New()
	h.Write(previous[:])
	h.Write(e.canonicalBytes())
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
func (e mgLCEntry) values() []any {
	return []any{e.generation, e.occurrence, e.channel, e.start, e.end, e.item, e.asset, e.library, e.source, e.title, e.rule, e.block, e.offset, e.slate}
}
