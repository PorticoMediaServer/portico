package selectionactions

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"math"
)

// Digest is the ordered streaming preview/admission digest. Its zero value is
// ready. It never allocates a serialized record or retains any command facts.
// OperationID and Started describe execution and deliberately do not participate.
type Digest struct{ h hash.Hash }

func (d *Digest) number(v int64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(v))
	_, _ = d.h.Write(b[:])
}
func (d *Digest) flag(v bool) {
	if v {
		d.number(1)
	} else {
		d.number(0)
	}
}
func (d *Digest) text(v string) {
	d.number(int64(len(v)))
	var b [512]byte
	for len(v) > 0 {
		n := copy(b[:], v)
		_, _ = d.h.Write(b[:n])
		v = v[n:]
	}
}
func (d *Digest) init() {
	if d.h == nil {
		d.h = sha256.New()
		d.text("portico-selection-command-v1")
	}
}
func (d *Digest) Add(r Record) {
	d.init()
	d.flag(r.Target != nil)
	if t := r.Target; t != nil {
		d.number(t.EntityID)
		for _, v := range []string{t.ItemID, t.LibraryID, t.Kind, t.Title, t.Identity, t.VersionID, t.DownloadIdentity, t.Lifetime} {
			d.text(v)
		}
	}
	d.flag(r.File != nil)
	if f := r.File; f != nil {
		d.number(f.TargetID)
		d.number(f.AssetID)
		for _, v := range []string{f.Token, f.Path, f.Root, f.RootIdentity, f.SourceID, f.SourceIncarnation} {
			d.text(v)
		}
		d.number(f.SourceGeneration)
		d.number(f.Size)
		d.number(f.ModifiedNS)
		d.flag(f.Shared)
		d.flag(f.Present)
		d.number(int64(f.Part))
		d.number(int64(math.Float64bits(f.Start)))
		d.flag(f.End != nil)
		if f.End != nil {
			d.number(int64(math.Float64bits(*f.End)))
		}
		d.text(f.Edition)
	}
	d.number(r.Finish)
}
func (d *Digest) String() string { d.init(); return hex.EncodeToString(d.h.Sum(nil)) }
