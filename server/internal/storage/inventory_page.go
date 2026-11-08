package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var ErrInventoryChanged = nativeinventory.ErrInventoryChanged

// WARNING: this legacy JSON string carrier substitutes U+FFFD for invalid
// UTF8 native bytes and can collide distinct filenames. Schema-v1 must use
// NativeInventoryRequest/NativeInventoryPage instead, never repaired text.
// Inventory cursors are adapter-owned and are valid only for one directory
// observation. Only bounded ignore configuration is opened; media and sidecar
// contents are reserved for analysis.
type InventoryRequest struct {
	nativeSeparateEOF bool // native helper only; drain names with a cookie before a separate empty EOF
	// Stream is an internal isolated-helper protocol flag, not a catalog cursor.
	Stream            bool   `json:"stream,omitempty"`
	Root              string `json:"root"`
	RelativePath      string `json:"relativePath"`
	RootIdentity      string `json:"rootIdentity"`
	DirectoryRevision string `json:"directoryRevision"`
	Cursor            string `json:"cursor"`
	Limit             int    `json:"limit"`
	FollowSymlinks    bool   `json:"followSymlinks"`
}
type InventoryIssue struct {
	Line    int    `json:"line"`
	Path    string `json:"path"`
	Reason  string `json:"reason"`
	Protect bool   `json:"protect"`
	Warning bool   `json:"warning"`
}

type InventoryPage struct {
	ErrorCode         string           `json:"errorCode,omitempty"`
	Issues            []InventoryIssue `json:"issues,omitempty"`
	Unstable          bool             `json:"unstable,omitempty"`
	Remote            *RemotePage      `json:"-"`
	Pending           bool             `json:"-"`
	RootIdentity      string           `json:"rootIdentity"`
	DirectoryIdentity string           `json:"directoryIdentity"`
	DirectoryRevision string           `json:"directoryRevision"`
	Entries           []Snapshot       `json:"entries"`
	NextCursor        string           `json:"nextCursor"`
	Complete          bool             `json:"complete"`
}

func InventoryRevision(v Snapshot) string {
	b, _ := json.Marshal([]any{v.ObjectIdentity, v.ChangeToken, v.Size, v.ModifiedNS})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func RootIdentity(v Snapshot) string {
	// dev/inode (or platform equivalent) distinguishes an unmounted backing
	// folder from the admitted mount. Never substitute a pathname as identity.
	return v.ObjectIdentity
}

func (c *Client) NativeInventoryPreflight(r nativeinventory.Request) ([]byte, error) {
	root := string(r.Root)
	if c.IsRemote(root) {
		return nil, ErrRemoteCursor
	}
	path := filepath.Join(root, string(r.RelativePath))
	if c.Guard != nil {
		if err := c.Guard(path); err != nil {
			return nil, err
		}
	}
	if c.MountedRoot != nil {
		return []byte(c.MountedRoot(path)), nil
	}
	return nil, nil
}
func (c *Client) NativeInventorySettings() nativeinventory.Settings {
	return nativeinventory.Settings{Timeout: c.Timeout, Monitor: &c.Supervisor.nativeMonitor}
}
func (c *Client) NativeInventoryExchange(ctx context.Context, exchange *nativeinventory.Exchange) error {
	return c.inventoryRequest(ctx, exchange.Key(), "inventory-page-native-v1", InventoryRequest{}, nil, exchange)
}

func (c *Client) inventoryRequest(ctx context.Context, key, op string, r InventoryRequest, out any, owned ...*nativeinventory.Exchange) error {
	var data []byte
	var native *nativeinventory.Exchange
	if len(owned) > 0 {
		native = owned[0]
		data = native.Input()
	} else {
		if c.Guard != nil {
			if err := c.Guard(filepath.Join(r.Root, r.RelativePath)); err != nil {
				return err
			}
		}
		timeout := c.Timeout
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
		req := request{Operation: op, Path: filepath.Join(r.Root, r.RelativePath), Inventory: &r}
		if c.MountedRoot != nil {
			req.MountedRoot = c.MountedRoot(req.Path)
		}
		data, _ = json.Marshal(req)
	}
	cmd := exec.Command(c.Binary, "--portico-storage-helper")
	cmd.Stdin = bytes.NewReader(data)
	consume := func(reader io.Reader) error {
		if native != nil {
			return native.Consume(reader)
		}
		// Legacy framing/decoding stays byte-for-byte equivalent in behavior.
		d := json.NewDecoder(io.LimitReader(reader, 2<<20))
		if err := d.Decode(out); err != nil {
			return err
		}
		var extra any
		if err := d.Decode(&extra); err != io.EOF {
			return errors.New("invalid inventory response")
		}
		return nil
	}

	if native != nil {
		return c.Supervisor.RunOwnedCompletion(ctx, "inventory:"+key, cmd, consume, nil, native.Retire)
	}
	return c.Supervisor.Run(ctx, "inventory:"+key, cmd, consume)
}

// InventoryPage is legacy: its JSON name/path strings can collide invalid
// bytes with literal U+FFFD. It is not a lossless native-identity carrier.
func (c *Client) InventoryPage(ctx context.Context, key string, r InventoryRequest) (InventoryPage, error) {
	var page InventoryPage
	if c.IsRemote(r.Root) {
		return page, ErrRemoteCursor
	}
	if runtime.GOOS != "linux" {
		return c.retainedInventoryPage(ctx, key, r)
	}
	err := c.inventoryRequest(ctx, key, "inventory-page", r, &page)
	if err == nil {
		err = inventoryCodeError(page.ErrorCode)
	}
	return page, err
}
func (c *Client) InventoryDirectory(ctx context.Context, key string, r InventoryRequest) (Snapshot, error) {
	var v Snapshot
	if c.IsRemote(r.Root) {
		return c.remoteInventoryStat(ctx, r, true)
	}
	err := c.inventoryRequest(ctx, key, "inventory-directory", r, &v)
	if err == nil {
		err = inventoryCodeError(v.ErrorCode)
	}
	return v, err
}

// LocalInventoryPage is also used by isolated deterministic tests. Production
// always calls through Client so a blocked OS-mounted filesystem is killable.
func LocalInventoryPage(r InventoryRequest) (InventoryPage, error) {
	p, _, err := localInventoryPage(r)
	return p, err
}

// localInventoryPage also reports how many names the name reader returned for
// the page, including the ones the page then drops (ignored, unreadable,
// special or issue-only). That raw count is what a listing budget charges.
func localInventoryPage(r InventoryRequest) (p InventoryPage, raw int, err error) {
	if r.Limit < 1 || r.Limit > 128 || len(r.Cursor) > 4096 {
		return p, raw, errors.New("invalid inventory page")
	}
	retained, err := openInventoryRoot(r)
	if err != nil {
		return p, raw, err
	}
	defer retained.Close()
	root, err := retained.Stat(".")
	if err != nil || !root.IsDir() {
		return p, raw, ErrInventoryChanged
	}
	rootID := RootIdentity(snapshot(r.Root, root))
	// The recorded root is the same folder when only its device number moved
	// (SameRoot); the page reports the identity observed now.
	if r.RootIdentity != "" && !SameRoot(r.RootIdentity, rootID) {
		return p, raw, ErrInventoryChanged
	}
	path, err := inventoryPath(r)
	if err != nil {
		return p, raw, err
	}
	anchored, err := filepath.Rel(r.Root, path)
	if err != nil || !filepath.IsLocal(anchored) {
		return p, raw, ErrInventoryChanged
	}
	f, err := retained.Open(anchored)
	if err != nil {
		return p, raw, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.IsDir() {
		return p, raw, ErrInventoryChanged
	}
	before := snapshot(path, info)
	rev := InventoryRevision(before)
	if r.DirectoryRevision != "" && rev != r.DirectoryRevision {
		return p, raw, ErrInventoryChanged
	}
	p = InventoryPage{RootIdentity: rootID, DirectoryIdentity: before.ObjectIdentity, DirectoryRevision: rev, Entries: []Snapshot{}}
	rules, issues := inventoryIgnoreRules(retained, r)
	p.Issues = append(p.Issues, issues...)
	var names []string
	var next string
	var done bool
	switch {
	case r.Stream || runtime.GOOS != "linux":
		// Open by the already resolved absolute path, not os.Root's display name.
		// SameFile below compares it with this anchored descriptor before use.
		names, next, done, err = retainedInventoryNamesAt(f, path, r.Cursor, r.Limit)
	case r.nativeSeparateEOF:
		names, next, done, err = inventoryNamesPage(f, r.Cursor, r.Limit, true)
	default:
		names, next, done, err = inventoryNames(f, r.Cursor, r.Limit)
	}
	if err != nil {
		return p, raw, err
	}
	raw = len(names)
	for _, name := range names {
		if name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, "/\\\x00") {
			return p, raw, ErrInventoryChanged
		}
		if naming.IgnoreNameBeforeStat(name) {
			p.Issues = append(p.Issues, InventoryIssue{Path: filepath.Join(r.RelativePath, name), Reason: "ignored_name"})
			continue
		}
		child := filepath.Join(path, name)
		bindingPath := child
		info, e := os.Lstat(child)
		if e != nil {
			p.Issues = append(p.Issues, InventoryIssue{Path: filepath.Join(r.RelativePath, name), Reason: "entry_unreadable", Protect: true, Warning: true})
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if !r.FollowSymlinks {
				p.Issues = append(p.Issues, InventoryIssue{Path: filepath.Join(r.RelativePath, name), Reason: "symlink_disabled"})
				continue
			}
			resolved, e := filepath.EvalSymlinks(child)
			if e != nil {
				p.Issues = append(p.Issues, InventoryIssue{Path: filepath.Join(r.RelativePath, name), Reason: "symlink_unreadable", Protect: true, Warning: true})
				continue
			}
			rel, e := filepath.Rel(r.Root, resolved)
			if e != nil || !filepath.IsLocal(rel) {
				p.Issues = append(p.Issues, InventoryIssue{Path: filepath.Join(r.RelativePath, name), Reason: "symlink_outside_root", Protect: true, Warning: true})
				continue
			}
			bindingPath = resolved
			info, e = retained.Stat(rel)
			if e != nil {
				p.Issues = append(p.Issues, InventoryIssue{Path: filepath.Join(r.RelativePath, name), Reason: "entry_unreadable", Protect: true, Warning: true})
				continue
			}
		} else {
			info, e = retained.Stat(filepath.Join(anchored, name))
			if e != nil {
				p.Issues = append(p.Issues, InventoryIssue{Path: filepath.Join(r.RelativePath, name), Reason: "entry_unreadable", Protect: true, Warning: true})
				continue
			}
		}
		if naming.IgnorePathWithSize(filepath.ToSlash(filepath.Join(r.RelativePath, name)), info.IsDir(), info.Size()) {
			p.Issues = append(p.Issues, InventoryIssue{Path: filepath.Join(r.RelativePath, name), Reason: "ignored_name"})
			continue
		}
		ignored := false
		for _, rule := range rules {
			ignored = ignored || rule.Match(filepath.ToSlash(filepath.Join(r.RelativePath, name)), info.IsDir())
		}
		if ignored {
			p.Issues = append(p.Issues, InventoryIssue{Path: filepath.Join(r.RelativePath, name), Reason: "ignored_rule"})
			continue
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			continue
		}
		v := snapshot(bindingPath, info)
		if v.ObjectIdentity == "" {
			reason := "entry_unreadable"
			if info.IsDir() {
				// A folder's identity includes its filesystem and requires opening
				// it. Protect an unreadable child alone, so healthy siblings still
				// reconcile instead of making the whole parent appear changed.
				reason = "directory_unreadable"
			}
			p.Issues = append(p.Issues, InventoryIssue{Path: filepath.Join(r.RelativePath, name), Reason: reason, Protect: true, Warning: true})
			continue
		}
		v.Path = filepath.Join(r.Root, r.RelativePath, name)
		v.Name = name
		p.Entries = append(p.Entries, v)
	}
	after, err := f.Stat()
	if err != nil || InventoryRevision(snapshot(path, after)) != rev {
		p.Unstable = true
	}
	root, err = os.Stat(r.Root)
	if err != nil || RootIdentity(snapshot(r.Root, root)) != rootID {
		return p, raw, ErrInventoryChanged
	}
	// Re-resolving catches replaced ancestors and changed symlink targets too.
	current, err := os.Stat(path)
	if err != nil || InventoryRevision(snapshot(path, current)) != rev {
		p.Unstable = true
	}
	p.NextCursor, p.Complete = next, done
	if p.Unstable {
		p.Complete = true
		p.NextCursor = ""
	}
	return p, raw, nil
}
func inventoryPageHelper(r InventoryRequest, out io.Writer) error {
	if r.Stream {
		return ErrInventoryChanged // streaming requires the bounded demand reader
	}
	p, err := LocalInventoryPage(r)
	if err != nil {
		return inventoryFailure(err, out)
	}
	return json.NewEncoder(out).Encode(p)
}
func inventoryPath(r InventoryRequest) (string, error) {
	if !filepath.IsAbs(r.Root) || r.RelativePath != "." && !filepath.IsLocal(r.RelativePath) {
		return "", errors.New("invalid inventory root")
	}
	root := filepath.Clean(r.Root)
	path := root
	for _, part := range strings.Split(filepath.Clean(r.RelativePath), string(filepath.Separator)) {
		if part == "." {
			continue
		}
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if !r.FollowSymlinks {
				return "", errors.New("inventory symlink disabled")
			}
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return "", err
			}
			rel, err := filepath.Rel(root, resolved)
			if err != nil || !filepath.IsLocal(rel) {
				return "", errors.New("inventory symlink escapes root")
			}
			path = resolved
		}
	}
	return path, nil
}
func inspectInventoryDirectory(r InventoryRequest) (Snapshot, error) {
	path, err := inventoryPath(r)
	if err != nil {
		return Snapshot{}, err
	}
	root, err := openInventoryRoot(r)
	if err != nil {
		return Snapshot{}, err
	}
	defer root.Close()
	relative, err := filepath.Rel(r.Root, path)
	if err != nil || !filepath.IsLocal(relative) {
		return Snapshot{}, ErrInventoryChanged
	}
	info, err := root.Stat(relative)
	if err != nil || !info.IsDir() {
		return Snapshot{}, ErrInventoryChanged
	}
	return snapshot(path, info), nil
}

// LocalInventoryDirectory performs only listing/stat metadata operations.
func LocalInventoryDirectory(r InventoryRequest) (Snapshot, error) {
	return inspectInventoryDirectory(r)
}
func parseInventoryOffset(cursor string) (int64, error) {
	if cursor == "" {
		return 0, nil
	}
	v, e := strconv.ParseInt(cursor, 10, 64)
	if e != nil || v < 0 {
		return 0, ErrInventoryChanged
	}
	return v, nil
}

func inspectInventoryObject(r InventoryRequest) (Snapshot, error) {
	path, err := inventoryPath(r)
	if err != nil {
		return Snapshot{}, err
	}
	root, err := openInventoryRoot(r)
	if err != nil {
		return Snapshot{}, err
	}
	defer root.Close()
	relative, err := filepath.Rel(r.Root, path)
	if err != nil || !filepath.IsLocal(relative) {
		return Snapshot{}, ErrInventoryChanged
	}
	info, err := root.Stat(relative)
	if err != nil || !info.Mode().IsRegular() {
		return Snapshot{}, ErrInventoryChanged
	}
	return snapshot(filepath.Join(r.Root, r.RelativePath), info), nil
}
func (c *Client) InventoryStat(ctx context.Context, key string, r InventoryRequest) (Snapshot, error) {
	var v Snapshot
	if c.IsRemote(r.Root) {
		return c.remoteInventoryStat(ctx, r, false)
	}
	err := c.inventoryRequest(ctx, key, "inventory-stat", r, &v)
	if err == nil {
		err = inventoryCodeError(v.ErrorCode)
	}
	return v, err
}
func LocalInventoryStat(r InventoryRequest) (Snapshot, error) { return inspectInventoryObject(r) }
