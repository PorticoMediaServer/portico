// Package entityid is the one conversion between a catalogue entity's stored
// public id (16 bytes in catalog_entities.public_id) and the id clients see:
// 22 characters of unpadded base64url. Inside the database every reference is
// the entity's integer id; the public form exists only where a row becomes an
// API value.
//
// SQL does the conversion where a query builds an API value: pid(public_id)
// encodes and pid_blob(text) decodes (NULL for anything malformed, so a bad id
// finds no row and reads as not found). Both are registered by dbwork for
// every connection and are defined here so Go and SQL agree byte for byte.
package entityid

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
)

// Size is the stored public id length in bytes; Length is its public form's.
const (
	Size   = 16
	Length = 22
)

// ErrNotFound is what an unknown or malformed public id resolves to.
var ErrNotFound = errors.New("catalogue entity not found")

var encoding = base64.RawURLEncoding

// Encode returns the public form of a stored public id.
func Encode(raw []byte) string {
	if len(raw) != Size {
		return ""
	}
	return encoding.EncodeToString(raw)
}

// Decode returns the stored form of a public id, or false when it is not one.
func Decode(public string) ([]byte, bool) {
	if len(public) != Length {
		return nil, false
	}
	raw, err := encoding.DecodeString(public)
	if err != nil || len(raw) != Size || encoding.EncodeToString(raw) != public {
		return nil, false
	}
	return raw, true
}

// New returns a fresh random stored public id.
func New() []byte {
	raw := make([]byte, Size)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return raw
}

// Querier is a transaction, connection or handle.
type Querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Resolve returns the integer id of the entity a public id names; the id of a
// title merged into another names the one that stayed. An unknown or
// malformed id is ErrNotFound, never a server error.
func Resolve(ctx context.Context, q Querier, public string) (int64, error) {
	raw, ok := Decode(public)
	if !ok {
		return 0, ErrNotFound
	}
	var id int64
	err := q.QueryRowContext(ctx, `SELECT id FROM catalog_entities WHERE public_id=?1
 UNION ALL SELECT entity_id FROM catalog_merged_ids WHERE public_id=?1 LIMIT 1`, raw).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

// Survivor returns the public id of the title that absorbed a merged-away
// title, or "" when public names no merged title.
func Survivor(ctx context.Context, q Querier, public string) (string, error) {
	raw, ok := Decode(public)
	if !ok {
		return "", nil
	}
	var survivor []byte
	err := q.QueryRowContext(ctx, `SELECT e.public_id FROM catalog_merged_ids m JOIN catalog_entities e ON e.id=m.entity_id
 WHERE m.public_id=?1 AND NOT EXISTS(SELECT 1 FROM catalog_entities WHERE public_id=?1)`, raw).Scan(&survivor)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return Encode(survivor), nil
}

// Public returns the public id of an entity by its integer id.
func Public(ctx context.Context, q Querier, id int64) (string, error) {
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT public_id FROM catalog_entities WHERE id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return Encode(raw), err
}
