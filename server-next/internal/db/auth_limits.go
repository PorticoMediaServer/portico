package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

// Limit is one count of wrong guesses. A limit makes whoever is guessing wait; it never
// locks an account, and it is counted the same whether or not the username exists.
type Limit struct {
	// Bucket names the kind of guess: "password", "pin", "source".
	Bucket string
	// Key is what is being counted, such as a source address with a username. It is stored hashed.
	Key string
	// FloorKey, if set, is a second count in the same bucket that every failure also adds
	// to, and that this count never falls below. It lets guesses spread over many sources
	// against one account slow each new source down, while a correct first try from a new
	// source still works at once.
	FloorKey string
	// Delay says how long to make the source wait after this many recent failures.
	Delay func(failures int) time.Duration
}

// limitHalfLife is how fast old failures stop counting: the count halves each period.
const limitHalfLife = 15 * time.Minute

func limitKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// limitWait returns how long the caller must still wait under any of the limits.
func limitWait(ctx context.Context, q queryer, now time.Time, limits []Limit) (time.Duration, error) {
	var wait time.Duration
	for _, l := range limits {
		var until sql.NullInt64
		err := q.QueryRowContext(ctx, `SELECT blocked_until_at FROM auth_limits WHERE bucket = ? AND key_hash = ?`, l.Bucket, limitKey(l.Key)).Scan(&until)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		if w := time.UnixMilli(until.Int64).Sub(now); until.Valid && w > wait {
			wait = w
		}
	}
	return wait, nil
}

// LimitWait is the check made before the costly password comparison. The transaction that
// records the result checks again, so a burst of guesses sent together gains nothing.
func (d *DB) LimitWait(ctx context.Context, now time.Time, limits ...Limit) (time.Duration, error) {
	return limitWait(ctx, d.read, now, limits)
}

func limitCount(tx *sql.Tx, bucket, key string, now time.Time) (int, error) {
	var failures int
	var last int64
	err := tx.QueryRow(`SELECT failures, last_failure_at FROM auth_limits WHERE bucket = ? AND key_hash = ?`, bucket, limitKey(key)).Scan(&failures, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	halvings := now.Sub(time.UnixMilli(last)) / limitHalfLife
	return failures >> min(max(halvings, 0), 30), err
}

func limitStore(tx *sql.Tx, bucket, key string, failures int, now time.Time, wait time.Duration) error {
	var until any
	if wait > 0 {
		until = now.Add(wait).UnixMilli()
	}
	_, err := tx.Exec(`INSERT INTO auth_limits (bucket, key_hash, failures, last_failure_at, blocked_until_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (bucket, key_hash) DO UPDATE SET failures = excluded.failures,
			last_failure_at = excluded.last_failure_at, blocked_until_at = excluded.blocked_until_at`,
		bucket, limitKey(key), failures, now.UnixMilli(), until)
	return err
}

// limitFail counts one wrong guess against every limit.
func limitFail(tx *sql.Tx, now time.Time, limits []Limit) error {
	// Counts that have had a day to fade no longer matter.
	if _, err := tx.Exec(`DELETE FROM auth_limits WHERE last_failure_at < ?`, now.Add(-24*time.Hour).UnixMilli()); err != nil {
		return err
	}
	for _, l := range limits {
		failures, err := limitCount(tx, l.Bucket, l.Key, now)
		if err != nil {
			return err
		}
		if l.FloorKey != "" {
			floor, err := limitCount(tx, l.Bucket, l.FloorKey, now)
			if err != nil {
				return err
			}
			if err := limitStore(tx, l.Bucket, l.FloorKey, floor+1, now, 0); err != nil {
				return err
			}
			failures = max(failures, floor)
		}
		failures++
		var wait time.Duration
		if l.Delay != nil {
			wait = l.Delay(failures)
		}
		if err := limitStore(tx, l.Bucket, l.Key, failures, now, wait); err != nil {
			return err
		}
	}
	return nil
}

// limitClear forgets the failures behind a proof that succeeded. A source-wide count has no
// Delay tied to one secret and is left alone: one good sign-in must not wipe a source's record.
func limitClear(tx *sql.Tx, limits []Limit) error {
	for _, l := range limits {
		if l.Bucket == "source" {
			continue
		}
		for _, key := range []string{l.Key, l.FloorKey} {
			if key == "" {
				continue
			}
			if _, err := tx.Exec(`DELETE FROM auth_limits WHERE bucket = ? AND key_hash = ?`, l.Bucket, limitKey(key)); err != nil {
				return err
			}
		}
	}
	return nil
}
