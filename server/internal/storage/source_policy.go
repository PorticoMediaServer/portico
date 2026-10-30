package storage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ErrSourceRootDenied is deliberately independent of the reason for refusal:
// callers must not disclose private host paths to a remote administrator.
var ErrSourceRootDenied = errors.New("source root is not permitted")

// MediaRoots is an optional operator limit on the folder picker. With no value,
// the owner may browse all readable host folders outside Portico's own state.
func MediaRoots() []string {
	parts := strings.Split(os.Getenv("PORTICO_MEDIA_ROOTS"), string(os.PathListSeparator))
	roots := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			roots = append(roots, part)
		}
	}
	return roots
}

// SourcePolicy protects Portico's own state from selection and browsing. An
// optional Roots list narrows the picker only, never existing library scans.
type SourcePolicy struct {
	Roots                 []string
	StateDirectory        string
	ManagedMountDirectory string
}

func canonical(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", ErrSourceRootDenied
	}
	clean := filepath.Clean(path)
	resolved, err := filepath.EvalSymlinks(clean)
	if os.IsNotExist(err) && filepath.Dir(clean) != clean {
		parent, e := canonical(filepath.Dir(clean))
		if e == nil {
			return filepath.Join(parent, filepath.Base(clean)), nil
		}
	}
	return resolved, err
}

func within(path, root string) bool {
	// Both inputs have already had their symlinks resolved. Compare filesystem
	// identities at each ancestor: lowercasing paths conflates distinct folders
	// on case-sensitive volumes and still misses Unicode aliases on APFS.
	rootInfo, err := os.Stat(root)
	if err != nil {
		return false
	}
	for current := path; ; current = filepath.Dir(current) {
		if info, err := os.Stat(current); err == nil && os.SameFile(info, rootInfo) {
			return true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false
		}
	}
}

func overlaps(path, root string) bool { return within(path, root) || within(root, path) }

// Check refuses the state tree and its ancestors as new library sources. The
// managed mount allocation is the one state subtree intentionally usable as a
// source. Existing sources are never rechecked during scans.
func (p SourcePolicy) Check(path string) error {
	resolved, err := canonical(path)
	if err != nil {
		return ErrSourceRootDenied
	}
	if p.StateDirectory == "" {
		return nil
	}
	state, err := canonical(p.StateDirectory)
	if err != nil {
		return ErrSourceRootDenied
	}
	if p.ManagedMountDirectory != "" {
		if mount, e := canonical(p.ManagedMountDirectory); e == nil && within(resolved, mount) && within(mount, state) {
			return nil
		}
	}
	if overlaps(resolved, state) {
		return ErrSourceRootDenied
	}
	return nil
}

// CheckBrowse permits navigation through a parent of state while hiding the
// protected state entry itself. It also applies an optional operator picker
// limit without changing source-add authority.
func (p SourcePolicy) CheckBrowse(path string) error {
	resolved, err := canonical(path)
	if err != nil {
		return ErrSourceRootDenied
	}
	if p.StateDirectory != "" {
		state, e := canonical(p.StateDirectory)
		if e != nil {
			return ErrSourceRootDenied
		}
		if within(resolved, state) {
			if p.ManagedMountDirectory == "" {
				return ErrSourceRootDenied
			}
			mount, e := canonical(p.ManagedMountDirectory)
			if e != nil || !within(resolved, mount) || !within(mount, state) {
				return ErrSourceRootDenied
			}
		}
	}
	if len(p.Roots) == 0 {
		return nil
	}
	for _, root := range p.Roots {
		if base, e := canonical(root); e == nil && within(resolved, base) {
			return nil
		}
	}
	return ErrSourceRootDenied
}

// SafeRoots lists optional picker roots, including parents of state whose
// protected child will be hidden when browsed.
func (p SourcePolicy) SafeRoots() []string {
	out := make([]string, 0, len(p.Roots))
	for _, root := range p.Roots {
		if p.CheckBrowse(root) == nil {
			resolved, _ := canonical(root)
			out = append(out, resolved)
		}
	}
	return out
}
