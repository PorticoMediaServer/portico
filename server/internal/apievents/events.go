package apievents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// RingSize is shared by playback, notifications, operations, and the feed.
const RingSize int64 = 20_000

func DeviceAudience(device string) string   { return "device:" + device }
func LibraryAudience(library string) string { return "library:" + library }
func GroupAudience(group string) string     { return "group:" + group }
func ProfileAudience(authority, account, profile string) string {
	return "profile:" + authority + ":" + account + ":" + profile
}
func AccountAudience(authority, account string) string { return "account:" + authority + ":" + account }

// CapacityAudience is distinct from account administration and profile events.
func CapacityAudience(authority, account string) string {
	return "capacity:" + authority + ":" + account
}

const AdminAudience = "admin"

// InboxAudience translates the durable notification scope into the shared
// event audience. Legacy/test scopes remain isolated from real viewers.
func InboxAudience(scope, kind string) string {
	var parts []string
	if json.Unmarshal([]byte(scope), &parts) == nil {
		if kind == "profile" && len(parts) == 3 {
			return ProfileAudience(parts[0], parts[1], parts[2])
		}
		if kind == "account-admin" && len(parts) == 2 {
			return AccountAudience(parts[0], parts[1])
		}
	}
	return "legacy:" + scope
}

// Append writes one event in the domain's transaction. Callers may wake readers
// only after commit. data is an optional JSON value, never a token or secret.
func Append(tx *sql.Tx, audience, kind, resourceKind, resourceID, revision string, data any) error {
	if tx == nil || audience == "" || kind == "" {
		return errors.New("event audience and type are required")
	}
	payload := ""
	if data != nil {
		raw, err := json.Marshal(data)
		if err != nil {
			return err
		}
		payload = string(raw)
	}
	result, err := tx.Exec(`INSERT INTO api_events(audience,type,resource_kind,resource_id,revision,data,at_ms) VALUES(?,?,?,?,?,?,?)`, audience, kind, resourceKind, resourceID, revision, payload, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	if id%64 == 0 {
		return Trim(tx, id)
	}
	return nil
}

// Trim bounds the ring by primary key in the same transaction as the append.
func Trim(tx *sql.Tx, newestID int64) error {
	_, err := tx.ExecContext(context.Background(), `DELETE FROM api_events WHERE id<=?`, newestID-RingSize)
	return err
}
