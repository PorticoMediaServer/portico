package catalog

import (
	"database/sql"
	"sort"
	"strconv"
	"strings"
)

// "Why this row": every personal row explains itself with server-owned copy and,
// where titles are the reason, up to three of the requesting profile's own
// titles. The evidence is read from the same profile signals and catalogue
// projections the rows rank from, so no other profile's history can appear and
// nothing the viewer may not see now is named.

const (
	recWhyFacet      = "next.home.whyFacet"
	recWhyPerson     = "next.home.whyPerson"
	recWhyNext       = "next.home.whyNext"
	recWhyLikes      = "next.home.whyLikes"
	recWhyRediscover = "next.home.whyRediscover"

	// recWhyTitlesMax is the client's bound on the titles behind a row.
	recWhyTitlesMax = 3
	// recWhyOwned bounds one read of the profile's own recent positive signals
	// before the viewer's visibility and restriction are applied. The row's
	// evidence is what the profile watched recently, like the seeds the
	// engine ranks from.
	recWhyOwned = 128
)

// recWhy attaches the row's reason and the profile's own evidence to a
// generated personal row. It is called once the row is chosen, so Home, the
// row's own page and Discover explain the same row the same way.
func (x *recSession) recWhy(row *recRow) error {
	spec := &row.spec
	switch {
	case spec.ID == "for_you:rediscover":
		spec.Why = &ServerText{Code: recWhyRediscover, Fallback: "Favorites you haven't watched in a while."}
		spec.WhyTitles = nil
	case strings.HasPrefix(spec.ID, "for_you:genre:"):
		label := spec.TitleParams["genre"]
		titles, err := x.recWhyFacet("g:" + strings.TrimPrefix(spec.ID, "for_you:genre:"))
		if err != nil {
			return err
		}
		spec.Why = &ServerText{Code: recWhyFacet, Params: map[string]string{"label": label}, Fallback: "Because you often watch " + label + "."}
		spec.WhyTitles = titles
	case strings.HasPrefix(spec.ID, "for_you:tag:"):
		label := recWhyTagLabel(*spec)
		titles, err := x.recWhyFacet("x:" + strings.TrimPrefix(spec.ID, "for_you:tag:"))
		if err != nil {
			return err
		}
		spec.Why = &ServerText{Code: recWhyFacet, Params: map[string]string{"label": label}, Fallback: "Because you often watch " + label + "."}
		spec.WhyTitles = titles
	case strings.HasPrefix(spec.ID, "for_you:person:"):
		facet := strings.TrimPrefix(spec.ID, "for_you:person:")
		name := spec.TitleParams["name"]
		count, err := x.recWhyPersonCount(facet)
		if err != nil {
			return err
		}
		titles, err := x.recWhyFacet(facet)
		if err != nil {
			return err
		}
		spec.Why = &ServerText{Code: recWhyPerson, Params: map[string]string{"count": strconv.Itoa(count), "name": name}, Fallback: recWhyPersonFallback(count, name)}
		spec.WhyTitles = titles
	case spec.ID == "for_you:series" || spec.ID == "for_you:franchise":
		titles, err := x.recWhyNext(row)
		if err != nil {
			return err
		}
		spec.Why = &ServerText{Code: recWhyNext, Fallback: "Next after what you've finished."}
		spec.WhyTitles = titles
	default:
		// Short picks, Hidden gems, Top rated and anything new built like them.
		titles, err := x.recWhySeeds()
		if err != nil {
			return err
		}
		spec.Why = &ServerText{Code: recWhyLikes, Fallback: "Based on what you like."}
		spec.WhyTitles = titles
	}
	return nil
}

// recWhyFacet reads up to three of the profile's own watched or engaged titles
// carrying one facet, strongest first.
func (x *recSession) recWhyFacet(facet string) ([]ContentEntityRef, error) {
	visible, args := recWhyVisible(x.r)
	bind := append([]any{x.r.Profile, facet}, args...)
	rows, err := x.s.read().Query(`SELECT pid(e.public_id),e.title FROM (
 SELECT s.work_id,s.weight,s.at FROM rec_profile_signals s INDEXED BY rec_profile_signals_positive_recent
 WHERE s.profile_id=? AND s.weight>0 AND s.engaged=1 AND s.hidden=0
 AND EXISTS(SELECT 1 FROM catalog_rec_facets f WHERE f.entity_id=s.work_id AND f.facet=?)
 ORDER BY s.at DESC LIMIT `+strconv.Itoa(recWhyOwned)+`) own
 CROSS JOIN catalog_entities e ON e.id=own.work_id
 WHERE `+visible+`
 ORDER BY own.weight DESC,own.at DESC,own.work_id LIMIT `+strconv.Itoa(recWhyTitlesMax), bind...)
	refs, e := scanEntityRefs(rows, err)
	if e != nil {
		return nil, e
	}
	return refs, nil
}

// recWhyTagLabel is a tag row's dataset label, whichever family named it.
func recWhyTagLabel(spec homeRowSpec) string {
	for _, key := range []string{"mood", "theme", "subgenre", "appeal"} {
		if label := spec.TitleParams[key]; label != "" {
			return label
		}
	}
	return ""
}

// recWhyPersonCount is how many titles with one person the profile has watched
// or engaged with.
func (x *recSession) recWhyPersonCount(facet string) (int, error) {
	var count int
	err := x.s.read().QueryRow(`SELECT count(*) FROM catalog_rec_facets f INDEXED BY catalog_rec_facet_titles
 CROSS JOIN rec_profile_signals s ON s.profile_id=? AND s.work_id=f.entity_id
 WHERE f.facet=? AND s.engaged=1 AND s.hidden=0 AND s.weight>0`, x.r.Profile, facet).Scan(&count)
	return count, err
}

// recWhyPersonFallback is the correctly pluralised English sentence for a
// person row (the client renders the ICU plural of the same copy).
func recWhyPersonFallback(count int, name string) string {
	noun := "titles"
	if count == 1 {
		noun = "title"
	}
	return "Because you've watched " + strconv.Itoa(count) + " " + noun + " with " + name + "."
}

// recWhyNext reads the finished titles a series or franchise row continues: the
// profile's own finished works in the same series or collection as the row's
// first titles.
func (x *recSession) recWhyNext(row *recRow) ([]ContentEntityRef, error) {
	if len(row.items) == 0 {
		return nil, nil
	}
	next := row.items
	if len(next) > recWhyTitlesMax {
		next = next[:recWhyTitlesMax]
	}
	ids := make([]string, len(next))
	for i, c := range next {
		ids[i] = c.ID
	}
	visible, args := recWhyVisible(x.r)
	bind := append([]any{idsJSON(ids), x.r.Profile}, args...)
	query := ""
	if row.spec.ID == "for_you:franchise" {
		query = `SELECT pid(e.public_id),e.title FROM json_each(?) next
 CROSS JOIN catalog_entities n ON n.public_id=pid_blob(next.value)
 CROSS JOIN catalog_collection_members nm ON nm.item_id=n.id
 CROSS JOIN catalog_collection_members pm ON pm.collection_id=nm.collection_id
 CROSS JOIN rec_profile_signals own ON own.work_id=pm.item_id AND own.profile_id=? AND own.engaged=1 AND own.finished=1 AND own.hidden=0 AND own.weight>0
 CROSS JOIN catalog_entities e ON e.id=own.work_id
 WHERE e.id<>n.id AND ` + visible + `
 ORDER BY own.weight DESC,own.at DESC,e.id LIMIT ` + strconv.Itoa(recWhyTitlesMax)
	} else {
		query = `SELECT pid(e.public_id),e.title FROM json_each(?) next
 CROSS JOIN catalog_entities n ON n.public_id=pid_blob(next.value)
 CROSS JOIN catalog_book_context nc ON nc.book_id=n.id AND nc.series_key<>''
 CROSS JOIN catalog_book_context pc ON pc.library_id=nc.library_id AND pc.series_key=nc.series_key
 CROSS JOIN rec_profile_signals own ON own.work_id=pc.book_id AND own.profile_id=? AND own.engaged=1 AND own.finished=1 AND own.hidden=0 AND own.weight>0
 CROSS JOIN catalog_entities e ON e.id=own.work_id
 WHERE e.id<>n.id AND ` + visible + `
 ORDER BY own.weight DESC,own.at DESC,e.id LIMIT ` + strconv.Itoa(recWhyTitlesMax)
	}
	rows, err := x.s.read().Query(query, bind...)
	refs, e := scanEntityRefs(rows, err)
	if e != nil {
		return nil, e
	}
	return recWhyUnique(refs), nil
}

// recWhySeeds reads the profile's strongest seed titles, up to three, as the
// viewer may see them.
func (x *recSession) recWhySeeds() ([]ContentEntityRef, error) {
	if len(x.taste.seeds) == 0 {
		return nil, nil
	}
	seeds := append([]recSeed(nil), x.taste.seeds...)
	sort.SliceStable(seeds, func(i, j int) bool {
		if seeds[i].weight != seeds[j].weight {
			return seeds[i].weight > seeds[j].weight
		}
		return seeds[i].work < seeds[j].work
	})
	works := make([]int64, len(seeds))
	for i, seed := range seeds {
		works[i] = seed.work
	}
	visible, args := recWhyVisible(x.r)
	bind := append([]any{idsJSON64(works)}, args...)
	rows, err := x.s.read().Query(`SELECT pid(e.public_id),e.title FROM json_each(?) own CROSS JOIN catalog_entities e ON e.id=own.value
 WHERE `+visible+` ORDER BY own.key LIMIT `+strconv.Itoa(recWhyTitlesMax), bind...)
	refs, e := scanEntityRefs(rows, err)
	if e != nil {
		return nil, e
	}
	return refs, nil
}

// recWhyVisible is what the viewer may see now: a live entity with an available,
// non-retired item (or an available member, for a show, album or book) and no
// DVR retirement, inside the viewer's content restriction — the same test
// recEligible applies to every recommended row. Library membership is not part
// of it: the row's own page and a library's Discover must explain the same
// personal row with the same titles.
func recWhyVisible(r HomeRequest) (string, []any) {
	restriction, args := EntityRestrictionSQL("e.id", r.Restrictions)
	return `e.retired=0
 AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=e.id)
 AND EXISTS(SELECT 1 FROM catalog_browse_rows br WHERE br.entity_id=e.id
  AND (br.available=1 OR br.item_id IS NULL AND EXISTS(SELECT 1 FROM catalog_browse_memberships m CROSS JOIN catalog_item_availability v ON v.entity_id=m.item_id WHERE m.entity_id=e.id AND v.available=1 AND v.retired=0)))
 AND ` + restriction, args
}

func scanEntityRefs(rows *sql.Rows, err error) ([]ContentEntityRef, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := []ContentEntityRef{}
	for rows.Next() {
		var ref ContentEntityRef
		if err = rows.Scan(&ref.ID, &ref.Name); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func recWhyUnique(refs []ContentEntityRef) []ContentEntityRef {
	seen := map[string]bool{}
	out := make([]ContentEntityRef, 0, len(refs))
	for _, ref := range refs {
		if seen[ref.ID] {
			continue
		}
		seen[ref.ID] = true
		out = append(out, ref)
		if len(out) == recWhyTitlesMax {
			break
		}
	}
	return out
}
