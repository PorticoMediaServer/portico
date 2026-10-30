// Package operations provides independently sampled, owner-only Console facts.
// Measurements describe this process; no host capacity or health is inferred.
package operations

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/diagnostics"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"runtime/metrics"
	"time"
)

var ErrUnavailable = errors.New("operation panel unavailable")
var ErrPanel = requestinput.New("unknown operation panel")

type Memory struct {
	HeapObjectsBytes     uint64 `json:"heapObjectsBytes"`
	RuntimeReservedBytes uint64 `json:"runtimeReservedBytes"`
}
type Database struct {
	Readable bool `json:"readable"`
}
type Panel struct {
	Name       string             `json:"name"`
	ObservedAt string             `json:"observedAt"`
	FreshUntil string             `json:"freshUntil"`
	Memory     *Memory            `json:"memory,omitempty"`
	Database   *Database          `json:"database,omitempty"`
	Build      *diagnostics.Build `json:"build,omitempty"`
}

func Read(ctx context.Context, db *sql.DB, name string) (Panel, error) {
	out := Panel{Name: name}
	if ctx.Err() != nil {
		return out, ErrUnavailable
	}
	switch name {
	case "memory":
		samples := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}, {Name: "/memory/classes/total:bytes"}}
		metrics.Read(samples)
		for _, sample := range samples {
			if sample.Value.Kind() != metrics.KindUint64 || sample.Value.Uint64() > 9007199254740991 {
				return out, ErrUnavailable
			}
		}
		out.Memory = &Memory{samples[0].Value.Uint64(), samples[1].Value.Uint64()}
	case "database":
		if db == nil {
			return out, ErrUnavailable
		}
		var revision int64
		if err := db.QueryRowContext(ctx, `SELECT revision FROM admin_revision WHERE id=1`).Scan(&revision); err != nil || revision < 1 {
			return out, ErrUnavailable
		}
		out.Database = &Database{Readable: true}
	case "build":
		build := diagnostics.BuildFacts()
		out.Build = &build
	default:
		return out, ErrPanel
	}
	if ctx.Err() != nil {
		return out, ErrUnavailable
	}
	now := time.Now().UTC()
	out.ObservedAt = now.Format(time.RFC3339Nano)
	out.FreshUntil = now.Add(30 * time.Second).Format(time.RFC3339Nano)
	return out, nil
}
