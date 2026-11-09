package writer

import (
	"bytes"
	"path"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/readingcap"
)

const iaReadingRequestVersion uint16 = 1

// IA-15 narrows the reading draft to the static accepting variant. The request
// carries only prepared observations: the item identity source key and title are
// caller naming/scan evidence, never derived, normalized or allocated here. No
// current authority, asset row, entity ID or search generation is captured. No
// decoded text, page count, cover or readable-representation promise is carried:
// a source extension admits inventory only.
type iaReadingInventoryRequest struct {
	scope     inventoryScope
	source    inventorySource
	run       inventoryRun
	directory inventoryDirectory
	files     []inventoryFile // exactly one original reading media observation
	complete  bool            // complete cohort assertion, not source proof

	itemKey, itemTitle string
	itemKind           int64  // 20 ebook, 21 comic; matches the library kind
	format             string // closed reading source format, exactly the extension
	publicationType    string // "", "book", "magazine", "newspaper" (descriptive only)
	pathHead           bool   // first accepted publication of this item
	pathSequence       int64  // publisher sequence, only on the path head
}

type iaReadingInventoryCommand struct {
	identity CommandIdentity
	request  iaReadingInventoryRequest
	digest   RequestDigest
	valid    bool
}

func newIAReadingInventoryCommand(id CommandIdentity, r iaReadingInventoryRequest) (*iaReadingInventoryCommand, error) {
	// Counts precede all text traversal, copying and hashing.
	if len(r.files) != 1 {
		return nil, invalid("ia_reading_count")
	}
	size := uint64(len(canonicalRequestCodec) + 4 + 16 + 8 + 16)
	iaEpisodeWireSize(reflect.ValueOf(r), &size)
	if size > maxCanonicalRequestBytes {
		return nil, &PolicyError{Field: "ia_reading_bytes", Limit: maxCanonicalRequestBytes, Cause: ErrCap}
	}
	if !iaEpisodeValidText(reflect.ValueOf(r)) || !utf8.Valid(r.source.resolvedRoot) || bytes.IndexByte(r.source.resolvedRoot, 0) >= 0 {
		return nil, invalid("ia_reading_text")
	}
	if err := validateIAReadingOriginal(id, r); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(IAAcceptReadingInventory, iaReadingRequestVersion, id, r)
	if err != nil {
		return nil, err
	}
	r.files = slices.Clone(r.files)
	r.source.resolvedRoot = bytes.Clone(r.source.resolvedRoot)
	for _, s := range iaReadingTexts(&r) {
		*s = strings.Clone(*s)
	}
	return &iaReadingInventoryCommand{identity: id, request: r, digest: digest, valid: true}, nil
}

// Every accepting gate is a closed witness comparison. The item key is the
// caller's prepared evidence, so a reused reading item is bound by the same exact
// key rather than by a title or a movie heuristic. The format is admitted only
// through the existing closed reading capability rules, and a payload that cannot
// be read at all (no bytes) is refused before any effect.
func validateIAReadingOriginal(id CommandIdentity, r iaReadingInventoryRequest) error {
	s, u, d := r.source, r.run, r.directory
	if id.Sequence <= 0 || id.OperationID == ([16]byte{}) || id.Incarnation == ([16]byte{}) ||
		r.scope.runID == "" || r.scope.libraryKey == "" || r.scope.libraryID <= 0 ||
		s.id == "" || s.incarnation == "" || s.rootIdentity == "" || s.generation <= 0 || len(s.resolvedRoot) == 0 || s.resolvedRoot[0] != '/' ||
		u.revision <= 0 || u.policyRevision <= 0 || u.leaseOwner <= 0 || u.leaseGeneration <= 0 || u.leaseExpiresMS <= 0 || u.lastSequence != id.Sequence-1 || u.acknowledgedSequence != u.lastSequence || u.phase != "inventory" || !inventoryScanMode(u.scanMode) ||
		d.identity == "" || d.revision == "" || d.cursorKind != inventoryLinuxCursor || !inventoryPath(d.path, true) || d.pages < 0 || d.skipped < 0 || d.rawNames != 1 || d.unstable {
		return invalid("ia_reading_original")
	}
	root := string(s.resolvedRoot)
	f := r.files[0]
	if !r.complete || strings.ContainsAny(root, `\`) || path.Clean(root) != root || f.sourceKey == "" || f.objectIdentity == "" || f.revision == "" ||
		f.directory || f.size <= 0 || f.part < 0 || f.year < 0 || f.metadataLanguage == "" || !inventoryPath(f.relativePath, false) ||
		path.Dir(f.relativePath) != d.path || strings.ContainsAny(f.assetPath, `\`) || path.Clean(f.assetPath) != f.assetPath || f.assetPath != path.Join(root, f.relativePath) {
		return invalid("ia_reading_file")
	}
	if !iaReadingFormatAdmits(r.itemKind, r.format) {
		return invalid("ia_reading_format")
	}
	// The accepted format is the observed extension. A name that disagrees with
	// the admitted format is an invented publication, never a rename.
	extension := strings.TrimPrefix(strings.ToLower(path.Ext(f.relativePath)), ".")
	if extension != r.format {
		return invalid("ia_reading_format")
	}
	if !iaReadingPublicationType(r.publicationType) {
		return invalid("ia_reading_publication_type")
	}
	if !iaEpisodeKey(r.itemKey) || strings.TrimSpace(r.itemTitle) == "" {
		return invalid("ia_reading_metadata")
	}
	// A path head is the first accepted publication with its exact publisher
	// sequence; an alternative publication never claims the head slot.
	if r.pathHead != (r.pathSequence > 0) {
		return invalid("ia_reading_path_head")
	}
	if err := ValidateMetadataShape(r.itemTitle, "", 0, 0, 0); err != nil {
		return err
	}
	return nil
}

// iaReadingFormatAdmits is catalog.readingFormat's closed extension/build rule.
// An extension admits inventory only; the rooted reading runtime still validates
// the actual package/PDF/image bytes before exposing a readable representation.
func iaReadingFormatAdmits(kind int64, format string) bool {
	switch kind {
	case 20:
		return format == "epub" || format == "pdf" || format == "mobi" || format == "azw3"
	case 21:
		switch format {
		case "cbz", "pdf":
			return true
		case "cb7":
			return readingcap.CB7()
		case "cbr":
			return readingcap.CBR()
		}
	}
	return false
}

func iaReadingPublicationType(value string) bool {
	return value == "" || value == "book" || value == "magazine" || value == "newspaper"
}

func (c *iaReadingInventoryCommand) checkOriginal() error {
	if c == nil || !c.valid {
		return invalid("ia_reading_command")
	}
	checked, err := newIAReadingInventoryCommand(c.identity, c.request)
	if err != nil || checked.digest != c.digest {
		return ErrConflict
	}
	return nil
}

func iaReadingTexts(r *iaReadingInventoryRequest) []*string {
	base := inventoryAcceptRequest{scope: r.scope, source: r.source, run: r.run, directory: r.directory, files: r.files}
	texts := []*string{&r.scope.runID, &r.scope.libraryKey, &r.source.id, &r.source.incarnation, &r.source.rootIdentity, &r.run.phase, &r.run.scanMode, &r.directory.path, &r.directory.identity, &r.directory.revision, &r.directory.expectedCursor, &r.directory.nextCursor, &r.directory.cursorKind}
	texts = append(texts, inventoryRequestTexts(&base)[14:]...)
	texts = append(texts, &r.itemKey, &r.itemTitle, &r.format, &r.publicationType)
	return texts
}
