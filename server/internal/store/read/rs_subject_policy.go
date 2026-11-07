package read

import (
	"crypto/sha256"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const rsDetailSubjectPolicy readPolicyID = 42

// Detail has different empty-container semantics from a browse listing:
// EntityRestrictionSQL admits a container with no membership; hierarchy browse
// additionally requires members/linked TV episodes. This recipe grants only
// this exact subject detail. It never opens the default browse recipe.
func init() {
	registerReadPolicy(readPolicyRecipe{id: rsDetailSubjectPolicy, domain: "rs-subject-detail", kinds: []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13}, validate: rsDetailSubjectEligibility})
}
func rsDetailSubjectEligibility(q browseQuery, in BrowseInput, f browseFence) (string, error) {
	if in.policy.subject <= 0 {
		return "", ErrInput
	}
	rows, err := q("rs.subject.eligibility", in.policy.subject, in.Library, in.Kind)
	if err != nil {
		return "", err
	}
	if len(rows) != 1 {
		return "", ErrDenied
	}
	r := rows[0]
	if in.Kind == 10 {
		automatic, err := q("rs.subject.automatic", in.policy.subject)
		if err != nil {
			return "", err
		}
		// Source8's authored-membership tuple does not prove inferred R21 members.
		// Automatic detail must select its separately closed complete-set recipe.
		if len(automatic) != 0 {
			return "", ErrUnqualified
		}
	}
	if r[3].(int64) != 0 {
		return "", ErrDenied
	}
	if f.container == 0 {
		if r[4] == nil && f.allowUnrated == 0 || r[4] != nil && f.maximumAge >= 0 && r[4].(int64) > f.maximumAge {
			return "", ErrDenied
		}
	} else {
		// These are the canonical ACCEPTED admission tuple, not pending scalar
		// contributions. The common fence already requires age_input==published and
		// zero pending operations. An active image must also match its installed tuple.
		count, unrated := r[5].(int64), r[6].(int64)
		if count < 0 || unrated < 0 || unrated > count || count > unrated && r[7] == nil || count == unrated && r[7] != nil {
			return "", ErrUnqualified
		}
		if r[12] != nil {
			publications, err := q("rs.subject.publication", r[12], r[0])
			if err != nil {
				return "", err
			}
			if len(publications) != 1 {
				return "", ErrUnqualified
			}
			p := publications[0]
			if p[0] != "published" && p[0] != "folding" {
				return "", ErrUnqualified
			}
			if !equalSource(r[5:10], p[3:8]) {
				return "", ErrUnqualified
			}
			rows = append(rows, p)
		}
		permitted := count == 0 || f.allowUnrated != 0 && unrated > 0 || r[7] != nil && (f.maximumAge < 0 || r[7].(int64) <= f.maximumAge)
		if !permitted {
			return "", ErrDenied
		}
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%#v", rows)))), nil
}
func rsDetailEligibilityDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	return []stmt.Definition{
		{ID: readStatementID("rs.subject.automatic"), SQL: `SELECT 1 FROM catalog_automatic_collections INDEXED BY catalog_automatic_collection_entity WHERE entity_id=? LIMIT 1`, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_subject_policy.go:rsDetailSubjectEligibility"}, Params: []stmt.Param{i("item")}, Cohorts: []stmt.Cohort{{Name: "empty", Bindings: []any{int64(1)}}, {Name: "absent", Bindings: []any{int64(999999)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"catalog_automatic_collection_entity"}}},
		{ID: readStatementID("rs.subject.eligibility"), SQL: `SELECT id,library_id,kind,retired,admission_own_age,admission_member_count,admission_unrated_members,admission_min_known_age,admission_allow_age,admission_known_age,admission_revision,aggregate_revision,aggregate_publication_id,metadata_incarnation,scalar_source_revision,music_membership_revision,tv_hierarchy_revision,collection_revision,source_revision,identity_revision,metadata_revision FROM catalog_entities WHERE id=? AND library_id=? AND kind=?`, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_subject_policy.go:rsDetailSubjectEligibility"}, Params: []stmt.Param{i("item"), i("library"), i("kind")}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(1), int64(1), int64(1)}}, {Name: "empty", Bindings: []any{int64(999999), int64(1), int64(1)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}}},
		{ID: readStatementID("rs.subject.publication"), SQL: `SELECT phase,captured_revision,generation,admission_member_count,admission_unrated_members,admission_min_known_age,admission_allow_age,admission_known_age FROM catalog_aggregate_publications WHERE id=? AND parent_id=?`, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_subject_policy.go:rsDetailSubjectEligibility"}, Params: []stmt.Param{i("publication"), i("parent")}, Cohorts: []stmt.Cohort{{Name: "empty", Bindings: []any{int64(1), int64(1)}}, {Name: "absent", Bindings: []any{int64(999999), int64(1)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_aggregate_publications"}}},
	}
}
