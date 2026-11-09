package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/read"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const mpReadingMaxRevision int64 = 9007199254740991

// Values only. UO supplies the current disclosed profile/public receipt scope
// and owns Check/Save, quota, personal/history effects and the terminal commit.
// This component cannot construct Applied or write a personal row or receipt.
type mpReadingLocation struct {
	Format, CFI, Section string
	Page                 sql.NullInt64
	Progression          float64
}
type mpReadingPublication struct {
	self       *mpReadingPublication
	life       context.Context
	source     read.MPReadingSource
	sections   []string
	pages      int64
	conversion bool
}

// Trusted acquisition supplies actual parsed spine/page facts and its lifetime.
// No IO or archive parsing is performed inside the writer turn.
func holdMPReadingPublication(life context.Context, source read.MPReadingSourceValues, conversion bool, sections []string, pages int64) (*mpReadingPublication, error) {
	if life == nil || life.Err() != nil || len(sections) > 4096 || pages < 0 || pages > 1000001 {
		return nil, ErrInvalid
	}
	s, err := read.DecodeMPReadingSource(source, conversion)
	if err != nil {
		return nil, err
	}
	p := &mpReadingPublication{life: life, source: s, pages: pages, conversion: conversion}
	for _, v := range sections {
		if !mpReadingSection(v) {
			return nil, ErrInvalid
		}
		p.sections = append(p.sections, strings.Clone(v))
	}
	p.self = p
	return p, nil
}
func mpReadingSection(s string) bool {
	return len(s) > 0 && len(s) <= 1024 && utf8.ValidString(s) && !path.IsAbs(s) && path.Clean(s) == s && s != "." && s != ".." && !strings.HasPrefix(s, "../") && !strings.ContainsAny(s, "\\\x00") && strings.IndexFunc(s, unicode.IsControl) < 0
}
func validateMPReadingLocation(p *mpReadingPublication, l mpReadingLocation) error {
	if p == nil || p.self != p || p.life == nil || p.life.Err() != nil || l.Format != p.source.PresentationFormat || math.IsNaN(l.Progression) || math.IsInf(l.Progression, 0) || l.Progression < 0 || l.Progression > 1 {
		return ErrInvalid
	}
	if l.Format == "epub" {
		if l.Page != (sql.NullInt64{}) || len(l.CFI) < 11 || len(l.CFI) > 4096 || !utf8.ValidString(l.CFI) || !strings.HasPrefix(l.CFI, "epubcfi(/") || !strings.HasSuffix(l.CFI, ")") || strings.ContainsAny(l.CFI, "<>") || strings.IndexFunc(l.CFI, unicode.IsControl) >= 0 {
			return ErrInvalid
		}
		if l.Section != "" {
			if !mpReadingSection(l.Section) {
				return ErrInvalid
			}
			found := false
			for _, s := range p.sections {
				found = found || s == l.Section
			}
			if !found {
				return ErrInvalid
			}
		}
	} else if l.Format == "pdf" || l.Format == "cbz" || l.Format == "cb7" || l.Format == "cbr" {
		if !l.Page.Valid || l.Page.Int64 < 0 || l.Page.Int64 > 1000000 || l.CFI != "" || l.Section != "" || p.pages > 0 && l.Page.Int64 >= p.pages || l.Format != "pdf" && p.pages == 0 {
			return ErrInvalid
		}
	} else {
		return ErrInvalid
	}
	return nil
}

type mpReadingIntentInput struct {
	publication                                       *mpReadingPublication
	profile                                           string // UO's SAME-turn disclosed personal key, never user authority
	action                                            string // location, clear, reread
	historyPaused                                     bool   // UO effective preferences from this SAME freshly authorized turn
	expectedReadingRevision, expectedMetadataRevision int64
	location                                          mpReadingLocation
}
type mpReadingIntentValue struct {
	Saved                                                             mpReadingSavedPosition
	SavedStale                                                        bool
	Source                                                            read.MPReadingSource
	Location                                                          mpReadingLocation
	ReadingRevision, PersonalRevision, MetadataRevision, PersonalItem int64
	Changed, Clear, Reread, Stale                                     bool
	Reason                                                            string
	SelectedBytes                                                     int64
}

// Fresh-path proposal ONLY. Replay must read the typed original after UO's
// current disclosure and public receipt classification, without source lookup.
func mpReadingIntent(ctx context.Context, t *commandTurn, q mpReadingIntentInput) (out mpReadingIntentValue, err error) {
	defer func() {
		if q.publication != nil {
			out, err = finishCommand(ctx, q.publication.life, out, err)
		}
	}()
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil || t.transaction == nil || q.publication == nil || q.publication.self != q.publication || q.profile == "" || len(q.profile) > 512 || q.expectedReadingRevision < 0 || q.expectedReadingRevision >= mpReadingMaxRevision {
		return out, ErrInvalid
	}
	purpose := CommandID(770)
	if q.action == "clear" || q.action == "reread" {
		purpose = 771
	}
	if t.transaction.descriptor.ID != purpose {
		return out, ErrInvalid
	}
	if q.action != "location" && q.action != "clear" && q.action != "reread" {
		return out, ErrInvalid
	}
	if err = t.checkLifetimes(ctx); err != nil {
		return out, err
	}
	p := q.publication
	if p.life == nil || p.life.Err() != nil {
		return out, ErrDisclosureDenied
	}
	if q.action == "location" {
		if err = validateMPReadingLocation(p, q.location); err != nil {
			return out, err
		}
	}
	row, err := wc008DraftQuery(ctx, t, mpReadingPersonal, q.profile, p.source.Item, p.source.PublicID[:])
	if err != nil {
		return out, err
	}
	err = row.Scan(&out.PersonalItem, &out.ReadingRevision, &out.PersonalRevision)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err != nil {
		return out, err
	}
	if out.ReadingRevision != q.expectedReadingRevision || out.ReadingRevision < 0 || out.PersonalRevision < 0 || out.PersonalRevision >= mpReadingMaxRevision {
		return out, ErrConflict
	}
	out.Saved, err = readMPReadingSaved(ctx, t, out.PersonalItem, p.source.Asset)
	if err != nil {
		return out, err
	}
	out.SavedStale = out.Saved.Present && (out.Saved.Token != p.source.Token || out.Saved.Revision != p.source.Revision())
	current, n, err := selectMPReadingIntentSource(ctx, t, p.source, p.conversion, 256<<10)
	if err != nil {
		return out, err
	}
	out.SelectedBytes = n
	out.Source, out.Location = p.source, q.location
	if current.Revision() != p.source.Revision() || current.PublicID != p.source.PublicID || current.PresentationFormat != p.source.PresentationFormat || current.Converted != p.source.Converted {
		out.Stale = true
		out.Reason = "source_changed"
		return out, nil
	}
	row, err = wc008DraftQuery(ctx, t, mpReadingMetadata, p.source.Item)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out.MetadataRevision); err != nil {
		return out, err
	}
	if q.action == "reread" && (q.expectedMetadataRevision <= 0 || q.expectedMetadataRevision != out.MetadataRevision) {
		return out, ErrConflict
	}
	if q.historyPaused && q.action == "location" {
		out.Reason = "history_paused"
		return out, nil
	}
	out.Changed = true
	out.Clear = q.action == "clear"
	out.Reread = q.action == "reread"
	return out, t.checkLifetimes(ctx)
}

// Reuse MP01's exact SQL and byte registrations rather than a weaker parallel
// source decoder. The cap is a parsing bound; UO must fund these reported bytes
// in its one admitted command partition before accepting the proposal.
func selectMPReadingIntentSource(ctx context.Context, t *commandTurn, s read.MPReadingSource, conversion bool, cap int64) (out read.MPReadingSource, n int64, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(t), mpReadingSourceResult{out, n}, err)
		out, n, err = v.source, v.bytes, e
	}()
	var v read.MPReadingSourceValues
	v.Item, v.Asset, v.ItemKind = s.Item, s.Asset, s.ItemKind
	row, err := wc008DraftQuery(ctx, t, mpReadingLibraryKey, s.Item)
	if err != nil {
		return out, 0, err
	}
	if err = row.Scan(&v.Library, &v.LibraryName); err != nil {
		return out, 0, err
	}
	row, err = wc008DraftQuery(ctx, t, "read.browse.mp.reading.item", s.Item, v.Library, s.ItemKind)
	if err != nil {
		return out, 0, err
	}
	if err = row.Scan(&v.PublicID, &v.ItemKind, &v.ItemRetired); err != nil {
		return out, 0, err
	}
	row, err = wc008DraftQuery(ctx, t, "read.browse.mp.reading.library", v.LibraryName)
	if err != nil {
		return out, 0, err
	}
	if err = row.Scan(&v.LibraryKind); err != nil {
		return out, 0, err
	}
	row, err = wc008DraftQuery(ctx, t, "read.browse.mp.reading.retired", s.Item)
	if err != nil {
		return out, 0, err
	}
	var retired int64
	if err = row.Scan(&retired); err == nil {
		return out, 0, ErrDisclosureDenied
	} else if !errors.Is(err, sql.ErrNoRows) {
		return out, 0, err
	}
	row, err = wc008DraftQuery(ctx, t, "read.browse.mp.reading.publication_octets", s.Item, s.Asset, v.Library)
	if err != nil {
		return out, 0, err
	}
	var formatBytes, tokenBytes int64
	if err = row.Scan(&formatBytes, &tokenBytes); err != nil {
		return out, 0, err
	}
	n = 512 + formatBytes + tokenBytes
	if formatBytes < 0 || tokenBytes < 0 || n > cap {
		return out, 0, ErrCap
	}
	row, err = wc008DraftQuery(ctx, t, "read.browse.mp.reading.publication", s.Item, s.Asset, v.Library)
	if err != nil {
		return out, 0, err
	}
	if err = row.Scan(&v.Format, &v.LinkAvailable, &v.Token, &v.Size, &v.ModifiedNS, &v.AssetAvailable, &v.AssetLibrary); err != nil {
		return out, 0, err
	}
	object, octets := "read.browse.mp.reading.object", "read.browse.mp.reading.object_octets"
	if v.Format == "cb7" || v.Format == "cbr" {
		object, octets = "read.browse.mp.reading.local_object", "read.browse.mp.reading.local_object_octets"
	}
	args := []any{v.Token, v.LibraryName, v.Size, v.ModifiedNS, int64(1)}
	row, err = wc008DraftQuery(ctx, t, stmt.ID(octets), args...)
	if err != nil {
		return out, 0, err
	}
	var widths [16]int64
	dest := make([]any, len(widths))
	for i := range widths {
		dest[i] = &widths[i]
	}
	if err = row.Scan(dest...); err != nil {
		return out, 0, err
	}
	for _, w := range widths {
		if w < 0 || w > cap-n {
			return out, 0, ErrCap
		}
		n += w
	}
	row, err = wc008DraftQuery(ctx, t, stmt.ID(object), args...)
	if err != nil {
		return out, 0, err
	}
	if err = row.Scan(&v.ObjectID, &v.ObjectAssetID, &v.ObjectSourceID, &v.ObjectRootIncarnation, &v.RelativePath, &v.ObjectIdentity, &v.InventoryRevision, &v.Evidence, &v.ObjectSize, &v.ObjectModifiedNS, &v.ObjectRetired, &v.ObjectState, &v.UnsupportedReason, &v.SourceID, &v.SourceLibraryID, &v.SourceKind, &v.SourceGeneration, &v.SourceIncarnation, &v.RootIdentity, &v.SourceEnabled, &v.SourceHealth); err != nil {
		return out, 0, err
	}
	out, err = read.DecodeMPReadingSource(v, conversion)
	return
}

type mpReadingSourceResult struct {
	source read.MPReadingSource
	bytes  int64
}

// Per-source rows survive source replacement/deletion; no asset FK or inferred
// coordinate conversion is introduced. UO alone writes/clears these children.
type mpReadingSavedPosition struct {
	Present                   bool
	Token, Revision, Format   string
	CFI, Section              sql.NullString
	Page                      sql.NullInt64
	Progression               float64
	ChildRevision, ObservedMS int64
}

func readMPReadingSaved(ctx context.Context, t *commandTurn, personal, asset int64) (out mpReadingSavedPosition, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if personal == 0 {
		return out, nil
	}
	row, err := wc008DraftQuery(ctx, t, mpReadingSavedBytes, personal, asset)
	if err != nil {
		return out, err
	}
	var n int64
	if err = row.Scan(&n); errors.Is(err, sql.ErrNoRows) {
		return out, nil
	} else if err != nil {
		return out, err
	}
	if n < 0 || n > 8192 {
		return out, ErrCap
	}
	row, err = wc008DraftQuery(ctx, t, mpReadingSavedRead, personal, asset)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out.Token, &out.Revision, &out.Format, &out.CFI, &out.Section, &out.Page, &out.Progression, &out.ChildRevision, &out.ObservedMS); err != nil {
		return out, err
	}
	out.Present = true
	return out, nil
}
