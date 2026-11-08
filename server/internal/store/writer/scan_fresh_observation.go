package writer

import (
	"bytes"
	"encoding/json"
	"math"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/storage/objectidentity"
)

// inventoryFreshRequiredBytes forecasts SC's live Go preparation/bind/result
// envelope without copying or hashing input. It is NOT a quota or admission:
// W must cover this PLUS its native/index/pool/idle costs in the same grant.
// Four input copies cover retained input, request rehash, native TEXT readback
// and final receipt projection. Sixteen escaped-JSON copies cover both Marshal
// calls (including Revision's internal call), growth, prefixed hashing, string
// conversion, RETURNING and retained result buffers. The fixed 64KiB covers
// bounded argument/row/entropy/encoder bookkeeping for the finite N1 recipe.
func inventoryFreshRequiredBytes(c *inventoryAcceptCommand) (uint64, error) {
	if c != nil && c.valid && len(c.request.files) > 1 && len(c.request.files) <= maxInventoryAcceptFiles {
		var total uint64
		for i := range c.request.files {
			member := *c
			member.request.files = c.request.files[i : i+1]
			n, e := inventoryFreshRequiredBytes(&member)
			if e != nil {
				return 0, e
			}
			if n > math.MaxUint64-total {
				return 0, ErrCap
			}
			total += n
		}
		return total, nil
	}
	if c == nil || !c.valid || len(c.request.files) != 1 {
		return 0, ErrInvalid
	}
	r, f := c.request, c.request.files[0]
	texts := [...]string{r.variant, r.scope.runID, r.scope.libraryKey, r.source.id, r.source.incarnation, r.source.rootIdentity,
		r.run.phase, r.run.scanMode, r.directory.path, r.directory.identity, r.directory.revision, r.directory.expectedCursor, r.directory.nextCursor, r.directory.cursorKind,
		f.sourceKey, f.relativePath, f.assetPath, f.objectIdentity, f.revision, f.changeToken, f.title, f.sortTitle, f.sortKey, f.sortHead, f.sortKeyKeepArticles, f.sortHeadKeepArticles, f.metadataLanguage}
	payload := uint64(len(r.source.resolvedRoot))
	if payload > maxCanonicalRequestBytes {
		return 0, inventoryRequestCapacity()
	}
	for _, value := range texts {
		if uint64(len(value)) > maxCanonicalRequestBytes-payload {
			return 0, inventoryRequestCapacity()
		}
		payload += uint64(len(value))
	}
	return inventoryFreshWorkspaceBytes(payload, uint64(len(r.source.id))+1+uint64(len(r.source.incarnation)),
		uint64(len(f.objectIdentity)), uint64(len(f.assetPath)), uint64(len(f.changeToken)))
}

// One allocation-free arithmetic implementation is shared with W's funded
// pre-constructor preparation. Inputs are byte counts, never authority/budgets.
func inventoryFreshWorkspaceBytes(payload, scope, object, assetPathBytes, changeBytes uint64) (uint64, error) {
	if payload > maxCanonicalRequestBytes || assetPathBytes > payload || object > payload || scope > 1024 || changeBytes > 4096 || changeBytes > payload {
		return 0, invalid("inventory_evidence_shape")
	}
	if object == 0 {
		object = 5 + assetPathBytes // evidence-only path: fallback
	} else if object < 24 {
		object = 24 // longest stable ino:uint64 projection
	}
	if object > 8192 {
		return 0, invalid("inventory_evidence_shape")
	}
	// Existing total-input preflight bounds every term; keep the arithmetic
	// independently checked so widening that policy cannot wrap this forecast.
	if object > (math.MaxUint64-296)/6-scope-5-changeBytes {
		return 0, inventoryRequestCapacity()
	}
	jsonBytes := uint64(296) + 6*(5+scope+object+changeBytes)
	if jsonBytes > (math.MaxUint64-65536-4*payload)/16 {
		return 0, inventoryRequestCapacity()
	}
	return 65536 + 4*payload + 16*jsonBytes, nil
}

// Called only after W's complete preparation reservation and original history
// miss. This proves value parity/lexical containment, not a filesystem syscall,
// mount-cookie stability, byte continuity or a ScanDriver capability.
func prepareInventoryFreshObservation(c *inventoryAcceptCommand) (inventoryObjectFacts, error) {
	var zero inventoryObjectFacts
	if c == nil || !c.valid || len(c.request.files) != 1 {
		return zero, ErrInvalid
	}
	r, f := c.request, c.request.files[0]
	if err := validateInventoryRequest(c.identity, r); err != nil {
		return zero, err
	}
	if r.directory.complete || r.directory.unstable || r.source.followSymlinks || r.directory.cursorKind != inventoryLinuxCursor {
		return zero, &Refusal{Code: RefusalUnqualified}
	}
	oldCookie, err := inventoryFreshCookie(r.directory.expectedCursor)
	if err != nil {
		return zero, err
	}
	nextCookie, err := inventoryFreshCookie(r.directory.nextCursor)
	if err != nil || r.directory.nextCursor == "" || oldCookie == nextCookie {
		return zero, ErrConflict
	}
	// Root transport must round-trip EXACTLY. Reject unsupported spelling or
	// invalid native bytes instead of cleaning/replacing them into a match.
	if !utf8.Valid(r.source.resolvedRoot) || bytes.IndexByte(r.source.resolvedRoot, 0) >= 0 {
		return zero, ErrConflict
	}
	root := string(r.source.resolvedRoot)
	if root == "" || root[0] != '/' || path.Clean(root) != root {
		return zero, ErrConflict
	}
	prefix := root
	if prefix != "/" {
		prefix += "/"
	}
	if len(f.assetPath) != len(prefix)+len(f.relativePath) || !strings.HasPrefix(f.assetPath, prefix) ||
		f.assetPath[len(prefix):] != f.relativePath || path.Dir(f.relativePath) != r.directory.path {
		return zero, ErrConflict
	}
	gotDigest, err := canonicalRequestDigest(InventoryAccept, inventoryAcceptRequestVersion, c.identity, r)
	if err != nil || gotDigest != c.digest {
		return zero, ErrConflict
	}
	stable := objectidentity.StableObjectIdentity(f.objectIdentity, r.source.rootIdentity)
	object := stable
	if object == "" {
		object = "path:" + f.assetPath
	}
	e := mediasource.InventoryEvidence{Kind: "local", Scope: r.source.id + ":" + r.source.incarnation,
		Object: object, Size: f.size, ModifiedNS: f.modifiedNS, ChangeToken: f.changeToken}
	revision, err := e.Revision()
	if err != nil || revision != f.revision {
		return zero, ErrConflict
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return zero, err
	}
	return inventoryObjectFacts{sourceID: r.source.id, incarnation: r.source.incarnation, relativePath: f.relativePath,
		objectIdentity: stable, revision: revision, evidenceJSON: string(raw), size: f.size, modifiedNS: f.modifiedNS,
		runID: r.scope.runID, directoryPath: r.directory.path}, nil
}

func inventoryFreshCookie(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 {
		return 0, ErrConflict
	}
	return n, nil
}
