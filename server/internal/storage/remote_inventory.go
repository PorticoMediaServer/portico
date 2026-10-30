package storage

import (
	"context"
	"path/filepath"
)

func (c *Client) remoteInventoryStat(ctx context.Context, r InventoryRequest, directory bool) (Snapshot, error) {
	if !filepath.IsAbs(r.Root) || !filepath.IsLocal(r.RelativePath) {
		return Snapshot{}, ErrRemoteConfig
	}
	// Identity comes from the adapter's admitted virtual namespace. Never inspect
	// its empty backing folder or a concurrently mounted FUSE projection.
	root, err := c.Remote.InspectRoot(ctx, r.Root)
	if err != nil {
		return Snapshot{}, err
	}
	if r.RootIdentity != "" && RootIdentity(root) != r.RootIdentity {
		return Snapshot{}, ErrInventoryChanged
	}
	if r.RelativePath == "." {
		return root, nil
	}
	v, err := c.Remote.Stat(ctx, filepath.Join(r.Root, r.RelativePath))
	if err == nil && directory && !v.Directory {
		err = ErrInventoryChanged
	}
	return v, err
}
