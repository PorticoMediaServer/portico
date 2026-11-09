package db

import (
	"database/sql"
	"errors"
	"time"
)

// idempotencyKeep is how long a key is remembered. Apps drop an unsent request after 30 days.
const idempotencyKeep = 35 * 24 * time.Hour

// createdBefore looks up an Idempotency-Key inside the transaction that would create
// something. It returns the public id of what the same request created earlier, or "" if
// the key is new (or absent). The same key with a different request is ErrKeyReused.
func createdBefore(tx *sql.Tx, userID int64, key, requestHash string) (string, error) {
	if key == "" {
		return "", nil
	}
	var hash, ref string
	err := tx.QueryRow(`SELECT request_hash, created_ref FROM idempotency_keys WHERE user_id = ? AND key = ?`, userID, key).Scan(&hash, &ref)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err == nil && hash != requestHash {
		return "", ErrKeyReused
	}
	return ref, err
}

// rememberCreated records what a keyed request created, in the same transaction, and drops
// keys too old to be repeated.
func rememberCreated(tx *sql.Tx, userID int64, key, requestHash, createdRef string, now time.Time) error {
	if key == "" {
		return nil
	}
	if _, err := tx.Exec(`DELETE FROM idempotency_keys WHERE created_at < ?`, now.Add(-idempotencyKeep).UnixMilli()); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO idempotency_keys (user_id, key, request_hash, created_ref, created_at) VALUES (?, ?, ?, ?, ?)`,
		userID, key, requestHash, createdRef, now.UnixMilli())
	return err
}
