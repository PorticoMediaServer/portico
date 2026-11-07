package writer

import (
	"context"
	"math"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Values only. These do not contain a personal executor, callback, SQL handle,
// grant, source opener or timed-media unit. UO owns all personal/history/root
// mutation and native reading receipt/floor state for purposes 770 and 771.
type mpReadingLocationValue struct {
	format, cfi, section string
	page                 int64
	hasPage              bool
	progression          float64
}
type mpReadingSelection struct {
	item, asset, size, modified, sourceGeneration, preparedRevision, metadataRevision                                                       int64
	publicID                                                                                                                                [16]byte
	source, sourceID, objectID, objectIdentity, objectRevision, sourceIncarnation, rootIdentity, relative, revision, originalFormat, format string
}
type mpReadingPublication struct {
	selection  mpReadingSelection
	sections   []string        // actual complete spine captured from the selected bytes
	pages      int64           // actual structural PDF/comic count; zero = unknown PDF count
	sourceLife context.Context // held actual acquisition, never a request bool
}
type mpReadingPersonalValues struct {
	// Supplied by UO's native current root read in the SAME W turn.
	authority, account, profile, member, lifetime, profileKey                string
	readingRevision, personalRevision, historyGeneration, activityGeneration int64
	paused                                                                   bool
}
type mpReadingEffect struct {
	action, reason, profileKey, account, profile, member, lifetime                                                            string
	selection                                                                                                                 mpReadingSelection
	location                                                                                                                  mpReadingLocationValue
	expectedReadingRevision, nextReadingRevision, expectedPersonalRevision, observedMS, historyGeneration, activityGeneration int64
	changed, clear, reread                                                                                                    bool
}
type mpReadingInput struct {
	token, action                                     string
	expectedReadingRevision, expectedMetadataRevision int64
	publication                                       mpReadingPublication
	location                                          mpReadingLocationValue
	personal                                          mpReadingPersonalValues
	now                                               time.Time
}

func mpValidateReadingLocation(p mpReadingPublication, l mpReadingLocationValue) error {
	if p.selection.originalFormat != "" && p.selection.originalFormat != p.selection.format && !(p.selection.format == "epub" && (p.selection.originalFormat == "mobi" || p.selection.originalFormat == "azw3")) {
		return ErrInvalid
	}
	if l.format != p.selection.format || math.IsNaN(l.progression) || math.IsInf(l.progression, 0) || l.progression < 0 || l.progression > 1 {
		return ErrInvalid
	}
	switch l.format {
	case "epub":
		if l.hasPage || len(l.cfi) > 4096 || len(l.section) > 1024 || !utf8.ValidString(l.cfi) || !strings.HasPrefix(l.cfi, "epubcfi(/") || !strings.HasSuffix(l.cfi, ")") || strings.ContainsAny(l.cfi, "<>") || strings.IndexFunc(l.cfi, unicode.IsControl) >= 0 {
			return ErrInvalid
		}
		if l.section != "" {
			if !utf8.ValidString(l.section) || path.IsAbs(l.section) || path.Clean(l.section) != l.section || l.section == "." || strings.HasPrefix(l.section, "../") || strings.ContainsAny(l.section, "\\\x00") || strings.IndexFunc(l.section, unicode.IsControl) >= 0 {
				return ErrInvalid
			}
			found := false
			for _, s := range p.sections {
				if s == l.section {
					found = true
					break
				}
			}
			if !found {
				return ErrInvalid
			}
		}
	case "pdf", "cbz", "cb7", "cbr":
		if !l.hasPage || l.page < 0 || l.page > 1_000_000 || l.cfi != "" || l.section != "" || p.pages < 0 || p.pages > 0 && l.page >= p.pages || l.format != "pdf" && p.pages == 0 {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func mpReadingDefinitions() []stmt.Definition {
	return []stmt.Definition{
		mpDefinition("mp.reading.source_bytes", `SELECT length(e.public_id)+length(CAST(o.object_identity AS BLOB))+length(CAST(o.revision AS BLOB))+length(CAST(o.relative_path AS BLOB))+length(CAST(s.incarnation AS BLOB))+length(CAST(s.root_identity AS BLOB))+length(CAST(p.format AS BLOB)) FROM reading_publications p JOIN catalog_entities e ON e.id=p.item_id JOIN inventory_objects o ON o.id=? JOIN library_sources s ON s.id=o.source_id WHERE p.item_id=? AND p.asset_id=?`, "sii", stmt.Read),
		mpDefinition("mp.reading.source", `SELECT e.public_id,e.metadata_revision,a.size,a.modified_ns,a.prepared_source_revision,o.object_identity,o.revision,o.relative_path,s.generation,s.incarnation,s.root_identity,p.format
 FROM reading_publications p JOIN catalog_entities e ON e.id=p.item_id
 JOIN catalog_asset_links link ON link.entity_id=e.id AND link.asset_id=p.asset_id
 JOIN catalog_assets a ON a.id=p.asset_id
 JOIN inventory_objects o ON o.id=? AND o.asset_id=a.token
 JOIN library_sources s ON s.id=o.source_id
 JOIN catalog_libraries cl ON cl.id=e.library_id AND cl.library_id=s.library_id
 JOIN libraries lib ON lib.id=cl.library_id
 WHERE p.item_id=? AND p.asset_id=? AND a.token=? AND s.id=? AND e.retired=0 AND cl.retired=0 AND link.available=1 AND a.available=1 AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=e.id) AND ((lib.kind='book' AND e.kind=20 AND p.format IN('epub','pdf','mobi','azw3')) OR (lib.kind='comic' AND e.kind=21 AND p.format IN('cbz','cb7','cbr'))) AND o.retired=0 AND o.state='available' AND o.unsupported_reason='' AND o.size=a.size AND o.modified_ns=a.modified_ns AND o.root_incarnation=s.incarnation AND s.enabled=1 AND s.health NOT IN('offline','root_changed','removing') AND (p.format NOT IN('cb7','cbr') OR s.kind='local')`, "siiss", stmt.Read),
		// Read-only CAS witness. UO's lifetime-bound root read establishes the
		// profile key; MP never upserts or increments personal_items itself.
		mpDefinition("mp.reading.cas", `SELECT COALESCE((SELECT COALESCE(reading_revision,0) FROM personal_items INDEXED BY personal_current_live WHERE profile_id=? AND item_id=?),0)`, "si", stmt.Read),
	}
}
func mpCheckReadingSource(ctx context.Context, t *commandTurn, s mpReadingSelection, metadata bool) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if s.item < 1 || s.asset < 1 || s.size < 1 || s.sourceGeneration < 1 || s.preparedRevision < 1 || s.publicID == [16]byte{} || s.revision == "" || s.objectIdentity == "" || s.objectRevision == "" || s.rootIdentity == "" || s.sourceIncarnation == "" || len(s.revision) > 512 {
		return ErrInvalid
	}
	pre, e := mpQuery(ctx, t, "mp.reading.source_bytes", s.objectID, s.item, s.asset)
	if e != nil {
		return e
	}
	var bytes int64
	if e = pre.Scan(&bytes); e != nil {
		return e
	}
	if bytes < 0 || bytes > 16384 {
		return errMPBound
	}
	row, err := mpQuery(ctx, t, "mp.reading.source", s.objectID, s.item, s.asset, s.source, s.sourceID)
	if err != nil {
		return err
	}
	var got mpReadingSelection
	var public []byte
	if err = row.Scan(&public, &got.metadataRevision, &got.size, &got.modified, &got.preparedRevision, &got.objectIdentity, &got.objectRevision, &got.relative, &got.sourceGeneration, &got.sourceIncarnation, &got.rootIdentity, &got.originalFormat); err != nil {
		return err
	}
	if string(public) != string(s.publicID[:]) || metadata && got.metadataRevision != s.metadataRevision || got.size != s.size || got.modified != s.modified || got.preparedRevision != s.preparedRevision || got.objectIdentity != s.objectIdentity || got.objectRevision != s.objectRevision || got.relative != s.relative || got.sourceGeneration != s.sourceGeneration || got.sourceIncarnation != s.sourceIncarnation || got.rootIdentity != s.rootIdentity || got.originalFormat != s.originalFormat {
		return ErrConflict
	}
	return nil
}

// This CLOSED fresh-path component validates source/location/CAS and emits a
// proposal to UO. Parent checks full current item policy before it and consumes
// the effect in UO under the same transaction; only UO may decide evidence and
// increment shared revisions. The receipt saves UO's actual result, not this
// proposal. Original replay bypasses current source CAS, after fresh disclosure.
func mpReadingIntent(ctx context.Context, t *commandTurn, q mpReadingInput) (out mpReadingEffect, err error) {
	defer func() {
		out, err = finishCommand(ctx, commandLifetime(t), out, err)
		out, err = finishCommand(ctx, q.publication.sourceLife, out, err)
	}()
	id := mpReadingLocation
	if q.action == "clear" || q.action == "reread" {
		id = mpReadingClear
	}
	if err = mpPurpose(t, id); err != nil {
		return out, err
	}
	if q.expectedReadingRevision < 0 || q.expectedReadingRevision == math.MaxInt64 || len(q.publication.sections) > 4096 {
		return out, ErrInvalid
	}
	if q.publication.sourceLife == nil || q.publication.sourceLife.Err() != nil {
		return out, ErrInvalid
	}
	a, err := mpCurrentActor(ctx, t, q.token, q.now)
	if err != nil {
		return out, err
	}
	p := q.personal
	if p.authority != "local" || p.account != a.account || p.profile != a.profile || p.member != a.member || p.lifetime != a.lifetime || !mpID(p.profileKey) || p.personalRevision < 0 || p.readingRevision < 0 || p.historyGeneration < 1 || p.activityGeneration < 1 {
		return out, ErrDisclosureDenied
	}
	if err = mpCheckReadingSource(ctx, t, q.publication.selection, q.action == "reread"); err != nil {
		return out, err
	}
	row, err := mpQuery(ctx, t, "mp.reading.cas", p.profileKey, q.publication.selection.item)
	if err != nil {
		return out, err
	}
	var revision int64
	if err = row.Scan(&revision); err != nil {
		return out, err
	}
	if revision != q.expectedReadingRevision || revision != p.readingRevision {
		return out, ErrConflict
	}
	out = mpReadingEffect{action: q.action, profileKey: p.profileKey, account: a.account, profile: a.profile, member: a.member, lifetime: a.lifetime, selection: q.publication.selection, expectedReadingRevision: revision, nextReadingRevision: revision, expectedPersonalRevision: p.personalRevision, observedMS: q.now.UnixMilli(), historyGeneration: p.historyGeneration, activityGeneration: p.activityGeneration}
	switch q.action {
	case "location":
		if err = mpValidateReadingLocation(q.publication, q.location); err != nil {
			return out, err
		}
		out.location = q.location
	case "clear":
		out.clear = true
	case "reread":
		if q.expectedMetadataRevision < 1 || q.expectedMetadataRevision != q.publication.selection.metadataRevision {
			return out, ErrConflict
		}
		out.reread = true
	default:
		return out, ErrInvalid
	}
	if p.paused && q.action == "location" {
		out.reason = "history_paused"
		return out, nil
	}
	out.changed = true
	out.nextReadingRevision++
	return out, nil
}
