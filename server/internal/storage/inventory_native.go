package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"runtime"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/naming"
)

// NativeInventoryRequest is a distinct Linux getdents64 wire contract. Path
// bytes are base64 JSON, never JSON strings. It has no retained-stream cursor
// mode. Text fields are generated adapter witnesses, not native filenames.
type NativeInventoryRequest struct {
	Root, RelativePath                      []byte
	RootIdentity, DirectoryRevision, Cursor string
	Limit                                   int
	FollowSymlinks                          bool
}

type NativeInventoryEntry struct {
	Path, Name                  []byte
	ObjectIdentity, ChangeToken string
	Size, ModifiedNS            int64
	Directory                   bool
}

type NativeInventoryIssue struct {
	Path, Reason     []byte
	Line             int
	Protect, Warning bool
}

type NativeInventoryPage struct {
	Version                                            int
	ErrorCode                                          string
	RootIdentity, DirectoryIdentity, DirectoryRevision string
	Entries                                            []NativeInventoryEntry
	Issues                                             []NativeInventoryIssue
	NextCursor                                         string
	Complete, Unstable                                 bool
}

type nativeInventoryWire struct {
	NativeInventoryRequest
	MountedRoot []byte `json:",omitempty"`
}

var ErrNativeInventory = errors.New("invalid native-byte inventory transport")

// NativeInventoryPage preserves bytes through the existing supervised helper.
// Each call joins its OWN physical retirement, including cancellation, before
// returning. It never closes the shared owner; other operations may still live.
// A stuck helper can therefore delay return until actual Wait/consumer retirement.
// There is no fallback to the lossy legacy InventoryPage JSON string protocol.
func (c *Client) NativeInventoryPage(ctx context.Context, owner *OperationScope, key string, r NativeInventoryRequest) (NativeInventoryPage, error) {
	if runtime.GOOS != "linux" || owner == nil || !validNativeRequest(r) {
		return NativeInventoryPage{}, ErrNativeInventory
	}
	return c.nativeInventoryOwned(ctx, owner, key, r)
}

func validNativeRequest(r NativeInventoryRequest) bool {
	if len(r.Root) > naming.MaxPathBytes || len(r.RelativePath) > naming.MaxPathBytes || len(r.Cursor) > 4096 || r.Limit < 1 || r.Limit > 128 ||
		bytes.IndexByte(r.Root, 0) >= 0 || bytes.IndexByte(r.RelativePath, 0) >= 0 ||
		!filepath.IsAbs(string(r.Root)) || string(r.RelativePath) != "." && !filepath.IsLocal(string(r.RelativePath)) {
		return false
	}
	for _, v := range []string{r.RootIdentity, r.DirectoryRevision, r.Cursor} {
		if len(v) > 4096 || !utf8.ValidString(v) {
			return false
		}
	}
	return true
}

func nativeInventoryHelper(w *nativeInventoryWire, out io.Writer) (result error) {
	if runtime.GOOS != "linux" || w == nil || !validNativeRequest(w.NativeInventoryRequest) ||
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
	r := InventoryRequest{Root: string(w.Root), RelativePath: string(w.RelativePath), RootIdentity: w.RootIdentity,
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
	if err := nativePageText(n); err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(n)
}

// The sole Begin here is the typed operation lease, not database/sql. Returning
// its release callback does not invoke arbitrary callable state in this file.
func nativeInventoryLease(owner *OperationScope) (func(), error) { return owner.Begin() }
