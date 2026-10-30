package ingestion

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"time"
)

type remoteIgnoreScope struct {
	rules  []naming.IgnoreRules
	issues []storage.InventoryIssue
	bytes  int
}

type remoteIgnoreReadKey struct{}
type remoteIgnoreRead struct {
	job, source, listing string
	member               storage.Snapshot
}

const maxRemoteIgnoreScopes = 128
const maxRemoteIgnoreBytes = 512 << 10

// Managed listings are staged completely before a page is published. Looking
// up two exact configuration names there avoids probing absent files and works
// even when an ignore file would have appeared after media in the page order.
func (s *Service) remoteIgnoreScope(ctx context.Context, job string, source catalog.LibrarySource, scope string, page storage.RemotePage, read func(context.Context, string, int64) ([]byte, error)) (remoteIgnoreScope, error) {
	s.listingMu.Lock()
	cached, ok := s.ignoreScopes[job][scope]
	s.listingMu.Unlock()
	if ok {
		return cached, nil
	}
	out := remoteIgnoreScope{rules: []naming.IgnoreRules{}, issues: []storage.InventoryIssue{}}
	directory := filepath.Join(source.ResolvedPath, scope)
	listing := page.ID
	if scope != filepath.Clean(mustRelative(source.ResolvedPath, page.Directory)) {
		err := s.db.QueryRowContext(ctx, `SELECT id FROM remote_listing_sessions WHERE source_id=? AND generation=? AND directory=? AND ready=1 ORDER BY CASE WHEN job_id=? THEN 0 ELSE 1 END,created_at DESC,id DESC LIMIT 1`, page.SourceID, page.Generation, directory, job).Scan(&listing)
		if errors.Is(err, sql.ErrNoRows) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT snapshot FROM remote_listing_entries WHERE listing_id=? AND name IN(?,?) ORDER BY name`, listing, filepath.Join(directory, ".plexignore"), filepath.Join(directory, ".ignore"))
	if err != nil {
		return out, err
	}
	files := []storage.Snapshot{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return out, err
		}
		var v storage.Snapshot
		if err = json.Unmarshal([]byte(raw), &v); err != nil {
			rows.Close()
			return out, err
		}
		files = append(files, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	for _, file := range files {
		relative, e := filepath.Rel(source.ResolvedPath, file.Path)
		if e != nil || !filepath.IsLocal(relative) || filepath.Dir(relative) != scope {
			return out, catalog.ErrAdminQuery
		}
		if file.Directory || file.Size < 0 || file.Size > naming.MaxIgnoreBytes {
			out.issues = append(out.issues, storage.InventoryIssue{Path: relative, Reason: "ignore_file_budget_exceeded", Warning: true})
			continue
		}
		// Keep the exact selected listing/member through the existing callback.
		// The later retained reader must not refresh a failed listing handle or
		// substitute the media file's observation for this config file.
		readCtx := context.WithValue(ctx, remoteIgnoreReadKey{}, remoteIgnoreRead{job: job, source: source.ID, listing: listing, member: file})
		raw, err := read(readCtx, file.Path, naming.MaxIgnoreBytes)
		if err != nil {
			if errors.Is(err, storage.ErrBusy) || errors.Is(err, storage.ErrRemoteChanged) || errors.Is(err, storage.ErrInventoryChanged) || ctx.Err() != nil {
				return out, err
			}
			out.issues = append(out.issues, storage.InventoryIssue{Path: relative, Reason: "ignore_unreadable", Warning: true})
			continue
		}
		out.bytes += len(raw)
		rule, diagnostics := naming.ParseIgnore(filepath.ToSlash(scope), string(raw), scope == ".")
		out.rules = append(out.rules, rule)
		for _, diagnostic := range diagnostics {
			if len(out.issues) >= 128 {
				out.issues = append(out.issues, storage.InventoryIssue{Path: relative, Reason: "ignore_diagnostic_budget_exceeded", Line: diagnostic.Line, Warning: true})
				break
			}
			out.issues = append(out.issues, storage.InventoryIssue{Path: relative, Reason: diagnostic.Reason, Line: diagnostic.Line, Warning: true})
		}
	}
	s.listingMu.Lock()
	defer s.listingMu.Unlock()
	if s.ignoreScopes == nil {
		s.ignoreScopes = map[string]map[string]remoteIgnoreScope{}
	}
	if s.ignoreScopes[job] == nil {
		s.ignoreScopes[job] = map[string]remoteIgnoreScope{}
	}
	used := 0
	for _, entry := range s.ignoreScopes[job] {
		used += entry.bytes
	}
	if len(s.ignoreScopes[job]) < maxRemoteIgnoreScopes && used+out.bytes <= maxRemoteIgnoreBytes {
		s.ignoreScopes[job][scope] = out
	}
	return out, nil
}
func mustRelative(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return ""
	}
	return relative
}

func (s *Service) applyRemoteIgnores(ctx context.Context, job string, source catalog.LibrarySource, relative string, page *storage.InventoryPage, read func(context.Context, string, int64) ([]byte, error)) error {
	if page.Pending {
		return nil
	}
	scopes := []string{}
	scope := filepath.Clean(relative)
	for len(scopes) <= naming.MaxDepth {
		scopes = append(scopes, scope)
		if scope == "." {
			break
		}
		scope = filepath.Dir(scope)
	}
	if len(scopes) > naming.MaxDepth {
		return storage.ErrRemoteLimit
	}
	rules := []naming.IgnoreRules{}
	budget := 256 << 10
	for i := len(scopes) - 1; i >= 0; i-- {
		configuration, err := s.remoteIgnoreScope(ctx, job, source, scopes[i], *page.Remote, read)
		if err != nil {
			return err
		}
		if budget < configuration.bytes {
			page.Issues = append(page.Issues, storage.InventoryIssue{Path: filepath.Join(scopes[i], ".plexignore"), Reason: "ignore_file_budget_exceeded", Warning: true})
			break
		}
		budget -= configuration.bytes
		rules = append(rules, configuration.rules...)
		for _, issue := range configuration.issues {
			if len(page.Issues) < 256 {
				page.Issues = append(page.Issues, issue)
			}
		}
	}
	kept := make([]storage.Snapshot, 0, len(page.Entries))
	for _, entry := range page.Entries {
		child := filepath.Join(relative, entry.Name)
		ignored := false
		for _, rule := range rules {
			ignored = ignored || rule.Match(filepath.ToSlash(child), entry.Directory)
		}
		if ignored {
			page.Issues = append(page.Issues, storage.InventoryIssue{Path: child, Reason: "ignored_rule"})
		} else {
			kept = append(kept, entry)
		}
	}
	page.Entries = kept
	return nil
}

func (s *Service) readRemoteIgnore(ctx context.Context, job string, source catalog.LibrarySource, path string, limit int64) ([]byte, error) {
	observed, ok := ctx.Value(remoteIgnoreReadKey{}).(remoteIgnoreRead)
	if !ok || observed.job != job || observed.source != source.ID || observed.listing == "" || observed.member.Path != path || observed.member.Directory || filepath.Base(path) != ".plexignore" && filepath.Base(path) != ".ignore" {
		return nil, storage.ErrRemoteConfig
	}
	policy, err := s.catalog.ScanPolicy(ctx, source.LibraryID)
	if err != nil {
		return nil, err
	}
	admission := storage.InventoryBlobAdmission{Purpose: storage.InventoryBlobIgnore,
		JobID: job, LibraryID: source.LibraryID, LibrarySourceID: source.ID,
		RootIncarnation: source.Incarnation, RootIdentity: source.RootIdentity, SourceGeneration: source.Generation,
		PolicyRevision: policy.Revision, ListingID: observed.listing, Member: observed.member}
	check := func(ctx context.Context, _ string) error {
		var valid bool
		err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_runs r JOIN jobs j ON j.id=r.job_id JOIN library_sources s ON s.id=r.source_id WHERE r.job_id=? AND j.status='running' AND s.id=? AND s.enabled=1 AND r.source_generation=s.generation AND r.root_incarnation=s.incarnation AND r.root_identity=s.root_identity)`, job, source.ID).Scan(&valid)
		if err != nil {
			return err
		}
		if !valid {
			return context.Canceled
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ctx = storage.WithScanInput(ctx, s.storage, source.ID, storage.InventoryRequest{Root: source.ResolvedPath, RootIdentity: source.RootIdentity, FollowSymlinks: source.FollowSymlinks}, admission)
	return s.storage.ReadSmall(storage.WithScanReadGuard(ctx, check), source.ID, path, limit)
}
