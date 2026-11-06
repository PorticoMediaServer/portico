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
	SourceKey string
	Year      int
	Metadata  InventoryFilenameMetadata
}

// ProjectOrdinaryFilename accepts the actual adapter's root-relative path bytes.
// It performs no IO, source lookup or admission. The trusted driver separately
// proves native containment, regular-file identity, source scope and provenance
// of an explicit sort override (FIRST filename-only ingress supplies empty).
// Producers pass this complete value unchanged into InventoryFilenameInput.
func ProjectOrdinaryFilename(relativePath []byte, explicitSortTitle, metadataLanguage string) (InventoryOrdinaryFilename, error) {
	remaining := maxCanonicalRequestBytes
	for _, n := range [...]int{len(relativePath), len(explicitSortTitle), len(metadataLanguage)} {
		if n > remaining {
			return InventoryOrdinaryFilename{}, inventoryRequestCapacity()
		}
		remaining -= n
	}
	// Preserve the adopted CHANGE-003 distinction, including mixed bad input:
	// NUL is invalid native input, never a filename_unrepresentable poison skip.
	if bytes.IndexByte(relativePath, 0) >= 0 {
		return InventoryOrdinaryFilename{}, invalid("inventory_native_path")
	}
	if !utf8.Valid(relativePath) {
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
	title, year := naming.Read(relative, "movie").PreferredTitle()
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
	key := identityvalue.MovieKey(title, year)
	if identityvalue.IdentityText(title) == "" {
		// The existing locator recipe takes an already root-relative path here.
		// It has no physical-root component and is scoped by the caller's library.
		key = identityvalue.ItemKey("", relative, 0)
	}
	return InventoryOrdinaryFilename{SourceKey: key, Year: year, Metadata: metadata}, nil
}
