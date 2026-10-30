package catalog

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// An occurrence is a private locator for one exact root/child/link/asset
// lifetime. It is not a grant, a mutable path, or a query-result record.
type fileInfoOccurrenceProof struct {
	Opening         [32]byte
	Root            int64
	RootIncarnation [16]byte
	Coordinate      fileInfoOccurrence
	ItemIncarnation [16]byte
	AssetToken      [32]byte
	Expires         time.Time
	Group           fileInfoGroupNumeric
	IsGroup         bool
	DiscWinner      bool
}

const fileInfoOccurrenceBytes = 203
const fileInfoOccurrenceText = 271

func (r *FileInfoRuntime) occurrenceMAC(caller readprep.Caller, raw []byte) []byte {
	h := hmac.New(sha256.New, r.anchors.key[:])
	h.Write([]byte("portico.file-info.occurrence.v1\x00"))
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(caller)))
	h.Write(n[:])
	h.Write([]byte(caller))
	h.Write(raw)
	return h.Sum(nil)[:16]
}
func (r *FileInfoRuntime) encodeOccurrence(caller readprep.Caller, p fileInfoOccurrenceProof) (string, error) {
	if r == nil || caller == "" || !p.valid(r.now()) {
		return "", readprep.ErrStale
	}
	var b [fileInfoOccurrenceBytes]byte
	b[0] = 1
	if p.IsGroup {
		b[1] = 1
	}
	if p.DiscWinner {
		b[1] |= 2
	}
	binary.BigEndian.PutUint64(b[2:10], uint64(p.Expires.UnixNano()))
	copy(b[10:42], p.Opening[:])
	binary.BigEndian.PutUint64(b[42:50], uint64(p.Root))
	copy(b[50:66], p.RootIncarnation[:])
	binary.BigEndian.PutUint64(b[66:74], uint64(p.Coordinate.Item))
	binary.BigEndian.PutUint64(b[74:82], uint64(p.Coordinate.Asset))
	copy(b[82:98], p.ItemIncarnation[:])
	copy(b[98:130], p.AssetToken[:])
	fields := [7]int64{p.Group.ShapeHead, p.Group.Head, p.Group.Shape, p.Group.OfferedMembers, p.Group.Members, p.Group.SizeBytes, p.Group.DurationMS}
	for i, v := range fields {
		binary.BigEndian.PutUint64(b[130+i*8:138+i*8], uint64(v))
	}
	b[186] = p.Group.Type
	copy(b[187:], r.occurrenceMAC(caller, b[:187]))
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
func (p fileInfoOccurrenceProof) valid(now time.Time) bool {
	return p.Root > 0 && p.RootIncarnation != ([16]byte{}) && p.Coordinate.Item > 0 && p.Coordinate.Asset > 0 && p.ItemIncarnation != ([16]byte{}) && p.AssetToken != ([32]byte{}) && p.Opening != ([32]byte{}) && p.Expires.After(now) && (!p.IsGroup || fileInfoGroupDescriptorOK(p.Group) && p.Coordinate.Asset == p.Group.Head)
}
func (r *FileInfoRuntime) decodeOccurrence(caller readprep.Caller, token string) (fileInfoOccurrenceProof, error) {
	var p fileInfoOccurrenceProof
	if r == nil || caller == "" || len(token) != fileInfoOccurrenceText {
		return p, readprep.ErrStale
	}
	b, e := base64.RawURLEncoding.DecodeString(token)
	if e != nil || len(b) != fileInfoOccurrenceBytes || b[0] != 1 || b[1] > 3 || base64.RawURLEncoding.EncodeToString(b) != token || !hmac.Equal(b[187:], r.occurrenceMAC(caller, b[:187])) {
		return p, readprep.ErrStale
	}
	p.IsGroup = b[1]&1 != 0
	p.DiscWinner = b[1]&2 != 0
	p.Expires = time.Unix(0, int64(binary.BigEndian.Uint64(b[2:10])))
	copy(p.Opening[:], b[10:42])
	p.Root = int64(binary.BigEndian.Uint64(b[42:50]))
	copy(p.RootIncarnation[:], b[50:66])
	p.Coordinate = fileInfoOccurrence{int64(binary.BigEndian.Uint64(b[66:74])), int64(binary.BigEndian.Uint64(b[74:82]))}
	copy(p.ItemIncarnation[:], b[82:98])
	copy(p.AssetToken[:], b[98:130])
	fields := [7]*int64{&p.Group.ShapeHead, &p.Group.Head, &p.Group.Shape, &p.Group.OfferedMembers, &p.Group.Members, &p.Group.SizeBytes, &p.Group.DurationMS}
	for i, v := range fields {
		*v = int64(binary.BigEndian.Uint64(b[130+i*8 : 138+i*8]))
	}
	p.Group.Type = b[186]
	if !p.valid(r.now()) {
		return fileInfoOccurrenceProof{}, readprep.ErrStale
	}
	return p, nil
}

func (r *FileInfoRuntime) occurrenceLifetime(ctx context.Context, ad FileInfoAdmission, coord fileInfoOccurrence) ([16]byte, [32]byte, error) {
	var nonce [16]byte
	var asset [32]byte
	if coord.Item != ad.Identity.Item {
		if !ad.Children || ad.ChildVisible == nil {
			return nonce, asset, readprep.ErrStale
		}
		allowed, e := ad.ChildVisible(ctx, coord.Item)
		if e != nil {
			return nonce, asset, e
		}
		if !allowed {
			return nonce, asset, readprep.ErrStale
		}
	}
	var incarnation, token string
	var child bool
	e := fileInfoQuery(ctx, r.db).QueryRowContext(ctx, `SELECT h.incarnation,a.token,(e.id=? OR e.parent_id=?) FROM catalog_asset_links l JOIN catalog_entities e ON e.id=l.entity_id JOIN metadata_publication_heads h ON h.item_id=e.id JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=? AND l.asset_id=? AND e.retired=0`, ad.Identity.Item, ad.Identity.Item, coord.Item, coord.Asset).Scan(&incarnation, &token, &child)
	if errors.Is(e, sql.ErrNoRows) || e == nil && !child {
		return nonce, asset, readprep.ErrStale
	}
	if e != nil {
		return nonce, asset, e
	}
	raw, e := hex.DecodeString(incarnation)
	if e != nil || len(raw) != 16 {
		return nonce, asset, readprep.ErrStale
	}
	copy(nonce[:], raw)
	asset = sha256.Sum256([]byte(token))
	return nonce, asset, nil
}
func (r *FileInfoRuntime) issueOccurrence(ctx context.Context, caller readprep.Caller, opening string, proof fileInfoOpeningProof, ad FileInfoAdmission, coord fileInfoOccurrence, g *fileInfoGroupNumeric) (string, error) {
	nonce, asset, e := r.occurrenceLifetime(ctx, ad, coord)
	if e != nil {
		return "", e
	}
	p := fileInfoOccurrenceProof{Opening: fileInfoOpeningDigest(opening), Root: proof.Item, RootIncarnation: proof.ItemIncarnation, Coordinate: coord, ItemIncarnation: nonce, AssetToken: asset, Expires: proof.Expires}
	if g != nil {
		p.IsGroup = true
		p.Group = *g
	}
	return r.encodeOccurrence(caller, p)
}
func (r *FileInfoRuntime) resolveOccurrence(ctx context.Context, caller readprep.Caller, opening, token string, proof fileInfoOpeningProof, ad FileInfoAdmission, around bool) (fileInfoOccurrenceProof, error) {
	p, e := r.decodeOccurrence(caller, token)
	if e != nil {
		return p, e
	}
	if p.Root != proof.Item || p.RootIncarnation != proof.ItemIncarnation || !around && p.Opening != fileInfoOpeningDigest(opening) {
		return p, readprep.ErrStale
	}
	nonce, asset, e := r.occurrenceLifetime(ctx, ad, p.Coordinate)
	if e != nil {
		return p, e
	}
	if nonce != p.ItemIncarnation || asset != p.AssetToken {
		return p, readprep.ErrStale
	}
	return p, nil
}

func (r *FileInfoRuntime) issueDiscWinner(ctx context.Context, caller readprep.Caller, opening string, proof fileInfoOpeningProof, ad FileInfoAdmission, coord fileInfoOccurrence) (string, error) {
	if !ad.Owner {
		return "", readprep.ErrStale
	}
	token, e := r.issueOccurrence(ctx, caller, opening, proof, ad, coord, nil)
	if e != nil {
		return "", e
	}
	p, e := r.decodeOccurrence(caller, token)
	if e != nil {
		return "", e
	}
	p.DiscWinner = true
	return r.encodeOccurrence(caller, p)
}
