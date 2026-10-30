package catalog

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// A FileInfo opening is a stateless, authenticated reference to the original
// accepted Detail for one item, captured when a caller first enters the bounded
// FileInfo surface. Nothing is stored between requests: the token below is the
// whole opening until its original expiry. It carries internal item/library
// coordinates, the item's full incarnation nonce, the public item id, the exact
// accepted informational epoch, the original accepted Detail catalog/viewer
// revisions, the original expiry and a digest of the original accepted
// DetailScope.
//
// It never carries a readprep handle, query, label, path, source/technical
// string or any server content payload, so a token cannot act as data. It is
// not a grant: adapters authenticate the caller, check live authority before and
// after use, and bind the token to that exact private caller.
//
// Every lane, poll and continuation replays the same opening. A later accepted
// Detail is a new opening with its own token; decoding never consults the
// current catalog revision, current content readiness or current Detail content,
// so an original capture cannot be silently replaced by a newer one. The
// original expiry is echoed unchanged and can never be extended by encoding or
// replay.
type fileInfoOpeningProof struct {
	Item    int64
	Library int64
	// ItemIncarnation is the complete 128-bit incarnation nonce of the item,
	// taken from the existing metadata or artwork publication head incarnation
	// (32 hex, created and deleted with the entity). It is
	// stored and matched in full, never truncated or re-hashed. Because it dies
	// with the row, a deleted and recreated item cannot replay an old opening
	// even when it reuses the row id, the original public id and info epoch 1;
	// this needs no new DDL. The library needs no such witness: its internal id
	// is AUTOINCREMENT and is never reused.
	ItemIncarnation [16]byte
	PublicItem      [16]byte
	// InfoEpoch is the exact accepted informational epoch of the item when the
	// opening was accepted. It is not re-read from current content.
	InfoEpoch int64
	// ConfigurationRevision fences source/root configuration independently of scan content.
	ConfigurationRevision int64
	Revision              ContentRevision
	Expires               time.Time
	// ScopeDigest is SHA256 over the original accepted DetailScope, all four
	// strings length-prefixed. CheckScope compares it exactly.
	ScopeDigest [32]byte
}

const (
	// Opening tokens are version 2 with a separate MAC domain. Existing
	// continuation anchors are version 1, so the two forms never cross-decode.
	fileInfoOpeningVersion byte = 2
	// version(1) + item,library,infoEpoch,catalog,viewer,expiry(8 each = 48)
	// + configuration(8) + itemIncarnation(16) + publicItem(16) + scopeDigest(32) + mac(16).
	fileInfoOpeningSize = 137
	// Domain separation keeps an opening MAC distinct from an anchor MAC even
	// though both use the one process-scoped private key.
	fileInfoOpeningDomain = "portico.catalog.fileinfo.opening.v2\x00"
	fileInfoScopeDomain   = "portico.catalog.fileinfo.detail-scope.v2\x00"
)

type fileInfoOpeningCodec struct {
	key [32]byte
	now func() time.Time
}

// newFileInfoOpeningCodec derives the opening codec from the existing anchor
// codec so both share one process-scoped private key: a restart or key
// replacement invalidates outstanding openings with the anchors. The anchor
// clock is followed dynamically, so tests pin one deterministic instant for
// both codecs.
func newFileInfoOpeningCodec(anchors *fileInfoAnchorCodec) (*fileInfoOpeningCodec, error) {
	if anchors == nil {
		return nil, readprep.ErrInvalid
	}
	c := &fileInfoOpeningCodec{key: anchors.key, now: time.Now}
	if anchors.now != nil {
		c.now = func() time.Time { return anchors.now() }
	}
	return c, nil
}

func (c *fileInfoOpeningCodec) clock() time.Time {
	if c.now == nil {
		return time.Now()
	}
	return c.now()
}

// encodeOpening serializes the original descriptor. The supplied proof is the
// original capture: encoding never mints or extends an expiry, and adds nothing
// beyond the fixed descriptor plus the caller's MAC.
func (c *fileInfoOpeningCodec) encodeOpening(caller readprep.Caller, proof fileInfoOpeningProof) (string, error) {
	if c == nil || caller == "" || !proof.valid(c.clock()) {
		return "", readprep.ErrStale
	}
	var raw [fileInfoOpeningSize]byte
	raw[0] = fileInfoOpeningVersion
	binary.BigEndian.PutUint64(raw[1:9], uint64(proof.Item))
	binary.BigEndian.PutUint64(raw[9:17], uint64(proof.Library))
	copy(raw[17:33], proof.ItemIncarnation[:])
	binary.BigEndian.PutUint64(raw[33:41], uint64(proof.InfoEpoch))
	binary.BigEndian.PutUint64(raw[41:49], uint64(proof.Revision.Catalog))
	binary.BigEndian.PutUint64(raw[49:57], uint64(proof.Revision.Viewer))
	binary.BigEndian.PutUint64(raw[57:65], uint64(proof.Expires.UnixNano()))
	copy(raw[65:81], proof.PublicItem[:])
	copy(raw[81:113], proof.ScopeDigest[:])
	binary.BigEndian.PutUint64(raw[113:121], uint64(proof.ConfigurationRevision))
	copy(raw[121:], c.mac(caller, raw[:121]))
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// decodeOpening authenticates and decodes the original descriptor for this
// exact caller. It reads no current revision, content or readiness state, so the
// caller-visible original capture survives unrelated catalog changes.
func (c *fileInfoOpeningCodec) decodeOpening(caller readprep.Caller, token string) (fileInfoOpeningProof, error) {
	var proof fileInfoOpeningProof
	// Fixed length is checked before decoding to prevent attacker-sized work.
	if c == nil || caller == "" || len(token) != base64.RawURLEncoding.EncodedLen(fileInfoOpeningSize) {
		return proof, readprep.ErrStale
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != fileInfoOpeningSize || raw[0] != fileInfoOpeningVersion || base64.RawURLEncoding.EncodeToString(raw) != token || !hmac.Equal(raw[121:], c.mac(caller, raw[:121])) {
		return fileInfoOpeningProof{}, readprep.ErrStale
	}
	proof = fileInfoOpeningProof{
		Item:                  int64(binary.BigEndian.Uint64(raw[1:9])),
		Library:               int64(binary.BigEndian.Uint64(raw[9:17])),
		InfoEpoch:             int64(binary.BigEndian.Uint64(raw[33:41])),
		ConfigurationRevision: int64(binary.BigEndian.Uint64(raw[113:121])),
		Revision: ContentRevision{
			Catalog: int64(binary.BigEndian.Uint64(raw[41:49])),
			Viewer:  int64(binary.BigEndian.Uint64(raw[49:57])),
		},
		Expires: time.Unix(0, int64(binary.BigEndian.Uint64(raw[57:65]))),
	}
	copy(proof.ItemIncarnation[:], raw[17:33])
	copy(proof.PublicItem[:], raw[65:81])
	copy(proof.ScopeDigest[:], raw[81:113])
	if !proof.valid(c.clock()) {
		return fileInfoOpeningProof{}, readprep.ErrStale
	}
	return proof, nil
}

// CheckScope verifies that the proof belongs to this exact accepted DetailScope.
// The caller reconstructs the scope from its current authoritative server,
// library and public ids plus the ORIGINAL detail authority fence; a mismatch in
// any field is stale, and the check never adopts current content or revision.
func (p fileInfoOpeningProof) CheckScope(scope DetailScope) error {
	if p.ScopeDigest != fileInfoScopeDigest(scope) {
		return readprep.ErrStale
	}
	return nil
}

func (p fileInfoOpeningProof) valid(now time.Time) bool {
	var zeroPublic [16]byte
	var zeroIncarnation [16]byte
	var zeroScope [32]byte
	return p.Item > 0 && p.Library > 0 &&
		p.ItemIncarnation != zeroIncarnation && p.PublicItem != zeroPublic && p.InfoEpoch > 0 &&
		p.ConfigurationRevision >= 0 && p.Revision.Catalog >= 0 && p.Revision.Viewer >= 0 &&
		p.ScopeDigest != zeroScope &&
		p.Expires.UnixNano() > 0 && p.Expires.After(now)
}

// fileInfoScopeDigest hashes the exact accepted DetailScope. Every string is
// length-prefixed so concatenation ambiguity cannot produce a shared digest.
// The digest binds content; it is not a grant.
func fileInfoScopeDigest(scope DetailScope) (digest [32]byte) {
	h := sha256.New()
	_, _ = h.Write([]byte(fileInfoScopeDomain))
	var size [8]byte
	for _, value := range []string{scope.ServerID, scope.LibraryID, scope.ItemID, scope.ViewerFence} {
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		_, _ = h.Write(size[:])
		_, _ = h.Write([]byte(value))
	}
	copy(digest[:], h.Sum(nil))
	return digest
}

func (c *fileInfoOpeningCodec) mac(caller readprep.Caller, raw []byte) []byte {
	m := hmac.New(sha256.New, c.key[:])
	_, _ = m.Write([]byte(fileInfoOpeningDomain))
	// Length-prefixing isolates caller identity from the fixed token payload.
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(caller)))
	_, _ = m.Write(size[:])
	_, _ = m.Write([]byte(caller))
	_, _ = m.Write(raw)
	return m.Sum(nil)[:16]
}
