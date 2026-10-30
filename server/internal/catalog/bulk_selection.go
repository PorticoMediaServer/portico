package catalog

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func (s *Service) bulkSelection(p identity.Principal, v Viewer, in JobRequest) (string, []any, error) {
	if s.readTx == nil && dbwork.Snapshot(s.Context()) == nil && !dbwork.InWriteTx(s.Context()) {
		var query string
		var args []any
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var err error
			query, args, err = s.WithContext(ctx).bulkSelection(p, v, in)
			return err
		})
		return query, args, err
	}
	selector := in.Selector
	metadataEdit := in.Command == "metadata-edit"
	n := 0
	where := ""
	from := "catalog_entities i"
	distinct := ""
	args := []any{}
	library := ""
	revision := ""
	if selector.Items != nil {
		n++
		ids := selector.Items.IDs
		if len(ids) == 0 || len(ids) > 200 {
			return "", nil, jobInvalid("items.ids must contain 1 to 200 IDs")
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if id == "" || len(id) > 256 || seen[id] {
				return "", nil, jobInvalid("invalid or duplicate item ID")
			}
			seen[id] = true
		}
		where = `i.public_id IN(SELECT pid_blob(value) FROM json_each(?))`
		args = append(args, idsJSON(ids))
	}
	if selector.Container != nil {
		n++
		c := selector.Container
		if c.ID == "" || len(c.ID) > 256 {
			return "", nil, jobInvalid("invalid container ID")
		}
		switch c.Kind {
		case "library":
			library = c.ID
			where = `i.library_id=(SELECT id FROM catalog_libraries WHERE library_id=? AND retired=0)`
			args = append(args, c.ID)
		case "playlist":
			_, _, deleted, e := catalogPlaylistRole(s.read(), c.ID, ResourceActor{Authority: p.Authority, AccountID: p.AccountID, ProfileID: p.ProfileID})
			if e != nil {
				return "", nil, e
			}
			if deleted {
				return "", nil, sql.ErrNoRows
			}
			where = `i.id IN(SELECT entry.item_id FROM catalog_playlist_entries entry JOIN catalog_playlists playlist ON playlist.id=entry.playlist_id WHERE playlist.token=? AND playlist.deleted=0)`
			args = append(args, c.ID)
		case "show", "season", "album", "artist", "book", "collection":
			var kind string
			if e := s.read().QueryRow(`SELECT `+compactBrowseKindSQL("row.kind")+`,l.library_id FROM catalog_browse_rows row JOIN catalog_libraries l ON l.id=row.library_id WHERE row.entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) LIMIT 1`, c.ID).Scan(&kind, &library); e != nil {
				return "", nil, e
			}
			if kind != c.Kind {
				return "", nil, sql.ErrNoRows
			}
			if metadataEdit {
				where = `i.public_id=pid_blob(?)`
				args = append(args, c.ID)
				break
			}
			from = `catalog_browse_memberships bm JOIN catalog_entities i ON i.id=bm.item_id`
			distinct = "DISTINCT "
			where = `bm.entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`
			args = append(args, c.ID)
		default:
			return "", nil, jobInvalid("unsupported container kind")
		}
	}
	if selector.Query != nil {
		n++
		q := selector.Query
		if err := s.checkBrowseSelectionBasis(v, q.LibraryID, q.SelectionBasis); err != nil {
			return "", nil, err
		}
		var node *BrowseNode
		if len(q.Filter) > 0 {
			decoder := json.NewDecoder(bytes.NewReader(q.Filter))
			decoder.DisallowUnknownFields()
			if e := decoder.Decode(&node); e != nil {
				return "", nil, jobInvalid("invalid query filter")
			}
		}
		library = q.LibraryID
		lib, e := s.library(library)
		if e != nil {
			return "", nil, e
		}
		pivot, ok := pivotForKind(lib.Kind, q.Pivot)
		if !ok || !pivot.Browsable {
			return "", nil, ErrBrowsePivot
		}
		sorts, e := resolveBrowseSorts(pivot, q.Sort)
		if e != nil {
			return "", nil, e
		}
		scope, values := browseScopeSQL(library, pivot)
		c := &browseCompiler{now: s.recommendationNow(s.browseInstant), profile: v.Profile, restrictions: v.EffectiveRestrictions()}
		readingEligible, e := s.bindReadingBrowse(library, c)
		if e != nil {
			return "", nil, e
		}
		scope += " AND " + readingEligible
		// Newly supported search forms replay only through the bounded worker.
		// Validate intent here without executing the compiler's filter SQL.
		if selectionBatchHasLocalSearch(node) {
			if selector.Items != nil || selector.Container != nil {
				return "", nil, jobInvalid("selector requires exactly one of container, query, items")
			}
			bound, e := s.bulkReplayBound(v, q)
			if e != nil {
				return "", nil, e
			}
			if e = selectionBatchSearchQualified(bound.Query); e != nil {
				return "", nil, e
			}
			if s.ReadPreparations() == nil {
				return "", nil, ErrSelectionBatchCapacity
			}
			if in.Expected != nil && in.Expected.CatalogRevision != "" {
				// Home/jobs and Browse have different private viewer fences. Bind the
				// COMPLETE cached full query to the live held basis. This proof
				// replaces a private page revision comparison across route fences.
				proof, e := s.resolvePreparedBrowseSelection(v, *q, in.Expected.CatalogRevision, false)
				if e != nil {
					return "", nil, e
				}
				proof.Release()
			}
			return bulkReplaySelection, nil, nil
		}
		filter, e := c.compile(node)
		if e != nil {
			return "", nil, e
		}
		if metadataEdit {
			where = `i.id IN(SELECT e.entity_id FROM catalog_browse_rows e WHERE ` + scope + ` AND ` + filter + `)`
		} else {
			where = `i.id IN(SELECT bm.item_id FROM catalog_browse_rows e JOIN catalog_browse_memberships bm ON bm.entity_id=e.entity_id WHERE ` + scope + ` AND ` + filter + `)`
		}
		args = append(args, values...)
		args = append(args, c.args...)
		revision, e = s.browseRevision(BrowseRequest{Library: library, Profile: v.Profile, ViewerFence: v.Fence, Restrictions: v.EffectiveRestrictions(), Query: node}, pivot, sorts)
		if e != nil {
			return "", nil, e
		}
	}
	if n != 1 {
		return "", nil, jobInvalid("selector requires exactly one of container, query, items")
	}
	needsBrowse := selector.Query != nil || selector.Container != nil && selector.Container.Kind != "playlist" && selector.Container.Kind != "library"
	if needsBrowse {
		if err := s.compactProjectionReady(18, 19); err != nil {
			return "", nil, err
		}
	}
	if selector.Query != nil || selector.Container != nil && selector.Container.Kind != "playlist" && selector.Container.Kind != "library" {
		if err := s.browseReady(library); err != nil {
			return "", nil, err
		}
	}
	if library != "" && !v.AllowsLibrary(library) {
		return "", nil, sql.ErrNoRows
	}
	if in.Expected != nil && in.Expected.CatalogRevision != "" {
		if revision == "" {
			if library == "" {
				return "", nil, jobInvalid("catalogRevision requires a library-scoped selector")
			}
			r, e := s.ContentRevision(library, v.Profile)
			if e != nil {
				return "", nil, e
			}
			revision = strconv.FormatInt(r.Catalog, 10)
		}
		if in.Expected.CatalogRevision != revision {
			return "", nil, ErrStaleContinuation
		}
	}
	visible, bound := v.itemVisibilitySQL("i.id")
	if metadataEdit {
		visible, bound = v.entityVisibilitySQL("i.id", "l.library_id")
		args = append(args, bound...)
		query := `SELECT ` + distinct + `i.id AS id FROM ` + from + ` JOIN catalog_libraries l ON l.id=i.library_id JOIN catalog_kinds k ON k.id=i.kind WHERE k.name IN('movie','episode','song','audiobook_file','show','season','album','artist','book') AND i.retired=0 AND ` + where + ` AND ` + visible + recordingsClause("i.id", v.EffectiveRestrictions())
		var kinds int
		if err := s.read().QueryRow(`SELECT count(DISTINCT kind) FROM (`+query+`) selected JOIN catalog_entities target ON target.id=selected.id`, args...).Scan(&kinds); err != nil {
			return "", nil, err
		}
		if kinds > 1 {
			return "", nil, jobInvalid("metadata editing needs one displayed content kind")
		}
		return query + ` ORDER BY i.id`, args, nil
	}
	args = append(args, bound...)
	// Only explicit collection-add picks admit Reading. Query/container and every
	// other job retain the playable-only producer unchanged.
	kindClause := "k.playable=1"
	if in.Command == "collection-add" && selector.Items != nil {
		reading, err := s.explicitReadingCollectionSQL(p, v, selector.Items.IDs)
		if err != nil {
			return "", nil, err
		}
		kindClause = "(k.playable=1 OR (" + reading + "))"
	}
	return `SELECT ` + distinct + `i.id AS id FROM ` + from + ` JOIN catalog_kinds k ON k.id=i.kind AND ` + kindClause + ` WHERE i.retired=0 AND ` + where + ` AND ` + visible + recordingsClause("i.id", v.EffectiveRestrictions()) + ` AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=i.id) ORDER BY i.id`, args, nil
}

func (s *Service) BulkFailures(profile, id, cursor string, v Viewer) (JobFailures, error) {
	out := JobFailures{Items: []JobFailure{}}
	job, e := s.BulkJob(profile, id)
	if e != nil {
		return out, e
	}
	after := int64(0)
	if cursor != "" {
		n, e := strconv.ParseInt(cursor, 10, 64)
		if e != nil || n < 0 {
			return out, ErrCursor
		}
		after = n
	}
	visible, args := v.itemVisibilitySQL("i.id")
	kindClause := "k.playable=1"
	if job.Command == "metadata-edit" {
		visible, args = v.entityVisibilitySQL("i.id", "l.library_id")
		kindClause = "k.name IN('movie','episode','song','audiobook_file','show','season','album','artist','book')"
	}
	args = append([]any{id, after}, args...)
	query := `SELECT f.ordinal,pid(i.public_id),f.code FROM personal_job_failures f JOIN catalog_entities i ON i.id=f.item_id JOIN catalog_libraries l ON l.id=i.library_id JOIN catalog_kinds k ON k.id=i.kind AND ` + kindClause + ` WHERE f.job_id=? AND f.ordinal>? AND i.retired=0 AND ` + visible + recordingsClause("i.id", v.EffectiveRestrictions()) + ` ORDER BY f.ordinal LIMIT 101`
	if job.Command == "playlist-add" || job.Command == "collection-add" {
		// Membership failures retain their captured public identity after the
		// ordinary captured item rows are purged or integer IDs are reused.
		query = `SELECT f.rowid,pid(i.public_id),f.code FROM personal_job_membership_outcomes f JOIN catalog_entities i ON i.public_id=f.item_public_id JOIN catalog_kinds k ON k.id=i.kind AND k.playable=1 WHERE f.job_id=? AND f.rowid>? AND f.outcome='failed' AND i.retired=0 AND ` + visible + recordingsClause("i.id", v.EffectiveRestrictions()) + ` ORDER BY f.rowid LIMIT 101`
	}
	rows, e := s.read().Query(query, args...)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	var last int64
	for rows.Next() {
		var n int64
		var f JobFailure
		if e = rows.Scan(&n, &f.ItemID, &f.Code); e != nil {
			return out, e
		}
		if len(out.Items) == 100 {
			out.NextCursor = strconv.FormatInt(last, 10)
			break
		}
		out.Items = append(out.Items, f)
		last = n
	}
	return out, rows.Err()
}
