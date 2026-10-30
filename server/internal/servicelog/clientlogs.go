package servicelog

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxClientUploadBytes bounds one client log upload. A client that has more to
// say sends it as several uploads rather than one unbounded body, so a
// misbehaving client cannot fill the state directory in a single request.
const MaxClientUploadBytes = 256 << 10

const ClientUploadsPerDevice = 5
const ClientLogDailyBytes = 1 << 20

var ErrClientUpload = errors.New("invalid client log upload")
var ErrClientUploadDevice = errors.New("client log upload requires a device session")
var ErrClientLogBudget = errors.New("daily client log budget exhausted")

var (
	bearerSecret = regexp.MustCompile(`(?i)\bBearer\s+[^\s"'<>]+`)
	mediaGrant   = regexp.MustCompile(`(?i)/v1/media/[^\s"'<>]+`)
	querySecret  = regexp.MustCompile(`(?i)([?&](?:grant|token|key|credential|api[_-]?key|access[_-]?token|refresh[_-]?token|device[_-]?code|password|secret|signature)=)[^\s&#"'<>]+`)
	// Short passwords, factor seeds and provider keys do not match the generic
	// token length heuristic. Scrub their named values in JSON and key/value logs.
	namedSecret    = regexp.MustCompile(`(?i)(\b(?:password|passphrase|pin|totp[_-]?secret|factor[_-]?seed|recovery[_-]?code|api[_-]?key|access[_-]?token|refresh[_-]?token|device[_-]?code|client[_-]?secret|authorization|cookie|set-cookie|secret|credential)\b["']?\s*[:=]\s*)("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\s,;&}]+)`)
	secretHeader   = regexp.MustCompile(`(?im)(\b(?:authorization|proxy-authorization|cookie|set-cookie)\s*:\s*)[^\r\n]+`)
	urlCredentials = regexp.MustCompile(`(?i)(https?://)[^/\s?#"']+@`)
	longSecret     = regexp.MustCompile(`[A-Za-z0-9_-]{40,}`)
)

func redactClientLog(body string) string {
	body = secretHeader.ReplaceAllString(body, "${1}[REDACTED]")
	body = bearerSecret.ReplaceAllString(body, "Bearer [REDACTED]")
	body = namedSecret.ReplaceAllStringFunc(body, func(match string) string {
		parts := namedSecret.FindStringSubmatch(match)
		value := "[REDACTED]"
		if strings.HasPrefix(parts[2], "\"") {
			value = `"[REDACTED]"`
		} else if strings.HasPrefix(parts[2], "'") {
			value = "'[REDACTED]'"
		}
		return parts[1] + value
	})
	body = urlCredentials.ReplaceAllString(body, "${1}[REDACTED]@")
	body = bearerSecret.ReplaceAllString(body, "Bearer [REDACTED]")
	body = mediaGrant.ReplaceAllString(body, "/v1/media/[REDACTED]")
	body = querySecret.ReplaceAllString(body, "${1}[REDACTED]")
	return longSecret.ReplaceAllString(body, "[REDACTED]")
}

// ClientLogStore holds client log uploads. They are stored per device and
// profile so an administrator can find the one report that matters.
type ClientLogStore struct {
	DB  *sql.DB
	Now func() time.Time
}

func NewClientLogStore(db *sql.DB) *ClientLogStore {
	return &ClientLogStore{DB: db, Now: time.Now}
}

type ClientUpload struct {
	ID          string `json:"id"`
	AccountID   string `json:"accountId"`
	ProfileID   string `json:"profileId"`
	DeviceID    string `json:"deviceId"`
	Platform    string `json:"platform"`
	DeviceClass string `json:"deviceClass"`
	AppVersion  string `json:"appVersion,omitempty"`
	ReceivedAt  string `json:"receivedAt"`
	Bytes       int64  `json:"bytes"`
	// Body is present only on a single-upload read, never in a list.
	Body string `json:"body,omitempty"`
}

type ClientUploadPage struct {
	Limit      int            `json:"limit"`
	NextCursor string         `json:"nextCursor,omitempty"`
	Items      []ClientUpload `json:"items"`
}

type ClientUploadRequest struct {
	DeviceID    string `json:"deviceId"`
	Platform    string `json:"platform"`
	DeviceClass string `json:"deviceClass"`
	AppVersion  string `json:"appVersion"`
	Body        string `json:"body"`
}

func bounded(v string, max int) bool { return utf8.ValidString(v) && len(v) <= max }

// Store records one upload. The device is resolved from the authenticated
// access token; the client's deviceId is never trusted for retention.
func (s *ClientLogStore) Store(ctx context.Context, accountID, profileID, sessionHash string, q ClientUploadRequest) (ClientUpload, error) {
	var out ClientUpload
	if s == nil || s.DB == nil {
		return out, ErrClientUpload
	}
	if !bounded(q.DeviceID, 128) || !bounded(q.Platform, 60) || !bounded(q.AppVersion, 60) || !bounded(q.DeviceClass, 60) {
		return out, ErrClientUpload
	}
	body := strings.TrimSpace(q.Body)
	if body == "" || len(body) > MaxClientUploadBytes || !utf8.ValidString(body) {
		return out, ErrClientUpload
	}
	now := s.Now().UTC()
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return out, err
	}
	id := base64.RawURLEncoding.EncodeToString(key)
	write, err := dbwork.Begin(ctx, s.DB, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if err != nil {
		return out, err
	}
	defer write.Rollback()
	tx := write.Tx()
	var deviceID string
	err = tx.QueryRowContext(ctx, `SELECT d.device_id FROM authorization_family_tokens t JOIN authorization_session_families f ON f.id=t.family_id JOIN identity_device_families d ON d.family_id=f.id WHERE t.token_hash=? AND f.account_id=? AND f.profile_id=? AND f.revoked=0 AND t.retired=0`, sessionHash, accountID, profileID).Scan(&deviceID)
	if err == sql.ErrNoRows {
		return out, ErrClientUploadDevice
	}
	if err != nil {
		return out, err
	}
	dayStart := now.Truncate(24 * time.Hour).UnixMilli()
	var used int64
	err = tx.QueryRowContext(ctx, `INSERT INTO client_log_daily_usage(account_id,day_ms,bytes) VALUES(?,?,?) ON CONFLICT(account_id,day_ms) DO UPDATE SET bytes=client_log_daily_usage.bytes+excluded.bytes WHERE client_log_daily_usage.bytes+excluded.bytes<=? RETURNING bytes`, accountID, dayStart, len(body), ClientLogDailyBytes).Scan(&used)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrClientLogBudget
	}
	if err != nil {
		return out, err
	}
	redacted := redactClientLog(body)
	if len(redacted) > MaxClientUploadBytes {
		redacted = strings.ToValidUTF8(redacted[:MaxClientUploadBytes], "")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO client_log_uploads(id,account_id,profile_id,device_id,platform,app_version,device_class,received_ms,bytes,body) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, accountID, profileID, deviceID, q.Platform, q.AppVersion, q.DeviceClass, now.UnixMilli(), len(redacted), redacted); err != nil {
		return out, err
	}
	// The accepted report owns one slot even when the clock ties an older
	// arrival. Random ids order pages, but must never evict this receipt's row.
	if _, err = tx.ExecContext(ctx, `DELETE FROM client_log_uploads WHERE account_id=? AND device_id=? AND id<>? AND id NOT IN(SELECT id FROM client_log_uploads WHERE account_id=? AND device_id=? AND id<>? ORDER BY received_ms DESC,id DESC LIMIT ?)`, accountID, deviceID, id, accountID, deviceID, id, ClientUploadsPerDevice-1); err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM client_log_uploads WHERE id<>? AND id NOT IN (SELECT id FROM client_log_uploads WHERE id<>? ORDER BY received_ms DESC,id DESC LIMIT 99)`, id, id); err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM client_log_daily_usage WHERE day_ms<?`, dayStart-30*24*60*60*1000); err != nil {
		return out, err
	}
	if err = write.Commit(); err != nil {
		return out, err
	}
	return ClientUpload{ID: id, AccountID: accountID, ProfileID: profileID, DeviceID: deviceID, Platform: q.Platform, AppVersion: q.AppVersion, DeviceClass: q.DeviceClass, ReceivedAt: now.Format(time.RFC3339), Bytes: int64(len(redacted))}, nil
}

// List pages uploads newest first.
func (s *ClientLogStore) List(ctx context.Context, cursor string, limit int) (ClientUploadPage, error) {
	out := ClientUploadPage{Limit: limit, Items: []ClientUpload{}}
	if limit <= 0 {
		out.Limit = 25
		limit = 25
	}
	if limit > 100 {
		return out, ErrClientUpload
	}
	before := int64(0)
	beforeID := ""
	if cursor != "" {
		stamp, id, ok := strings.Cut(cursor, ".")
		if !ok || len(id) != 22 {
			return out, ErrClientUpload
		}
		beforeID = id
		n, err := strconv.ParseInt(stamp, 10, 64)
		if err != nil {
			return out, ErrClientUpload
		}
		before = n
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,account_id,profile_id,device_id,platform,app_version,device_class,received_ms,bytes FROM client_log_uploads WHERE (?=0 OR received_ms<? OR (received_ms=? AND id<?)) ORDER BY received_ms DESC,id DESC LIMIT ?`, before, before, before, beforeID, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	received := []int64{}
	for rows.Next() {
		var u ClientUpload
		var ms int64
		if err = rows.Scan(&u.ID, &u.AccountID, &u.ProfileID, &u.DeviceID, &u.Platform, &u.AppVersion, &u.DeviceClass, &ms, &u.Bytes); err != nil {
			return out, err
		}
		u.ReceivedAt = time.UnixMilli(ms).UTC().Format(time.RFC3339)
		out.Items, received = append(out.Items, u), append(received, ms)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextCursor = strconv.FormatInt(received[limit-1], 10) + "." + out.Items[limit-1].ID
	}
	return out, nil
}

// Read returns one upload with its body.
func (s *ClientLogStore) Read(ctx context.Context, id string) (ClientUpload, error) {
	var u ClientUpload
	var ms int64
	err := s.DB.QueryRowContext(ctx, `SELECT id,account_id,profile_id,device_id,platform,app_version,device_class,received_ms,bytes,body FROM client_log_uploads WHERE id=?`, id).
		Scan(&u.ID, &u.AccountID, &u.ProfileID, &u.DeviceID, &u.Platform, &u.AppVersion, &u.DeviceClass, &ms, &u.Bytes, &u.Body)
	if err != nil {
		return u, err
	}
	u.ReceivedAt = time.UnixMilli(ms).UTC().Format(time.RFC3339)
	return u, nil
}

// PruneClientLogs drops uploads older than the client retention category.
func (s *ClientLogStore) Prune(ctx context.Context, days int) error {
	if days <= 0 {
		return nil
	}
	cutoff := s.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
	return dbwork.WithWriteTx(ctx, s.DB, dbwork.ClassFrom(ctx, dbwork.ClassInteractive), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM client_log_uploads WHERE received_ms<?`, cutoff); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM client_log_daily_usage WHERE day_ms<?`, s.Now().UTC().Truncate(24*time.Hour).Add(-time.Duration(days)*24*time.Hour).UnixMilli())
		return err
	})
}

func (s *ClientLogStore) Delete(ctx context.Context, id string) error {
	result, err := dbwork.ExecWrite(ctx, s.DB, dbwork.ClassInteractive, `DELETE FROM client_log_uploads WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
