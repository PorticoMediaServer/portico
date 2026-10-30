package recordingmedia

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dirsync"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels/dvr"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
)

// Share installs another profile's immutable path using a hard link. Each
// profile retains independent catalogue authority, retirement and retention.
// The filesystem releases physical bytes only when the final path is removed.
func (d *Driver) Share(ctx context.Context, target, source dvr.CaptureRequest, cp dvr.CaptureCheckpoint) (dvr.CaptureResult, error) {
	reader, root, path, e := d.shareObject(ctx, target, source, cp.Object)
	if e != nil {
		return dvr.CaptureResult{}, e
	}
	defer reader.Close()
	facts, e := d.probe(ctx, target.Recording.ID, reader, cp.Object.Size)
	if e != nil {
		return dvr.CaptureResult{}, e
	}
	return d.result(target, cp, root, path, facts)
}

func (d *Driver) shareObject(ctx context.Context, target, source dvr.CaptureRequest, object mediaartifact.Object) (*mediaartifact.Reader, string, string, error) {
	original, _, e := d.store(source.Owner, source.ArtifactKey)
	if e != nil {
		return nil, "", "", e
	}
	reader, e := original.Open(ctx, object)
	if e != nil {
		return nil, "", "", e
	}
	defer reader.Close()
	origin, e := original.ObjectPath(object)
	if e != nil {
		return nil, "", "", e
	}
	destination, root, e := d.store(target.Owner, target.ArtifactKey)
	if e != nil {
		return nil, "", "", e
	}
	path := filepath.Join(destination.Root(), "objects", object.Digest)
	if e = os.Link(origin, path); e != nil && !errors.Is(e, os.ErrExist) {
		return nil, "", "", e
	}
	if e = dirsync.Sync(filepath.Dir(path)); e != nil {
		return nil, "", "", e
	}
	linked, e := destination.Open(ctx, object)
	if e != nil {
		return nil, "", "", e
	}
	return linked, root, path, nil
}
