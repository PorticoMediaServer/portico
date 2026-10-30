// Package streamlimits resolves account stream settings against the owner's
// hard ceiling and counts the same durable slots for every admission surface.
// Callers own authorization and the SQLite transaction.
package streamlimits

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

type Mode string

const (
	Inherit   Mode = "inherit"
	Unlimited Mode = "unlimited"
	Limit     Mode = "limit"
)

func (Mode) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": []string{"inherit", "unlimited", "limit"}}
}
func Valid(mode Mode, value int) bool {
	return mode == Limit && value >= 1 && value <= 1000 || (mode == Inherit || mode == Unlimited) && value == 0
}

type Facts struct {
	AwayQuality    AwayQuality
	Mode           Mode `json:"mode"`
	Limit          *int `json:"limit"`
	ServerCeiling  *int `json:"serverCeiling"`
	EffectiveLimit *int `json:"effectiveLimit"`
}

// Effective never lets an unlimited person setting bypass the server ceiling.
func Effective(person, ceiling *int) *int {
	if person == nil {
		if ceiling == nil {
			return nil
		}
		v := *ceiling
		return &v
	}
	v := *person
	if ceiling != nil && *ceiling < v {
		v = *ceiling
	}
	return &v
}

// AwayQuality is a person's own away-from-home quality ceiling: "server" follows
// the server's, otherwise it names one published quality-ladder id. It can only
// narrow what the server already allows.
type AwayQuality string

const AwayQualityServer AwayQuality = "server"

// AwayQualities is the closed set the supplier accepts.
var AwayQualities = []AwayQuality{AwayQualityServer, "2160p", "1080p-high", "1080p-low", "720p-high", "720p-low", "480p", "240p"}

// JSONSchema publishes the exact closed domain the column and the supplier share,
// so a generated client sees a sentinel-and-ladder enum rather than any string.
func (AwayQuality) JSONSchema() map[string]any {
	offered := make([]string, 0, len(AwayQualities))
	for _, value := range AwayQualities {
		offered = append(offered, string(value))
	}
	return map[string]any{"type": "string", "enum": offered}
}

// UnmarshalJSON refuses a supplied value outside the ladder, including an empty
// string and null. Only an absent field keeps the zero value that means "leave
// whatever is stored", so a client cannot clear a saved cap by sending "".
func (m *AwayQuality) UnmarshalJSON(raw []byte) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return errors.New("invalid limits.awayQuality")
	}
	if !ValidAwayQuality(AwayQuality(value)) {
		return errors.New("invalid limits.awayQuality")
	}
	*m = AwayQuality(value)
	return nil
}

func ValidAwayQuality(value AwayQuality) bool {
	for _, offered := range AwayQualities {
		if value == offered {
			return true
		}
	}
	return false
}

func ReadTx(ctx context.Context, tx *sql.Tx, account string) (out Facts, err error) {
	var value, ceiling sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(s.mode,'inherit'),s.stream_limit,COALESCE(s.away_quality,''),p.per_account_cap FROM accounts a LEFT JOIN account_stream_limits s ON s.account_id=a.id JOIN playback_owner_policy p ON p.singleton=1 WHERE a.id=?`, account).Scan(&out.Mode, &value, &out.AwayQuality, &ceiling)
	if err != nil {
		return out, err
	}
	if out.AwayQuality == "" {
		out.AwayQuality = AwayQualityServer
	}
	if value.Valid {
		v := int(value.Int64)
		out.Limit = &v
	}
	if ceiling.Valid {
		v := int(ceiling.Int64)
		out.ServerCeiling = &v
	}
	out.EffectiveLimit = Effective(out.Limit, out.ServerCeiling)
	return out, nil
}
func WriteTx(ctx context.Context, tx *sql.Tx, account string, mode Mode, value int) error {
	if !Valid(mode, value) {
		return errors.New("invalid account stream setting")
	}
	var v any
	if mode == Limit {
		v = value
	}
	_, e := tx.ExecContext(ctx, `INSERT INTO account_stream_limits(account_id,mode,stream_limit) VALUES(?,?,?) ON CONFLICT(account_id) DO UPDATE SET mode=excluded.mode,stream_limit=excluded.stream_limit`, account, mode, v)
	return e
}

// WriteAwayQualityTx stores one person's away ceiling in the same normalized row
// the stream mode lives in, so the cap and the count that honours it are read
// together. It is written only when the owner supplied a value: an omitted field
// keeps whatever the person already had.
func WriteAwayQualityTx(ctx context.Context, tx *sql.Tx, account string, value AwayQuality) error {
	if !ValidAwayQuality(value) {
		return errors.New("invalid account away quality")
	}
	_, e := tx.ExecContext(ctx, `INSERT INTO account_stream_limits(account_id,mode,away_quality) VALUES(?,'inherit',?) ON CONFLICT(account_id) DO UPDATE SET away_quality=excluded.away_quality`, account, string(value))
	return e
}

// CountTx counts across profiles/devices. V1 leases count once; their backing
// media row and private presentations sharing another live slot do not add one.
func CountTx(ctx context.Context, tx *sql.Tx, authority, account string, scoped bool, now time.Time, replacingV1, replacingMedia string) (int64, error) {
	stamp := now.UTC().Format(time.RFC3339)
	// Keep scope predicates explicit so own-account reads can use the existing
	// active-account/owner indexes. Only these fixed SQL fragments are composed;
	// authority/account values remain bound and profiles remain included.
	vScope, mediaScope := "", ""
	args := []any{now.UnixMilli(), replacingV1, replacingMedia, stamp}
	if scoped {
		vScope = " AND v.authority=?5 AND v.account_id=?6"
		mediaScope = " AND auth.authority=?5 AND ps.account_id=?6"
		args = append(args, authority, account)
	}
	var count int64
	e := tx.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM playback_v1_sessions v WHERE v.ended_ms=0 AND v.lease_expires_ms>?1 AND v.id<>?2 AND (?3='' OR v.media_session_id<>?3)`+vScope+`)
 + (SELECT count(*) FROM playback_sessions ps JOIN authorization_access auth ON auth.hash=`+identity.CurrentFamilyTokenSQL("ps.session_hash")+` WHERE ps.state NOT IN('stopped','ended','failed') AND ps.expires_at>?4 AND auth.expires_at>?4 AND auth.revoked=0 AND ps.id<>?3`+mediaScope+`
 AND NOT EXISTS(SELECT 1 FROM playback_v1_sessions v WHERE v.media_session_id=ps.id AND v.ended_ms=0 AND v.lease_expires_ms>?1)
 AND NOT EXISTS(SELECT 1 FROM playback_private_presentations pp JOIN playback_sessions owner ON owner.id=pp.owner_session_id WHERE pp.session_id=ps.id AND owner.state NOT IN('stopped','ended','failed') AND owner.expires_at>?4))`, args...).Scan(&count)
	return count, e
}
