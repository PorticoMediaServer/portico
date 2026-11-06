package writer

import (
	"context"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

type nativeEngineGetter interface {
	SQLiteEngine() (version, sourceID string, err error)
}

func (c *ownedPhysicalConn) SQLiteEngine() (string, string, error) {
	getter, ok := c.writerDriverConn.(nativeEngineGetter)
	if !ok {
		return "", "", invalid("physical_engine_getter")
	}
	return getter.SQLiteEngine()
}

func (c *physicalCloseBoundary) SQLiteEngine() (string, string, error) {
	// The physical owner is captured before optional fault decoration, and
	// always owns disposal. Decoration cannot substitute another engine pair.
	return c.native.SQLiteEngine()
}

func (c *safeWriterConn) SQLiteEngine() (version, sourceID string, err error) {
	values, err := safeDriver(c.scope, "engine identity", true, func() ([2]string, error) {
		getter, ok := c.writerDriverConn.(nativeEngineGetter)
		if !ok {
			return [2]string{}, invalid("physical_engine_getter")
		}
		version, sourceID, err := getter.SQLiteEngine()
		if err != nil {
			return [2]string{}, err
		}
		return [2]string{version, sourceID}, nil
	})
	return values[0], values[1], err
}

// searchImageEngine supplies fresh compatibility evidence only to the closed
// publisher turn AFTER historical resolution. It reads the SAME held physical
// connection on every call; no registry, startup or previous-turn cache exists.
// Replacements must supply their own getter. This is not a permission/grant.
func (t *commandTurn) searchImageEngine(ctx context.Context) (out searchpolicy.Engine, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if ctx == nil || t == nil || t.transaction == nil {
		return searchpolicy.Engine{}, ErrInvalid
	}
	tx := t.transaction
	if tx.descriptor.ID != SearchImageStage || tx.conn == nil || !tx.policyVerified {
		return searchpolicy.Engine{}, ErrInvalid
	}
	if tx.done || !tx.begun {
		return searchpolicy.Engine{}, ErrClosed
	}
	defer tx.guardPanic()
	if err := t.checkLifetimes(ctx); err != nil {
		return searchpolicy.Engine{}, err
	}
	actual, err := sqliteconn.AttestEngine(ctx, tx.conn)
	t.panicAfter(err)
	if err != nil {
		return searchpolicy.Engine{}, err
	}
	return searchpolicy.Engine{Version: actual.Version, SourceID: actual.SourceID}, nil
}
