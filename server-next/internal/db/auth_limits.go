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
	// Lasting failures do not fade: they stand until cleared, or for a day after the last one.
	// Without it the count halves every 15 minutes.
	Lasting bool
	// Doubling makes each wait twice as long as the one before, up to a day, and starts
	// over once a day has passed since the last wait ended.
	Doubling bool
	// Shown keeps the key readable beside its hash while a wait is in force, so the owner
	// can be told which network is blocked. Only a network's own address is ever shown.
	Shown bool
}

// limitHalfLife is how fast old failures stop counting: the count halves each period.
// limitForget is when a count is dropped altogether.
const (
	limitHalfLife = 15 * time.Minute
	limitForget   = 24 * time.Hour
)

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

func limitCount(tx *sql.Tx, bucket, key string, now time.Time, lasting bool) (int, error) {
	var failures int
	var last int64
	err := tx.QueryRow(`SELECT failures, last_failure_at FROM auth_limits WHERE bucket = ? AND key_hash = ?`, bucket, limitKey(key)).Scan(&failures, &last)
	age := now.Sub(time.UnixMilli(last))
	switch {
	case errors.Is(err, sql.ErrNoRows), err == nil && age >= limitForget:
		return 0, nil
	case lasting:
		return failures, err
	}
	return failures >> min(max(age/limitHalfLife, 0), 30), err
}

// limitStore records a count as of at, and the wait that follows it, if any.
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
	// Counts that are a day old no longer matter.
	if _, err := tx.Exec(`DELETE FROM auth_limits WHERE last_failure_at < ?`, now.Add(-limitForget).UnixMilli()); err != nil {
		return err
	}
	for _, l := range limits {
		failures, err := limitCount(tx, l.Bucket, l.Key, now, l.Lasting)
		if err != nil {
			return err
		}
		if l.FloorKey != "" {
			floor, err := limitCount(tx, l.Bucket, l.FloorKey, now, l.Lasting)
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
		if wait > 0 && l.Doubling {
			// How many waits came before this one is kept beside the count, dated when the
			// last of them ended.
			repeats, err := limitCount(tx, l.Bucket+"_repeat", l.Key, now, true)
			if err != nil {
				return err
			}
			wait = min(wait<<min(repeats, 10), limitForget)
			if err := limitStore(tx, l.Bucket+"_repeat", l.Key, repeats+1, now.Add(wait), 0); err != nil {
				return err
			}
		}
		if err := limitStore(tx, l.Bucket, l.Key, failures, now, wait); err != nil {
			return err
		}
		if wait > 0 && l.Shown {
			if _, err := tx.Exec(`UPDATE auth_limits SET shown_key = ? WHERE bucket = ? AND key_hash = ?`, l.Key, l.Bucket, limitKey(l.Key)); err != nil {
				return err
			}
		}
	}
	return nil
}

// limitClear forgets the failures behind a proof that succeeded. A network's count of
// everything it got wrong is left alone: one good sign-in must not wipe its record.
func limitClear(tx *sql.Tx, limits []Limit) error {
	for _, l := range limits {
		if l.Doubling {
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

// RecordFailure counts one wrong guess that had no transaction of its own, such as a
// sign-in code nobody issued.
func (d *DB) RecordFailure(ctx context.Context, now time.Time, limits ...Limit) error {
	return d.write(ctx, User, func(tx *sql.Tx) error { return limitFail(tx, now, limits) })
}

// SourceBucket counts everything one network away from home gets wrong.
const SourceBucket = "source"

// BlockedNetwork is a network that is refused every sign-in, PIN and code until Until.
type BlockedNetwork struct {
	// ID names the block in UnblockNetwork.
	ID string
	// Network is an IPv4 address, or the /64 of an IPv6 address.
	Network string
	Until   time.Time
}

// BlockedNetworks lists the networks blocked now, the one blocked longest first.
func (d *DB) BlockedNetworks(ctx context.Context, now time.Time) ([]BlockedNetwork, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT key_hash, shown_key, blocked_until_at FROM auth_limits
		WHERE bucket = ? AND blocked_until_at > ? AND shown_key IS NOT NULL ORDER BY blocked_until_at DESC LIMIT 500`, SourceBucket, now.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BlockedNetwork{}
	for rows.Next() {
		var b BlockedNetwork
		var until int64
		if err := rows.Scan(&b.ID, &b.Network, &until); err != nil {
			return nil, err
		}
		b.Until = time.UnixMilli(until)
		out = append(out, b)
	}
	return out, rows.Err()
}

// UnblockNetwork lets a blocked network try again at once and forgets how often it has
// been blocked, so its next block is a first one. A block that has already ended is ErrNotFound.
func (d *DB) UnblockNetwork(ctx context.Context, actorID int64, id string, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		var network string
		err := tx.QueryRow(`DELETE FROM auth_limits WHERE bucket = ? AND key_hash = ? AND blocked_until_at > ? RETURNING ifnull(shown_key, '')`,
			SourceBucket, id, now.UnixMilli()).Scan(&network)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM auth_limits WHERE bucket = ? AND key_hash = ?`, SourceBucket+"_repeat", id); err != nil {
			return err
		}
		return audit(tx, actorID, "network.unblock", network, "", now)
	})
}
