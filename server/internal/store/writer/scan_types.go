package writer

import (
	"bytes"
	"io/fs"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/naming"
)

const (
	inventoryOrdinaryBatch               = "ordinary_batch"
	inventoryLinuxCursor                 = "local_getdents64_cookie_v1"
	inventoryAcceptRequestVersion uint16 = 2 // Part is an original captured link witness
	maxInventoryAcceptFiles              = 1 // FIRST preparation shape; widening requires qualification
)

// These values are the COMPLETE original PRE-allocation input, not disclosure
// or a grant. Source and file revisions retain their native, independent types.
// Fresh effects must re-prove the captured values after historical resolution.
type inventoryAcceptRequest struct {
	variant   string
	scope     inventoryScope
	source    inventorySource
	run       inventoryRun
	directory inventoryDirectory
	files     []inventoryFile
}

type inventoryScope struct {
	runID, libraryKey string
	libraryID         int64
}

type inventorySource struct {
	id, incarnation, rootIdentity string
	resolvedRoot                  []byte // exact ORIGINAL native Linux root bytes
	generation                    int64
	followSymlinks                bool
}

type inventoryRun struct {
	revision, policyRevision                    int64
	leaseOwner, leaseGeneration, leaseExpiresMS int64
	lastSequence, acknowledgedSequence          int64
	phase, scanMode                             string
}

type inventoryDirectory struct {
	path, identity, revision, expectedCursor, nextCursor string
	cursorKind                                           string
	pages, skipped, rawNames                             int64
	complete, unstable                                   bool
}

// Inventory supplies prepared filename facts, never technical/Basic facts.
// New object/public/asset IDs and entity incarnations are writer allocations,
// deliberately absent here. Revision is the exact native observation revision
// later published as inventory_objects.revision and the N1 receipt echo; it is
// not a source-generation or directory-revision alias.
type inventoryFile struct {
	sourceKey, relativePath, assetPath, objectIdentity, revision, changeToken      string
	directory                                                                      bool // captured native observation; FIRST must be a file
	size, modifiedNS                                                               int64
	title, sortTitle, sortKey, sortHead, sortKeyKeepArticles, sortHeadKeepArticles string
	year, part                                                                     int64
	metadataLanguage                                                               string
}

// The sole facade's dispatcher will match this concrete type, not invoke a
// marker. No SQL, execution callback, resource recipe or capability is stored.
type inventoryAcceptCommand struct {
	identity CommandIdentity
	request  inventoryAcceptRequest
	digest   RequestDigest
	valid    bool
}

func (*inventoryAcceptCommand) isInventoryAcceptCommand() {}

var _ InventoryAcceptCommand = (*inventoryAcceptCommand)(nil)

// newInventoryAcceptCommand prepares values only. The caller exclusively owns
// the input for this call. No filesystem work, naming, allocation of durable
// identities, source lookup, admission or receipt resolution occurs here.
func newInventoryAcceptCommand(identity CommandIdentity, in inventoryAcceptRequest) (*inventoryAcceptCommand, error) {
	if identity.Sequence <= 0 || len(in.files) == 0 {
		return nil, invalid("inventory_request")
	}
	if err := count("inventory_files", len(in.files), maxInventoryAcceptFiles); err != nil {
		return nil, err
	}
	// Inspect ALL native payload lengths before scanning UTF-8, cloning or
	// hashing. The shared encoder then checks its COMPLETE typed wire envelope
	// (including descriptors) before this constructor owns any input buffers.
	remaining := uint64(maxCanonicalRequestBytes)
	if uint64(len(in.source.resolvedRoot)) > remaining {
		return nil, inventoryRequestCapacity()
	}
	remaining -= uint64(len(in.source.resolvedRoot))
	texts := inventoryRequestTexts(&in)
	for _, value := range texts {
		if uint64(len(*value)) > remaining {
			return nil, inventoryRequestCapacity()
		}
		remaining -= uint64(len(*value))
	}
	for _, value := range texts {
		if !utf8.ValidString(*value) {
			return nil, invalid("inventory_text")
		}
	}
	if err := validateInventoryRequest(identity, in); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(InventoryAccept, inventoryAcceptRequestVersion, identity, in)
	if err != nil {
		return nil, err
	}
	// Freeze the bounded slice before replacing its strings: mutating the
	// caller's backing array here would violate immutable retry preparation.
	in.files = append([]inventoryFile(nil), in.files...)
	in.source.resolvedRoot = bytes.Clone(in.source.resolvedRoot)
	for _, value := range inventoryRequestTexts(&in) {
		*value = strings.Clone(*value)
	}
	return &inventoryAcceptCommand{identity: identity, request: in, digest: digest, valid: true}, nil
}

func inventoryRequestCapacity() error {
	return &PolicyError{Field: "inventory_request_bytes", Limit: maxCanonicalRequestBytes, Cause: ErrCap}
}

func inventoryRequestTexts(in *inventoryAcceptRequest) []*string {
	text := []*string{&in.variant, &in.scope.runID, &in.scope.libraryKey,
		&in.source.id, &in.source.incarnation, &in.source.rootIdentity,
		&in.run.phase, &in.run.scanMode, &in.directory.path, &in.directory.identity,
		&in.directory.revision, &in.directory.expectedCursor, &in.directory.nextCursor, &in.directory.cursorKind}
	for i := range in.files {
		f := &in.files[i]
		text = append(text, &f.sourceKey, &f.relativePath, &f.assetPath,
			&f.objectIdentity, &f.revision, &f.changeToken, &f.title, &f.sortTitle,
			&f.sortKey, &f.sortHead, &f.sortKeyKeepArticles, &f.sortHeadKeepArticles, &f.metadataLanguage)
	}
	return text
}

func validateInventoryRequest(identity CommandIdentity, in inventoryAcceptRequest) error {
	if in.variant != inventoryOrdinaryBatch || in.scope.runID == "" || in.scope.libraryKey == "" || in.scope.libraryID <= 0 ||
		in.source.id == "" || in.source.incarnation == "" || in.source.rootIdentity == "" || in.source.generation <= 0 ||
		len(in.source.resolvedRoot) == 0 || in.source.resolvedRoot[0] != '/' || bytes.IndexByte(in.source.resolvedRoot, 0) >= 0 ||
		in.run.revision <= 0 || in.run.policyRevision <= 0 || in.run.leaseOwner <= 0 || in.run.leaseGeneration <= 0 || in.run.leaseExpiresMS <= 0 ||
		in.run.lastSequence != identity.Sequence-1 || in.run.acknowledgedSequence != in.run.lastSequence || in.run.phase != "inventory" ||
		!inventoryScanMode(in.run.scanMode) || in.directory.identity == "" || in.directory.revision == "" || in.directory.cursorKind != inventoryLinuxCursor ||
		!inventoryPath(in.directory.path, true) || in.directory.pages < 0 || in.directory.skipped < 0 ||
		in.directory.rawNames != int64(len(in.files)) || in.directory.unstable {
		return invalid("inventory_original_witness")
	}
	for _, f := range in.files {
		if f.sourceKey == "" || !inventoryPath(f.relativePath, false) || path.Dir(f.relativePath) != in.directory.path ||
			f.assetPath == "" || f.assetPath[0] != '/' || strings.IndexByte(f.assetPath, 0) >= 0 ||
			f.revision == "" || f.size < 0 || f.year < 0 || f.part < 0 || f.directory || strings.TrimSpace(f.title) == "" ||
			f.metadataLanguage == "" {
			return invalid("inventory_file")
		}
		projection, err := ProjectFilenameMetadata(f.title, f.sortTitle, f.metadataLanguage)
		if err != nil {
			return err
		}
		if f.sortKey != projection.SortKey || f.sortHead != projection.SortHead || f.sortKeyKeepArticles != projection.SortKeyKeepArticles || f.sortHeadKeepArticles != projection.SortHeadKeepArticles {
			return invalid("inventory_sort_projection")
		}
	}
	// Clock expiry, source/root equality, next-cursor progress, counter overflow,
	// same-library ownership and final shape are FRESH-only native checks. This
	// constructor cannot attest containment, mount stability or directory EOF.
	return nil
}

func inventoryPath(value string, directory bool) bool {
	return (directory || value != ".") && fs.ValidPath(value) && !strings.ContainsAny(value, "\\\x00") &&
		len(value) <= naming.MaxPathBytes && strings.Count(value, "/") < naming.MaxDepth
}

func inventoryScanMode(value string) bool {
	switch value {
	case "first-import", "incremental", "integrity", "full":
		return true
	default:
		return false
	}
}

func (c *inventoryAcceptCommand) receiptRequest() (ScanReceiptRequest, error) {
	if c == nil || !c.valid {
		return ScanReceiptRequest{}, invalid("inventory_command")
	}
	return ScanReceiptRequest{RunID: c.request.scope.runID, LibraryID: c.request.scope.libraryID,
		LibraryKey: c.request.scope.libraryKey, Receipt: ReceiptReference{Identity: c.identity, RequestDigest: c.digest}}, nil
}

// inventoryAcceptReference is a domain projection for W's closed facade, not
// current disclosure or historical resolution. Never invoke an interface
// marker: an externally embedded/wrapped command is not this concrete input.
func inventoryAcceptReference(input InventoryAcceptCommand) (ScanReceiptRequest, error) {
	c, ok := input.(*inventoryAcceptCommand)
	if !ok || c == nil || !c.valid {
		return ScanReceiptRequest{}, invalid("inventory_command_type")
	}
	return c.receiptRequest()
}
