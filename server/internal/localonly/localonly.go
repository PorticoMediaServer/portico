// Package localonly answers whether a library uses local metadata only (its
// metadata agent is "local"), for every package that can reach the network on
// a library's behalf. It only reads.
package localonly

import (
	"context"
	"database/sql"
	"errors"
)

// Err answers an online lookup requested for a local-only library.
var Err = errors.New("Online metadata lookup is disabled for this library or server.")

type querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Library reports whether the library must never go online.
func Library(ctx context.Context, q querier, library string) (bool, error) {
	online, _, err := Online(ctx, q)
	if err != nil {
		return false, err
	}
	if !online {
		return true, nil
	}
	var local bool
	err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM libraries l LEFT JOIN library_metadata_agents a ON a.library_id=l.id WHERE l.id=? AND (a.agent='local' OR l.kind IN('book','comic','audiobook')))`, library).Scan(&local)
	return local, err
}

// Item reports whether the library holding the item must never go online.
func Item(ctx context.Context, q querier, item string) (bool, error) {
	online, _, err := Online(ctx, q)
	if err != nil {
		return false, err
	}
	if !online {
		return true, nil
	}
	var local bool
	err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id JOIN libraries l ON l.id=cl.library_id LEFT JOIN library_metadata_agents a ON a.library_id=cl.library_id WHERE e.public_id=pid_blob(?) AND (a.agent='local' OR l.kind IN('book','comic','audiobook')))`, item).Scan(&local)
	return local, err
}

// Online is the server-wide lookup admission, separate from stored library choice
// and independent provider consent. Epoch changes retire captured remote work.
func Online(ctx context.Context, q querier) (bool, int64, error) {
	var enabled bool
	var epoch int64
	err := q.QueryRowContext(ctx, `SELECT online_lookups,lookup_epoch FROM metadata_runtime_settings WHERE singleton=1`).Scan(&enabled, &epoch)
	if err == nil && epoch < 1 {
		err = errors.New("invalid metadata lookup epoch")
	}
	return enabled, epoch, err
}

// CaptureOnline and CheckOnline bind remote acquisition to one server policy epoch.
func CaptureOnline(ctx context.Context, q querier) (int64, error) {
	enabled, epoch, err := Online(ctx, q)
	if err != nil {
		return 0, err
	}
	if !enabled {
		return 0, Err
	}
	return epoch, nil
}
func CheckOnline(ctx context.Context, q querier, expected int64) error {
	epoch, err := CaptureOnline(ctx, q)
	if err != nil {
		return err
	}
	if expected < 1 || epoch != expected {
		return Err
	}
	return nil
}
