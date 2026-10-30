package playback

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/ownedcapture"
	"github.com/PorticoMediaServer/portico/server/internal/playbackdisc"
)

// This is ordinary active-presentation authority, separate from preparation's
// token/deadline and worker/continuation checks. The consumer supplies the exact
// accepted legacy key AND snapshot ID. No asset/digest/latest lookup is offered.
type discPlayReaderOptions struct {
	DB          *sql.DB
	Store       *playbackdisc.Store
	Captures    *ownedcapture.Service
	Key         playbackdisc.Key
	Snapshot    string
	Tool        disc.Tool
	Tools       func(context.Context) (playbackdisc.ToolPins, error)
	Current     playbackdisc.Check
	Conditional DiscConditionalSource
}

// openPrivateDiscPresentation opens a NEW independently checked reader for an
// ACTIVE immutable snapshot. It never borrows a preparation's descriptor or
// 120s context. The normalized presentation pin commits before descriptor/hash
// work or helper launch. Retain the returned whole input until its child exits.
func openPrivateDiscPresentation(ctx context.Context, o discPlayReaderOptions) (_ *assets.PlaybackInput, err error) {
	if ctx == nil || ctx.Done() == nil || dbwork.Snapshot(ctx) != nil || o.DB == nil || o.Store == nil || o.Captures == nil || o.Current == nil || o.Tools == nil || o.Key.SessionID == "" || o.Key.Generation < 1 || o.Snapshot == "" {
		return nil, disc.ErrProtocol
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	tools, err := o.Tools(ctx)
	if err != nil {
		return nil, err
	}
	var original playbackdisc.Snapshot
	var pin playbackdisc.Pin
	err = dbwork.WithWriteTxContext(ctx, o.DB, dbwork.ClassEstablishedPlayback, func(check context.Context, tx *sql.Tx) error {
		var e error
		original, e = o.Store.ResolveTx(check, tx, o.Key, o.Snapshot, o.Current)
		if e != nil {
			return e
		}
		if original.Tools != tools || (original.Input.Captured == nil) == (original.Input.Conditional == nil) || original.Input.Conditional != nil && o.Conditional == nil {
			return disc.ErrRemoteUnqualified
		}
		var until int64
		if e = tx.QueryRowContext(check, `SELECT expires_ms FROM playback_disc_lifecycle WHERE snapshot_id=? AND state='active' AND sealed=1`, original.ID).Scan(&until); e != nil {
			return e
		}
		if deadline, finite := ctx.Deadline(); finite && deadline.UnixMilli() < until {
			until = deadline.UnixMilli()
		}
		pin, e = o.Store.PinTx(check, tx, o.Key, o.Snapshot, identity.Token(), time.UnixMilli(until), o.Current)
		return e
	})
	if err != nil {
		return nil, err
	}
	life, cancel := context.WithDeadline(ctx, pin.ExpiresAt)
	var releaseOnce sync.Once
	var releaseErr error
	releasePin := func() error {
		releaseOnce.Do(func() {
			cancel()
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer stop()
			releaseErr = dbwork.WithWriteTxContext(cleanup, o.DB, dbwork.ClassMaintenance, func(check context.Context, tx *sql.Tx) error {
				return o.Store.ReleasePinTx(check, tx, pin)
			})
		})
		return releaseErr
	}
	good := false
	defer func() {
		if !good {
			err = errors.Join(err, releasePin())
		}
	}()
	var retired atomic.Bool
	pinCurrentTx := func(check context.Context, tx *sql.Tx) (checkErr error) {
		defer func() {
			if checkErr != nil {
				retired.Store(true)
				cancel()
			}
		}()
		if tx == nil || retired.Load() || life.Err() != nil {
			return playbackdisc.ErrCurrent
		}
		actual, e := o.Store.ResolvePinTx(check, tx, pin, o.Current)
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(actual, original) {
			return playbackdisc.ErrCurrent
		}
		return life.Err()
	}
	var conditionalProof interface {
		CheckTx(context.Context, *sql.Tx) error
	}
	currentTx := func(check context.Context, tx *sql.Tx) error {
		if err := pinCurrentTx(check, tx); err != nil {
			return err
		}
		if conditionalProof != nil {
			if err := conditionalProof.CheckTx(check, tx); err != nil {
				retired.Store(true)
				cancel()
				return err
			}
		}
		return nil
	}
	current := func(check context.Context) error {
		if tx := dbwork.Snapshot(check); tx != nil {
			return currentTx(check, tx)
		}
		view, e := dbwork.BeginSnapshot(check, o.DB)
		if e != nil {
			retired.Store(true)
			cancel()
			return e
		}
		defer view.Rollback()
		return currentTx(view.Context(), view.Tx())
	}
	if err = current(life); err != nil {
		return nil, err
	}
	title, err := measuredDiscTitle(original.Disc.Structure, original.Disc.SelectedTitleKey)
	if err != nil {
		return nil, err
	}
	var input *assets.PlaybackInput
	if original.Input.Captured != nil {
		reader, e := o.Captures.Open(life, original.Input.Captured.Receipt)
		if e != nil {
			return nil, e
		}
		// The captured constructor consumes the complete reader on every valid
		// outside-SQL path. Never detach its raw file from source/receipt proof.
		input, err = capturedDiscInput(life, o.Tool, reader, original.Disc.Structure, title, current)
	} else {
		object, e := o.Conditional.Open(life, original.Source, func(check context.Context, tx *sql.Tx) error {
			return pinCurrentTx(check, tx)
		})
		if e != nil {
			return nil, e
		}
		if object == nil {
			return nil, disc.ErrRemoteUnqualified
		}
		var ok bool
		conditionalProof, ok = object.(interface {
			CheckTx(context.Context, *sql.Tx) error
		})
		if !ok {
			object.Close()
			return nil, disc.ErrRemoteUnqualified
		}
		version := object.Version()
		if version.ID() != original.Input.Conditional.Version.ID() || version.Evidence().Kind != mediasource.StrongETag && version.Evidence().Kind != mediasource.ImmutableProviderVersion || object.Snapshot().Size != original.Source.Size || object.Snapshot().ModifiedNS != original.Source.ModifiedNS {
			object.Close()
			return nil, mediasource.ErrSourceChanged
		}
		retained, e := conditionalDiscInput(life, o.Tool, object, original.Disc.Structure, title, current)
		if e != nil {
			return nil, e
		}
		input = retained.Input
	}
	if err != nil {
		return nil, err
	}
	inputOwned := true
	defer func() {
		if inputOwned {
			input.Close()
		}
	}()
	after, err := o.Tools(life)
	if err != nil {
		return nil, err
	}
	if after != tools {
		return nil, ErrStaleOffer
	}
	if err = input.Validate(life); err != nil {
		return nil, err
	}
	// Revalidate the original tool closure around each new helper/seek selection,
	// outside SQL. Authority polling itself remains DB/source checking rather
	// than repeatedly hashing the server binaries. A changed closure retires this
	// reader; restoring old code cannot revive it.
	selectInput := input.InputAt
	input.InputAt = func(check context.Context, ms int64) ([]string, string, int64, error) {
		if dbwork.Snapshot(check) != nil {
			return nil, "", 0, disc.ErrProtocol
		}
		witness := func() error {
			actual, e := o.Tools(check)
			if e == nil && actual != tools {
				e = ErrStaleOffer
			}
			if e != nil {
				retired.Store(true)
				cancel()
			}
			return e
		}
		if e := witness(); e != nil {
			return nil, "", 0, e
		}
		options, address, offset, e := selectInput(check, ms)
		if e != nil {
			return nil, "", 0, e
		}
		if e = witness(); e != nil {
			return nil, "", 0, e
		}
		return options, address, offset, nil
	}
	// The original wrapper owns File, source lease and capture pin together.
	// Release ONLY this additional presentation reference after closing it.
	closeSource := input.ReleaseSource
	input.ReleaseSource = func() error {
		err := closeSource()
		return errors.Join(err, releasePin())
	}
	inputOwned, good = false, true
	return input, nil
}
