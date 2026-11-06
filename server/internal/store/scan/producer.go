package scan

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/naming"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// InventoryReader is the existing storage stream, not filesystem or SQL
// authority. The composing driver owns its operation scope and physical join.
type InventoryReader interface {
	InventoryPage(context.Context, string, storage.InventoryRequest) (storage.InventoryPage, error)
	StopInventory(string)
}

// InventoryTurn preserves the ORIGINAL request/cookies and native observation.
// A turn can contain a directory, an issue or empty EOF instead of a file.
// Those turns require their own writer protocol; they are never fabricated
// into an N1 file accept. Consumers must not retain or mutate the page slices.
type InventoryTurn struct {
	Request storage.InventoryRequest
	Page    storage.InventoryPage
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
func WalkInventory(ctx context.Context, reader InventoryReader, key, root string, maxBytes int64, visit func(context.Context, InventoryTurn) error) (counts TraversalCounts, err error) {
	if reader == nil || visit == nil || key == "" || len(key) > 1024 || !filepath.IsAbs(root) || len(root) > naming.MaxPathBytes || maxBytes <= 0 {
		return counts, ErrInvalid
	}
	defer reader.StopInventory(key)
	type frame struct {
		request  storage.InventoryRequest
		identity string
	}
	stack := []frame{{request: storage.InventoryRequest{Root: root, RelativePath: ".", Limit: 1}}}
	rootIdentity := ""
	counts.MaxDepth = 1
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return counts, err
		}
		f := &stack[len(stack)-1]
		f.request.RootIdentity = rootIdentity
		request := f.request
		page, err := reader.InventoryPage(ctx, key, request)
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
			child = filepath.Join(request.RelativePath, page.Entries[0].Name)
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
			stack = append(stack, frame{request: storage.InventoryRequest{Root: root, RelativePath: child, Limit: 1}})
		}
		counts.MaxDepth = max(counts.MaxDepth, len(stack))
	}
	return counts, nil
}

func checkInventoryTurn(r storage.InventoryRequest, p storage.InventoryPage, identity string, budget int64) error {
	if p.ErrorCode != "" || p.Pending || p.Remote != nil || p.Unstable || p.RootIdentity == "" || p.DirectoryIdentity == "" || p.DirectoryRevision == "" ||
		len(p.Entries) > 1 || len(p.Issues) > 512 || p.Complete == (p.NextCursor != "") ||
		!p.Complete && p.NextCursor == r.Cursor || len(p.NextCursor) > 4096 ||
		r.DirectoryRevision != "" && r.DirectoryRevision != p.DirectoryRevision || identity != "" && identity != p.DirectoryIdentity {
		return storage.ErrInventoryChanged
	}
	charge := func(s string) bool {
		if int64(len(s)) > budget {
			return false
		}
		budget -= int64(len(s))
		return true
	}
	for _, s := range []string{p.RootIdentity, p.DirectoryIdentity, p.DirectoryRevision, p.NextCursor} {
		if !charge(s) {
			return ErrCapacity
		}
	}
	for _, e := range p.Entries {
		for _, s := range []string{e.ErrorCode, e.Revision, e.ObjectID, e.ObjectIdentity, e.ChangeToken, e.Path, e.Name, e.Data} {
			if !charge(s) {
				return ErrCapacity
			}
		}
		relative := filepath.ToSlash(filepath.Join(r.RelativePath, e.Name))
		if e.ErrorCode != "" || e.Data != "" || e.Name == "" || e.Name == "." || e.Name == ".." || strings.ContainsAny(e.Name, "/\\\x00") ||
			!utf8.ValidString(e.Name) || !validPath(relative) || e.Path != filepath.Join(r.Root, r.RelativePath, e.Name) || e.Size < 0 || e.ObjectIdentity == "" {
			return fmt.Errorf("%w: native inventory entry", ErrInvalid)
		}
	}
	for _, issue := range p.Issues {
		if !charge(issue.Path) || !charge(issue.Reason) {
			return ErrCapacity
		}
	}
	return nil
}
