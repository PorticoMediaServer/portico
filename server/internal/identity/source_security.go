package identity

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/networkpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/notify"
)

// The per-source budget is the one brute-force rule that is not about an
// account. A credential-stuffing run guesses a few passwords for many names
// from one place; the per-account ledgers in credential_security.go see each
// name once and never react. This budget sees the place.
//
// It is charged on every evaluated credential failure — password, two-step
// code, PIN, Quick Connect code, invitation code, reset or recovery code — and
// checked before the account is looked up, so a blocked source learns nothing
// about which names exist. It decays exactly as the per-account tables do: by
// half every fifteen minutes since the last failure.
const (
	// sourceFailureThreshold is the number of failures in the decaying window
	// that blocks a remote source. The sign-in policy scales it: relaxed
	// doubles it and strict halves it.
	sourceFailureThreshold = 30
	// A first block lasts fifteen minutes; every re-block doubles it, up to a
	// day. The failure count keeps the source at the threshold through the
	// block, so a source that returns is blocked again after a handful of
	// guesses rather than after a whole new run.
	sourceBlockBase    = int64(15 * 60)
	sourceBlockCeiling = int64(24 * 60 * 60)
	// A server-wide burst alert needs fifty failed sign-ins in ten minutes,
	// and at most one alert an hour.
	sourceBurstThreshold = 50
	sourceBurstWindow    = int64(10 * 60)
	sourceBurstAlertGap  = int64(60 * 60)
	// One source-block alert per network per day.
	sourceBlockAlertGap = int64(24 * 60 * 60)
	// A source that comes back a day after its last block starts the ladder
	// again instead of inheriting the longest wait it ever earned.
	sourceBlockMemory = int64(24 * 60 * 60)
	// The counters are bounded by pruning: hours live a week, minutes two
	// hours, and alert throttles a week.
	sourceCountRetention = int64(7 * 24 * 60 * 60)
	sourceMinuteRetain   = int64(2 * 60 * 60)
	// The row prune runs on writes, at most once a minute.
	sourcePruneGap = int64(60)
)

// ErrSourceBlocked is the per-source refusal. It is deliberately not an
// ErrUnauthorized: the credentials may be right, and clients read 401 as "your
// sign-in ended".
var ErrSourceBlocked = errors.New("Too many failed attempts from this network. Try again later.")

// SourceBlockError carries the exact Retry-After a blocked source receives.
type SourceBlockError struct{ RetryAfter int64 }

func (e *SourceBlockError) Error() string { return ErrSourceBlocked.Error() }
func (e *SourceBlockError) Unwrap() error { return ErrSourceBlocked }

// signInPeerAddress is the verified peer the request arrived from, without its
// port. credentialSource and sourceBucket both key their ledgers on it.
func signInPeerAddress(ctx context.Context) string {
	peer, _ := ctx.Value(signInSourceKey{}).(string)
	if peer == "" {
		claim, _ := ctx.Value(issuingDeviceKey{}).(issuingDevice)
		peer = claim.peer
	}
	host, _, err := net.SplitHostPort(peer)
	if err != nil {
		host = peer
	}
	return host
}

// sourceBucket is who a block applies to: the exact IPv4 address, or the /64
// an IPv6 customer is given. ok is false for a peer that cannot be parsed, which
// is never blocked: every such peer would share one bucket, and refusing it
// would refuse everyone behind whatever produced it.
func sourceBucket(ctx context.Context) (string, bool) {
	addr, err := netip.ParseAddr(signInPeerAddress(ctx))
	if err != nil {
		return "", false
	}
	addr = addr.Unmap().WithZone("")
	if !addr.IsValid() || addr.IsUnspecified() || addr.IsMulticast() {
		return "", false
	}
	if addr.Is4() {
		return addr.String(), true
	}
	return netip.PrefixFrom(addr, 64).Masked().String(), true
}

// sourceNetwork is what the owner sees and what an alert may say: a /24 for
// IPv4 and a /48 for IPv6, never the exact address. A bucket is either an
// address (IPv4) or a /64 prefix (IPv6).
func sourceNetwork(bucket string) string {
	addr, err := netip.ParseAddr(bucket)
	if err != nil {
		prefix, prefixErr := netip.ParsePrefix(bucket)
		if prefixErr != nil {
			return bucket
		}
		addr = prefix.Addr()
	}
	addr = addr.Unmap().WithZone("")
	if addr.Is4() {
		return netip.PrefixFrom(addr, 24).Masked().String()
	}
	return netip.PrefixFrom(addr, 48).Masked().String()
}

func sourceThreshold(policy AttemptPolicy) int {
	switch policy {
	case "relaxed":
		return sourceFailureThreshold * 2
	case "strict":
		return sourceFailureThreshold / 2
	}
	return sourceFailureThreshold
}

func sourceBlockDuration(blocks int) int64 {
	steps := min(max(blocks-1, 0), 10)
	return min(sourceBlockBase<<steps, sourceBlockCeiling)
}

// sourceBlocked is the refusal a blocked source gets before any account lookup.
// A home request is never blocked: an owner locked out of their own server by a
// shared public address is worse than the attack this stops.
func (s *Service) sourceBlocked(ctx context.Context) error {
	if networkpolicy.Home(ctx) {
		return nil
	}
	bucket, ok := sourceBucket(ctx)
	if !ok {
		return nil
	}
	read, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return err
	}
	var until int64
	err = read.Tx().QueryRowContext(ctx, `SELECT COALESCE((SELECT blocked_until FROM identity_source_attempts WHERE source_bucket=?),0)`, bucket).Scan(&until)
	if closeErr := read.Commit(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if wait := until - time.Now().Unix(); wait > 0 {
		return &SourceBlockError{RetryAfter: max(1, wait)}
	}
	return nil
}

// SourceBlocked applies the per-source refusal to a route that checks a
// credential outside this package (an invitation code). Identity's own sign-in
// paths call sourceBlocked themselves.
func (s *Service) SourceBlocked(ctx context.Context) error { return s.sourceBlocked(ctx) }

// RecordSourceFailure charges one evaluated credential failure to the request's
// source. It is for credential checks that do not run inside an identity
// transaction; the sign-in paths charge in their own transaction instead.
func (s *Service) RecordSourceFailure(ctx context.Context) error {
	return dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		return s.chargeSourceFailureTx(ctx, tx)
	})
}

// chargeSourceFailureTx records one failure and blocks the source when the
// decaying count reaches the policy's threshold. Home sources are counted but
// never blocked. The counters and the deduplicated owner alerts commit with the
// failure, so a retried request never produces a second notice.
func (s *Service) chargeSourceFailureTx(ctx context.Context, tx *sql.Tx) error {
	bucket, ok := sourceBucket(ctx)
	if !ok {
		return nil
	}
	policy, err := ReadSignInPolicyTx(ctx, tx)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	var attempts, blocks int
	var last, until, lastBlock int64
	err = tx.QueryRowContext(ctx, `SELECT failed_attempts,last_failure,blocked_until,block_count,last_block FROM identity_source_attempts WHERE source_bucket=?`, bucket).Scan(&attempts, &last, &until, &blocks, &lastBlock)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if last > 0 && now > last {
		attempts >>= min((now-last)/900, 32)
	}
	attempts++
	// A request that raced the guard may arrive while the block stands. It is
	// recorded but neither extends the block nor re-alerts: only a failure
	// after the block ends can start the next one.
	if until <= now && !networkpolicy.Home(ctx) && attempts >= sourceThreshold(policy.Values.PasswordAttempts) {
		if lastBlock > 0 && now-lastBlock > sourceBlockMemory {
			blocks = 0
		}
		blocks++
		duration := sourceBlockDuration(blocks)
		until = now + duration
		lastBlock = now
		if err = s.raiseSourceBlockedTx(ctx, tx, now, bucket, blocks, duration); err != nil {
			return err
		}
		if err = countSecurityEventTx(ctx, tx, now, "blocked_source"); err != nil {
			return err
		}
	}
	sourceID := s.setupHash("source-id", bucket)
	if _, err = tx.ExecContext(ctx, `INSERT INTO identity_source_attempts(source_bucket,source_id,failed_attempts,last_failure,blocked_until,block_count,last_block) VALUES(?,?,?,?,?,?,?)
 ON CONFLICT(source_bucket) DO UPDATE SET source_id=excluded.source_id,failed_attempts=excluded.failed_attempts,last_failure=excluded.last_failure,blocked_until=excluded.blocked_until,block_count=excluded.block_count,last_block=excluded.last_block`, bucket, sourceID, attempts, now, until, blocks, lastBlock); err != nil {
		return err
	}
	if err = countSecurityEventTx(ctx, tx, now, "failed_sign_in"); err != nil {
		return err
	}
	if err = s.raiseSignInBurstTx(ctx, tx, now); err != nil {
		return err
	}
	return s.pruneSourceSecurityTx(ctx, tx, now)
}

// raiseSourceBlockedTx tells the owner and administrators that one network was
// blocked, at most once per network per day. The body names the /24 or /48,
// never the address that was actually blocked.
func (s *Service) raiseSourceBlockedTx(ctx context.Context, tx *sql.Tx, now int64, bucket string, blocks int, duration int64) error {
	network := sourceNetwork(bucket)
	key := "source-blocked:" + strings.ReplaceAll(network, "/", "_")
	fresh, err := alertDueTx(ctx, tx, now, key, sourceBlockAlertGap)
	if err != nil || !fresh {
		return err
	}
	_, err = notify.NotifySourceBlocked(tx, now*1000, network, blocks, duration)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO identity_security_alerts(alert_key,raised_at) VALUES(?,?) ON CONFLICT(alert_key) DO UPDATE SET raised_at=excluded.raised_at`, key, now)
	return err
}

// raiseSignInBurstTx warns once an hour when fifty or more failed sign-ins
// happened server-wide in the last ten minutes.
func (s *Service) raiseSignInBurstTx(ctx context.Context, tx *sql.Tx, now int64) error {
	minute := now / 60
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(count),0) FROM identity_security_minutes WHERE minute>?`, minute-sourceBurstWindow/60).Scan(&count); err != nil {
		return err
	}
	if count < sourceBurstThreshold {
		return nil
	}
	fresh, err := alertDueTx(ctx, tx, now, "sign-in-burst", sourceBurstAlertGap)
	if err != nil || !fresh {
		return err
	}
	if _, err = notify.NotifySignInBurst(tx, now*1000, count); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO identity_security_alerts(alert_key,raised_at) VALUES(?,?) ON CONFLICT(alert_key) DO UPDATE SET raised_at=excluded.raised_at`, "sign-in-burst", now)
	return err
}

// alertDueTx reports whether an alert with this key may be raised, keeping a
// throttle row outside the notification tables so a duplicate alert updates no
// inbox.
func alertDueTx(ctx context.Context, tx *sql.Tx, now int64, key string, gap int64) (bool, error) {
	var raised int64
	err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT raised_at FROM identity_security_alerts WHERE alert_key=?),0)`, key).Scan(&raised)
	if err != nil {
		return false, err
	}
	return raised == 0 || now-raised >= gap, nil
}

// countSecurityEventTx advances one of the summary's bounded counters. The
// hourly counter feeds the summary; failed sign-ins alone also feed the
// minutely counter behind the burst alert, which is about failures.
func countSecurityEventTx(ctx context.Context, tx *sql.Tx, now int64, kind string) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO identity_security_counts(hour,kind,count) VALUES(?,?,1) ON CONFLICT(hour,kind) DO UPDATE SET count=count+1`, now/3600, kind); err != nil {
		return err
	}
	if kind != "failed_sign_in" {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO identity_security_minutes(minute,count) VALUES(?,1) ON CONFLICT(minute) DO UPDATE SET count=count+1`, now/60)
	return err
}

// pruneSourceSecurityTx keeps the new tables bounded. It runs on writes, at most
// once a minute, and deletes a source row only once its block has ended and its
// failures have decayed to zero, so the next visit from that source starts over.
func (s *Service) pruneSourceSecurityTx(ctx context.Context, tx *sql.Tx, now int64) error {
	if now-s.lastSecurityPrune < sourcePruneGap {
		return nil
	}
	s.lastSecurityPrune = now
	buckets := []string{}
	rows, err := tx.QueryContext(ctx, `SELECT source_bucket,failed_attempts,last_failure FROM identity_source_attempts WHERE blocked_until<=? AND last_failure<=? LIMIT 512`, now, now-900)
	if err != nil {
		return err
	}
	for rows.Next() {
		var bucket string
		var attempts int
		var last int64
		if err = rows.Scan(&bucket, &attempts, &last); err != nil {
			rows.Close()
			return err
		}
		if last > 0 && now > last {
			attempts >>= min((now-last)/900, 32)
		}
		if attempts == 0 {
			buckets = append(buckets, bucket)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, bucket := range buckets {
		if _, err = tx.ExecContext(ctx, `DELETE FROM identity_source_attempts WHERE source_bucket=?`, bucket); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM identity_security_counts WHERE hour<?`, now/3600-sourceCountRetention/3600); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM identity_security_minutes WHERE minute<?`, now/60-sourceMinuteRetain/60); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM identity_security_alerts WHERE raised_at<?`, now-sourceCountRetention)
	return err
}

// SecuritySummaryCounts is one window of the owner's security summary.
type SecuritySummaryCounts struct {
	FailedSignIns    int64 `json:"failedSignIns"`
	BlockedSources   int64 `json:"blockedSources"`
	NewRemoteDevices int64 `json:"newRemoteDevices"`
	// Connections from outside the remote gate refused, in total and by
	// reason (remotegate.RefusalReasons). The gate keeps them in memory, so
	// the HTTP layer fills them in; they restart with the server.
	RefusedConnections int64            `json:"refusedConnections"`
	RefusedByReason    map[string]int64 `json:"refusedByReason"`
}

// SecurityBlock is one source the server is refusing right now. Network is the
// /24 or /48; ID is the opaque handle POST /v1/admin/security/blocks/{id}/unblock
// takes. The exact address is never part of the answer.
type SecurityBlock struct {
	ID             string `json:"id"`
	Network        string `json:"network"`
	FailedAttempts int64  `json:"failedAttempts"`
	BlockCount     int64  `json:"blockCount"`
	BlockedUntil   string `json:"blockedUntil"`
	LastFailure    string `json:"lastFailure,omitempty"`
}

// SecuritySummary is what the owner screen reads: counts for the last day and
// week, and the blocks in force.
type SecuritySummary struct {
	Counts struct {
		Last24Hours SecuritySummaryCounts `json:"last24Hours"`
		Last7Days   SecuritySummaryCounts `json:"last7Days"`
	} `json:"counts"`
	Blocks []SecurityBlock `json:"blocks"`
}

// SecuritySummary reads the summary. Counters are hourly and pruned to a week,
// so both windows are exact to the hour and bounded in size.
func (s *Service) SecuritySummary(ctx context.Context) (out SecuritySummary, err error) {
	out.Blocks = []SecurityBlock{}
	read, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return out, err
	}
	defer read.Rollback()
	tx := read.Tx()
	now := time.Now().Unix()
	for _, window := range []struct {
		hours int64
		into  *SecuritySummaryCounts
	}{{24, &out.Counts.Last24Hours}, {7 * 24, &out.Counts.Last7Days}} {
		for _, kind := range []struct {
			name string
			into *int64
		}{{"failed_sign_in", &window.into.FailedSignIns}, {"blocked_source", &window.into.BlockedSources}, {"new_remote_device", &window.into.NewRemoteDevices}} {
			if err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(count),0) FROM identity_security_counts WHERE kind=? AND hour>?`, kind.name, now/3600-window.hours).Scan(kind.into); err != nil {
				return out, err
			}
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT source_id,source_bucket,failed_attempts,block_count,blocked_until,last_failure FROM identity_source_attempts WHERE blocked_until>? ORDER BY blocked_until,source_bucket`, now)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var block SecurityBlock
		var bucket string
		var until, last int64
		if err = rows.Scan(&block.ID, &bucket, &block.FailedAttempts, &block.BlockCount, &until, &last); err != nil {
			return out, err
		}
		block.Network = sourceNetwork(bucket)
		block.BlockedUntil = time.Unix(until, 0).UTC().Format(time.RFC3339)
		if last > 0 {
			block.LastFailure = time.Unix(last, 0).UTC().Format(time.RFC3339)
		}
		out.Blocks = append(out.Blocks, block)
	}
	return out, rows.Err()
}

// validSourceID is the base64url handle setupHash gives a source, and the only
// shape an unblock path value may have.
func validSourceID(v string) bool { return len(v) == 43 && validFamilyID(v) }

// UnblockSource clears one block and its failure count, so the owner can let a
// mistaken block go at once instead of waiting it out.
func (s *Service) UnblockSource(ctx context.Context, id string) error {
	if !validSourceID(id) {
		return ErrDirectInput
	}
	return dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM identity_source_attempts WHERE source_id=?`, id)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil {
			return err
		} else if n != 1 {
			return sql.ErrNoRows
		}
		return nil
	})
}
