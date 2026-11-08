// Package nativeinventory owns the existing byte-preserving inventory protocol
// and physical observation lifetime without loading storage's database policy.
package nativeinventory

import (
	"bytes"
	"errors"
	"path/filepath"
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

type Wire struct {
	NativeInventoryRequest
	MountedRoot []byte `json:",omitempty"`
}

var ErrNativeInventory = errors.New("invalid native-byte inventory transport")

func validNativeRequest(r NativeInventoryRequest) bool {
	// A native directory cookie has meaning only in the observed revision of
	// that directory. Never accept a continuation without its change fence.
	if r.Cursor != "" && r.DirectoryRevision == "" {
		return false
	}
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

// Short names are the same concrete native protocol values, not new carriers.
type Request = NativeInventoryRequest
type Page = NativeInventoryPage
type Entry = NativeInventoryEntry
type Issue = NativeInventoryIssue

// ErrInventoryChanged is shared by identity with the legacy storage adapter.
var ErrInventoryChanged = errors.New("inventory directory or root changed")

func ValidRequest(r Request) bool { return validNativeRequest(r) }
