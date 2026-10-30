package main

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"

	adminops "github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/backup"
	"github.com/PorticoMediaServer/portico/server/internal/configfile"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels/dvr"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// storedBackgroundPriority reads the owner's background work priority. The
// row is created with the database and never recreated, so start-up fails
// without it.
func storedBackgroundPriority(ctx context.Context, db *sql.DB) (string, error) {
	var priority string
	e := db.QueryRowContext(ctx, `SELECT background_priority FROM maintenance_policy WHERE singleton=1`).Scan(&priority)
	return priority, e
}

// restoredRequirement is one thing start-up needs of a state folder and its
// database that nothing at start-up would create or repair. A state that
// fails one is a state the server refuses to start on, every time.
type restoredRequirement struct {
	// reason is what the owner is told when a restore is refused for it.
	reason string
	check  func(ctx context.Context, db *sql.DB, state string) error
}

// restoredStateRequirements are those needs, in the order start-up meets
// them. Each check is the code start-up itself runs for that step, or the
// read-only half of it, so the two cannot disagree about what is acceptable:
//
//   - the config file, read with the same bounds and parser as start-up;
//   - the server authority file (openCurrentAuthority);
//   - the schema: every statement start-up and the services it constructs run
//     needs its tables, their columns, indexes, triggers and views, so all of
//     them must be there (mounts, remote sources, subtitles, analysis,
//     recordings, the playback runtime, the console and the rest);
//   - the rows a fresh install starts with, which start-up reads and never
//     recreates (the maintenance policy and the recording storage policy are
//     read fatally; the others are read by the services it starts);
//   - Portico's own stored paths, rebased to this folder: the same idempotent
//     step start-up runs next, and the one step run for real here, because it
//     only rewrites the restored database and cannot be judged by reading;
//   - the networking claim schema, exactly (persistence.VerifyNetworkingClaims);
//   - the signing key of the identity the database records
//     (networking.OpenCurrentKeys);
//   - the background work priority, as a value start-up accepts;
//   - the recording storage policy (dvr.ConfigureCapture);
//   - the two restored folders start-up opens as stores: uploaded subtitles
//     (mediaartifact.New) and mount configurations (mounts.New).
//
// Not judged here, because it is not about the restored state or cannot be
// known by reading it:
//
//   - what start-up needs of the machine (the media tools, environment
//     settings, ports, room to create its working folders): that fails the
//     same way on the previous state;
//   - the bodies of triggers and views outside the networking claim schema,
//     which are checked for presence, not compared.
//
// A restored TLS key that cannot be read is replaced, and identity.New and
// the stores create what they find missing; none of those stops a start.
var restoredStateRequirements = []restoredRequirement{
	{"the restored portico.toml cannot be read as a valid configuration file", func(ctx context.Context, db *sql.DB, state string) error {
		return configurationStoreForState(db, state).ValidateConfiguration(ctx, storagepaths.Path(state, "portico.toml"))
	}},
	{"the restored state has no usable server authority file; restore from a whole, unmodified backup folder, or move the whole state folder", func(_ context.Context, _ *sql.DB, state string) error {
		_, e := readCurrentAuthority(state)
		return e
	}},
	{"the restored database is missing part of the schema this version requires", func(ctx context.Context, db *sql.DB, _ string) error {
		return persistence.VerifySchemaObjects(ctx, db)
	}},
	{"the restored database is missing settings every Portico database starts with", func(ctx context.Context, db *sql.DB, _ string) error {
		return persistence.VerifySeededRows(ctx, db)
	}},
	{"the restored database's stored paths could not be moved to this state folder", persistence.RebaseStatePaths},
	{"the restored database's server identity tables are not in the shape this version requires", func(ctx context.Context, db *sql.DB, _ string) error {
		return persistence.VerifyNetworkingClaims(ctx, db)
	}},
	{"the restored database belongs to a server whose identity keys are not in this state folder; restore from a whole backup folder, or move the whole state folder", networking.VerifyStateKeys},
	{"the restored database's background work setting is not one this version accepts", func(ctx context.Context, db *sql.DB, _ string) error {
		priority, e := storedBackgroundPriority(ctx, db)
		if e != nil {
			return e
		}
		return storage.CheckBackgroundTaskPriority(priority)
	}},
	{"the restored database has no recording storage policy", func(ctx context.Context, db *sql.DB, _ string) error {
		_, e := dvr.ReadStoragePolicy(ctx, db)
		return e
	}},
	{"the restored subtitles folder is not a folder Portico can use", func(_ context.Context, _ *sql.DB, state string) error {
		return mediaartifact.Inspect(storagepaths.Path(state, "subtitles"))
	}},
	{"the restored mount configuration folder is not a folder Portico can use", func(_ context.Context, _ *sql.DB, state string) error {
		return mounts.InspectPrivateDirectory(state)
	}},
}

// Keep registration shared with normal startup so restore cannot omit a domain.
func configurationStoreForState(db *sql.DB, state string) *operations.Store {
	store := operations.New(db)
	store.ConfigurationDomains = []operations.ConfigurationDomain{adminops.MaintenanceConfiguration{}, backup.Configuration{Store: backup.NewPolicyStore(db, state)}, metadata.Configuration{}}
	return store
}

// A fresh loader checks the on-disk file without creating or changing state,
// importing settings, or keeping a previously valid document after an error.
func validateStateConfiguration(state string) error {
	_, err := configfile.New(storagepaths.Path(state, "portico.toml")).Reload()
	return err
}

// verifyRestoredState is what a restored state folder must pass before the
// restore is kept: the database's own checks, then every requirement above.
// A server left on a state that fails any of them would refuse every start.
// Failing here, while the previous state can still be put back, rolls the
// restore back instead. Nothing is created or adopted to make a restore pass:
// a bare database from another server has no keys here and is refused, and a
// whole backup folder is judged on the files it brought.
func verifyRestoredState(state string) backup.VerifyFunc {
	return func(ctx context.Context, path string) error {
		if err := backup.VerifyRestored(ctx, path); err != nil {
			return err
		}
		db, err := persistence.Open(path)
		if err != nil {
			return err
		}
		defer db.Close()
		for _, requirement := range restoredStateRequirements {
			if err = requirement.check(ctx, db, state); err != nil {
				// The cause travels with the reason, so a check that could not
				// be made is not mistaken for one that found something missing.
				return errors.New("backup: " + requirement.reason + " (" + err.Error() + ")")
			}
		}
		return nil
	}
}
