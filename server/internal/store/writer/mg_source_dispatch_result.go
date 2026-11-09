package writer

import (
	"bytes"
	"encoding/binary"
	"io"
)

const mgSourceDenialCap = 512

func preflightMGSourceDenial(v mgSourceOriginalDenial) error {
	switch v.Command {
	case MGConfigureTuner, MGSetSourceEnabled, MGRetireSource, MGSetSourceViewerAccess:
	default:
		return ErrResolveRequired
	}
	if v.Version != 1 || v.Revision <= 0 || !mpWorkerID(v.Source) || v.Code != "source_retired" || v.AppliedMS <= 0 {
		return ErrResolveRequired
	}
	return nil
}
func writeMGSourceDenial(e *requestEncoder, id CommandIdentity, q RequestDigest, v mgSourceOriginalDenial) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("mg_source_denial")
	e.bytes(id.Incarnation[:])
	e.number(uint64(id.Sequence))
	e.bytes(id.OperationID[:])
	e.bytes(q[:])
	e.number(uint64(v.Command))
	e.number(uint64(v.Version))
	e.number(uint64(v.Revision))
	e.text(v.Source)
	e.text(v.Code)
	e.number(uint64(v.AppliedMS))
	return e.err
}
func mgSourceDenialPayload(id CommandIdentity, q RequestDigest, v mgSourceOriginalDenial) ([]byte, ResultDigest, error) {
	d, err := canonicalResultDigest(id, q, v)
	if err != nil {
		return nil, ResultDigest{}, err
	}
	b := &mpCodecBuffer{}
	e := requestEncoder{h: b}
	if err = encodeCanonicalResult(&e, id, q, v); err != nil {
		return nil, ResultDigest{}, err
	}
	if b.Len() > mgSourceDenialCap {
		return nil, ResultDigest{}, ErrCap
	}
	return b.Bytes(), d, nil
}

// Bounded closed decoder, with exact W re-encoding including identity, request,
// command kind, original native revision and time. No JSON/result registry.
func decodeMGSourceDenial(id CommandIdentity, q RequestDigest, p []byte, d ResultDigest) (out mgSourceOriginalDenial, err error) {
	if len(p) == 0 || len(p) > mgSourceDenialCap {
		return out, ErrResolveRequired
	}
	r := bytes.NewReader(p)
	// Every prefix is fixed-size except two bounded result strings. Re-encoding
	// below checks the full prefix, not just the fields projected by this decoder.
	prefix := len(canonicalResultCodec) + 2 + 8 + len("mg_source_denial") + 16 + 8 + 16 + 32
	if _, err = r.Seek(int64(prefix), io.SeekStart); err != nil {
		return out, ErrResolveRequired
	}
	var kind uint64
	for _, n := range []any{&kind, &out.Version, &out.Revision} {
		if binary.Read(r, binary.BigEndian, n) != nil {
			return mgSourceOriginalDenial{}, ErrResolveRequired
		}
	}
	out.Command = CommandID(kind)
	text := func() (string, error) {
		var n uint64
		if binary.Read(r, binary.BigEndian, &n) != nil || n > 128 || n > uint64(r.Len()) {
			return "", ErrResolveRequired
		}
		b := make([]byte, int(n))
		_, e := io.ReadFull(r, b)
		return string(b), e
	}
	if out.Source, err = text(); err != nil {
		return mgSourceOriginalDenial{}, ErrResolveRequired
	}
	if out.Code, err = text(); err != nil {
		return mgSourceOriginalDenial{}, ErrResolveRequired
	}
	if binary.Read(r, binary.BigEndian, &out.AppliedMS) != nil || r.Len() != 0 {
		return mgSourceOriginalDenial{}, ErrResolveRequired
	}
	expected, actual, e := mgSourceDenialPayload(id, q, out)
	if e != nil || actual != d || !bytes.Equal(expected, p) {
		return mgSourceOriginalDenial{}, ErrResolveRequired
	}
	return out, nil
}
