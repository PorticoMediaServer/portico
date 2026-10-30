package assets

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"time"
)

type PlaybackSource struct {
	Path             string
	Size, ModifiedNS int64
	Binding          *storage.PlaybackBinding
}

// playbackPinCurrentSQL is the persisted pin's identity, lifetime, root and
// acquisition witness. Health is read separately so an offline storage state is
// named only after all of this still holds.
const playbackPinCurrentSQL = `pin.source_id=physical.source_id AND source.enabled=1 AND source.identity_confirmed=1 AND source.incarnation=physical.incarnation AND source.generation=physical.configuration_generation AND source.root=pin.root AND source.root_identity=pin.root_identity AND ((pin.binding='inventory' AND source.kind='local') OR (pin.binding='mounted' AND source.kind='remote'))`

func ResolvePlaybackSource(ctx context.Context, db *sql.DB, session, grantHash string) (PlaybackSource, error) {
	var v PlaybackSource
	var binding storage.PlaybackBinding
	var mode, health, library, container string
	var current bool
	e := db.QueryRowContext(ctx, `SELECT a.path,pin.size,pin.modified_ns,pin.binding,pin.root,pin.root_identity,pin.relative_path,pin.object_identity,pin.follow_symlinks,
 COALESCE(`+playbackPinCurrentSQL+`,0),COALESCE(source.health,''),COALESCE(cl.library_id,''),COALESCE(a.container,'')
 FROM playback_sessions ps JOIN catalog_assets a ON a.token=ps.asset_id JOIN playback_source_pins pin ON pin.session_id=ps.id
 LEFT JOIN catalog_entities e ON e.id=ps.item_id LEFT JOIN catalog_libraries cl ON cl.id=e.library_id
 LEFT JOIN playback_physical_source_pins physical ON physical.session_id=ps.id LEFT JOIN library_sources source ON source.id=pin.source_id
 WHERE (ps.id=? OR ps.grant_hash=?) AND a.available=1 AND pin.asset_id=a.token AND pin.size=a.size AND pin.modified_ns=a.modified_ns`, session, grantHash).Scan(&v.Path, &v.Size, &v.ModifiedNS, &mode, &binding.Inventory.Root, &binding.Inventory.RootIdentity, &binding.Inventory.RelativePath, &binding.ObjectIdentity, &binding.Inventory.FollowSymlinks, &current, &health, &library, &container)
	if e != nil {
		return PlaybackSource{}, e
	}
	if mode == "none" {
		// Source-less publications retain their no-follow size/mtime
		// acquisition. When the item's library still has a configured folder
		// over this path, the same fallback the catalogue advertises is read
		// once here: a matching source that is only offline is named, and the
		// same request works again the moment it reconnects.
		if container != "strm" {
			if refusal := sourceFallbackRefusal(ctx, db, library, v.Path); refusal != nil {
				return PlaybackSource{}, refusal
			}
		}
		return v, nil
	}
	if !current || (mode != "inventory" && mode != "mounted") || !validPlaybackBinding(binding) {
		return PlaybackSource{}, storage.ErrPlaybackSource
	}
	if refusal := storage.SourceHealthRefusal(health); refusal != nil {
		return PlaybackSource{}, refusal
	}
	binding.Mounted = mode == "mounted"
	v.Path = filepath.Join(binding.Inventory.Root, binding.Inventory.RelativePath)
	v.Binding = &binding
	return v, e
}

// ResolveCatalogPlaybackSource retains the same scan mapping and acquisition
// requirements for reads without a session, including DLNA's authorized item.
func ResolveCatalogPlaybackSource(ctx context.Context, db *sql.DB, asset string) (PlaybackSource, error) {
	var v PlaybackSource
	var container string
	err := db.QueryRowContext(ctx, `SELECT path,size,modified_ns,container FROM catalog_assets WHERE token=? AND available=1`, asset).Scan(&v.Path, &v.Size, &v.ModifiedNS, &container)
	if err != nil {
		return PlaybackSource{}, err
	}
	v.Binding, err = resolvePlaybackBinding(ctx, db, v, asset, "", "", 0)
	if err != nil {
		return PlaybackSource{}, err
	}
	if v.Binding == nil && container != "strm" {
		// The catalogue's folder fallback covers this file; a matching source
		// that is only offline is named rather than attempted and reported as a
		// missing file. Unassigned files keep their data-plane checks.
		if err = catalogFallbackRefusal(ctx, db, asset, v.Path); err != nil {
			return PlaybackSource{}, err
		}
	}
	return v, nil
}

func resolvePlaybackBinding(ctx context.Context, db *sql.DB, v PlaybackSource, asset, source, incarnation string, generation int64) (*storage.PlaybackBinding, error) {
	evidence, err := resolvePlaybackEvidence(ctx, db, v, asset, source, incarnation, generation)
	if err != nil || evidence == nil {
		return nil, err
	}
	return &evidence.binding, nil
}

type IsolatedPlaybackReader struct {
	io.ReadSeekCloser
	Modified time.Time
}

func OpenIsolatedPlayback(ctx context.Context, db *sql.DB, c *storage.Client, session, grantHash string) (*IsolatedPlaybackReader, error) {
	v, e := ResolvePlaybackSource(ctx, db, session, grantHash)
	if e != nil {
		return nil, e
	}
	return OpenIsolatedSource(ctx, c, v)
}

// OpenIsolatedSource opens a catalogued file through the storage helper. Local
// scan evidence binds its filesystem identity; remote inventory requires the
// registered physical mount and retains the existing size/mtime check. Missing
// or stale evidence never silently downgrades a mapped source.
func OpenIsolatedSource(ctx context.Context, c *storage.Client, v PlaybackSource) (*IsolatedPlaybackReader, error) {
	// A local file is opened and version-checked once by the supervised helper,
	// which hands back the open descriptor and exits; the bytes are then read
	// in-process. The fully isolated reader keeps a helper process alive for
	// the whole response and moves every 32 KB chunk through a JSON command
	// and two pipes, which is the right price for a Portico-managed network
	// mount (a hung mount blocks the child, not the server) and far too high
	// for local disks: fifty direct-play streams were fifty-plus processes and
	// a process launch for every range request a player issues.
	if storage.DescriptorPassing && (c.MountedRoot == nil || c.MountedRoot(v.Path) == "") {
		file, err := c.OpenPlaybackDescriptor(ctx, v.Path, v.Size, v.ModifiedNS, v.Binding)
		if err != nil {
			return nil, err
		}
		return &IsolatedPlaybackReader{file, time.Unix(0, v.ModifiedNS)}, nil
	}
	f, e := c.OpenPlayback(ctx, v.Path, v.Size, v.ModifiedNS, v.Binding)
	if e != nil {
		return nil, e
	}
	return &IsolatedPlaybackReader{f, time.Unix(0, v.ModifiedNS)}, nil
}

// OpenIsolatedPlaybackDescriptor is the Unix-only form: it returns the
// transferred descriptor and nothing else. Converted playback uses
// OpenPlaybackInput instead, which also says what to hand ffmpeg — because on
// Windows that is a path, not a descriptor.
func OpenIsolatedPlaybackDescriptor(ctx context.Context, db *sql.DB, c *storage.Client, session string) (*os.File, error) {
	v, e := ResolvePlaybackSource(ctx, db, session, "")
	if e != nil {
		return nil, e
	}
	return c.OpenPlaybackDescriptor(ctx, v.Path, v.Size, v.ModifiedNS, v.Binding)
}
