package writer

import (
	"bytes"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog/identityvalue"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
)

// InventoryOrdinaryFilename is the complete ordinary filename projection.
// SourceKey is a logical catalogue identity in the separately supplied library
// namespace; it is never an inode, source ID, root hash or observation revision.
type InventoryOrdinaryFilename struct {
	SourceKey  string
	Year, Part int
	Metadata   InventoryFilenameMetadata
}

// InventoryOrdinaryFilenameInput contains actual retained/native values. The
// identity root is the catalogue's retained locator root, which can differ from
// the physical source root. The caller must not substitute one for the other.
type InventoryOrdinaryFilenameInput struct {
	IdentityRoot, AssetPath, RelativePath []byte
	ExplicitSortTitle, MetadataLanguage   string
}

// ProjectOrdinaryFilename accepts the actual adapter's root-relative path bytes.
// It performs no IO, source lookup or admission. The trusted driver separately
// proves native containment, regular-file identity, source scope and provenance
// of an explicit sort override (FIRST filename-only ingress supplies empty).
// Producers pass this complete value unchanged into InventoryFilenameInput.
func ProjectOrdinaryFilename(input InventoryOrdinaryFilenameInput) (InventoryOrdinaryFilename, error) {
	relativePath, explicitSortTitle, metadataLanguage := input.RelativePath, input.ExplicitSortTitle, input.MetadataLanguage
	remaining := maxCanonicalRequestBytes
	for _, n := range [...]int{len(input.IdentityRoot), len(input.AssetPath), len(relativePath), len(explicitSortTitle), len(metadataLanguage)} {
		if n > remaining {
			return InventoryOrdinaryFilename{}, inventoryRequestCapacity()
		}
		remaining -= n
	}
	// Preserve the adopted CHANGE-003 distinction, including mixed bad input:
	// NUL is invalid native input, never a filename_unrepresentable poison skip.
	for _, raw := range [3][]byte{input.IdentityRoot, input.AssetPath, relativePath} {
		if bytes.IndexByte(raw, 0) >= 0 {
			return InventoryOrdinaryFilename{}, invalid("inventory_native_path")
		}
	}
	if input.IdentityRoot == nil || len(input.AssetPath) == 0 || !utf8.Valid(input.IdentityRoot) {
		return InventoryOrdinaryFilename{}, invalid("inventory_identity_root")
	}
	if !utf8.Valid(relativePath) || !utf8.Valid(input.AssetPath) {
		return InventoryOrdinaryFilename{}, &PolicyError{Field: "inventory_native_path", Cause: ErrFilenameUnrepresentable}
	}
	if len(relativePath) > naming.MaxPathBytes {
		return InventoryOrdinaryFilename{}, &PolicyError{Field: "inventory_native_path", Limit: naming.MaxPathBytes, Cause: ErrCap}
	}
	if !utf8.ValidString(explicitSortTitle) || !utf8.ValidString(metadataLanguage) {
		return InventoryOrdinaryFilename{}, invalid("inventory_metadata")
	}
	// Conversion happens only after complete byte admission and native policy.
	relative := string(relativePath)
	if !inventoryPath(relative, false) {
		return InventoryOrdinaryFilename{}, invalid("inventory_native_path")
	}
	reading := naming.Read(relative, "movie")
	title, year := reading.PreferredTitle()
	part := 0
	if len(reading.Parts) > 0 {
		part = reading.Parts[0].Value
	}
	// Match AttachMovieTx BEFORE display fallback. An uninterpretable title's
	// locator is the complete existing ItemKey call, including a split part.
	key := identityvalue.MovieKey(title, year)
	if identityvalue.IdentityText(title) == "" {
		key = identityvalue.ItemKey(string(input.IdentityRoot), string(input.AssetPath), part)
	}
	if title == "" {
		base := path.Base(relative)
		title = strings.TrimSuffix(base, path.Ext(base))
		if title == "" {
			title = base
		}
	}
	if strings.TrimSpace(title) == "" {
		return InventoryOrdinaryFilename{}, &PolicyError{Field: "inventory_title", Cause: ErrFilenameUnrepresentable}
	}
	metadata, err := ProjectFilenameMetadata(title, explicitSortTitle, metadataLanguage)
	if err != nil {
		return InventoryOrdinaryFilename{}, err
	}
	return InventoryOrdinaryFilename{SourceKey: key, Year: year, Part: part, Metadata: metadata}, nil
}
