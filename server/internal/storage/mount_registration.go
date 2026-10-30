package storage

import (
	"context"
)

// MountedIdentity observes the actual mounted filesystem in a bounded storage
// child. Managed runtime binds the first ready identity and rejects replacement.
func (c *Client) MountedIdentity(ctx context.Context, path string) (string, error) {
	raw := *c
	raw.Guard, raw.MountedRoot = nil, nil
	var identity string
	err := raw.run(ctx, "mount-identity:"+path, "mount-identity", path, func(value Snapshot) error {
		if identity != "" || value.Data == "" {
			return ErrRootLease
		}
		identity = value.Data
		return nil
	})
	if err != nil {
		return "", err
	}
	if identity == "" {
		return "", ErrRootLease
	}
	return identity, nil
}
