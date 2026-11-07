package read

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"slices"
	"strings"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

const MaxNativeRetained = 128
const MaxNativeStampBytes = 640 << 10

// Digest coordinates retain no DTO text. A subset must match both issued public
// values and its private source-policy proof; copying a proof to another hit
// cannot manufacture membership.
type nativeStampedHit struct {
	digest [32]byte
	policy nativePolicyWitness
}

func nativeDigestInteger(h hash.Hash, n uint64) {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], n)
	h.Write(b[:])
}
func nativeDigestString(h hash.Hash, value string) {
	nativeDigestInteger(h, uint64(len(value)))
	var block [4096]byte
	for len(value) > 0 {
		n := copy(block[:], value)
		h.Write(block[:n])
		value = value[n:]
	}
}
func nativeHitIdentity(hit FullSearchHit) [32]byte {
	h := sha256.New()
	for _, v := range []uint64{uint64(hit.Subject), uint64(hit.ID), uint64(hit.Library), uint64(hit.Kind), uint64(hit.Tier), uint64(hit.PeopleSubtier), uint64(hit.Item.ID), uint64(hit.Item.Revision), uint64(hit.Year)} {
		nativeDigestInteger(h, v)
	}
	h.Write(hit.Item.PublicID[:])
	for _, v := range []string{hit.Item.Title, hit.PersonName, hit.SortKey, hit.AddedAt} {
		nativeDigestString(h, v)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
func nativeItemIdentity(row []any) ([32]byte, error) {
	h := sha256.New()
	for _, v := range row {
		switch v := v.(type) {
		case nil:
			h.Write([]byte{0})
		case int64:
			h.Write([]byte{1})
			nativeDigestInteger(h, uint64(v))
		case string:
			h.Write([]byte{2})
			nativeDigestString(h, v)
		case []byte:
			h.Write([]byte{3})
			nativeDigestInteger(h, uint64(len(v)))
			h.Write(v)
		default:
			return [32]byte{}, ErrUnqualified
		}
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}

// MetadataBytes is an owned-field accounting figure, not RSS or a new grant.
// Issued stamps intern witness strings; shared backing values are counted once.
func (s *FullSearchReadStamp) MetadataBytes() int64 {
	if s == nil {
		return 0
	}
	n := int64(unsafe.Sizeof(*s)) + int64(len(s.query))
	f := s.fence
	p := f.permission
	for _, v := range []string{f.state, f.engineVersion, f.engineSource, f.profile.RecipeVersion, f.profile.EngineVersion, f.profile.UnicodeFingerprint, p.database, p.token, p.account, p.profile, p.membership, p.lifetime, p.library, p.role, p.memberLibraries, p.profileLibraries, p.profileLabels, p.expiresAt, p.horizon, p.root, p.bridgeRoot, p.policyWitness} {
		n += int64(len(v))
	}
	n += int64(cap(s.hits)) * int64(unsafe.Sizeof(nativeStampedHit{}))
	n += int64(cap(s.policies)+cap(s.checked)) * int64(unsafe.Sizeof(nativePolicyWitness{}))
	seen := make(map[string]bool)
	for _, p := range s.policies {
		if !seen[p.witness] {
			n += int64(len(p.witness))
			seen[p.witness] = true
		}
	}
	for _, p := range s.checked {
		if !seen[p.witness] {
			n += int64(len(p.witness))
			seen[p.witness] = true
		}
	}
	for _, entry := range s.hits {
		if !seen[entry.policy.witness] {
			n += int64(len(entry.policy.witness))
			seen[entry.policy.witness] = true
		}
	}

	return n
}

// RetainHits only narrows an issued stamp. It performs no SQL, admission or
// permission check and preserves the original clock/deadline/fence. Trusted
// composition charges prospective old/new metadata inside output/RB custody.
func (s *FullSearchReadStamp) RetainHits(hits []FullSearchHit) (*FullSearchReadStamp, error) {
	if s == nil || s.pool == nil || s.output == nil || s.MetadataBytes() > MaxNativeStampBytes || len(hits) > MaxNativeRetained {
		return nil, ErrInput
	}
	out := *s
	out.hits = nil
	out.policies = nil
	out.checked = nil
	for _, hit := range hits {
		entry := nativeStampedHit{nativeHitIdentity(hit), hit.permission}
		if !entry.policy.valid || !slices.Contains(s.hits, entry) || !slices.Contains(s.checked, entry.policy) {
			return nil, ErrStale
		}
		if slices.Contains(out.hits, entry) {
			return nil, ErrInput
		}
		out.hits = append(out.hits, entry)
		if !slices.Contains(out.policies, entry.policy) {
			out.policies = append(out.policies, entry.policy)
		}
	}
	out.checked = slices.Clone(out.policies)
	if out.MetadataBytes() > MaxNativeStampBytes {
		return nil, governor.ErrSnapshotBudget
	}
	return &out, nil
}

// Make independent owned witness strings and canonical aliases before exposing
// a stamp. Equal witness values then have exactly one backing allocation.
func (s *FullSearchReadStamp) ownWitnesses() {
	intern := make(map[string]string)
	for i := range s.checked {
		v := s.checked[i].witness
		owned, ok := intern[v]
		if !ok {
			owned = strings.Clone(v)
			intern[owned] = owned
		}
		s.checked[i].witness = owned
	}
	for i := range s.policies {
		if j := slices.Index(s.checked, s.policies[i]); j >= 0 {
			s.policies[i] = s.checked[j]
		}
	}
	for i := range s.hits {
		if j := slices.Index(s.checked, s.hits[i].policy); j >= 0 {
			s.hits[i].policy = s.checked[j]
		}
	}
}
