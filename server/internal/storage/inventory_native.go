package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"runtime"

	"github.com/PorticoMediaServer/portico/server/internal/naming"
	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
)

type NativeInventoryRequest = nativeinventory.Request
type NativeInventoryEntry = nativeinventory.Entry
type NativeInventoryIssue = nativeinventory.Issue
type NativeInventoryPage = nativeinventory.Page
type nativeInventoryWire = nativeinventory.Wire

var ErrNativeInventory = nativeinventory.ErrNativeInventory

func (c *Client) NativeInventoryObserver() nativeinventory.Observer {
	return nativeinventory.NewObserver(c)
}

func (c *Client) NativeInventoryPage(ctx context.Context, owner *OperationScope, key string, r NativeInventoryRequest) (NativeInventoryPage, error) {
	return c.NativeInventoryObserver().Observe(ctx, owner, key, r)
}

func nativeInventoryHelper(w *nativeInventoryWire, out io.Writer) (result error) {
	if runtime.GOOS != "linux" || w == nil || !nativeinventory.ValidRequest(w.NativeInventoryRequest) ||
		len(w.MountedRoot) > naming.MaxPathBytes || bytes.IndexByte(w.MountedRoot, 0) >= 0 {
		return ErrNativeInventory
	}
	if err := nativeInventoryMount(w.MountedRoot); err != nil {
		return err
	}
	result = nativeInventoryEmit(w, out)
	if result == nil {
		result = nativeInventoryMount(w.MountedRoot)
	}
	return result
}
func nativeInventoryMount(root []byte) error {
	if len(root) == 0 {
		return nil
	}
	ok, err := isMount(string(root))
	if err != nil || !ok {
		return errors.New("managed mount unavailable")
	}
	return nil
}
func nativeInventoryEmit(w *nativeInventoryWire, out io.Writer) error {
	// Conversion occurs only after lossless byte decoding, for OS APIs. No path
	// string crosses a JSON boundary. Stream=false always selects native d_off.
	r := InventoryRequest{nativeSeparateEOF: true, Root: string(w.Root), RelativePath: string(w.RelativePath), RootIdentity: w.RootIdentity,
		DirectoryRevision: w.DirectoryRevision, Cursor: w.Cursor, Limit: w.Limit, FollowSymlinks: w.FollowSymlinks}
	p, err := LocalInventoryPage(r)
	if err != nil {
		return json.NewEncoder(out).Encode(NativeInventoryPage{Version: 1, ErrorCode: inventoryErrorCode(err)})
	}
	n := NativeInventoryPage{Version: 1, RootIdentity: p.RootIdentity, DirectoryIdentity: p.DirectoryIdentity,
		DirectoryRevision: p.DirectoryRevision, NextCursor: p.NextCursor, Complete: p.Complete, Unstable: p.Unstable}
	for _, e := range p.Entries {
		if e.ErrorCode != "" || e.Revision != "" || e.ObjectID != "" || e.Data != "" {
			return ErrNativeInventory
		}
		n.Entries = append(n.Entries, NativeInventoryEntry{Path: []byte(e.Path), Name: []byte(e.Name), ObjectIdentity: e.ObjectIdentity,
			ChangeToken: e.ChangeToken, Size: e.Size, ModifiedNS: e.ModifiedNS, Directory: e.Directory})
	}
	for _, i := range p.Issues {
		n.Issues = append(n.Issues, NativeInventoryIssue{Path: []byte(i.Path), Reason: []byte(i.Reason), Line: i.Line, Protect: i.Protect, Warning: i.Warning})
	}
	if err := nativeinventory.ValidatePageText(n); err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(n)
}
