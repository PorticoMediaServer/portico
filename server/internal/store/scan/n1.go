package scan

import (
	"bytes"
	"path"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
	"github.com/PorticoMediaServer/portico/server/internal/storage/objectidentity"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// FilenameNaming contains only W-owned retained locator root and naming options.
// IdentityRoot is NOT the physical ResolvedRoot. SC's one ordinary projector
// owns SourceKey, Year, Part and the complete metadata; no algorithm lives here.
type FilenameNaming struct {
	IdentityRoot                        []byte
	ExplicitSortTitle, MetadataLanguage string
}

// PreparedN1 holds the frozen command AND its ORIGINAL reference, captured before
// any submission. It conveys no authority, Applied boundary or completion. W's
// preparation must own observation, funding, physical retirement and consumer
// custody. This value never owns a scope, callback, supervisor or writer.
type PreparedN1 struct {
	command   writer.InventoryAcceptCommand
	reference writer.ScanReceiptRequest
	filename  writer.InventoryOrdinaryFilename
}

func (p PreparedN1) Command() writer.InventoryAcceptCommand     { return p.command }
func (p PreparedN1) Reference() writer.ScanReceiptRequest       { return p.reference }
func (p PreparedN1) Filename() writer.InventoryOrdinaryFilename { return p.filename }

// BindN1 maps ONE complete owned raw observation to SC's existing constructor.
// The composing W preparation supplies the original identity and claimed-directory
// template. It must call ObserveInventory before this binder and keep the owned
// data alive through freeze; no harness WalkInventory callback enters this path.
// Directory discovery, EOF, unsupported issues and traversal completion require
// separate commands. A refusal leaves the original committed cursor unchanged.
func BindN1(identity writer.CommandIdentity, template writer.InventoryAcceptInput, turn InventoryTurn, filename FilenameNaming) (PreparedN1, error) {
	r, p, d := turn.Request, turn.Page, template.Directory
	if len(template.Files) != 0 || r.Limit != 1 || r.FollowSymlinks || template.Source.FollowSymlinks ||
		p.Version != 1 || p.ErrorCode != "" || p.Complete || p.Unstable || len(p.Entries) != 1 || len(p.Issues) != 0 ||
		d.Complete || d.Unstable || d.CursorKind != "local_getdents64_cookie_v1" ||
		!bytes.Equal(r.Root, template.Source.ResolvedRoot) || !bytes.Equal(r.RelativePath, d.Path) ||
		r.RootIdentity != template.Source.RootIdentity || p.RootIdentity != template.Source.RootIdentity ||
		p.DirectoryIdentity == "" || p.DirectoryIdentity != d.Identity || p.DirectoryRevision == "" ||
		r.DirectoryRevision != d.Revision || p.DirectoryRevision != d.Revision || r.Cursor != d.ExpectedCursor ||
		d.NextCursor != "" && d.NextCursor != p.NextCursor || d.RawNames != 0 && d.RawNames != 1 {
		return PreparedN1{}, ErrInvalid
	}
	old, ok := n1Cookie(r.Cursor)
	next, nextOK := n1Cookie(p.NextCursor)
	if !ok || !nextOK || p.NextCursor == "" || old == next {
		return PreparedN1{}, ErrInvalid
	}
	f := p.Entries[0]
	// Bound native bytes before converting or building derived path/evidence
	// payloads. These are existing transport/evidence bounds, not a RAM grant.
	if f.Directory || f.Size < 0 || len(r.Root) > naming.MaxPathBytes || len(d.Path) > naming.MaxPathBytes ||
		len(f.Name) == 0 || len(f.Name) > naming.MaxPathBytes || len(f.Path) > naming.MaxPathBytes ||
		len(f.ObjectIdentity) > 4096 || len(f.ChangeToken) > 4096 ||
		len(template.Source.ID) > 1023 || len(template.Source.Incarnation) > 1023-len(template.Source.ID) ||
		bytes.ContainsAny(f.Name, "/\\\x00") || bytes.Equal(f.Name, []byte(".")) || bytes.Equal(f.Name, []byte("..")) {
		return PreparedN1{}, ErrInvalid
	}
	if !validNativeRelative(d.Path) && !bytes.Equal(d.Path, []byte(".")) || bytes.IndexByte(r.Root, 0) >= 0 ||
		len(r.Root) == 0 || r.Root[0] != '/' || path.Clean(string(r.Root)) != string(r.Root) {
		return PreparedN1{}, ErrInvalid
	}
	relative := f.Name
	if !bytes.Equal(d.Path, []byte(".")) {
		if len(d.Path)+1+len(f.Name) > naming.MaxPathBytes {
			return PreparedN1{}, ErrCapacity
		}
		relative = append(append(bytes.Clone(d.Path), '/'), f.Name...)
	}
	// Compare the observed absolute path byte-for-byte; never clean or replace it.
	prefix := r.Root
	if !bytes.Equal(prefix, []byte("/")) {
		prefix = append(bytes.Clone(prefix), '/')
	}
	if len(f.Path) != len(prefix)+len(relative) || !bytes.HasPrefix(f.Path, prefix) || !bytes.Equal(f.Path[len(prefix):], relative) {
		return PreparedN1{}, ErrInvalid
	}
	// SC checks every raw field before Unicode naming/evidence conversion and
	// owns the adopted TEXT-policy refusal on the EXACT native bytes.
	projection, err := writer.ProjectOrdinaryFilename(writer.InventoryOrdinaryFilenameInput{
		IdentityRoot: filename.IdentityRoot, AssetPath: f.Path, RelativePath: relative,
		ExplicitSortTitle: filename.ExplicitSortTitle, MetadataLanguage: filename.MetadataLanguage})
	if err != nil {
		return PreparedN1{}, err
	}
	metadata := projection.Metadata
	file := writer.InventoryFilenameInput{RelativePath: relative, AssetPath: f.Path, ObjectIdentity: f.ObjectIdentity,
		ChangeToken: f.ChangeToken, Size: f.Size, ModifiedNS: f.ModifiedNS,
		SourceKey: projection.SourceKey, Year: projection.Year, Part: projection.Part,
		Title: metadata.Title, SortTitle: metadata.SortTitle, SortKey: metadata.SortKey, SortHead: metadata.SortHead,
		SortKeyKeepArticles: metadata.SortKeyKeepArticles, SortHeadKeepArticles: metadata.SortHeadKeepArticles,
		MetadataLanguage: metadata.MetadataLanguage}
	object := objectidentity.StableObjectIdentity(f.ObjectIdentity, template.Source.RootIdentity)
	if object == "" {
		object = "path:" + string(f.Path)
	}
	revision, err := (mediasource.InventoryEvidence{Kind: "local", Scope: template.Source.ID + ":" + template.Source.Incarnation,
		Object: object, Size: f.Size, ModifiedNS: f.ModifiedNS, ChangeToken: f.ChangeToken}).Revision()
	if err != nil {
		return PreparedN1{}, err
	}
	file.Revision = revision
	template.Directory.NextCursor, template.Directory.RawNames = p.NextCursor, 1
	template.Files = []writer.InventoryFilenameInput{file}
	command, err := writer.NewInventoryAcceptCommand(identity, template)
	if err != nil {
		return PreparedN1{}, err
	}
	reference, err := writer.InventoryAcceptReference(command)
	if err != nil {
		return PreparedN1{}, err
	}
	return PreparedN1{command: command, reference: reference, filename: projection}, nil
}

func n1Cookie(value string) (int64, bool) {
	if value == "" {
		return 0, true
	}
	if len(value) > 19 {
		return 0, false
	}
	n, err := strconv.ParseInt(value, 10, 64)
	return n, err == nil && n >= 0 && strconv.FormatInt(n, 10) == value
}
