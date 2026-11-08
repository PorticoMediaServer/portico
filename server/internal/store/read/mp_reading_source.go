package read

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/readingcap"
	"github.com/PorticoMediaServer/portico/server/internal/readingidentity"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// MP reading source selection is a bounded, typed decode of the current native
// selection facts. It owns no file handle, archive parser, source lease or
// permission: the observation revision is evidence for a later opener, never
// authority to open anything.
const (
	mpReadingNativePolicy     readPolicyID = 700
	mpReadingConversionPolicy readPolicyID = 701
	mpReadingMaxSource        int64        = 2 << 30 // the existing reading.MaxSource bound
)

// mpReadingSourceState carries the exact asset to an otherwise item-keyed read
// recipe. It is a private compiled value, never a caller assertion or wire field.
type mpReadingSourceState struct{ asset int64 }

func (mpReadingSourceState) readPolicyState() {}

func init() {
	for _, recipe := range []struct {
		id         readPolicyID
		conversion bool
	}{{mpReadingNativePolicy, false}, {mpReadingConversionPolicy, true}} {
		conversion := recipe.conversion
		registerReadPolicy(readPolicyRecipe{id: recipe.id, domain: fmt.Sprint("mp-reading-source-conversion-", conversion), kinds: []int64{20, 21}, stateful: true, validate: func(q browseQuery, in BrowseInput, f browseFence) (string, error) {
			state, ok := in.policy.state.(mpReadingSourceState)
			if !ok || state.asset <= 0 {
				return "", ErrInput
			}
			source, err := mpReadingSourceSelect(q, in.Library, in.Kind, in.policy.subject, state.asset, f.library, conversion)
			if err != nil {
				return "", err
			}
			return mpReadingSourceWitness(source), nil
		}})
	}
}

// MPReadingSourceInput selects one exact publication asset of one visible item.
// Kind is the item kind (20 ebook, 21 comic) and must match the stored row.
type MPReadingSourceInput struct {
	View  BrowseInput
	Item  int64
	Asset int64
}

// MPReadingSource is the decoded native source selection. The inventory row id
// selected the row; it is deliberately absent from the observation revision.
type MPReadingSource struct {
	Item               int64
	PublicID           [16]byte
	ItemKind           int64
	Asset              int64
	Token              string
	Format             string
	PresentationFormat string
	Converted          bool
	Size               int64
	ModifiedNS         int64
	SourceID           string
	SourceGeneration   int64
	SourceIncarnation  string
	RootIdentity       string
	RelativePath       string
	ObjectIdentity     string
	InventoryRevision  string
	Evidence           string
	InventoryObject    string
}

// Revision is the existing ordered 14-field physical observation fence. It is
// the ORIGINAL source revision. A converted EPUB keeps the original token and
// this native revision; its rendered resume uses the conversion provenance
// revision produced by the converter, never this value.
func (s MPReadingSource) Revision() string {
	return readingidentity.Revision(s.Item, s.Asset, s.Format, s.Token, s.Size, s.ModifiedNS, s.SourceID, s.SourceGeneration, s.SourceIncarnation, s.RootIdentity, s.RelativePath, s.ObjectIdentity, s.InventoryRevision, s.Evidence)
}

// MPReadingSourceValues is the values-only tuple a caller already read. It is
// the writer seam: no SQL handle, file, grant or viewport crosses it.
type MPReadingSourceValues struct {
	Item, ItemKind, Asset, Library                      int64
	LibraryKind, LibraryName                            string
	ItemRetired, DVRRetired                             int64
	PublicID                                            []byte
	Format, Token                                       string
	Size, ModifiedNS                                    int64
	LinkAvailable, AssetAvailable, AssetLibrary         int64
	ObjectID, ObjectAssetID, ObjectSourceID             string
	ObjectRootIncarnation, RelativePath, ObjectIdentity string
	InventoryRevision, Evidence                         string
	ObjectSize, ObjectModifiedNS, ObjectRetired         int64
	ObjectState, UnsupportedReason                      string
	SourceID, SourceLibraryID, SourceKind               string
	SourceGeneration                                    int64
	SourceIncarnation, RootIdentity                     string
	SourceEnabled                                       int64
	SourceHealth                                        string
}

// DecodeMPReadingSource validates one current native tuple and returns the
// typed selection. conversion is trusted composition's configured MOBI/AZW3
// conversion, never a request option.
func DecodeMPReadingSource(v MPReadingSourceValues, conversion bool) (MPReadingSource, error) {
	if v.Item <= 0 || v.Asset <= 0 || v.Library <= 0 || v.ItemKind != 20 && v.ItemKind != 21 {
		return MPReadingSource{}, ErrInput
	}
	if len(v.PublicID) != 16 {
		return MPReadingSource{}, ErrUnqualified
	}
	if v.ItemRetired != 0 || v.DVRRetired != 0 || v.LinkAvailable != 1 || v.AssetAvailable != 1 {
		return MPReadingSource{}, ErrDenied
	}
	if v.AssetLibrary != v.Library {
		return MPReadingSource{}, ErrUnqualified
	}
	if v.ObjectAssetID != v.Token || v.ObjectSourceID != v.SourceID || v.ObjectRootIncarnation != v.SourceIncarnation {
		return MPReadingSource{}, ErrUnqualified
	}
	if v.ItemKind == 20 && v.LibraryKind != "book" || v.ItemKind == 21 && v.LibraryKind != "comic" {
		return MPReadingSource{}, ErrUnqualified
	}
	if !mpReadingIdentifier(v.Token, 512) || !mpReadingIdentifier(v.SourceID, 512) || !mpReadingIdentifier(v.ObjectID, 512) {
		return MPReadingSource{}, ErrUnqualified
	}
	if v.Size < 1 || v.Size > mpReadingMaxSource || v.ObjectSize != v.Size || v.ObjectModifiedNS != v.ModifiedNS {
		return MPReadingSource{}, ErrUnqualified
	}
	if v.SourceGeneration < 1 || !mpReadingIdentifier(v.SourceIncarnation, 256) || !mpReadingIdentifier(v.RootIdentity, 4096) {
		return MPReadingSource{}, ErrUnqualified
	}
	if !mpReadingIdentifier(v.SourceLibraryID, 512) || !mpReadingIdentifier(v.SourceKind, 64) {
		return MPReadingSource{}, ErrUnqualified
	}
	if v.SourceLibraryID != v.LibraryName || v.SourceEnabled != 1 || v.SourceHealth == "" || len(v.SourceHealth) > 64 || !utf8.ValidString(v.SourceHealth) {
		return MPReadingSource{}, ErrDenied
	}
	if v.SourceHealth == "offline" || v.SourceHealth == "root_changed" || v.SourceHealth == "removing" {
		return MPReadingSource{}, ErrDenied
	}
	if v.ObjectRetired != 0 || v.ObjectState != "available" || v.UnsupportedReason != "" {
		return MPReadingSource{}, ErrDenied
	}
	if !mpReadingRelativePath(v.RelativePath) || !mpReadingIdentifier(v.ObjectIdentity, 8192) {
		return MPReadingSource{}, ErrUnqualified
	}
	// The row id selected a row; it can never stand in for physical identity.
	if v.ObjectIdentity == v.ObjectID {
		return MPReadingSource{}, ErrUnqualified
	}
	if err := mpReadingEvidence(v); err != nil {
		return MPReadingSource{}, err
	}
	presentation, converted, err := mpReadingFormat(v.ItemKind, v.Format, conversion, v.SourceKind)
	if err != nil {
		return MPReadingSource{}, err
	}
	source := MPReadingSource{
		Item: v.Item, ItemKind: v.ItemKind, Asset: v.Asset,
		Token: v.Token, Format: v.Format, PresentationFormat: presentation, Converted: converted,
		Size: v.Size, ModifiedNS: v.ModifiedNS,
		SourceID: v.SourceID, SourceGeneration: v.SourceGeneration, SourceIncarnation: v.SourceIncarnation, RootIdentity: v.RootIdentity,
		RelativePath: v.RelativePath, ObjectIdentity: v.ObjectIdentity, InventoryRevision: v.InventoryRevision,
		Evidence: v.Evidence, InventoryObject: v.ObjectID,
	}
	copy(source.PublicID[:], v.PublicID)
	if source.Revision() == "" {
		return MPReadingSource{}, ErrUnqualified
	}
	return source, nil
}

func mpReadingEvidence(v MPReadingSourceValues) error {
	if len(v.Evidence) == 0 || len(v.Evidence) > 16384 || !utf8.ValidString(v.Evidence) || !json.Valid([]byte(v.Evidence)) {
		return ErrUnqualified
	}
	if len(v.InventoryRevision) != 64 {
		return ErrUnqualified
	}
	if _, err := hex.DecodeString(v.InventoryRevision); err != nil {
		return ErrUnqualified
	}
	var evidence mediasource.InventoryEvidence
	if err := json.Unmarshal([]byte(v.Evidence), &evidence); err != nil {
		return ErrUnqualified
	}
	revision, err := evidence.Revision()
	if err != nil || revision != v.InventoryRevision || evidence.Object != v.ObjectIdentity || evidence.Size != v.ObjectSize || evidence.ModifiedNS != v.ObjectModifiedNS {
		return ErrUnqualified
	}
	return nil
}

// mpReadingFormat applies the existing reader capability gates. Unsupported
// formats and unconfigured conversion are denials; an archive source must be
// the current local source, never a remote range.
func mpReadingFormat(kind int64, format string, conversion bool, sourceKind string) (string, bool, error) {
	switch kind {
	case 20:
		switch format {
		case "epub", "pdf":
			return format, false, nil
		case "mobi", "azw3":
			if !conversion {
				return "", false, ErrDenied
			}
			return "epub", true, nil
		}
	case 21:
		switch format {
		case "cbz", "pdf":
			return format, false, nil
		case "cb7":
			if !readingcap.CB7() || sourceKind != "local" {
				return "", false, ErrDenied
			}
			return "cb7", false, nil
		case "cbr":
			if !readingcap.CBR() || sourceKind != "local" {
				return "", false, ErrDenied
			}
			return "cbr", false, nil
		}
	}
	return "", false, ErrDenied
}

func mpReadingIdentifier(v string, maximum int) bool {
	if len(v) == 0 || len(v) > maximum || !utf8.ValidString(v) {
		return false
	}
	return strings.IndexFunc(v, func(r rune) bool { return r == 0 || r < 32 || r == 127 }) < 0
}

// mpReadingRelativePath refuses absolute, rooted, traversal and control forms on
// every host. Both separators are treated as separators, so a Windows-only
// spelling cannot slip past a Unix check or the reverse.
func mpReadingRelativePath(v string) bool {
	if len(v) == 0 || len(v) > 4096 || !utf8.ValidString(v) {
		return false
	}
	if strings.HasPrefix(v, "/") || strings.HasPrefix(v, "\\") {
		return false
	}
	if len(v) > 1 && v[1] == ':' && (v[0] >= 'a' && v[0] <= 'z' || v[0] >= 'A' && v[0] <= 'Z') {
		return false
	}
	if strings.Contains(v, "//") || strings.Contains(v, "\\\\") {
		return false
	}
	if strings.IndexFunc(v, func(r rune) bool { return r == 0 || r == 127 || r < 32 }) >= 0 {
		return false
	}
	for _, segment := range strings.FieldsFunc(v, func(r rune) bool { return r == '/' || r == '\\' }) {
		if segment == ".." {
			return false
		}
	}
	return path.Clean(v) != "." && path.Clean(v) != ".." && !strings.HasPrefix(path.Clean(v), "../")
}

func mpReadingSourceWitness(s MPReadingSource) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d|%d|%d|%t|%s", s.Revision(), s.InventoryObject, s.Item, s.Asset, s.ItemKind, s.Converted, s.PresentationFormat)))
	return hex.EncodeToString(sum[:])
}

// MPReadingSourceReader selects the current native source for one publication
// asset through the shared two-view publication protocol.
type MPReadingSourceReader struct {
	snapshots  browseSnapshots
	pool       *governor.Snapshots
	conversion bool
}

func NewMPReadingSourceReader(s *governor.Snapshots) (*MPReadingSourceReader, error) {
	if s == nil {
		return nil, ErrInput
	}
	return &MPReadingSourceReader{snapshots: s, pool: s}, nil
}

// Conversion is private trusted composition, never a request option. The phase4
// adapter must derive it from the existing configured converter.
func newMPReadingSourceReaderWithConversion(s *governor.Snapshots) (*MPReadingSourceReader, error) {
	r, err := NewMPReadingSourceReader(s)
	if err == nil {
		r.conversion = true
	}
	return r, err
}

func (r *MPReadingSourceReader) Select(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in MPReadingSourceInput) (MPReadingSource, BrowseReadResult, error) {
	if r == nil {
		return MPReadingSource{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (MPReadingSource, BrowseReadResult, error) {
		in.View.Now = at
		return r.selectSource(ctx, request, budget, output, in)
	})
}

func (r *MPReadingSourceReader) selectSource(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in MPReadingSourceInput) (MPReadingSource, BrowseReadResult, error) {
	if r == nil || in.Item <= 0 || in.Asset <= 0 || in.View.Kind != 20 && in.View.Kind != 21 || in.View.After != nil || in.View.Descending || in.View.KeepArticles {
		return MPReadingSource{}, BrowseReadResult{}, ErrInput
	}
	in.View.policy = readPolicyBinding{recipe: mpReadingNativePolicy, subject: in.Item, state: mpReadingSourceState{asset: in.Asset}}
	if r.conversion {
		in.View.policy.recipe = mpReadingConversionPolicy
	}
	// Nine fence points plus the closed source recipe stay under this bounded
	// call recipe; payload bytes are still charged by their own octet points.
	windows := &rsByteWindow{source: r.snapshots, preflightCalls: 32}
	return publishRead(ctx, windows, request, budget, output, in.View, []readStep[MPReadingSource]{func(ctx context.Context, q browseQuery, f browseFence, out *MPReadingSource) error {
		got, err := mpReadingSourceSelect(q, in.View.Library, in.View.Kind, in.Item, in.Asset, f.library, r.conversion)
		if err != nil {
			return err
		}
		*out = got
		return ctx.Err()
	}})
}

func mpReadingSourceSelect(q browseQuery, library, kind, item, asset int64, libraryName string, conversion bool) (MPReadingSource, error) {
	var v MPReadingSourceValues
	v.Item, v.ItemKind, v.Asset, v.Library = item, kind, asset, library
	v.LibraryName = libraryName
	rows, err := q("mp.reading.item", item, library, kind)
	if err != nil {
		return MPReadingSource{}, err
	}
	if len(rows) != 1 {
		return MPReadingSource{}, ErrDenied
	}
	v.PublicID = rows[0][0].([]byte)
	if rows[0][1].(int64) != kind {
		return MPReadingSource{}, ErrUnqualified
	}
	v.ItemRetired = rows[0][2].(int64)
	rows, err = q("mp.reading.library", libraryName)
	if err != nil {
		return MPReadingSource{}, err
	}
	if len(rows) != 1 {
		return MPReadingSource{}, ErrDenied
	}
	v.LibraryKind = rows[0][0].(string)
	rows, err = q("mp.reading.publication", item, asset, library)
	if err != nil {
		return MPReadingSource{}, err
	}
	if len(rows) != 1 {
		return MPReadingSource{}, ErrDenied
	}
	v.Format, v.LinkAvailable, v.Token = rows[0][0].(string), rows[0][1].(int64), rows[0][2].(string)
	v.Size, v.ModifiedNS = rows[0][3].(int64), rows[0][4].(int64)
	v.AssetAvailable, v.AssetLibrary = rows[0][5].(int64), rows[0][6].(int64)
	rows, err = q("mp.reading.retired", item)
	if err != nil {
		return MPReadingSource{}, err
	}
	if len(rows) != 0 {
		return MPReadingSource{}, ErrDenied
	}
	archive := v.Format == "cb7" || v.Format == "cbr"
	if archive {
		rows, err = q("mp.reading.local", v.Token, libraryName)
		if err != nil {
			return MPReadingSource{}, err
		}
		if len(rows) != 1 {
			return MPReadingSource{}, ErrDenied
		}
	}
	objectStatement := "mp.reading.object"
	if archive {
		objectStatement = "mp.reading.local_object"
	}
	rows, err = q(objectStatement, v.Token, libraryName, v.Size, v.ModifiedNS, int64(1))
	if err != nil {
		return MPReadingSource{}, err
	}
	if len(rows) != 1 {
		return MPReadingSource{}, ErrDenied
	}
	o := rows[0]
	v.ObjectID, v.ObjectAssetID, v.ObjectSourceID = o[0].(string), o[1].(string), o[2].(string)
	v.ObjectRootIncarnation, v.RelativePath, v.ObjectIdentity = o[3].(string), o[4].(string), o[5].(string)
	v.InventoryRevision, v.Evidence = o[6].(string), o[7].(string)
	v.ObjectSize, v.ObjectModifiedNS, v.ObjectRetired = o[8].(int64), o[9].(int64), o[10].(int64)
	v.ObjectState, v.UnsupportedReason = o[11].(string), o[12].(string)
	v.SourceID, v.SourceLibraryID, v.SourceKind = o[13].(string), o[14].(string), o[15].(string)
	v.SourceGeneration, v.SourceIncarnation, v.RootIdentity = o[16].(int64), o[17].(string), o[18].(string)
	v.SourceEnabled, v.SourceHealth = o[19].(int64), o[20].(string)
	return DecodeMPReadingSource(v, conversion)
}
