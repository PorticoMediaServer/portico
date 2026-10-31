package wirejson

// This file is public-only. object.go beside it is synced from Hosted
// (server/internal/claimweb/source.json); edit it there.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

var ErrResponse = errors.New("invalid response document")

// Response reads a plain Hosted response document into out. Like the generated
// clients (R-09b), it ignores a field a newer Hosted adds at any depth. It
// still refuses a key named twice in one object (or twice up to encoding/json's
// case folding, which would read both into one field), anything after the
// document, and malformed JSON. Signed statements never come through here:
// their verifiers decode exact.
func Response(raw []byte, out any) error {
	if repeatsKey(raw) {
		return ErrResponse
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if d.Decode(out) != nil || d.Decode(new(any)) != io.EOF {
		return ErrResponse
	}
	return nil
}

// repeatsKey reports whether any object in raw names a key twice, folded as
// encoding/json matches field names. Invalid JSON is left for the decoder.
func repeatsKey(raw []byte) bool {
	type object struct {
		keys    map[string]bool
		wantKey bool
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	var open []*object // nil for an open array
	valueDone := func() {
		if n := len(open); n > 0 && open[n-1] != nil {
			open[n-1].wantKey = true
		}
	}
	for {
		token, e := d.Token()
		if e != nil {
			return false
		}
		if n := len(open); n > 0 && open[n-1] != nil && open[n-1].wantKey {
			if key, ok := token.(string); ok {
				// Upper then lower folds U+017F (ſ) to s and U+212A (Kelvin) to k, as encoding/json does.
				top, folded := open[n-1], strings.ToLower(strings.ToUpper(key))
				if top.keys[folded] {
					return true
				}
				top.keys[folded], top.wantKey = true, false
				continue
			}
		}
		switch token {
		case json.Delim('{'):
			open = append(open, &object{keys: map[string]bool{}, wantKey: true})
		case json.Delim('['):
			open = append(open, nil)
		case json.Delim('}'), json.Delim(']'):
			open = open[:len(open)-1]
			valueDone()
		default:
			valueDone()
		}
	}
}
