package storage

import (
	"io"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
	"strings"
)

// Ignore files are configuration, read inside the same retained source root as
// enumeration. Depth, total bytes and pattern counts are bounded independently.
func inventoryIgnoreRules(root *os.Root, r InventoryRequest) ([]naming.IgnoreRules, []InventoryIssue) {
	rules := []naming.IgnoreRules{}
	issues := []InventoryIssue{}
	parts := strings.Split(filepath.Clean(r.RelativePath), string(filepath.Separator))
	scopes := []string{"."}
	scope := "."
	if r.RelativePath != "." {
		for _, part := range parts {
			scope = filepath.Join(scope, part)
			scopes = append(scopes, scope)
		}
	}
	budget := 256 << 10
	if len(scopes) > naming.MaxDepth {
		return rules, []InventoryIssue{{Path: r.RelativePath, Reason: "path_depth_exceeded", Protect: true, Warning: true}}
	}
	for _, scope := range scopes {
		for _, name := range []string{".plexignore", ".ignore"} {
			relative := filepath.Join(scope, name)
			// Resolve with the normal follow/containment rules before opening a file.
			req := r
			req.RelativePath = relative
			path, err := inventoryPath(req)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				issues = append(issues, InventoryIssue{Path: relative, Reason: "ignore_unreadable", Warning: true})
				continue
			}
			rel, err := filepath.Rel(r.Root, path)
			if err != nil || !filepath.IsLocal(rel) {
				continue
			}
			f, err := root.Open(rel)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				issues = append(issues, InventoryIssue{Path: relative, Reason: "ignore_unreadable", Warning: true})
				continue
			}
			info, err := f.Stat()
			if err != nil || !info.Mode().IsRegular() {
				f.Close()
				issues = append(issues, InventoryIssue{Path: relative, Reason: "ignore_unreadable", Warning: true})
				continue
			}
			content, err := io.ReadAll(io.LimitReader(f, int64(min(naming.MaxIgnoreBytes+1, budget+1))))
			f.Close()
			if err != nil {
				issues = append(issues, InventoryIssue{Path: relative, Reason: "ignore_unreadable", Warning: true})
				continue
			}
			budget -= len(content)
			if budget < 0 {
				issues = append(issues, InventoryIssue{Path: relative, Reason: "ignore_file_budget_exceeded", Warning: true})
				return rules, issues
			}
			rule, diagnostics := naming.ParseIgnore(filepath.ToSlash(scope), string(content), scope == ".")
			rules = append(rules, rule)
			for _, d := range diagnostics {
				if len(issues) >= 256 {
					issues = append(issues, InventoryIssue{Path: relative, Reason: "ignore_diagnostic_budget_exceeded", Line: d.Line, Warning: true})
					return rules, issues
				}
				issues = append(issues, InventoryIssue{Path: relative, Reason: d.Reason, Line: d.Line, Warning: true})
			}
		}
	}
	return rules, issues
}
