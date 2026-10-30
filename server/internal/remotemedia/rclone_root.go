package remotemedia

import (
	"context"
	"errors"
)

// CheckRoot is connection-test metadata only, never file/content authority.
// Upstream StatJSON(remote="",dirsOnly=true) calls the backend List before
// returning one directory item. A VFS root/readiness or fsinfo alone need not
// contact the backend. Hashes, MIME, modtime and object payloads are not read.
// rclone may buffer its underlying directory list; the response here is one
// bounded item, and the caller's exclusive-test context supplies the deadline.
func (m *RcloneMetadata) CheckRoot(ctx context.Context, check func(context.Context) error) error {
	ctx, release, err := m.endpoint.budget.enter(ctx)
	if err != nil {
		return err
	}
	defer release()
	var raw struct {
		Item *struct {
			Path  string
			IsDir bool
		}
	}
	if err = m.request(ctx, "operations/stat", map[string]any{"fs": m.fs, "remote": "", "opt": map[string]any{"dirsOnly": true, "showHash": false, "noModTime": true, "noMimeType": true}}, &raw, check); err != nil {
		return err
	}
	if raw.Item == nil || raw.Item.Path != "" || !raw.Item.IsDir {
		return ErrDAVResponse
	}
	return check(ctx)
}

// CheckRootFile replaces the ordinary root check for a WebDAV setup test.
// It uses that one backend listing, retaining at most its first nonempty raw
// file for the later diagnostic. There is no second list, hash or payload read.
func (m *RcloneMetadata) CheckRootFile(ctx context.Context, check func(context.Context) error) (*DAVEntry, error) {
	if check == nil {
		return nil, ErrPolicy
	}
	ctx, release, err := m.endpoint.budget.enter(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	var file *DAVEntry
	found := errors.New("setup root file retained")
	err = m.visitDirectory(ctx, "", check, func(entry RcloneFileMetadata) error {
		if entry.Directory || entry.Size < 1 {
			return nil
		}
		file = &DAVEntry{Relative: entry.Path, Size: entry.Size}
		return found
	}, map[string]any{"recurse": false, "showHash": false, "noModTime": true, "noMimeType": true})
	if err != nil && !errors.Is(err, found) {
		return nil, err
	}
	if err = check(ctx); err != nil {
		return nil, err
	}
	return file, nil
}
