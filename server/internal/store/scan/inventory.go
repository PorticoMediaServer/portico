// Package scan prepares bounded local observations for the schema-v1 typed
// writer. It is not connected to the shipped scanner. Preparation neither
// accepts catalogue facts nor grants filesystem, playback, or removal authority.
package scan

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/naming"
)

var (
	ErrCapacity = errors.New("scan preparation exceeds budget")
	ErrInvalid  = errors.New("invalid scan observation")
)

// SourceWitness preserves the native opaque source fence. LibraryID is the
// retained catalogue namespace, not a root path or the mutable library name.
type SourceWitness struct {
	LibraryID                       int64
	SourceID, Incarnation, Revision string
}

func (s SourceWitness) valid() bool {
	return s.LibraryID > 0 && s.SourceID != "" && s.Incarnation != "" && s.Revision != ""
}

// FileWitness binds one observation to its library/source and exact object
// revision. ObjectIdentity may be absent on an adapter without stable file IDs;
// it is never substituted for Revision. ModifiedNS may predate the Unix epoch.
type FileWitness struct {
	Source                       SourceWitness
	RelativePath, ObjectIdentity string
	Revision                     string
	Size, ModifiedNS             int64
}

func (f FileWitness) valid() bool {
	return f.Source.valid() && validPath(f.RelativePath) && f.Revision != "" && f.Size >= 0
}

func validPath(p string) bool {
	return p != "." && fs.ValidPath(p) && !strings.ContainsAny(p, "\\\x00") &&
		len(p) <= naming.MaxPathBytes && strings.Count(p, "/") < naming.MaxDepth
}

// FileObservation is supplied by the sandboxed inventory adapter. SourceKey is
// its existing identity key; preparation must not invent, hash, or normalize it.
// Hardlink/path deduplication is source policy, never a global identity map.
type FileObservation struct {
	Witness   FileWitness
	SourceKey string
}

// FilenameCandidate contains only local naming evidence. Technical facts are
// intentionally absent until Basic acceptance. The writer resolves SourceKey
// within LibraryID to the retained subject, including on reimport.
type FilenameCandidate struct {
	FileObservation
	Title string
	Year  int
}

// InventoryBudget bounds this prepared batch, not the entire library. Bytes
// counts string payloads (including generated titles), not heap/RSS; the driver
// must separately reserve working memory before calling PrepareInventory.
type InventoryBudget struct {
	Files int
	Bytes int64
}

// Inventory is an immutable values-only batch. There is no directory coverage,
// absence, cursor advancement, Applied receipt, or availability claim here.
type Inventory struct {
	files []FilenameCandidate
	bytes int64
}

func (b Inventory) Len() int { return len(b.files) }

func (b Inventory) Bytes() int64 { return b.bytes }

// File returns a value copy; callers cannot mutate another queued command's
// input through the original observation slice or through this accessor.
func (b Inventory) File(i int) (FilenameCandidate, bool) {
	if i < 0 || i >= len(b.files) {
		return FilenameCandidate{}, false
	}
	return b.files[i], true
}

// PrepareInventory validates the complete raw batch before copying or naming.
// Invalid or over-budget input returns no partial batch. It performs no IO,
// provider lookup, technical probe, or database write. kind uses naming.Read's
// existing movie/tv/anime/music/audiobook context.
func PrepareInventory(source SourceWitness, kind string, files []FileObservation, budget InventoryBudget) (Inventory, error) {
	if !source.valid() || !validKind(kind) {
		return Inventory{}, fmt.Errorf("%w: source or naming kind", ErrInvalid)
	}
	if budget.Files <= 0 || budget.Bytes <= 0 || len(files) > budget.Files {
		return Inventory{}, ErrCapacity
	}
	if len(files) == 0 {
		return Inventory{}, nil
	}
	remaining := budget.Bytes
	charge := func(s string) bool {
		if int64(len(s)) > remaining {
			return false
		}
		remaining -= int64(len(s))
		return true
	}
	// Bound raw payloads before allocating duplicate-check maps or output. Charge
	// the repeated source witness too, since each output value contains it.
	for _, f := range files {
		w := f.Witness
		if !w.valid() || w.Source != source || f.SourceKey == "" {
			return Inventory{}, fmt.Errorf("%w: file witness", ErrInvalid)
		}
		for _, s := range []string{source.SourceID, source.Incarnation, source.Revision, w.RelativePath, w.ObjectIdentity, w.Revision, f.SourceKey} {
			if !charge(s) {
				return Inventory{}, ErrCapacity
			}
		}
	}
	paths := make(map[string]struct{}, len(files))
	keys := make(map[string]struct{}, len(files))
	for _, f := range files {
		if _, ok := paths[f.Witness.RelativePath]; ok {
			return Inventory{}, fmt.Errorf("%w: duplicate path", ErrInvalid)
		}
		if _, ok := keys[f.SourceKey]; ok {
			return Inventory{}, fmt.Errorf("%w: duplicate source key", ErrInvalid)
		}
		paths[f.Witness.RelativePath] = struct{}{}
		keys[f.SourceKey] = struct{}{}
	}
	prepared := make([]FilenameCandidate, 0, len(files))
	// Do not pin arbitrarily large adapter buffers through string substrings.
	sealedSource := SourceWitness{LibraryID: source.LibraryID, SourceID: strings.Clone(source.SourceID),
		Incarnation: strings.Clone(source.Incarnation), Revision: strings.Clone(source.Revision)}
	for _, f := range files {
		title, year := naming.Read(f.Witness.RelativePath, kind).PreferredTitle()
		if title == "" {
			base := path.Base(f.Witness.RelativePath)
			title = strings.TrimSuffix(base, path.Ext(base))
			if title == "" {
				title = base
			}
		}
		if !charge(title) {
			return Inventory{}, ErrCapacity
		}
		w := f.Witness
		sealed := FileObservation{SourceKey: strings.Clone(f.SourceKey), Witness: FileWitness{
			Source: sealedSource, RelativePath: strings.Clone(w.RelativePath), ObjectIdentity: strings.Clone(w.ObjectIdentity),
			Revision: strings.Clone(w.Revision), Size: w.Size, ModifiedNS: w.ModifiedNS,
		}}
		prepared = append(prepared, FilenameCandidate{FileObservation: sealed, Title: strings.Clone(title), Year: year})
	}
	return Inventory{files: prepared, bytes: budget.Bytes - remaining}, nil
}

func validKind(kind string) bool {
	switch kind {
	case "movie", "tv", "anime", "music", "audiobook", "book", "comic", "magazine", "newspaper":
		return true
	default:
		return false
	}
}
