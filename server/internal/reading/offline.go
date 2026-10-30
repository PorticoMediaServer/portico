package reading

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"regexp"
	"unicode/utf8"
)

// OfflineDocument is a bounded private descriptor for exactly one observed
// publication. Resource digests describe the original archive members, never URLs.
// It contains no annotation capability, credentials, filesystem paths or session.
type OfflineResource struct {
	Offset         int64  `json:"offset"`
	CompressedSize int64  `json:"compressedSize"`
	Compression    uint16 `json:"compression"`
	Path           string `json:"path"`
	MIME           string `json:"mime"`
	Size           int64  `json:"size"`
	SHA256         string `json:"sha256"`
}
type OfflineMetadata struct {
	Title     string `json:"title"`
	Author    string `json:"author,omitempty"`
	Language  string `json:"language,omitempty"`
	Direction string `json:"direction,omitempty"`
}
type OfflineContents struct {
	Title    string `json:"title"`
	Section  string `json:"section"`
	Fragment string `json:"fragment,omitempty"`
	Depth    int    `json:"depth"`
}
type OfflineLocation struct {
	Format      string  `json:"format"`
	CFI         string  `json:"cfi,omitempty"`
	Section     string  `json:"section,omitempty"`
	PageIndex   *int64  `json:"pageIndex,omitempty"`
	Progression float64 `json:"progression"`
}
type OfflineProgress struct {
	SourceID       string          `json:"sourceId"`
	SourceRevision string          `json:"sourceRevision"`
	Location       OfflineLocation `json:"location"`
	Revision       int64           `json:"revision"`
	ObservedAt     int64           `json:"observedAt"`
	Stale          bool            `json:"stale"`
}
type OfflineDocument struct {
	Progress  *OfflineProgress  `json:"progress"`
	ItemID    string            `json:"itemId"`
	SourceID  string            `json:"sourceId"`
	Revision  string            `json:"revision"`
	Format    string            `json:"format"`
	Size      int64             `json:"size"`
	Metadata  OfflineMetadata   `json:"metadata"`
	Resources []OfflineResource `json:"resources"`
	Spine     []string          `json:"spine"`
	PageNames []string          `json:"pageNames"`
	Contents  []OfflineContents `json:"contents"`
}

func (d OfflineDocument) Digest() string {
	raw, _ := json.Marshal(d)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// SelectOfflineTx joins download admission to the same current inventory and
// Reading authority as online open, inside the admitting transaction.
func (s *Service) SelectOfflineTx(ctx context.Context, tx *sql.Tx, p identity.Principal, item, source string) (Selection, error) {
	selected, err := s.selectTx(ctx, tx, p, item, source)
	if err != nil {
		return selected, err
	}
	// The existing original-download opener is the isolated local/mounted storage
	// transport. Direct remote Reading leases and online MOBI conversions are
	// not that transfer authority; only the descriptor formats are offered.
	// Managed offline CB7/CBR stays unavailable: classic archives have no
	// ZIP member offsets, and a real bounded offline representation and client
	// consumer do not exist yet. Raw classic-archive bytes never masquerade as ZIP resources.
	if (selected.Format != "epub" && selected.Format != "pdf" && selected.Format != "cbz") || s.Storage.IsRemote(selected.path) {
		return selected, ErrUnavailable
	}
	return selected, nil
}

// OfflineDocument validates every resource using the existing reader validators.
// No accepted offline EPUB can later bypass XHTML/CSS checks by reading raw ZIP
// members. Work is cancellable and bounded by existing per-member/source limits
// plus a cumulative expansion ceiling; only one member is materialized at a time.
func (s *Service) OfflineDocument(ctx context.Context, p identity.Principal, item, source string) (OfflineDocument, error) {
	selected, err := s.Resolve(ctx, p, item, source)
	if err != nil {
		return OfflineDocument{}, err
	}
	if (selected.Format != "epub" && selected.Format != "pdf" && selected.Format != "cbz") || s.Storage.IsRemote(selected.path) {
		return OfflineDocument{}, ErrUnavailable
	}
	out := OfflineDocument{Resources: []OfflineResource{}, Spine: []string{}, PageNames: []string{}, Contents: []OfflineContents{}}
	f, err := s.open(ctx, p, item, selected.SourceID, selected.Revision, true)
	if err != nil {
		return out, err
	}
	defer f.Close()
	doc, err := Inspect(f)
	if err != nil {
		return out, err
	}
	out.ItemID = f.ItemID
	out.SourceID = f.SourceID
	out.Revision = f.Revision
	out.Format = f.Format
	out.Size = f.Size
	saved, err := f.Progress()
	if err != nil {
		return out, err
	}
	if saved != nil {
		l := saved.Location
		out.Progress = &OfflineProgress{saved.SourceID, saved.SourceRevision, OfflineLocation{l.Format, l.CFI, l.Section, l.PageIndex, l.Progression}, saved.Revision, saved.ObservedAt, saved.Stale}
	}
	out.Metadata = OfflineMetadata{Title: doc.Metadata.Title, Author: doc.Metadata.Author, Language: doc.Metadata.Language, Direction: doc.Metadata.Direction}
	out.Spine = append(out.Spine, doc.Spine...)
	out.PageNames = append(out.PageNames, doc.PageNames...)
	for _, entry := range doc.Contents {
		out.Contents = append(out.Contents, OfflineContents{Title: entry.Title, Section: entry.Section, Fragment: entry.Fragment, Depth: entry.Depth})
	}
	var total int64
	add := func(archive *archive, path, mime string, data []byte) error {
		entry := archive.files[path]
		if entry == nil || entry.zipped == nil {
			return ErrUnavailable
		}
		zipped := entry.zipped
		if zipped.Method != zip.Store && zipped.Method != zip.Deflate {
			return ErrUnavailable
		}
		offset, err := zipped.DataOffset()
		if err != nil {
			return err
		}
		if offset < 0 || zipped.CompressedSize64 > uint64(f.Size) || uint64(offset) > uint64(f.Size)-zipped.CompressedSize64 {
			return ErrInvalid
		}
		total += int64(len(data))
		if total > 512<<20 {
			return ErrLimit
		}
		sum := sha256.Sum256(data)
		out.Resources = append(out.Resources, OfflineResource{Offset: offset, CompressedSize: int64(zipped.CompressedSize64), Compression: zipped.Method, Path: path, MIME: mime, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])})
		return ctx.Err()
	}
	switch f.Format {
	case "epub":
		book, e := f.EPUB()
		if e != nil {
			return out, e
		}
		for _, r := range doc.Resources {
			data, mime, e := book.Asset(f.Context(), r.Path)
			if e != nil {
				return out, e
			}
			if e = add(book.archive, r.Path, mime, data); e != nil {
				return out, e
			}
		}
	case "cbz":
		book, e := f.Comic()
		if e != nil {
			return out, e
		}
		for index := range doc.PageNames {
			page, e := book.Page(f.Context(), index, 0)
			if e != nil {
				return out, e
			}
			if e = add(book.archive, page.Name, page.Image.MIME, page.Bytes); e != nil {
				return out, e
			}
		}
	case "pdf":
		if err = f.CheckPDF(); err != nil {
			return out, err
		}
	default:
		return out, ErrInvalid
	}
	if err = f.Validate(); err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	return out, nil
}

const OfflineDocumentLimit = 2 << 20
const offlineSafeInteger int64 = 9007199254740991

var offlineID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var offlineDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)
var offlineMIME = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.+-]*/[a-zA-Z0-9][a-zA-Z0-9.+-]*$`)

func offlineKeys(raw json.RawMessage, keys ...string) bool {
	var fields map[string]json.RawMessage
	if len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, &fields) != nil {
		return false
	}
	allowed := map[string]bool{}
	for _, key := range keys {
		allowed[key] = true
	}
	for key := range fields {
		if !allowed[key] {
			return false
		}
	}
	return true
}

// Presence is independent of zero/false. Optional scalar omission is allowed;
// a supplied scalar null never supplies evidence. Only top-level progress is nullable.
func offlinePresent(raw json.RawMessage, nullable string, required ...string) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return false
	}
	for _, key := range required {
		if _, ok := fields[key]; !ok {
			return false
		}
	}
	for key, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) && key != nullable {
			return false
		}
	}
	return true
}
func ParseOfflineLocation(raw []byte) (OfflineLocation, error) {
	var out OfflineLocation
	if !offlinePresent(raw, "", "format", "progression") {
		return out, ErrInvalid
	}
	if _, err := apikit.CanonicalJSON(raw); err != nil {
		return out, ErrInvalid
	}
	if json.Unmarshal(raw, &out) != nil {
		return out, ErrInvalid
	}
	switch out.Format {
	case "epub":
		if !offlineKeys(raw, "format", "cfi", "section", "progression") || !offlinePresent(raw, "", "format", "cfi", "progression") {
			return out, ErrInvalid
		}
	case "pdf", "cbz":
		if !offlineKeys(raw, "format", "pageIndex", "progression") || !offlinePresent(raw, "", "format", "pageIndex", "progression") {
			return out, ErrInvalid
		}
	default:
		return out, ErrInvalid
	}
	if err := (Location{Format: out.Format, CFI: out.CFI, Section: out.Section, PageIndex: out.PageIndex, Progression: out.Progression}).Validate(); err != nil {
		return out, err
	}
	return out, nil
}

// ParseOfflineDocument validates literal stored bytes without rewriting them.
// It is used before signing, renewing or delivering any persisted descriptor.
func ParseOfflineDocument(raw string) (OfflineDocument, error) {
	var out OfflineDocument
	if len(raw) == 0 || len(raw) > OfflineDocumentLimit || !utf8.ValidString(raw) {
		return out, ErrInvalid
	}
	if _, err := apikit.CanonicalJSON([]byte(raw)); err != nil {
		return out, ErrInvalid
	} // duplicate/trailing rejection only; keep ORIGINAL raw
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil {
		return out, ErrInvalid
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return out, ErrInvalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &fields) != nil || !offlineKeys([]byte(raw), "itemId", "sourceId", "revision", "format", "size", "metadata", "resources", "spine", "pageNames", "contents", "progress") {
		return out, ErrInvalid
	}
	if !offlinePresent([]byte(raw), "progress", "itemId", "sourceId", "revision", "format", "size", "metadata", "resources", "spine", "pageNames", "contents", "progress") {
		return out, ErrInvalid
	}
	if !offlineID.MatchString(out.ItemID) || !offlineID.MatchString(out.SourceID) || !offlineDigest.MatchString(out.Revision) || out.Size < 1 || out.Size > MaxSource {
		return out, ErrInvalid
	}
	for _, key := range []string{"resources", "spine", "pageNames", "contents"} {
		if len(fields[key]) == 0 || fields[key][0] != '[' {
			return out, ErrInvalid
		}
	}
	if !offlinePresent(fields["metadata"], "", "title") || !offlineKeys(fields["metadata"], "title", "author", "language", "direction") || len(out.Metadata.Title) > 4096 || len(out.Metadata.Author) > 4096 || len(out.Metadata.Language) > 128 {
		return out, ErrInvalid
	}
	if out.Metadata.Direction != "" && out.Metadata.Direction != "ltr" && out.Metadata.Direction != "rtl" && out.Metadata.Direction != "vertical" {
		return out, ErrInvalid
	}
	if len(out.Resources) > MaxEntries || len(out.Spine) > MaxEntries || len(out.PageNames) > MaxEntries || len(out.Contents) > 512 {
		return out, ErrLimit
	}
	var resourceFields []json.RawMessage
	json.Unmarshal(fields["resources"], &resourceFields)
	known := map[string]bool{}
	var total int64
	for index, r := range out.Resources {
		if !offlinePresent(resourceFields[index], "", "offset", "compressedSize", "compression", "path", "mime", "size", "sha256") || !offlineKeys(resourceFields[index], "offset", "compressedSize", "compression", "path", "mime", "size", "sha256") || !safePath(r.Path) || len(r.Path) > 4096 || known[r.Path] || !offlineMIME.MatchString(r.MIME) || len(r.MIME) > 128 || !offlineDigest.MatchString(r.SHA256) {
			return out, ErrInvalid
		}
		if r.Offset < 0 || r.CompressedSize < 0 || r.CompressedSize > out.Size || r.Offset > out.Size-r.CompressedSize || r.Size < 0 || r.Size > MaxMember || (r.Compression != 0 && r.Compression != 8) || (r.Compression == 0 && r.CompressedSize != r.Size) {
			return out, ErrInvalid
		}
		total += r.Size
		if total > 512<<20 {
			return out, ErrLimit
		}
		known[r.Path] = true
	}
	seen := map[string]bool{}
	for _, path := range out.Spine {
		if !known[path] || seen[path] {
			return out, ErrInvalid
		}
		seen[path] = true
	}
	seen = map[string]bool{}
	for _, path := range out.PageNames {
		if !known[path] || seen[path] {
			return out, ErrInvalid
		}
		seen[path] = true
	}
	switch out.Format {
	case "epub":
		if len(out.Resources) == 0 || len(out.Spine) == 0 || len(out.PageNames) != 0 {
			return out, ErrInvalid
		}
	case "cbz":
		if len(out.PageNames) == 0 || len(out.PageNames) != len(out.Resources) || len(out.Spine) != 0 || len(out.Contents) != 0 {
			return out, ErrInvalid
		}
	case "pdf":
		if len(out.Resources) != 0 || len(out.Spine) != 0 || len(out.PageNames) != 0 || len(out.Contents) != 0 {
			return out, ErrInvalid
		}
	default:
		return out, ErrInvalid
	}
	var contentsFields []json.RawMessage
	json.Unmarshal(fields["contents"], &contentsFields)
	for index, c := range out.Contents {
		if !offlinePresent(contentsFields[index], "", "title", "section", "depth") || !offlineKeys(contentsFields[index], "title", "section", "fragment", "depth") || len(c.Title) < 1 || len(c.Title) > 4096 || !known[c.Section] || len(c.Fragment) > 1024 || c.Depth < 0 || c.Depth > 16 {
			return out, ErrInvalid
		}
	}
	progressRaw := fields["progress"]
	if len(progressRaw) == 0 {
		return out, ErrInvalid
	}
	if out.Progress != nil {
		p := out.Progress
		if !offlinePresent(progressRaw, "", "sourceId", "sourceRevision", "location", "revision", "observedAt", "stale") || !offlineKeys(progressRaw, "sourceId", "sourceRevision", "location", "revision", "observedAt", "stale") || p.SourceID != out.SourceID || !offlineDigest.MatchString(p.SourceRevision) || p.Revision < 0 || p.Revision > offlineSafeInteger || p.ObservedAt < 1 || p.ObservedAt > offlineSafeInteger {
			return out, ErrInvalid
		}
		var progressFields map[string]json.RawMessage
		json.Unmarshal(progressRaw, &progressFields)
		if _, err := ParseOfflineLocation(progressFields["location"]); err != nil {
			return out, err
		}
		if !offlineKeys(progressFields["location"], "format", "cfi", "section", "pageIndex", "progression") {
			return out, ErrInvalid
		}
		l := p.Location
		if l.Format != out.Format || l.PageIndex != nil && *l.PageIndex > offlineSafeInteger {
			return out, ErrInvalid
		}
		if err := (Location{Format: l.Format, CFI: l.CFI, Section: l.Section, PageIndex: l.PageIndex, Progression: l.Progression}).Validate(); err != nil {
			return out, err
		}
		if !p.Stale && p.SourceRevision != out.Revision {
			return out, ErrInvalid
		}
	}
	return out, nil
}
