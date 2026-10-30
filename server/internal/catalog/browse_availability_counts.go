package catalog

import (
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"strings"
)

// Single availability predicates have exact maintained counts. Restriction
// keys admit whole playable buckets; containers keep their member-aware SQL.
func (s *Service) availabilitySummary(library string, kinds []string, node *BrowseNode, restrictions identity.ContentRestrictions) (browseSummary, error) {
	out := browseSummary{}
	if node == nil || node.Field != "availability" {
		return out, nil
	}
	if restrictions.Active() {
		key, generation, ready, err := s.publishedVisibilityClass(library, restrictions)
		if err != nil {
			return out, err
		}
		if ready {
			compiler := browseCompiler{}
			field, _ := browseFieldByID("availability")
			field.Entity = "availability_state"
			predicate, err := compiler.entityPredicate(field, *node)
			if err != nil {
				return out, err
			}
			args := []any{key, generation, library}
			for _, kind := range kinds {
				k, err := compactcatalog.ParseKind(kind)
				if err != nil {
					return out, err
				}
				args = append(args, k)
			}
			args = append(args, compiler.args...)
			err = s.read().QueryRow(`SELECT COALESCE(sum(total),0) FROM compact_visibility_availability_counts WHERE class_id=(SELECT id FROM compact_visibility_classes WHERE class_key=?) AND generation=? AND library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND kind IN (`+placeholders(len(kinds))+`) AND `+predicate, args...).Scan(&out.total)
			if err == nil {
				// A deep page reads the same class's rows (filteredRangePage).
				err = s.read().QueryRow(`SELECT id FROM compact_visibility_classes WHERE class_key=?`, key).Scan(&out.classID)
			}
			out.exact = err == nil
			if out.exact {
				out.classKey, out.generation = key, generation
			}
			return out, err
		}
	}
	if len(restrictions.MemberDeniedLabels) > 0 {
		return out, nil
	}
	if restrictions.Active() {
		ambiguous, err := s.ambiguousRestrictionKeys()
		if err != nil {
			return out, err
		}
		if ambiguous {
			return out, nil
		}
	}
	compiler := browseCompiler{}
	field, _ := browseFieldByID("availability")
	field.Entity = "availability_state"
	predicate, err := compiler.entityPredicate(field, *node)
	if err != nil {
		return out, err
	}
	args := []any{library}
	for _, kind := range kinds {
		k, err := compactcatalog.ParseKind(kind)
		if err != nil {
			return out, err
		}
		args = append(args, k)
	}
	args = append(args, compiler.args...)
	blocked := map[string]bool{}
	for _, label := range restrictions.BlockedLabels {
		blocked[strings.ToLower(label)] = true
	}
	var ages ratingAges
	if restrictions.Active() {
		ages, err = s.ratingAges()
		if err != nil {
			return out, err
		}
	}
	countScope := "container=-1"
	if restrictions.Active() {
		countScope = "container>=0"
	}
	rows, err := s.read().Query(`SELECT CASE WHEN container<0 THEN 0 ELSE container END,rating_key,label_key,total FROM catalog_browse_availability_counts WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND kind IN (`+placeholders(len(kinds))+`) AND `+predicate+` AND `+countScope, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var b browseBucket
		if err = rows.Scan(&b.container, &b.ratingKey, &b.labelKey, &b.total); err != nil {
			return out, err
		}
		if b.container && restrictions.Active() {
			return browseSummary{}, nil
		}
		if !restrictions.Active() || bucketVisible(b, ages, restrictions, blocked) {
			out.total += b.total
		}
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	out.exact = true
	return out, nil
}
