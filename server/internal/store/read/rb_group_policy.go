package read

import "fmt"

func rbGroupPolicy(f RBGroupFamily) readPolicyID { return 32300 + readPolicyID(f) }
func init() {
	for _, spec := range rbGroupSpecs() {
		spec := spec
		registerReadPolicy(readPolicyRecipe{id: rbGroupPolicy(spec.family), domain: "rb-group-" + spec.name, kinds: spec.kinds, validate: func(q browseQuery, in BrowseInput, f browseFence) (string, error) {
			// This slice deliberately proves no restricted-container aggregate or
			// label/availability inheritance. The common fence rejects unsupported
			// labels/account policy; this closed recipe also refuses all age policies.
			if f.maximumAge >= 0 || f.allowUnrated == 0 {
				return "", ErrUnqualified
			}
			if in.policy.subject <= 0 {
				return "", ErrInput
			}
			name := "rb.group.subject"
			if spec.family == RBGroupPlaylistEntries {
				name = "rb.group.playlist"
			}
			rows, e := q(name, in.policy.subject)
			if e != nil {
				return "", e
			}
			if len(rows) != 1 {
				return "", ErrDenied
			}
			row := rows[0]
			if spec.family == RBGroupPlaylistEntries {
				if row[1] != "local" || row[2] != f.account || row[3] != f.profile || row[5] != int64(0) {
					return "", ErrDenied
				}
			} else if row[1] != in.Library || row[2] != spec.subjectKind || row[3] != int64(0) {
				return "", ErrDenied
			}
			// Bounded exact comparable source witness, evaluated initial/final. It
			// includes identity/incarnation and accepted revisions, never child metrics.
			return fmt.Sprintf("%v", row), nil
		}})
	}
}
