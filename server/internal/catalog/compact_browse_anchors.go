package catalog

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// compactBrowseSortAnchors reads the projector's fixed bucket axes. It is
// called only for a default pivot (no ad hoc expression); policy classes have
// their own published bucket generation and must be fully settled first.
func (s *Service) compactBrowseSortAnchors(request BrowseRequest, pivot browsePivot, primary BrowseSortSelection) ([]BrowsePositionAnchor, error) {
	// An ad hoc query has no anchors, and neither has For you: a taste order
	// has no letters or years to jump to.
	if request.Query != nil || primary.Field == "forYou" {
		return []BrowsePositionAnchor{}, nil
	}
	// A sort without a counted ordering (Aired) has no positions to anchor.
	if definition, ok := browseSortByID(primary.Field); ok && definition.Ordering < 0 && !definition.Profile {
		return []BrowsePositionAnchor{}, nil
	}
	if err := s.browseReady(request.Library); err != nil {
		return nil, err
	}
	var classID int
	var generation int64
	if request.Restrictions.Active() {
		key, published, ready, err := s.publishedVisibilityClass(request.Library, request.Restrictions)
		if err != nil {
			return nil, err
		}
		if !ready {
			return []BrowsePositionAnchor{}, nil
		}
		generation = published
		_, classID, err = s.currentCategoryClass(request.Library, key, generation)
		if err != nil {
			return nil, err
		}
		var pending bool
		if err = s.read().QueryRow(`SELECT (SELECT backfill_done=0 FROM catalog_visibility_sort_state WHERE id=1)`).Scan(&pending); err != nil {
			return nil, err
		}
		if pending {
			return []BrowsePositionAnchor{}, nil
		}
	}
	var libraryID int
	if err := s.read().QueryRow(`SELECT id FROM catalog_libraries WHERE library_id=?`, request.Library).Scan(&libraryID); err != nil {
		return nil, err
	}
	kinds := []int{}
	for _, name := range entityKindMembers(pivot) {
		kind, err := compactcatalog.ParseKind(name)
		if err != nil {
			return nil, err
		}
		kinds = append(kinds, int(kind))
	}
	if len(kinds) == 0 {
		return []BrowsePositionAnchor{}, nil
	}
	axes := []int{}
	switch primary.Field {
	case "title":
		axes = []int{0}
	case "year":
		axes = []int{1, 2}
	case "added":
		axes = []int{3, 4}
	case "communityRating":
		axes = []int{5}
	case "duration":
		axes = []int{6}
	case "personalRating":
		axes = []int{7}
	case "lastPlayed":
		axes = []int{8, 9}
	default:
		return nil, fmt.Errorf("unsupported browse sort anchor %q", primary.Field)
	}
	for index, axis := range axes {
		var counts map[string]int
		var err error
		if axis >= 7 {
			counts, err = s.readCompactPersonalAxis(request.Profile, libraryID, kinds, classID, generation, axis)
		} else {
			counts, err = s.readCompactBrowseAxis(libraryID, kinds, classID, generation, axis)
		}
		if err != nil {
			return nil, err
		}
		anchors := orderCompactBrowseAnchors(counts, primary, axis)
		if len(anchors) <= maxBrowseAnchors || index == len(axes)-1 {
			if len(anchors) > maxBrowseAnchors {
				anchors = anchors[:maxBrowseAnchors]
			}
			return anchors, nil
		}
	}
	return []BrowsePositionAnchor{}, nil
}

func (s *Service) readCompactPersonalAxis(profile string, libraryID int, kinds []int, classID int, generation int64, axis int) (map[string]int, error) {
	var pending bool
	if err := s.read().QueryRow(`SELECT (SELECT backfill_done=0 FROM catalog_personal_sort_state WHERE id=1)`).Scan(&pending); err != nil {
		return nil, err
	}
	if pending {
		return map[string]int{}, nil
	}
	base, err := s.readCompactBrowseAxis(libraryID, kinds, classID, generation, 0)
	if err != nil {
		return nil, err
	}
	baseTotal := 0
	for _, count := range base {
		baseTotal += count
	}
	query := `SELECT value,total FROM catalog_personal_sort_buckets WHERE profile_id=? AND library_id=? AND kind IN (` + placeholders(len(kinds)) + `) AND axis=? AND total>0`
	args := []any{profile, libraryID}
	if classID != 0 {
		query = `SELECT value,total FROM catalog_personal_visibility_sort_buckets WHERE profile_id=? AND class_id=? AND generation=? AND library_id=? AND kind IN (` + placeholders(len(kinds)) + `) AND axis=? AND total>0`
		args = []any{profile, classID, generation, libraryID}
	}
	for _, kind := range kinds {
		args = append(args, kind)
	}
	args = append(args, axis)
	rows, err := s.read().Query(query, args...)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	personalTotal := 0
	for rows.Next() {
		var key string
		var n int
		if err = rows.Scan(&key, &n); err != nil {
			rows.Close()
			return nil, err
		}
		counts[key] += n
		personalTotal += n
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if personalTotal > baseTotal {
		return map[string]int{}, nil
	}
	defaultKey := ""
	if axis == 7 {
		defaultKey = "0"
	}
	counts[defaultKey] += baseTotal - personalTotal
	return counts, nil
}

func (s *Service) readCompactBrowseAxis(libraryID int, kinds []int, classID int, generation int64, axis int) (map[string]int, error) {
	args := []any{}
	query := `SELECT value,total FROM catalog_browse_buckets WHERE library_id=? AND kind IN (` + placeholders(len(kinds)) + `) AND axis=? AND total>0`
	if classID != 0 {
		query = `SELECT value,total FROM catalog_visibility_sort_buckets WHERE class_id=? AND generation=? AND library_id=? AND kind IN (` + placeholders(len(kinds)) + `) AND axis=? AND total>0`
		args = append(args, classID, generation)
	}
	args = append(args, libraryID)
	for _, kind := range kinds {
		args = append(args, kind)
	}
	args = append(args, axis)
	rows, err := s.read().Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var key string
		var count int
		if err = rows.Scan(&key, &count); err != nil {
			return nil, err
		}
		if (axis == 5 || axis == 6) && key == "" {
			key = "0"
		}
		out[key] += count
	}
	return out, rows.Err()
}

func orderCompactBrowseAnchors(counts map[string]int, primary BrowseSortSelection, axis int) []BrowsePositionAnchor {
	keys := make([]string, 0, len(counts))
	for key, total := range counts {
		if total > 0 {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		less := false
		switch axis {
		case 1, 2, 5, 6, 7:
			a, _ := strconv.Atoi(strings.TrimSuffix(keys[i], "s"))
			b, _ := strconv.Atoi(strings.TrimSuffix(keys[j], "s"))
			if a == b {
				less = keys[i] < keys[j]
			} else {
				less = a < b
			}
		default:
			less = keys[i] < keys[j]
		}
		if primary.Direction == "desc" {
			return !less
		}
		return less
	})
	out := []BrowsePositionAnchor{}
	seen := map[string]bool{}
	position := 0
	for _, key := range keys {
		label := key
		if axis == 0 {
			label = browseHeadLetter(key)
		}
		if !seen[label] {
			out = append(out, BrowsePositionAnchor{Key: label, Index: position})
			seen[label] = true
		}
		position += counts[key]
	}
	return out
}
