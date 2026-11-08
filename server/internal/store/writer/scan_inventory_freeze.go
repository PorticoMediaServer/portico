package writer

import (
	"bytes"
	"context"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Freeze is the funded producer constructor. The public pure constructor still
// grants no authority; only this method binds its immutable command to W's
// private actual native observation and original preparation lifetime.
func (p *N1Preparation) Freeze(ctx context.Context, input InventoryAcceptInput) (value InventoryAcceptCommand, err error) {
	life := ctx
	var candidate *inventoryAcceptCommand
	locked := false
	defer func() {
		if !locked {
			return
		}
		if err != nil {
			p.driver.batchPolicy.width, p.driver.batchPolicy.short = 1, 0
		}
		if err != nil && candidate != nil {
			candidate.valid, candidate.request = false, inventoryAcceptRequest{}
			if p.command == candidate {
				p.command = nil
				p.reference = ScanReceiptRequest{}
				p.preparedBytes = 0
			}
		}
		p.driver.mu.Unlock()
	}()
	if p != nil && p.self == p && p.driver != nil {
		life = p.driver.lifetime
	}
	defer func() { value, err = finishCommand(ctx, life, value, err) }()
	if p == nil || p.self != p || p.driver == nil || ctx == nil {
		return nil, ErrInvalid
	}
	if !p.driver.mu.TryLock() {
		return nil, ErrBusy
	}
	locked = true
	if p.closed || p.observationFailed.Load() || p.historyOnly || p.preparation == nil || p.driver.preparation != p.preparation || p.command != nil {
		return nil, ErrClosed
	}
	b := p.preparation
	life = b.lifetime
	if err = errors.Join(ctx.Err(), p.driver.current(), b.current()); err != nil {
		return nil, err
	}
	if len(input.Files) < 1 || len(input.Files) > b.request.Limit {
		return nil, ErrInvalid
	}
	if err = inventoryPublicPathPreflight(input); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failed || b.closed || b.observing || b.observed == nil || b.source == nil {
		return nil, ErrClosed
	}
	page := b.observed
	if !p.matchesInventoryObservation(input, *page) {
		return nil, ErrConflict
	}
	required, err := inventoryPreparationRequiredBytes(input, *page, uint64(len(b.source.identityRoot)))
	if err != nil {
		return nil, err
	}
	if b.expected.Need[governor.MemoryBytes] <= 0 || required > uint64(b.expected.Need[governor.MemoryBytes]) {
		return nil, &Refusal{Code: RefusalCapacity}
	}
	// Naming, normalization, byte-to-string conversion, encoding and cloning
	// begin only after the complete original/derived envelope fits this grant.
	for _, f := range input.Files {
		projection, err := ProjectOrdinaryFilename(InventoryOrdinaryFilenameInput{IdentityRoot: b.source.identityRoot,
			AssetPath: f.AssetPath, RelativePath: f.RelativePath, MetadataLanguage: f.MetadataLanguage})
		if err != nil {
			return nil, err
		}
		if f.Part != projection.Part {
			return nil, ErrConflict
		}
		metadata := InventoryFilenameMetadata{f.Title, f.SortTitle, f.SortKey, f.SortHead, f.SortKeyKeepArticles, f.SortHeadKeepArticles, f.MetadataLanguage}
		if f.SourceKey != projection.SourceKey || f.Year != projection.Year || metadata != projection.Metadata {
			return nil, ErrConflict
		}
	}
	command, err := NewInventoryAcceptCommand(p.identity, input)
	if err != nil {
		return nil, err
	}
	c, ok := command.(*inventoryAcceptCommand)
	if !ok || c == nil {
		return nil, ErrInvalid
	}
	candidate = c
	if err = prepareInventoryFreshBatch(c); err != nil {
		return nil, err
	}
	reference, err := InventoryAcceptReference(c)
	if err != nil {
		return nil, err
	}
	p.command, p.reference, p.preparedBytes = c, reference, required
	return c, nil
}

func (p *N1Preparation) matchesInventoryObservation(input InventoryAcceptInput, page nativeinventory.Page) bool {
	if len(input.Files) < 1 || len(input.Files) > maxInventoryAcceptFiles || len(page.Entries) != len(input.Files) || len(page.Issues) != 0 || page.Version != 1 || page.ErrorCode != "" || page.Unstable || page.Complete {
		return false
	}
	t, s := p.template, input.Source
	if input.Scope != t.Scope || input.Run != t.Run || !bytes.Equal(s.ResolvedRoot, t.Source.ResolvedRoot) ||
		s.ID != t.Source.ID || s.Incarnation != t.Source.Incarnation || s.RootIdentity != t.Source.RootIdentity || s.Generation != t.Source.Generation || s.FollowSymlinks != t.Source.FollowSymlinks {
		return false
	}
	d, want := input.Directory, t.Directory
	if !(string(want.Path) == "." && bytes.Equal(d.Path, want.Path) && d.Identity == want.Identity && d.Revision == want.Revision &&
		d.ExpectedCursor == want.ExpectedCursor && d.Pages == want.Pages && d.Skipped == want.Skipped && d.CursorKind == want.CursorKind &&
		d.NextCursor == page.NextCursor && d.Complete == page.Complete && d.Unstable == page.Unstable && d.RawNames == int64(len(input.Files)) &&
		page.RootIdentity == s.RootIdentity && page.DirectoryIdentity == d.Identity && page.DirectoryRevision == d.Revision) {
		return false
	}
	for i, f := range input.Files {
		e := page.Entries[i]
		if !(!e.Directory && !f.Directory && bytes.Equal(f.RelativePath, e.Name) && bytes.Equal(f.AssetPath, e.Path) &&
			f.ObjectIdentity == e.ObjectIdentity && f.ChangeToken == e.ChangeToken && f.Size == e.Size && f.ModifiedNS == e.ModifiedNS) {
			return false
		}
	}
	return true
}

// No conversions or derived allocations. SC's ONE workspace arithmetic is
// reused; four additional original copies cover the public paths, constructor,
// private frozen command and exported preparation snapshot. The actual native
// page and retained logical identity root are counted in addition, never lent.
func inventoryPreparationRequiredBytes(input InventoryAcceptInput, page nativeinventory.Page, identityRootBytes uint64) (uint64, error) {
	if len(input.Files) > 1 && len(input.Files) <= maxInventoryAcceptFiles {
		var total uint64
		for i := range input.Files {
			member := input
			member.Files = input.Files[i : i+1]
			n, e := inventoryPreparationRequiredBytes(member, page, identityRootBytes)
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
	if len(input.Files) != 1 {
		return 0, ErrInvalid
	}
	f, s, r, d := input.Files[0], input.Source, input.Run, input.Directory
	payload := uint64(len(s.ResolvedRoot)) + uint64(len(d.Path)) + uint64(len(f.RelativePath)) + uint64(len(f.AssetPath))
	if payload > maxCanonicalRequestBytes {
		return 0, inventoryRequestCapacity()
	}
	for _, text := range [...]string{inventoryOrdinaryBatch, input.Scope.RunID, input.Scope.LibraryKey, s.ID, s.Incarnation, s.RootIdentity, r.Phase, r.ScanMode,
		d.Identity, d.Revision, d.ExpectedCursor, d.NextCursor, d.CursorKind, f.SourceKey, f.ObjectIdentity, f.Revision, f.ChangeToken,
		f.Title, f.SortTitle, f.SortKey, f.SortHead, f.SortKeyKeepArticles, f.SortHeadKeepArticles, f.MetadataLanguage} {
		if uint64(len(text)) > maxCanonicalRequestBytes-payload {
			return 0, inventoryRequestCapacity()
		}
		payload += uint64(len(text))
	}
	domain, err := inventoryFreshWorkspaceBytes(payload, uint64(len(s.ID))+1+uint64(len(s.Incarnation)), uint64(len(f.ObjectIdentity)), uint64(len(f.AssetPath)), uint64(len(f.ChangeToken)))
	if err != nil {
		return 0, err
	}
	observed := uint64(8192) + uint64(len(page.Entries))*256 + uint64(len(page.Issues))*128
	for _, text := range [...]string{page.RootIdentity, page.DirectoryIdentity, page.DirectoryRevision, page.NextCursor, page.ErrorCode} {
		observed += 4 * uint64(len(text))
	}
	for _, entry := range page.Entries {
		observed += 4 * (uint64(len(entry.Path)) + uint64(len(entry.Name)) + uint64(len(entry.ObjectIdentity)) + uint64(len(entry.ChangeToken)))
	}
	for _, issue := range page.Issues {
		observed += 4 * (uint64(len(issue.Path)) + uint64(len(issue.Reason)))
	}
	if identityRootBytes > (math.MaxUint64-domain)/32 {
		return 0, ErrCap
	}
	required := domain + 32*identityRootBytes
	if payload > (math.MaxUint64-required)/4 {
		return 0, ErrCap
	}
	required += 4 * payload
	if observed > math.MaxUint64-required {
		return 0, ErrCap
	}
	return required + observed, nil
}
