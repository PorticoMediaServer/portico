package scan

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/naming"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// InventoryReader is the existing storage stream, not filesystem or SQL
// authority. The composing driver owns its operation scope and physical join.
type InventoryReader interface {
	NativeInventoryPage(context.Context, *storage.OperationScope, string, storage.NativeInventoryRequest) (storage.NativeInventoryPage, error)
	StopInventory(string)
}

// InventoryTurn preserves the ORIGINAL request/cookies and native observation.
// A turn can contain a directory, an issue or empty EOF instead of a file.
// Those turns require their own writer protocol; they are never fabricated
// into an N1 file accept. Consumers must not retain or mutate the page slices.
type InventoryTurn struct {
	Request storage.NativeInventoryRequest
	Page    storage.NativeInventoryPage
}

// TraversalCounts describe observed traversal only, never accepted availability,
// durable cursor progress, directory coverage or authority to declare absence.
type TraversalCounts struct {
	Pages       uint64 `json:"pages"`
	Files       uint64 `json:"files"`
	Directories uint64 `json:"directories"`
	Issues      uint64 `json:"issues"`
	MaxDepth    int    `json:"max_depth"`
}

// WalkInventory emits one raw-name turn at a time with synchronous backpressure.
// N1 is deliberate: splitting a larger page cannot invent intermediate native
// cookies. DFS retains one cursor per ancestor, never a whole-library queue.
// visit must finish before any subsequent read; a refusal/error stops here,
// without retries or inferred success. Durable replay belongs to W's adapter.
// The caller must reserve working memory before entry; maxBytes bounds each
// returned observation's strings, not total heap/helper/native/RSS custody.
// This harness-only path follows no symlinks and cannot certify a probe seam.
func WalkInventory(ctx context.Context, reader InventoryReader, owner *storage.OperationScope, key string, root []byte, maxBytes int64, visit func(context.Context, InventoryTurn) error) (counts TraversalCounts, err error) {
	if reader == nil || owner == nil || visit == nil || key == "" || len(key) > 1024 || !filepath.IsAbs(string(root)) || len(root) > naming.MaxPathBytes || maxBytes <= 0 || bytes.IndexByte(root, 0) >= 0 {
		return counts, ErrInvalid
	}
	defer reader.StopInventory(key)
	type frame struct {
		request  storage.NativeInventoryRequest
		identity string
	}
	stack := []frame{{request: storage.NativeInventoryRequest{Root: bytes.Clone(root), RelativePath: []byte("."), Limit: 1}}}
	stackRoot := stack[0].request.Root
	rootIdentity := ""
	counts.MaxDepth = 1
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return counts, err
		}
		f := &stack[len(stack)-1]
		f.request.RootIdentity = rootIdentity
		request := f.request
		page, err := reader.NativeInventoryPage(ctx, owner, key, request)
		if err != nil {
			return counts, err
		}
		if err := checkInventoryTurn(request, page, f.identity, maxBytes); err != nil {
			return counts, err
		}
		if rootIdentity == "" {
			rootIdentity = page.RootIdentity
		}
		// Even an adapter accepting a moved device must not mix two roots in one run.
		if rootIdentity != page.RootIdentity {
			return counts, storage.ErrInventoryChanged
		}
		if err := ctx.Err(); err != nil {
			return counts, err
		}
		// Keep traversal's continuation independent of a consumer's value copy.
		next, revision, identity := page.NextCursor, page.DirectoryRevision, page.DirectoryIdentity
		child := ""
		if len(page.Entries) == 1 && page.Entries[0].Directory {
			child = filepath.Join(string(request.RelativePath), string(page.Entries[0].Name))
			if len(stack) >= naming.MaxDepth {
				return counts, ErrCapacity
			}
		}
		if err := visit(ctx, InventoryTurn{Request: request, Page: page}); err != nil {
			return counts, err
		}
		counts.Pages++
		counts.Issues += uint64(len(page.Issues))
		if len(page.Entries) == 1 {
			if child != "" {
				counts.Directories++
			} else {
				counts.Files++
			}
		}
		if page.Complete {
			stack = stack[:len(stack)-1]
		} else {
			f.request.Cursor, f.request.DirectoryRevision, f.identity = next, revision, identity
		}
		if child != "" {
			stack = append(stack, frame{request: storage.NativeInventoryRequest{Root: stackRoot, RelativePath: []byte(child), Limit: 1}})
		}
		counts.MaxDepth = max(counts.MaxDepth, len(stack))
	}
	return counts, nil
}

func checkInventoryTurn(r storage.NativeInventoryRequest, p storage.NativeInventoryPage, identity string, budget int64) error {
	if p.Version != 1 || p.ErrorCode != "" || p.Unstable || p.RootIdentity == "" || p.DirectoryIdentity == "" || p.DirectoryRevision == "" ||
		len(p.Entries) > 1 || len(p.Issues) > 512 || p.Complete == (p.NextCursor != "") || !p.Complete && p.NextCursor == r.Cursor || len(p.NextCursor) > 4096 ||
		r.DirectoryRevision != "" && r.DirectoryRevision != p.DirectoryRevision || identity != "" && identity != p.DirectoryIdentity {
		return storage.ErrInventoryChanged
	}
	charge := func(n int) bool {
		if int64(n) > budget {
			return false
		}
		budget -= int64(n)
		return true
	}
	if !charge(len(r.Root)) || !charge(len(r.RelativePath)) {
		return ErrCapacity
	}
	for _, s := range []string{p.RootIdentity, p.DirectoryIdentity, p.DirectoryRevision, p.NextCursor} {
		if !charge(len(s)) {
			return ErrCapacity
		}
	}
	for _, e := range p.Entries {
		for _, n := range []int{len(e.Path), len(e.Name), len(e.ObjectIdentity), len(e.ChangeToken)} {
			if !charge(n) {
				return ErrCapacity
			}
		}
		relative := filepath.Join(string(r.RelativePath), string(e.Name)) // OS bytes only, never JSON text
		if len(e.Name) == 0 || bytes.Equal(e.Name, []byte(".")) || bytes.Equal(e.Name, []byte("..")) || bytes.ContainsAny(e.Name, "/\\\x00") ||
			!validNativeRelative([]byte(relative)) || !bytes.Equal(e.Path, []byte(filepath.Join(string(r.Root), string(r.RelativePath), string(e.Name)))) || e.Size < 0 || e.ObjectIdentity == "" {
			return fmt.Errorf("%w: native inventory entry", ErrInvalid)
		}
	}
	for _, issue := range p.Issues {
		if !charge(len(issue.Path)) || !charge(len(issue.Reason)) {
			return ErrCapacity
		}
	}
	return nil
}

func validNativeRelative(p []byte) bool {
	// Native Linux filenames may contain invalid UTF8. Structural checks operate
	// on exact bytes; the later SC constructor owns its TEXT-path admission policy.
	s := string(p)
	return len(p) <= naming.MaxPathBytes && bytes.IndexByte(p, 0) < 0 && !bytes.Contains(p, []byte{'\\'}) &&
		s != "." && filepath.IsLocal(s) && filepath.Clean(s) == s && strings.Count(s, "/") < naming.MaxDepth
}
