// Package integrationids converts external GUIDs at the protocol boundary.
// Catalogue IDs are lossless encodings of the same 128 bits. Only other native
// ID types need the small, typed persistent map; no query results are stored.
package integrationids

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
)

type Store struct{ DB *sql.DB }

func (s Store) Encode(ctx context.Context, kind, native string) (string, error) {
	if kind == "item" {
		raw, ok := entityid.Decode(native)
		if !ok {
			return "", entityid.ErrNotFound
		}
		return hex.EncodeToString(raw), nil
	}
	if native == "" || (kind != "library" && kind != "user" && kind != "server" && kind != "session" && kind != "key") {
		return "", entityid.ErrNotFound
	}
	digest := sha256.Sum256([]byte("portico.integration.v1\x00" + kind + "\x00" + native))
	guid := hex.EncodeToString(digest[:16])
	var known string
	var knownExpiry int64
	err := dbwork.QueryRow(ctx, s.DB, `SELECT guid,expires_ms FROM integration_ids WHERE kind=? AND native_id=?`, kind, native).Scan(&known, &knownExpiry)
	if err == nil && (knownExpiry == 0 || knownExpiry > time.Now().Add(12*time.Hour).UnixMilli()) {
		return known, nil
	}
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	gated, err := dbwork.Begin(ctx, s.DB, dbwork.ClassSecurityFence)
	if err != nil {
		return "", err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	expires := int64(0)
	if kind == "session" {
		expires = time.Now().Add(24 * time.Hour).UnixMilli()
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM integration_ids WHERE rowid IN(SELECT rowid FROM integration_ids WHERE expires_ms>0 AND expires_ms<? LIMIT 32)`, time.Now().UnixMilli()); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO integration_ids(kind,native_id,guid,expires_ms) VALUES(?,?,?,?) ON CONFLICT(kind,native_id) DO UPDATE SET expires_ms=excluded.expires_ms`, kind, native, guid, expires); err != nil {
		return "", err
	}
	if err = gated.Commit(); err != nil {
		return "", err
	}
	return guid, nil
}
func (s Store) Decode(ctx context.Context, kind, guid string) (string, error) {
	if len(guid) != 32 && (len(guid) != 36 || guid[8] != '-' || guid[13] != '-' || guid[18] != '-' || guid[23] != '-') {
		return "", entityid.ErrNotFound
	}
	compact := strings.ReplaceAll(guid, "-", "")
	raw, err := hex.DecodeString(compact)
	if err != nil || len(raw) != 16 {
		return "", entityid.ErrNotFound
	}
	compact = hex.EncodeToString(raw)
	if kind == "item" {
		return entityid.Encode(raw), nil
	}
	var native string
	err = dbwork.QueryRow(ctx, s.DB, `SELECT native_id FROM integration_ids WHERE kind=? AND guid=? AND (expires_ms=0 OR expires_ms>?)`, kind, compact, time.Now().UnixMilli()).Scan(&native)
	if err == sql.ErrNoRows {
		return "", entityid.ErrNotFound
	}
	return native, err
}
