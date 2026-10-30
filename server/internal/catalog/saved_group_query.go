package catalog

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

type savedGroupLibrary struct {
	ID         string   `json:"id"`
	Handle     int64    `json:"handle"`
	Kind       string   `json:"kind"`
	Generation int64    `json:"generation"`
	Formats    []string `json:"formats"`
}
type savedGroupContext struct {
	Count, Revision int64
	Libraries       string
	Fence           string
}

func (s *Service) savedGroupContext(r ContentRequest) (savedGroupContext, error) {
	var out savedGroupContext
	if err := s.read().QueryRow(`SELECT count(*) FROM reading_saved_groups WHERE owner_key=? AND server_id=? AND listed=1`, r.Profile, r.ServerID).Scan(&out.Count); err != nil {
		return out, err
	}
	if out.Count == 0 {
		return out, nil
	}
	if out.Count < 0 || out.Count > 9007199254740991 {
		return out, ErrReadingProjectionChanged
	}
	if err := s.read().QueryRow(`SELECT revision FROM reading_saved_group_owners WHERE owner_key=? AND server_id=?`, r.Profile, r.ServerID).Scan(&out.Revision); err != nil {
		return out, err
	}
	if out.Revision < 1 || out.Revision > 9007199254740991 {
		return out, ErrReadingProjectionChanged
	}
	rows, err := s.read().Query(`SELECT DISTINCT g.library_id,cl.id,lib.kind FROM reading_saved_groups g
 LEFT JOIN catalog_libraries cl ON cl.library_id=g.library_id AND cl.retired=0 LEFT JOIN libraries lib ON lib.id=cl.library_id
 WHERE g.owner_key=? AND g.server_id=? AND g.listed=1 ORDER BY g.library_id`, r.Profile, r.ServerID)
	if err != nil {
		return out, err
	}
	var libraries []savedGroupLibrary
	for rows.Next() {
		var id string
		var handle sql.NullInt64
		var kind sql.NullString
		if err = rows.Scan(&id, &handle, &kind); err != nil {
			rows.Close()
			return out, err
		}
		if !r.Viewer.AllowsLibrary(id) || !handle.Valid || !kind.Valid || !isReadingLibrary(kind.String) {
			continue
		}
		libraries = append(libraries, savedGroupLibrary{ID: id, Handle: handle.Int64, Kind: kind.String})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	var witnesses []string
	for i := range libraries {
		generation, err := compactcatalog.ReadingProjectionReadyTx(s.Context(), s.readTxOrSnapshot(), libraries[i].Handle)
		if errors.Is(err, compactcatalog.ErrBuilding) {
			return out, ErrReadingProjectionChanged
		}
		if err != nil {
			return out, err
		}
		libraries[i].Generation = generation.Ready
		scope := readingBrowseScope{library: libraries[i].Handle, kind: libraries[i].Kind, generation: generation, mobi: libraries[i].Kind == "book" && s.readingMobiConfigured()}
		libraries[i].Formats = scope.sourceFormats()
		witnesses = append(witnesses, libraries[i].ID+":"+scope.fence())
	}
	if libraries == nil {
		libraries = []savedGroupLibrary{}
	}
	encoded, err := json.Marshal(libraries)
	if err != nil {
		return out, err
	}
	out.Libraries = string(encoded)
	sort.Strings(witnesses)
	fence, err := json.Marshal(struct {
		Server    string
		Revision  int64
		Libraries []string
	}{r.ServerID, out.Revision, witnesses})
	if err != nil {
		return out, err
	}
	out.Fence = fmt.Sprintf("%x", sha256.Sum256(fence))
	return out, nil
}

type savedMixedAnchor struct {
	Kind      int    `json:"kind"`
	Entity    int64  `json:"entity"`
	Reference string `json:"reference"`
}

func (a savedMixedAnchor) encode() string {
	raw, _ := json.Marshal(a)
	return base64.RawURLEncoding.EncodeToString(raw)
}
func decodeSavedMixedAnchor(raw string) (savedMixedAnchor, error) {
	var out savedMixedAnchor
	if len(raw) > 512 {
		return out, ErrCursor
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || json.Unmarshal(data, &out) != nil {
		return out, ErrCursor
	}
	if out.encode() != raw || !(out.Kind == 0 && out.Entity > 0 && out.Reference == "" || out.Kind == 1 && out.Entity == 0 && personalOperation.MatchString(out.Reference)) {
		return out, ErrCursor
	}
	return out, nil
}
func savedMixedOrderDigest(value string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
}

// All group members/labels/counts are derived before the candidate seek/LIMIT.
// Hidden references retain only an own private slot; no stale target cache exists.
func savedMixedCTE(r ContentRequest, groups savedGroupContext, leafBase string, leafArgs []any, order string) (string, []any) {
	visible, visibleArgs := r.Viewer.itemVisibilitySQL("e.id")
	eligible := readingEligiblePublicationSQL("e.id", "json_extract(j.value,'$.handle')", "json_extract(j.value,'$.kind')", "json_extract(j.value,'$.formats')")
	query := `WITH refs AS MATERIALIZED(SELECT reference_id,library_id,group_kind,series_key,revision FROM reading_saved_groups WHERE owner_key=? AND server_id=? AND listed=1),
 members AS MATERIALIZED(SELECT r.reference_id,r.group_kind,m.series_id,m.series_value,m.series_source,m.numbered_order,m.number_value,m.sort_key,m.issue_date,e.id AS entity,pid(e.public_id) AS public_id
 FROM refs r JOIN json_each(?) j ON json_extract(j.value,'$.id')=r.library_id
 JOIN reading_attribution_members m ON m.library_id=json_extract(j.value,'$.handle') AND m.generation=json_extract(j.value,'$.generation') AND m.series_key=r.series_key COLLATE BINARY
 JOIN catalog_entities e ON e.id=m.item_id AND e.library_id=m.library_id
 WHERE e.retired=0 AND m.series_id IS NOT NULL AND m.series_value IS NOT NULL AND ` + eligible + ` AND (` + visible + `)
 AND (r.group_kind='series' OR (json_extract(j.value,'$.kind')='book' AND m.publication_type=r.group_kind
 AND ((r.group_kind='magazine' AND length(m.issue_date) IN(7,10)) OR (r.group_kind='newspaper' AND length(m.issue_date)=10))))),
 counts AS(SELECT reference_id,count(*) AS total,min(series_id COLLATE BINARY) AS first_id,max(series_id COLLATE BINARY) AS last_id FROM members GROUP BY reference_id),
 spellings AS(SELECT reference_id,series_value,count(*) AS frequency,sum(series_source='owner') AS owners FROM members GROUP BY reference_id,series_value),
 labels AS(SELECT reference_id,series_value,row_number() OVER(PARTITION BY reference_id ORDER BY owners>0 DESC,CASE WHEN owners>0 THEN owners ELSE frequency END DESC,series_value COLLATE BINARY) AS choice FROM spellings),
 covers AS(SELECT reference_id,entity,public_id,row_number() OVER(PARTITION BY reference_id ORDER BY
 CASE WHEN group_kind='series' THEN numbered_order ELSE 0 END,
 CASE WHEN group_kind='series' THEN number_value ELSE 0 END,
 CASE WHEN group_kind='series' THEN sort_key ELSE '' END COLLATE BINARY,
 CASE WHEN group_kind<>'series' THEN issue_date ELSE '' END DESC,
 CASE WHEN group_kind='series' THEN entity ELSE 0 END,
 CASE WHEN group_kind<>'series' THEN entity ELSE 0 END DESC) AS choice FROM members),
 groups AS(SELECT r.reference_id,r.library_id,r.group_kind,r.revision,coalesce(c.total,0) AS total,
 coalesce(c.first_id,'') AS series_id,coalesce(c.last_id,'') AS last_id,coalesce(l.series_value,'') AS title,
 coalesce(cover.entity,0) AS cover_entity,coalesce(cover.public_id,'') AS cover_public_id,c.total IS NULL AS hidden
 FROM refs r LEFT JOIN counts c ON c.reference_id=r.reference_id LEFT JOIN labels l ON l.reference_id=r.reference_id AND l.choice=1
 LEFT JOIN covers cover ON cover.reference_id=r.reference_id AND cover.choice=1),
 candidates AS(SELECT 0 AS card_kind,i.id AS entity_id,'' AS reference_id,` + order + ` AS order_value,
 0 AS reference_revision,'' AS library_id,'' AS group_kind,'' AS series_id,'' AS last_id,'' AS title,0 AS total,0 AS cover_entity,'' AS cover_public_id,0 AS hidden` + leafBase + savedShapePredicate(r.Shape) + savedFilters[r.Filter] + `
 UNION ALL SELECT 1,0,g.reference_id,`
	groupOrder := `''`
	if savedNumericSorts[r.Sort] {
		groupOrder = `0.0`
	} else if r.Sort == "title" {
		groupOrder = `lower(g.title)`
	}
	query += groupOrder + `,g.revision,g.library_id,g.group_kind,g.series_id,g.last_id,g.title,g.total,g.cover_entity,g.cover_public_id,g.hidden FROM groups g)`
	args := append([]any{r.Profile, r.ServerID, groups.Libraries}, visibleArgs...)
	return query, append(args, leafArgs...)
}

// Preserve the explicit transaction used by both normal and group projections.
func (s *Service) readTxOrSnapshot() *sql.Tx {
	if s.readTx != nil {
		return s.readTx
	}
	return dbwork.Snapshot(s.Context())
}
