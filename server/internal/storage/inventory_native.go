package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"runtime"
	"time"
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
	MountedRoot []byte
}

var ErrNativeInventory = errors.New("invalid native-byte inventory transport")

// NativeInventoryPage preserves filesystem bytes through an isolated helper.
// The owner is REQUIRED: its debit ends only after physical Wait, consumers and
// supervisor permit retirement, even if this call returns early on cancellation.
// Close/Wait the owner before reporting shutdown or releasing its working memory.
// There is no fallback to the lossy legacy InventoryPage JSON string protocol.
func (c *Client) NativeInventoryPage(ctx context.Context, owner *OperationScope, key string, r NativeInventoryRequest) (NativeInventoryPage, error) {
	var empty NativeInventoryPage
	if runtime.GOOS != "linux" || owner == nil || !validNativeRequest(r) {
		return empty, ErrNativeInventory
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	root := string(r.Root) // exact OS bytes, NOT a JSON/UTF8-decoded string
	if c.IsRemote(root) {
		return empty, ErrRemoteCursor
	}
	path := filepath.Join(root, string(r.RelativePath))
	if c.Guard != nil {
		if err := c.Guard(path); err != nil {
			return empty, err
		}
	}
	wire := nativeInventoryWire{NativeInventoryRequest: r}
	if c.MountedRoot != nil {
		wire.MountedRoot = []byte(c.MountedRoot(path))
	}
	if len(wire.MountedRoot) > naming.MaxPathBytes || bytes.IndexByte(wire.MountedRoot, 0) >= 0 {
		return empty, ErrNativeInventory
	}
	data, err := json.Marshal(request{Operation: "inventory-page-native-v1", NativeInventory: &wire})
	if err != nil || len(data) > 64<<10 {
		return empty, ErrNativeInventory
	}
	done, err := owner.Begin()
	if err != nil {
		return empty, err
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	stop := context.AfterFunc(owner.Context(), cancel)
	defer stop()
	defer cancel()
	if owner.Context().Err() != nil {
		cancel()
	}
	var page NativeInventoryPage
	err = c.inventoryRequest(ctx, key, "inventory-page-native-v1", InventoryRequest{}, &page, &ownedInventoryWire{
		input: data, retired: done, validate: func() error {
			if page.Version != 1 || len(page.Entries) > r.Limit || len(page.Issues) > 512 {
				return ErrNativeInventory
			}
			return nativePageText(page)
		}})
	if err == nil {
		err = inventoryCodeError(page.ErrorCode)
	}
	if err != nil {
		return empty, err
	}
	return page, nil
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

func nativePageText(p NativeInventoryPage) error {
	for _, v := range []string{p.ErrorCode, p.RootIdentity, p.DirectoryIdentity, p.DirectoryRevision, p.NextCursor} {
		if !utf8.ValidString(v) {
			return ErrNativeInventory
		}
	}
	for _, e := range p.Entries {
		if !utf8.ValidString(e.ObjectIdentity) || !utf8.ValidString(e.ChangeToken) {
			return ErrNativeInventory
		}
	}
	return nil
}

func nativeInventoryHelper(w *nativeInventoryWire, out io.Writer) (result error) {
	if runtime.GOOS != "linux" || w == nil || !validNativeRequest(w.NativeInventoryRequest) ||
		len(w.MountedRoot) > naming.MaxPathBytes || bytes.IndexByte(w.MountedRoot, 0) >= 0 {
		return ErrNativeInventory
	}
	if len(w.MountedRoot) > 0 {
		check := func() error {
			ok, err := isMount(string(w.MountedRoot))
			if err != nil || !ok {
				return errors.New("managed mount unavailable")
			}
			return nil
		}
		if err := check(); err != nil {
			return err
		}
		defer func() {
			if result == nil {
				result = check()
			}
		}()
	}
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
