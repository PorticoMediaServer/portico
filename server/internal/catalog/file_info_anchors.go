package catalog

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// File information anchors carry only integer occurrence coordinates and a
// private memory handle. The opening digest binds the original accepted Detail,
// the separate FileInfo audience, and the exact source/item publication basis.
// Labels, paths, technical strings and query results never enter an anchor.
type fileInfoAnchor struct {
	Handle    readprep.Handle
	Opening   [32]byte
	Item      int64
	Asset     int64
	Row       int64
	Expires   time.Time
	Lane      byte
	Direction byte
}

const (
	fileInfoLaneFiles byte = iota + 1
	fileInfoLaneAudio
	fileInfoLaneSubtitles
	fileInfoLaneDisc
	fileInfoLaneSources
	fileInfoLaneGroups
	fileInfoLaneMembers
)

const (
	fileInfoForward byte = iota + 1
	fileInfoBackward
	fileInfoReload
)

type fileInfoAnchorCodec struct {
	key [32]byte
	now func() time.Time
}

func newFileInfoAnchorCodec() (*fileInfoAnchorCodec, error) {
	c := &fileInfoAnchorCodec{now: time.Now}
	if _, err := rand.Read(c.key[:]); err != nil {
		return nil, err
	}
	return c, nil
}

// The original Store lease expiry is supplied by the owner. Encoding or
// following an anchor never creates a fresh lifetime.
func (c *fileInfoAnchorCodec) encode(caller readprep.Caller, a fileInfoAnchor) (string, error) {
	if c == nil || caller == "" || !a.valid(c.now()) {
		return "", readprep.ErrStale
	}
	handle, err := base64.RawURLEncoding.DecodeString(string(a.Handle))
	if err != nil || len(handle) != 32 || base64.RawURLEncoding.EncodeToString(handle) != string(a.Handle) {
		return "", readprep.ErrStale
	}
	var raw [115]byte
	raw[0], raw[1], raw[2] = 1, a.Lane, a.Direction
	binary.BigEndian.PutUint64(raw[3:11], uint64(a.Expires.UnixNano()))
	copy(raw[11:43], handle)
	copy(raw[43:75], a.Opening[:])
	binary.BigEndian.PutUint64(raw[75:83], uint64(a.Item))
	binary.BigEndian.PutUint64(raw[83:91], uint64(a.Asset))
	binary.BigEndian.PutUint64(raw[91:99], uint64(a.Row))
	copy(raw[99:], c.mac(caller, raw[:99]))
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func (c *fileInfoAnchorCodec) decode(caller readprep.Caller, token string, opening [32]byte, lane byte) (fileInfoAnchor, error) {
	var a fileInfoAnchor
	// Fixed size is checked before decoding to prevent attacker-sized allocation.
	if c == nil || caller == "" || len(token) != 154 {
		return a, readprep.ErrStale
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 115 || raw[0] != 1 || base64.RawURLEncoding.EncodeToString(raw) != token || !hmac.Equal(raw[99:], c.mac(caller, raw[:99])) {
		return a, readprep.ErrStale
	}
	a.Lane, a.Direction = raw[1], raw[2]
	a.Expires = time.Unix(0, int64(binary.BigEndian.Uint64(raw[3:11])))
	a.Handle = readprep.Handle(base64.RawURLEncoding.EncodeToString(raw[11:43]))
	copy(a.Opening[:], raw[43:75])
	a.Item = int64(binary.BigEndian.Uint64(raw[75:83]))
	a.Asset = int64(binary.BigEndian.Uint64(raw[83:91]))
	a.Row = int64(binary.BigEndian.Uint64(raw[91:99]))
	if a.Opening != opening || a.Lane != lane || !a.valid(c.now()) {
		return fileInfoAnchor{}, readprep.ErrStale
	}
	return a, nil
}

func (a fileInfoAnchor) valid(now time.Time) bool {
	exactOccurrence := a.Lane == fileInfoLaneAudio || a.Lane == fileInfoLaneSubtitles || a.Lane == fileInfoLaneDisc || a.Lane == fileInfoLaneMembers
	return a.Lane >= fileInfoLaneFiles && a.Lane <= fileInfoLaneMembers && a.Direction >= fileInfoForward && a.Direction <= fileInfoReload && a.Item > 0 && a.Asset >= 0 && (!exactOccurrence || a.Asset > 0) && a.Row >= 0 && a.Expires.After(now) && a.Expires.UnixNano() > 0
}

func (c *fileInfoAnchorCodec) mac(caller readprep.Caller, raw []byte) []byte {
	m := hmac.New(sha256.New, c.key[:])
	// Length-prefixing isolates caller identity from the fixed token payload.
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(caller)))
	_, _ = m.Write(size[:])
	_, _ = m.Write([]byte(caller))
	_, _ = m.Write(raw)
	return m.Sum(nil)[:16]
}
