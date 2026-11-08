package read

import (
	"cmp"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

// These are canonical source coordinates, not presentation labels or a stored
// match set. A continuation never grants authority to its boundary subject.
type rsSearchOrderKey struct {
	Subject             search.ManifestSubject
	ID, Library, Kind   int64
	SortKey, AddedAt    string
	Year                int64
	Tier, PeopleSubtier uint8
}

func rsSearchOrderKeyFor(h FullSearchHit) rsSearchOrderKey {
	return rsSearchOrderKey{Subject: h.Subject, ID: h.ID, Library: h.Library, Kind: h.Kind, SortKey: h.SortKey, AddedAt: h.AddedAt, Year: h.Year, Tier: h.Tier, PeopleSubtier: h.PeopleSubtier}
}
func rsSearchOrderFor(group RSSearchGroup, order string) string {
	if order == "releaseYear" || order == "dateAdded" {
		switch group {
		case RSSearchPeople, 5, 10, 13:
			return "title"
		}
	}
	return order
}
func rsSearchDefaultDirection(order string) string {
	if order == "title" {
		return "asc"
	}
	return "desc"
}
func rsSearchCompareKey(a, b rsSearchOrderKey, order, direction string) int {
	var c int
	switch order {
	case "relevance":
		c = -compareFullRank(canonicalCandidateProof{key: fullResultKey{a.Subject, a.ID, a.Library}, tier: a.Tier, subtier: a.PeopleSubtier}, canonicalCandidateProof{key: fullResultKey{b.Subject, b.ID, b.Library}, tier: b.Tier, subtier: b.PeopleSubtier})
	case "title":
		if a.Subject == search.ManifestPerson {
			c = strings.Compare(a.SortKey, b.SortKey)
		} else {
			c = rsListeningKeyCompare(a.SortKey, b.SortKey)
		}
	case "releaseYear":
		c = cmp.Compare(a.Year, b.Year)
	case "dateAdded":
		c = strings.Compare(a.AddedAt, b.AddedAt)
	}
	if c == 0 {
		c = cmp.Compare(a.ID, b.ID)
	}
	if c == 0 {
		c = cmp.Compare(a.Library, b.Library)
	}
	if c == 0 {
		c = cmp.Compare(a.Subject, b.Subject)
	}
	if direction == "desc" {
		c = -c
	}
	return c
}
func rsSearchCompare(a, b FullSearchHit, order, direction string) int {
	if order == "relevance" {
		c := compareFullRank(hitRank(a), hitRank(b))
		if direction == "asc" {
			return -c
		}
		return c
	}
	return rsSearchCompareKey(rsSearchOrderKeyFor(a), rsSearchOrderKeyFor(b), order, direction)
}
