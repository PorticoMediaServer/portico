package metadata

import (
	"context"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

func TestTVDBPublicationProviderPageBudgets(t *testing.T) {
	for _, kind := range []string{"rows", "bytes", "page_cursor"} {
		t.Run(kind, func(t *testing.T) {
			s, db := tvdbPublicationDB(t)
			tvdbPublicationSelected(t, s)
			p := tvdbPublicationClaim(t, s)
			page := metadataprovider.EpisodePage{SeriesID: 42, Order: "absolute", Page: 0}
			if kind == "rows" {
				for i := 0; i < 1001; i++ {
					page.Episodes = append(page.Episodes, tvdbPublicationEpisode(int64(i+1), i+1))
				}
			}
			if kind == "bytes" {
				for i := 0; i < 257; i++ {
					v := tvdbPublicationEpisode(int64(i+1), i+1)
					v.Overview = strings.Repeat("x", 65536)
					page.Episodes = append(page.Episodes, v)
				}
			}
			if kind == "page_cursor" {
				p.Page = 99
				page.Page = 99
				next := 100
				page.NextPage = &next
			}
			if err := s.commitTVDBPage(context.Background(), *p, page); err == nil {
				t.Fatal("over-budget page accepted")
			}
			if tvdbPublicationText(t, db, `SELECT count(*) FROM tvdb_episode_evidence`) != "0" || tvdbPublicationText(t, db, `SELECT count(*) FROM tvdb_publication_pages`) != "0" {
				t.Fatal("over-budget page partly staged")
			}
		})
	}
}

// The capacity is 20,000 episodes (20 pages of 1,000), so this runs in the
// release and deep performance tiers; the per-page budget tests above guard
// the same staging rules in the default suite.
func TestTVDBPublicationWholeSetRowCapacity(t *testing.T) {
	requireScaleTier(t)
	s, db := tvdbPublicationDB(t)
	tvdbPublicationSelected(t, s)
	for pageNumber := 0; pageNumber < 20; pageNumber++ {
		p := tvdbPublicationClaim(t, s)
		next := pageNumber + 1
		page := metadataprovider.EpisodePage{SeriesID: 42, Order: "absolute", Page: pageNumber, NextPage: &next}
		for i := 0; i < 1000; i++ {
			number := pageNumber*1000 + i + 1
			page.Episodes = append(page.Episodes, tvdbPublicationEpisode(int64(number), number))
		}
		if err := s.commitTVDBPage(context.Background(), *p, page); err != nil {
			t.Fatal(pageNumber, err)
		}
	}
	p := tvdbPublicationClaim(t, s)
	if err := s.commitTVDBPage(context.Background(), *p, metadataprovider.EpisodePage{SeriesID: 42, Order: "absolute", Page: 20, Episodes: []metadataprovider.Episode{tvdbPublicationEpisode(20001, 20001)}}); err == nil {
		t.Fatal("set exceeded20000")
	}
	if tvdbPublicationText(t, db, `SELECT count(*) FROM tvdb_episode_evidence`) != "20000" || tvdbPublicationText(t, db, `SELECT count(*) FROM tvdb_publication_pages`) != "20" {
		t.Fatal("capacity failure changed accepted pages")
	}
}
