package scan

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/storage/objectidentity"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// These are synthetic VALUE fixtures, not a driver, native lease or accepting
// authority. Naming/sort values are supplied, not claimed as a new algorithm.
func n1Values(root, rootID, directoryID, revision string) (writer.CommandIdentity, writer.InventoryAcceptInput, FilenameNaming) {
	identity := writer.CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}
	input := writer.InventoryAcceptInput{
		Scope:     writer.InventoryScopeInput{RunID: "fixture-run", LibraryKey: "fixture-library", LibraryID: 3},
		Source:    writer.InventorySourceInput{ID: "fixture-source", Incarnation: "fixture-incarnation", ResolvedRoot: []byte(root), RootIdentity: rootID, Generation: 7},
		Run:       writer.InventoryRunInput{Revision: 4, PolicyRevision: 5, LeaseOwner: 6, LeaseGeneration: 7, LeaseExpiresMS: 1234567890, Phase: "inventory", ScanMode: "incremental"},
		Directory: writer.InventoryDirectoryInput{Path: []byte("."), Identity: directoryID, Revision: revision, CursorKind: "local_getdents64_cookie_v1"},
	}
	metadata := FilenameNaming{IdentityRoot: []byte("/retained-library-root"), MetadataLanguage: "en"}
	return identity, input, metadata
}

func n1Turn() InventoryTurn {
	return InventoryTurn{
		Request: storage.NativeInventoryRequest{Root: []byte("/media"), RelativePath: []byte("."), RootIdentity: "root", DirectoryRevision: "directory-revision", Limit: 1},
		Page: storage.NativeInventoryPage{Version: 1, RootIdentity: "root", DirectoryIdentity: "directory", DirectoryRevision: "directory-revision", NextCursor: "17",
			Entries: []storage.NativeInventoryEntry{{Path: []byte("/media/Film.mkv"), Name: []byte("Film.mkv"), ObjectIdentity: "dev:4:ino:9", ChangeToken: "change", Size: 123, ModifiedNS: -1}}},
	}
}

func TestN1BindingUsesCanonicalConstructorAndSavesOriginal(t *testing.T) {
	for _, tc := range []struct{ name, title, explicit, language, wantKey string }{
		{"absent explicit", "The Film", "", "en", "film"},
		{"explicit override", "The Film", "Override Sort", "fr", "override sort"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			identity, input, filename := n1Values("/media", "root", "directory", "directory-revision")
			filename.ExplicitSortTitle, filename.MetadataLanguage = tc.explicit, tc.language
			turn := n1Turn()
			turn.Page.Entries[0].Name = []byte(tc.title + " (1999).mkv")
			turn.Page.Entries[0].Path = append([]byte("/media/"), turn.Page.Entries[0].Name...)
			prepared, err := BindN1(identity, input, turn, filename)
			if err != nil {
				t.Fatal(err)
			}
			projection, err := writer.ProjectOrdinaryFilename(writer.InventoryOrdinaryFilenameInput{IdentityRoot: filename.IdentityRoot, AssetPath: turn.Page.Entries[0].Path, RelativePath: turn.Page.Entries[0].Name, ExplicitSortTitle: filename.ExplicitSortTitle, MetadataLanguage: filename.MetadataLanguage})
			metadata := projection.Metadata
			if err != nil {
				t.Fatal(err)
			}
			if metadata.SortTitle != tc.explicit || metadata.SortKey != tc.wantKey {
				t.Fatal("canonical default/override changed", metadata)
			}
			if prepared.Filename() != projection {
				t.Fatal("complete ordinary filename projection changed", prepared.Filename(), projection)
			}
			f := turn.Page.Entries[0]
			revision, err := (mediasource.InventoryEvidence{Kind: "local", Scope: input.Source.ID + ":" + input.Source.Incarnation,
				Object: objectidentity.StableObjectIdentity(f.ObjectIdentity, input.Source.RootIdentity), Size: f.Size, ModifiedNS: f.ModifiedNS, ChangeToken: f.ChangeToken}).Revision()
			if err != nil {
				t.Fatal(err)
			}
			input.Directory.NextCursor, input.Directory.RawNames = "17", 1
			input.Files = []writer.InventoryFilenameInput{{RelativePath: f.Name, AssetPath: f.Path, ObjectIdentity: f.ObjectIdentity,
				Revision: revision, ChangeToken: f.ChangeToken, Size: f.Size, ModifiedNS: f.ModifiedNS,
				SourceKey: projection.SourceKey, Title: metadata.Title, SortTitle: metadata.SortTitle, SortKey: metadata.SortKey, SortHead: metadata.SortHead,
				SortKeyKeepArticles: metadata.SortKeyKeepArticles, SortHeadKeepArticles: metadata.SortHeadKeepArticles, MetadataLanguage: metadata.MetadataLanguage, Year: projection.Year}}
			expected, err := writer.NewInventoryAcceptCommand(identity, input)
			if err != nil {
				t.Fatal(err)
			}
			ref, err := writer.InventoryAcceptReference(expected)
			if err != nil || ref != prepared.Reference() {
				t.Fatal("binder changed SC's canonical request/reference", err)
			}
			input.Source.ResolvedRoot[0], turn.Page.Entries[0].Path[0], turn.Page.Entries[0].Name[0] = 'X', 'X', 'X'
			frozen, err := writer.InventoryAcceptReference(prepared.Command())
			if err != nil || frozen != ref || prepared.Reference() != ref {
				t.Fatal("source mutation changed ORIGINAL reference", err)
			}
			// Real public facade refusals grant no authority or result. Recovery retains
			// the SAME original; no retry identity, cursor, ACK or Applied is fabricated.
			var unissued writer.Writer
			result, err := unissued.AcceptInventory(t.Context(), nil, prepared.Command())
			if err == nil || !reflect.DeepEqual(result, writer.InventoryResult{}) {
				t.Fatal("unissued writer accepted fixture values", result, err)
			}
			resolved, err := unissued.ResolveScan(t.Context(), nil, prepared.Reference())
			if err == nil || !reflect.DeepEqual(resolved, writer.InventoryResult{}) || prepared.Reference() != ref {
				t.Fatal("refusal/recovery lost ORIGINAL or created progress", resolved, err)
			}
		})
	}
}

func TestN1OrdinaryProjectionUsesRetainedRootAndPreservesPart(t *testing.T) {
	for _, name := range []string{"!!!.mkv", "Film - pt1.mkv", "Film - pt2.mkv", "!!! - pt2.mkv", "Film CD1.mkv", "Film CD2.mkv"} {
		t.Run(name, func(t *testing.T) {
			identity, input, filename := n1Values("/media", "root", "directory", "directory-revision")
			turn := n1Turn()
			turn.Page.Entries[0].Name = []byte(name)
			turn.Page.Entries[0].Path = []byte("/media/" + name)
			projectionInput := writer.InventoryOrdinaryFilenameInput{IdentityRoot: filename.IdentityRoot, AssetPath: turn.Page.Entries[0].Path,
				RelativePath: turn.Page.Entries[0].Name, MetadataLanguage: filename.MetadataLanguage}
			want, err := writer.ProjectOrdinaryFilename(projectionInput)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := BindN1(identity, input, turn, filename)
			if name == "!!!.mkv" {
				wrongRoot := projectionInput
				wrongRoot.IdentityRoot = input.Source.ResolvedRoot
				wrong, e := writer.ProjectOrdinaryFilename(wrongRoot)
				if e != nil || wrong.SourceKey == want.SourceKey {
					t.Fatal("fixture does not distinguish retained and physical roots", wrong, want, e)
				}
				if err != nil || prepared.Filename() != want || want.Part != 0 || prepared.Command() == nil {
					t.Fatal("part-zero projection changed or used the physical root", prepared.Filename(), want, err)
				}
			} else if err != nil || want.Part == 0 || prepared.Filename() != want || prepared.Command() == nil {
				t.Fatal("multipart projection or constructor lost Part", want, prepared, err)
			}
			if input.Directory.ExpectedCursor != "" || input.Directory.NextCursor != "" || len(input.Files) != 0 {
				t.Fatal("projection or refusal changed caller's committed boundary")
			}
		})
	}
}

func TestN1BindingStopsAtSeparateProtocolBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*InventoryTurn)
	}{
		{"directory", func(v *InventoryTurn) { v.Page.Entries[0].Directory = true }},
		{"EOF", func(v *InventoryTurn) { v.Page.Complete = true }},
		{"unsupported issue", func(v *InventoryTurn) {
			v.Page.Issues = []storage.NativeInventoryIssue{{Path: []byte("Film.mkv"), Reason: []byte("unsupported")}}
		}},
		{"unsliced page", func(v *InventoryTurn) { v.Page.Entries = append(v.Page.Entries, v.Page.Entries[0]) }},
		{"retained token", func(v *InventoryTurn) { v.Page.NextCursor = "stream1:session:1" }},
		{"no native progress", func(v *InventoryTurn) { v.Request.Cursor, v.Page.NextCursor = "17", "17" }},
		{"different claimed directory", func(v *InventoryTurn) { v.Page.DirectoryIdentity = "replacement" }},
		{"different raw path", func(v *InventoryTurn) { v.Page.Entries[0].Path = []byte("/outside/Film.mkv") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			identity, input, metadata := n1Values("/media", "root", "directory", "directory-revision")
			turn := n1Turn()
			tc.edit(&turn)
			if tc.name == "no native progress" {
				input.Directory.ExpectedCursor = "17"
			}
			prepared, err := BindN1(identity, input, turn, metadata)
			if err == nil || prepared.Command() != nil || prepared.Reference() != (writer.ScanReceiptRequest{}) || input.Directory.ExpectedCursor != turn.Request.Cursor {
				t.Fatal("unsupported observation created a command/progress", prepared, err)
			}
		})
	}
}

func TestN1RealHelperPreservesNativeBytesToSCTypedBoundary(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("FIRST is Linux native d_off only")
	}
	for _, name := range []string{"Film-\xff.mkv", "Film-\xfe.mkv", "Film-\ufffd.mkv", "Film-\x01\x7f.mkv"} {
		t.Run(strings.ReplaceAll(name, ".mkv", ""), func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, name), nil, 0600); err != nil {
				t.Fatal(err)
			}
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			client, owner := storage.New(binary), producerOwner(t)
			// Test-only physical owner; production observation belongs to W.
			directory, err := client.InventoryDirectory(t.Context(), "n1-directory-fixture", storage.InventoryRequest{Root: root, RelativePath: "."})
			if err != nil {
				t.Fatal(err)
			}
			r := storage.NativeInventoryRequest{Root: []byte(root), RelativePath: []byte("."), RootIdentity: directory.ObjectIdentity, DirectoryRevision: storage.InventoryRevision(directory), Limit: 1}
			page, err := client.NativeInventoryPage(context.Background(), owner, "n1-fixture", r)
			if err != nil || len(page.Entries) != 1 || !bytes.Equal(page.Entries[0].Name, []byte(name)) || !bytes.Equal(page.Entries[0].Path, []byte(filepath.Join(root, name))) {
				t.Fatal("native bytes changed before binding", page, err)
			}
			identity, input, metadata := n1Values(root, page.RootIdentity, page.DirectoryIdentity, page.DirectoryRevision)
			// W's production preparation owns this read. This VALUE-only
			// substitute passes the exact real-helper output through that seam.
			observation := &ownedObservation{page: page}
			prepared, err := ObserveN1(t.Context(), observation, identity, input, metadata)
			// ff and fe are distinct native inputs, never the literal U+FFFD.
			invalid := !utf8.ValidString(name)
			if invalid {
				if !errors.Is(err, writer.ErrFilenameUnrepresentable) || prepared.Command() != nil {
					t.Fatal("raw invalid UTF8 did not reach SC typed refusal", err)
				}
			} else if err != nil || prepared.Command() == nil {
				t.Fatal("representable native path was refused", err)
			}
			if !bytes.Equal(page.Entries[0].Name, []byte(name)) {
				t.Fatal("binding rewrote native bytes")
			}
		})
	}
}

func TestN1MetadataProjectionFailureReturnsNoCommand(t *testing.T) {
	identity, input, filename := n1Values("/media", "root", "directory", "directory-revision")
	filename.MetadataLanguage = string([]byte{0xff})
	prepared, err := BindN1(identity, input, n1Turn(), filename)
	if !errors.Is(err, writer.ErrInvalid) || prepared.Command() != nil || prepared.Reference() != (writer.ScanReceiptRequest{}) {
		t.Fatal("projection refusal created command/reference", prepared, err)
	}
	if input.Directory.ExpectedCursor != "" || input.Directory.NextCursor != "" || input.Directory.RawNames != 0 || len(input.Files) != 0 {
		t.Fatal("projection refusal mutated captured template")
	}
}

func TestN1MissingIdentityRootDoesNotBorrowPhysicalRoot(t *testing.T) {
	identity, input, filename := n1Values("/media", "root", "directory", "directory-revision")
	filename.IdentityRoot = nil
	prepared, err := BindN1(identity, input, n1Turn(), filename)
	if !errors.Is(err, writer.ErrInvalid) || !reflect.DeepEqual(prepared, PreparedN1{}) {
		t.Fatal("missing retained identity root defaulted to physical root", prepared, err)
	}
}
