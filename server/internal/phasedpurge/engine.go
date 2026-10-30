// Package phasedpurge executes compiled child-first cleanup units. Owners retain
// their literal SQL, authority/custody fences and durable jobs. A unit's source
// proof must include its indexes, raw candidate window and all FK/trigger costs;
// the runtime meter is a falsifier, never a substitute for that proof.
package phasedpurge

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"
)

const (
	EffectLimit = 24
	ReadLimit   = 128
	CursorLimit = 512
)

var (
	ErrCursor     = errors.New("invalid phased purge cursor")
	ErrPlan       = errors.New("invalid compiled phased purge plan")
	ErrEffects    = errors.New("phased purge effect budget exceeded")
	ErrReads      = errors.New("phased purge candidate budget exceeded")
	ErrNoProgress = errors.New("phased purge unit made no progress")
)

type Kind string

const (
	Text    Kind = "text"
	Integer Kind = "integer"
	Blob    Kind = "blob"
)

// Key preserves SQLite's storage class as well as its value across restart.
// Integers are canonical decimal int64; blobs are canonical unpadded base64url.
type Key struct {
	Kind  Kind   `json:"t"`
	Value string `json:"v"`
}
type Cursor struct {
	Unit   int   `json:"u,omitempty"`
	Key    []Key `json:"k,omitempty"`
	Verify int   `json:"v,omitempty"`
}
type Outcome struct {
	Drained bool
	Changed bool
	Next    []Key
	Rewind  bool
}
type Unit struct {
	Phase     int
	Name      string
	KeyKinds  []Kind
	ReadBound int
	Run       func(context.Context, *sql.Tx, []Key) (Outcome, error)
	Empty     func(context.Context, *sql.Tx) (bool, error)
}
type Owner struct {
	// Reserve every candidate/probe outside the units, including job selection,
	// target/custody validation and the two total_changes() scalar reads.
	ReadBound int
	Before    func(context.Context, *sql.Tx) error
	Save      func(context.Context, *sql.Tx, Cursor) error
	Finalize  func(context.Context, *sql.Tx) error
}
type Result struct {
	Cursor  Cursor
	Worked  bool
	Done    bool
	Reads   int64
	Effects int64
}

func Keys(values ...any) ([]Key, error) {
	out := make([]Key, len(values))
	for i, v := range values {
		switch x := v.(type) {
		case string:
			if !utf8.ValidString(x) {
				return nil, ErrCursor
			}
			out[i] = Key{Text, x}
		case int64:
			out[i] = Key{Integer, strconv.FormatInt(x, 10)}
		case int:
			out[i] = Key{Integer, strconv.FormatInt(int64(x), 10)}
		case []byte:
			out[i] = Key{Blob, base64.RawURLEncoding.EncodeToString(x)}
		default:
			return nil, ErrCursor
		}
	}
	return out, nil
}
func SQLKeys(keys []Key) ([]any, error) {
	out := make([]any, len(keys))
	for i, k := range keys {
		switch k.Kind {
		case Text:
			if !utf8.ValidString(k.Value) {
				return nil, ErrCursor
			}
			out[i] = k.Value
		case Integer:
			n, err := strconv.ParseInt(k.Value, 10, 64)
			if err != nil || strconv.FormatInt(n, 10) != k.Value {
				return nil, ErrCursor
			}
			out[i] = n
		case Blob:
			b, err := base64.RawURLEncoding.DecodeString(k.Value)
			if err != nil || base64.RawURLEncoding.EncodeToString(b) != k.Value {
				return nil, ErrCursor
			}
			out[i] = b
		default:
			return nil, ErrCursor
		}
	}
	return out, nil
}
func EncodeCursor(c Cursor) (string, error) {
	if c.Unit < 0 || c.Verify < 0 {
		return "", ErrCursor
	}
	if _, err := SQLKeys(c.Key); err != nil {
		return "", err
	}
	raw, err := json.Marshal(c)
	if err != nil || len(raw) > CursorLimit {
		return "", ErrCursor
	}
	return string(raw), nil
}
func DecodeCursor(raw string, plan []Unit) (Cursor, error) {
	var c Cursor
	if raw != "" {
		if bytes.Equal(bytes.TrimSpace([]byte(raw)), []byte("null")) {
			return c, ErrCursor
		}
		if len(raw) > CursorLimit || !utf8.ValidString(raw) {
			return c, ErrCursor
		}
		d := json.NewDecoder(bytes.NewBufferString(raw))
		d.DisallowUnknownFields()
		if err := d.Decode(&c); err != nil {
			return Cursor{}, ErrCursor
		}
		var extra any
		if err := d.Decode(&extra); err != io.EOF {
			return Cursor{}, ErrCursor
		}
	}
	if err := validateCursor(c, plan); err != nil {
		return Cursor{}, err
	}
	return c, nil
}
func validateCursor(c Cursor, plan []Unit) error {
	if c.Unit < 0 || c.Unit > len(plan) || c.Verify < 0 || c.Verify > len(plan) {
		return ErrCursor
	}
	if c.Unit < len(plan) && c.Verify != 0 {
		return ErrCursor
	}
	if c.Unit == len(plan) {
		if len(c.Key) != 0 || c.Verify == len(plan) {
			return ErrCursor
		}
		return nil
	}
	if len(c.Key) > 0 {
		kinds := plan[c.Unit].KeyKinds
		if len(c.Key) != len(kinds) {
			return ErrCursor
		}
		for i, k := range c.Key {
			if k.Kind != kinds[i] {
				return ErrCursor
			}
		}
	}
	if _, err := SQLKeys(c.Key); err != nil {
		return err
	}
	if _, err := EncodeCursor(c); err != nil {
		return err
	}
	return nil
}
func validatePlan(plan []Unit, o Owner) error {
	if len(plan) == 0 || o.ReadBound < 2 || o.ReadBound >= ReadLimit || o.Save == nil || o.Finalize == nil {
		return ErrPlan
	}
	phase := -1
	for _, u := range plan {
		if u.Name == "" || u.Phase < phase || u.Phase < 0 || u.ReadBound < 1 || u.ReadBound > ReadLimit-o.ReadBound || u.Run == nil || u.Empty == nil {
			return ErrPlan
		}
		for _, k := range u.KeyKinds {
			if k != Text && k != Integer && k != Blob {
				return ErrPlan
			}
		}
		phase = u.Phase
	}
	return nil
}
func changes(ctx context.Context, tx *sql.Tx) (int64, error) {
	var n int64
	err := tx.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&n)
	return n, err
}

// Turn never commits. The owner must roll back the complete transaction on any
// error, then persist a visible blocked/backoff outcome separately. Before, the
// unit, Save and Finalize are all inside the same metered writer transaction.
func Turn(ctx context.Context, tx *sql.Tx, plan []Unit, c Cursor, o Owner) (result Result, turnErr error) {
	initial := c
	result = Result{Cursor: c, Reads: int64(o.ReadBound)}
	defer func() {
		if turnErr != nil {
			result.Worked = false
			result.Done = false
			result.Cursor = initial
		}
	}()
	if tx == nil {
		return result, ErrPlan
	}
	if err := validatePlan(plan, o); err != nil {
		return result, err
	}
	if err := validateCursor(c, plan); err != nil {
		return result, err
	}
	before, err := changes(ctx, tx)
	if err != nil {
		return result, err
	}
	if o.Before != nil {
		if err = o.Before(ctx, tx); err != nil {
			return result, err
		}
	}
	finish := func(done bool) (Result, error) {
		result.Cursor = c
		if done {
			if err := o.Finalize(ctx, tx); err != nil {
				return result, err
			}
			result.Done = true
			result.Worked = true
			// A completion claim is never a resumable verification cursor.
			result.Cursor = Cursor{Unit: len(plan)}
		} else {
			if err := o.Save(ctx, tx, c); err != nil {
				return result, err
			}
		}
		after, err := changes(ctx, tx)
		if err != nil {
			return result, err
		}
		result.Effects = after - before
		if result.Effects < 0 || result.Effects > EffectLimit {
			return result, ErrEffects
		}
		return result, nil
	}
	if c.Unit < len(plan) {
		for n := 0; n < 4 && c.Unit < len(plan); n++ {
			u := plan[c.Unit]
			if result.Reads+int64(u.ReadBound) > ReadLimit {
				break
			}
			result.Reads += int64(u.ReadBound)
			out, err := u.Run(ctx, tx, c.Key)
			if err != nil {
				return result, fmt.Errorf("purge unit %s: %w", u.Name, err)
			}
			if out.Rewind {
				if out.Drained || out.Changed || len(out.Next) > 0 {
					return result, ErrPlan
				}
				start := c.Unit
				for start > 0 && plan[start-1].Phase == u.Phase {
					start--
				}
				if start == c.Unit {
					return result, ErrNoProgress
				}
				c = Cursor{Unit: start}
				result.Worked = true
				return finish(false)
			}
			if out.Drained {
				if len(out.Next) > 0 {
					return result, ErrPlan
				}
				c.Unit++
				c.Key = nil
				result.Worked = true
				if out.Changed {
					return finish(false)
				}
				continue
			}
			if !out.Changed {
				return result, ErrNoProgress
			}
			c.Key = out.Next
			if err := validateCursor(c, plan); err != nil {
				return result, err
			}
			result.Worked = true
			return finish(false)
		}
		if !result.Worked {
			return result, ErrReads
		}
		return finish(false)
	}
	for n := 0; n < 8 && c.Verify < len(plan); n++ {
		u := plan[c.Verify]
		if result.Reads+int64(u.ReadBound) > ReadLimit {
			break
		}
		result.Reads += int64(u.ReadBound)
		empty, err := u.Empty(ctx, tx)
		if err != nil {
			return result, fmt.Errorf("purge proof %s: %w", u.Name, err)
		}
		if !empty {
			start := c.Verify
			for start > 0 && plan[start-1].Phase == u.Phase {
				start--
			}
			c = Cursor{Unit: start}
			result.Worked = true
			return finish(false)
		}
		c.Verify++
		result.Worked = true
	}
	if c.Verify == len(plan) {
		return finish(true)
	}
	if !result.Worked {
		return result, ErrReads
	}
	return finish(false)
}
