package playback

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var ErrActivityQuery = errors.New("invalid activity query")
var ErrActivityConflict = errors.New("playback command conflicts with its original intent or session generation")
var ErrActivityExpired = errors.New("operation is outside its retry window; refresh before creating a new intent")
var ErrActivityCapacity = errors.New("playback command capacity reached; retry after receipt cleanup")

type ActivityScope struct {
	ServerID    string `json:"serverId"`
	ViewerFence string `json:"viewerFence"`
}
type ActivityRow struct {
	SessionID            string   `json:"sessionId"`
	Generation           int      `json:"generation"`
	ItemID               string   `json:"itemId"`
	Title                string   `json:"title"`
	LibraryID            string   `json:"libraryId"`
	LibraryName          string   `json:"libraryName"`
	LibraryKind          string   `json:"libraryKind"`
	State                string   `json:"state"`
	Delivery             string   `json:"delivery"`
	CreatedAt            *string  `json:"createdAt"`
	ReportedAt           *string  `json:"reportedAt"`
	PositionMs           *int64   `json:"positionMs"`
	ExpiresAt            string   `json:"expiresAt"`
	AuthorizationStopped bool     `json:"authorizationStopped"`
	CleanupStatus        string   `json:"cleanupStatus"`
	Actions              []string `json:"actions"`
}
type ActivityPage struct {
	Scope      ActivityScope `json:"scope"`
	ObservedAt string        `json:"observedAt"`
	Status     string        `json:"status"`
	Items      []ActivityRow `json:"items"`
	NextCursor string        `json:"nextCursor"`
}
type ActivityStop struct {
	OperationID        string `json:"operationId"`
	ExpectedGeneration int    `json:"expectedGeneration"`
}
type ActivityReceipt struct {
	Scope                ActivityScope `json:"scope"`
	OperationID          string        `json:"operationId"`
	SessionID            string        `json:"sessionId"`
	Generation           int           `json:"generation"`
	State                string        `json:"state"`
	AuthorizationStopped bool          `json:"authorizationStopped"`
	CleanupStatus        string        `json:"cleanupStatus"`
}
type activityCursor struct {
	Scope                ActivityScope
	Status               string
	Limit                int
	High, After, Expires int64
}

func activityOwner(tx *sql.Tx, p identity.Principal) error {
	if p.Authority != "local" || p.Role != "owner" {
		return identity.ErrUnauthorized
	}
	var n int
	e := tx.QueryRow(`SELECT count(*) FROM authorization_access s JOIN accounts a ON a.id=s.account_id AND a.profile_id=s.profile_id WHERE s.hash=? AND s.authority='local' AND s.role='owner' AND s.account_id=? AND s.profile_id=? AND s.epoch=? AND a.epoch=s.epoch AND s.revoked=0 AND s.expires_at>?`, p.Hash, p.AccountID, p.ProfileID, p.Epoch, time.Now().UTC().Format(time.RFC3339)).Scan(&n)
	if e != nil {
		return e
	}
	if n != 1 {
		return identity.ErrUnauthorized
	}
	return nil
}
func activityActor(p identity.Principal) string {
	b, _ := json.Marshal([]string{p.Authority, p.AccountID, p.ProfileID})
	return string(b)
}
func (s *Service) Activity(ctx context.Context, p identity.Principal, scope ActivityScope, status string, limit int, cursor string) (ActivityPage, error) {
	out := ActivityPage{Scope: scope, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Status: status, Items: []ActivityRow{}}
	if status != "open" && status != "recent" || limit < 1 || limit > 40 || len(cursor) > 4096 {
		return out, ErrActivityQuery
	}
	gated, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return out, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if e = activityOwner(tx, p); e != nil {
		return out, e
	}
	// The activity feed reads playback facts and catalogue facts only; both
	// are synchronous now, so there is no derived-domain wait.
	var key string
	if e = tx.QueryRowContext(ctx, `SELECT value FROM configuration WHERE key='activity_cursor_key'`).Scan(&key); e != nil {
		return out, e
	}
	c := activityCursor{Scope: scope, Status: status, Limit: limit, Expires: time.Now().Add(30 * time.Minute).Unix()}
	if cursor == "" {
		if e = tx.QueryRowContext(ctx, `SELECT COALESCE(max(ordinal),0) FROM playback_observations`).Scan(&c.High); e != nil {
			return out, e
		}
		c.After = c.High + 1
	} else {
		parts := strings.Split(cursor, ".")
		if len(parts) != 2 {
			return out, ErrActivityQuery
		}
		raw, er := base64.RawURLEncoding.DecodeString(parts[0])
		mac := hmac.New(sha256.New, []byte(key))
		mac.Write(raw)
		sig, _ := hex.DecodeString(parts[1])
		if er != nil || !hmac.Equal(sig, mac.Sum(nil)) || json.Unmarshal(raw, &c) != nil || c.Scope != scope || c.Status != status || c.Limit != limit || c.Expires < time.Now().Unix() {
			return out, ErrActivityQuery
		}
	}
	filter := ""
	if status == "open" {
		filter = " AND ps.state IN ('starting','playing','paused') AND ps.expires_at>?"
	}
	args := []any{c.High, c.After}
	if status == "open" {
		args = append(args, time.Now().UTC().Format(time.RFC3339))
	}
	args = append(args, limit+1)
	rows, e := tx.QueryContext(ctx, `SELECT o.ordinal,ps.id,ps.generation,pid(i.public_id),i.title,l.library_id,l.name,CASE l.kind WHEN 1 THEN 'movie' WHEN 2 THEN 'tv' WHEN 3 THEN 'anime' WHEN 4 THEN 'music' WHEN 5 THEN 'audiobook' END,ps.state,ps.mode,o.created_at,o.reported_at,o.position,ps.expires_at,EXISTS(SELECT 1 FROM playback_artifacts a WHERE a.session_id=ps.id) FROM playback_observations o JOIN playback_sessions ps ON ps.id=o.session_id JOIN catalog_entities i ON i.id=ps.item_id JOIN catalog_libraries l ON l.id=i.library_id WHERE o.ordinal<=? AND o.ordinal<?`+filter+` ORDER BY o.ordinal DESC LIMIT ?`, args...)
	if e != nil {
		return out, e
	}
	var last int64
	for rows.Next() {
		var row ActivityRow
		var ordinal int64
		var artifact bool
		var position *float64
		if e = rows.Scan(&ordinal, &row.SessionID, &row.Generation, &row.ItemID, &row.Title, &row.LibraryID, &row.LibraryName, &row.LibraryKind, &row.State, &row.Delivery, &row.CreatedAt, &row.ReportedAt, &position, &row.ExpiresAt, &artifact); e != nil {
			rows.Close()
			return out, e
		}
		if position != nil && *position >= 0 && !math.IsNaN(*position) && !math.IsInf(*position, 0) {
			ms := int64(math.Round(*position * 1000))
			row.PositionMs = &ms
		}
		if len(out.Items) == limit {
			break
		}
		last = ordinal
		row.Actions = []string{}
		row.AuthorizationStopped = row.State == "stopped" || row.State == "ended" || row.State == "failed" || row.ExpiresAt <= time.Now().UTC().Format(time.RFC3339)
		if !row.AuthorizationStopped {
			row.Actions = []string{"stop"}
		}
		row.CleanupStatus = "not_applicable"
		if row.Delivery == "hls" {
			row.CleanupStatus = "not_requested"
			if row.AuthorizationStopped {
				row.CleanupStatus = "complete"
				if artifact {
					row.CleanupStatus = "pending"
				}
			}
		}
		out.Items = append(out.Items, row)
	}
	more := len(out.Items) == limit && last > 0 // Probe exact continuation without materializing additional rows.
	if e = rows.Err(); e != nil {
		rows.Close()
		return out, e
	}
	rows.Close()
	if more {
		var n int
		a := []any{c.High, last}
		if status == "open" {
			a = append(a, time.Now().UTC().Format(time.RFC3339))
		}
		e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_observations o JOIN playback_sessions ps ON ps.id=o.session_id WHERE o.ordinal<=? AND o.ordinal<?`+filter+` LIMIT 1)`, a...).Scan(&n)
		if e != nil {
			return out, e
		}
		if n == 1 {
			c.After = last
			raw, _ := json.Marshal(c)
			mac := hmac.New(sha256.New, []byte(key))
			mac.Write(raw)
			out.NextCursor = base64.RawURLEncoding.EncodeToString(raw) + "." + hex.EncodeToString(mac.Sum(nil))
		}
	}
	if e = gated.Commit(); e != nil {
		return out, e
	}
	if s.hls != nil {
		s.hls.mu.Lock()
		defer s.hls.mu.Unlock()
		for i := range out.Items {
			if out.Items[i].AuthorizationStopped && out.Items[i].Delivery == "hls" {
				if _, active := s.hls.active[out.Items[i].SessionID]; active {
					out.Items[i].CleanupStatus = "pending"
				}
			}
		}
	}
	return out, nil
}

var activityOperationPattern = regexp.MustCompile(`^([0-9]{13})-[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func activityOperationTime(id string) (time.Time, error) {
	m := activityOperationPattern.FindStringSubmatch(id)
	if m == nil {
		return time.Time{}, ErrActivityQuery
	}
	n, e := strconv.ParseInt(m[1], 10, 64)
	return time.UnixMilli(n), e
}
func (s *Service) ActivityStop(ctx context.Context, p identity.Principal, scope ActivityScope, id string, cmd ActivityStop, recoverOnly bool) (ActivityReceipt, error) {
	out := ActivityReceipt{}
	at, e := activityOperationTime(cmd.OperationID)
	if e != nil {
		return out, e
	}
	if !recoverOnly && (id == "" || cmd.ExpectedGeneration < 1) {
		return out, ErrActivityQuery
	}
	gated2, e := dbwork.Begin(ctx, s.db, dbwork.ClassEstablishedPlayback)
	if e != nil {
		return out, e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	if e = activityOwner(tx, p); e != nil {
		return out, e
	}
	actor := activityActor(p)
	raw, _ := json.Marshal(struct {
		ID         string
		Generation int
	}{id, cmd.ExpectedGeneration})
	sum := sha256.Sum256(raw)
	fingerprint := hex.EncodeToString(sum[:])
	var storedActor, storedHash, response string
	var created int64
	e = tx.QueryRowContext(ctx, `SELECT actor,fingerprint,response,created_at FROM playback_admin_operations WHERE operation_id=?`, cmd.OperationID).Scan(&storedActor, &storedHash, &response, &created)
	if e == nil {
		if actor != storedActor || !recoverOnly && fingerprint != storedHash {
			return out, ErrActivityConflict
		}
		if time.Since(time.Unix(created, 0)) > 30*24*time.Hour {
			return out, ErrActivityExpired
		}
		if e = json.Unmarshal([]byte(response), &out); e != nil {
			return out, e
		}
		out.Scope = scope
		return out, gated2.Commit()
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	if time.Since(at) > 10*time.Minute || time.Until(at) > time.Minute {
		return out, ErrActivityExpired
	}
	if recoverOnly {
		return out, sql.ErrNoRows
	}
	// Each stop retires a bounded slice of expired receipts on the retention
	// index, so the playback writer never pays for a backlog in one go; only
	// unexpired receipts count against the capacity.
	expired := time.Now().Add(-30 * 24 * time.Hour).Unix()
	if _, e = tx.ExecContext(ctx, `DELETE FROM playback_admin_operations WHERE operation_id IN(SELECT operation_id FROM playback_admin_operations WHERE created_at<? ORDER BY created_at LIMIT 32)`, expired); e != nil {
		return out, e
	}
	var count int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM playback_admin_operations WHERE created_at>=?`, expired).Scan(&count); e != nil {
		return out, e
	}
	if count >= 65536 {
		return out, ErrActivityCapacity
	}
	var generation int
	var state, mode string
	if e = tx.QueryRowContext(ctx, `SELECT generation,state,mode FROM playback_sessions WHERE id=?`, id).Scan(&generation, &state, &mode); e != nil {
		return out, e
	}
	if generation != cmd.ExpectedGeneration {
		return out, ErrActivityConflict
	}
	if state != "stopped" && state != "ended" && state != "failed" {
		state = "stopped"
		if _, e = tx.ExecContext(ctx, `UPDATE playback_sessions SET state='stopped' WHERE id=? AND generation=?`, id, generation); e != nil {
			return out, e
		}
	}
	cleanup := "not_applicable"
	if mode == "hls" {
		cleanup = "pending"
	}
	out = ActivityReceipt{scope, cmd.OperationID, id, generation, state, true, cleanup}
	encoded, _ := json.Marshal(out)
	if _, e = tx.ExecContext(ctx, `INSERT INTO playback_admin_operations VALUES(?,?,?,?,?)`, cmd.OperationID, actor, fingerprint, string(encoded), time.Now().Unix()); e != nil {
		return out, e
	}
	return out, gated2.Commit()
}

// ActivityVideoHeight is the converted display height of a committed plan.
// Unknown source geometry cannot turn a ceiling into a measured picture size.
func ActivityVideoHeight(plan DeliveryPlan) int {
	if plan.VideoAction != "convert" || plan.Trace == nil || plan.Trace.Video == nil || plan.Trace.Video.Width <= 0 || plan.Trace.Video.Height <= 0 {
		return 0
	}
	_, height := convertedDimensions(plan)
	return height
}
