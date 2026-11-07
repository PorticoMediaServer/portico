package read

import (
	"fmt"
	"strings"
)

// Test-only compiled recipes exercise the seam; no production Reading or
// restricted-container capability is installed by this foundation PR.
const (
	rsTestReadingPolicy      readPolicyID = 65000
	rsTestContainerPolicy    readPolicyID = 65001
	rsTestReadingAliasPolicy readPolicyID = 65002
)

func init() {
	registerReadPolicy(readPolicyRecipe{id: rsTestReadingAliasPolicy, domain: "test-reading-alias", kinds: []int64{20}, validate: rsTestReadingEligibility})
	registerReadPolicy(readPolicyRecipe{id: rsTestReadingPolicy, domain: "test-reading", kinds: []int64{20, 21}, validate: rsTestReadingEligibility})
	registerReadPolicy(readPolicyRecipe{id: rsTestContainerPolicy, domain: "test-container", kinds: []int64{2}, validate: rsTestContainerEligibility})
}
func rsTestPolicyRoot(q browseQuery, in BrowseInput, f browseFence) ([]any, error) {
	if in.policy.subject <= 0 {
		return nil, ErrInput
	}
	rows, err := q("foreground.source", in.policy.subject, in.Library, in.Kind)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 || !admittedItem(rows[0][16:23], f) {
		return nil, ErrDenied
	}
	return rows[0], nil
}
func rsTestReadingEligibility(q browseQuery, in BrowseInput, f browseFence) (string, error) {
	row, err := rsTestPolicyRoot(q, in, f)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%x:%v", row[0], row[3], row[22]), nil
}
func rsTestContainerEligibility(q browseQuery, in BrowseInput, f browseFence) (string, error) {
	root, err := rsTestPolicyRoot(q, in, f)
	if err != nil {
		return "", err
	}
	rows, err := q("foreground.children", in.policy.subject, int64(0), int64(MaxRaw))
	if err != nil {
		return "", err
	}
	if len(rows) == MaxRaw {
		return "", ErrUnqualified
	} // Exhaustion is required, never inferred from a full raw window.
	var witness strings.Builder
	fmt.Fprintf(&witness, "%d:%x;", root[0], root[3])
	visible := false
	for _, row := range rows {
		if row[8].(int64) != 0 {
			return "", ErrUnqualified
		} // No guessed recursive container proof.
		fmt.Fprintf(&witness, "%d:%d:%d:%v;", row[0], row[4], row[5], row[6])
		visible = visible || admittedItem(row, f)
	}
	if !visible {
		return "", ErrDenied
	}
	return witness.String(), nil
}
