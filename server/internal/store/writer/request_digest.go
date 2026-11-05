package writer

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"math"
	"reflect"
	"unicode/utf8"
)

const canonicalRequestCodec = "portico-writer-request-sha256-v1\x00"
const maxCanonicalRequestBytes = 32 << 20
const maxCanonicalRequestDepth = 32

// canonicalRequestDigest is preparation, not admission. Domain constructors
// pass their COMPLETE immutable request struct (native owner, captured input,
// authority/lease/cursor/output witnesses), not a hand-selected field list.
// Reflection includes every field, even unexported fields and json:"-" fields;
// adding one cannot silently omit it. No callbacks, maps or opaque interfaces.
// The writer recomputes this digest; a caller-supplied digest grants no authority.
func canonicalRequestDigest(kind CommandID, version uint16, identity CommandIdentity, request any) (RequestDigest, error) {
	if _, err := Describe(kind); err != nil {
		return RequestDigest{}, err
	}
	if version == 0 || identity.Sequence <= 0 || request == nil {
		return RequestDigest{}, invalid("canonical_request")
	}
	v := reflect.ValueOf(request)
	if v.Kind() != reflect.Struct {
		return RequestDigest{}, invalid("canonical_request_struct")
	}
	e := requestEncoder{h: sha256.New(), path: make(map[uintptr]bool)}
	e.bytes([]byte(canonicalRequestCodec))
	var header [4]byte
	binary.BigEndian.PutUint16(header[:2], uint16(kind))
	binary.BigEndian.PutUint16(header[2:], version)
	e.bytes(header[:])
	e.bytes(identity.Incarnation[:])
	e.number(uint64(identity.Sequence))
	e.bytes(identity.OperationID[:])
	e.value(v, 0)
	if e.err != nil {
		return RequestDigest{}, e.err
	}
	var digest RequestDigest
	copy(digest[:], e.h.Sum(nil))
	return digest, nil
}

type requestEncoder struct {
	h       hash.Hash
	written uint64
	err     error
	path    map[uintptr]bool
}

func (e *requestEncoder) bytes(p []byte) {
	if e.err != nil {
		return
	}
	if uint64(len(p)) > maxCanonicalRequestBytes-e.written {
		e.err = invalid("canonical_request_bytes")
		return
	}
	e.written += uint64(len(p))
	_, e.err = e.h.Write(p)
}
func (e *requestEncoder) number(n uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], n)
	e.bytes(b[:])
}
func (e *requestEncoder) flag(v bool) {
	b := byte(0)
	if v {
		b = 1
	}
	e.bytes([]byte{b})
}
func (e *requestEncoder) text(s string) {
	if uint64(len(s))+8 > maxCanonicalRequestBytes-e.written {
		e.err = invalid("canonical_request_bytes")
		return
	}
	if !utf8.ValidString(s) {
		e.err = invalid("canonical_request_text")
		return
	}
	e.number(uint64(len(s)))
	e.bytes([]byte(s))
}
func (e *requestEncoder) value(v reflect.Value, depth int) {
	if e.err != nil {
		return
	}
	if depth > maxCanonicalRequestDepth {
		e.err = invalid("canonical_request_depth")
		return
	}
	typ := v.Type()
	kind := v.Kind()
	if typ.PkgPath() == "time" && typ.Name() == "Time" {
		e.err = invalid("canonical_request_native_timestamp")
		return
	}
	// Type descriptors also cover nil containers and named byte elements.
	e.typeRef(typ, depth)
	if e.err != nil {
		return
	}
	switch kind {
	case reflect.Bool:
		e.flag(v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		e.number(uint64(v.Int()))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n := v.Uint()
		if n > math.MaxInt64 {
			e.err = invalid("canonical_request_unsigned")
			return
		}
		e.number(n)
	case reflect.Float64:
		n := v.Float()
		if math.IsNaN(n) || math.IsInf(n, 0) {
			e.err = invalid("canonical_request_float")
			return
		}
		e.number(math.Float64bits(n))
	case reflect.String:
		e.text(v.String())
	case reflect.Struct:
		e.number(uint64(v.NumField()))
		for i := 0; i < v.NumField() && e.err == nil; i++ {
			e.text(typ.Field(i).Name)
			e.value(v.Field(i), depth+1)
		}
	case reflect.Array, reflect.Slice:
		if kind == reflect.Slice {
			e.flag(!v.IsNil())
			if v.IsNil() {
				return
			}
		}
		e.number(uint64(v.Len()))
		if typ.Elem().Kind() == reflect.Uint8 {
			if kind == reflect.Slice {
				e.bytes(v.Bytes())
				return
			}
			var chunk [256]byte
			for at := 0; at < v.Len(); {
				n := min(len(chunk), v.Len()-at)
				for i := 0; i < n; i++ {
					chunk[i] = byte(v.Index(at + i).Uint())
				}
				e.bytes(chunk[:n])
				if e.err != nil {
					return
				}
				at += n
			}
			return
		}

		for i := 0; i < v.Len() && e.err == nil; i++ {
			e.value(v.Index(i), depth+1)
		}
	case reflect.Pointer:
		e.flag(!v.IsNil())
		if v.IsNil() {
			return
		}
		address := v.Pointer()
		if e.path[address] {
			e.err = invalid("canonical_request_cycle")
			return
		}
		e.path[address] = true
		e.value(v.Elem(), depth+1)
		delete(e.path, address)
	}
}

func requestTag(kind reflect.Kind) byte {
	switch kind {
	case reflect.Bool:
		return 1
	case reflect.Int:
		return 2
	case reflect.Int8:
		return 3
	case reflect.Int16:
		return 4
	case reflect.Int32:
		return 5
	case reflect.Int64:
		return 6
	case reflect.Uint:
		return 7
	case reflect.Uint8:
		return 8
	case reflect.Uint16:
		return 9
	case reflect.Uint32:
		return 10
	case reflect.Uint64:
		return 11
	case reflect.Float64:
		return 12
	case reflect.String:
		return 13
	case reflect.Array:
		return 14
	case reflect.Slice:
		return 15
	case reflect.Struct:
		return 16
	case reflect.Pointer:
		return 17
	default:
		return 0
	}
}
func (e *requestEncoder) typeRef(t reflect.Type, depth int) {
	if depth > maxCanonicalRequestDepth {
		e.err = invalid("canonical_request_type_depth")
		return
	}
	tag := requestTag(t.Kind())
	if tag == 0 {
		e.err = invalid("canonical_request_type")
		return
	}
	e.bytes([]byte{tag})
	e.text(t.PkgPath())
	e.text(t.Name())
	if t.Kind() == reflect.Struct && t.Name() == "" {
		e.number(uint64(t.NumField()))
		for i := 0; i < t.NumField() && e.err == nil; i++ {
			e.text(t.Field(i).Name)
			e.typeRef(t.Field(i).Type, depth+1)
		}
	}
	if t.Kind() == reflect.Array || t.Kind() == reflect.Slice || t.Kind() == reflect.Pointer {
		if t.Kind() == reflect.Array {
			e.number(uint64(t.Len()))
		}
		e.typeRef(t.Elem(), depth+1)
	}
}
