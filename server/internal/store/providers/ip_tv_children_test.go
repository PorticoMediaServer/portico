package providers

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/enrich"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

func tvChildEpisodeFor(id string, season, episode, absolute int64, order TVChildNumbering) TVChildEpisode {
	return TVChildEpisode{EvidenceID: id, Order: order, SeasonNumber: season, EpisodeNumber: episode,
		AbsoluteNumber: absolute, SeasonPresent: true, EpisodePresent: true, AbsolutePresent: true}
}

func tvChildSeriesObservation(numbering TVChildNumbering) TVChildObservation {
	return TVChildObservation{Kind: enrich.Series, Numbering: numbering, DeclaredSeasons: 2,
		Seasons: []TVChildSeason{
			{SeasonNumber: 1, DeclaredEpisodes: 2, Episodes: []TVChildEpisode{
				tvChildEpisodeFor("episode-1", 1, 1, 3, numbering),
				tvChildEpisodeFor("episode-2", 1, 2, 1, numbering),
			}},
			{SeasonNumber: 2, DeclaredEpisodes: 1, Episodes: []TVChildEpisode{
				tvChildEpisodeFor("episode-3", 2, 1, 2, numbering),
			}},
		}}
}

func TestIPTVChildrenCompleteNormalization(t *testing.T) {
	o := tvChildSeriesObservation(TVAired)
	set, err := NormalizeTVChildren(o)
	if err != nil {
		t.Fatal(err)
	}
	if set.Kind != enrich.Series || set.Numbering != TVAired || !set.Complete() {
		t.Fatal(set)
	}
	want := []string{"episode-1", "episode-2", "episode-3"}
	if !reflect.DeepEqual(set.EvidenceIDs(), want) {
		t.Fatal(set.EvidenceIDs())
	}
	for i, member := range set.Members {
		if member.Ordinal != int64(i) || member.EvidenceID != want[i] {
			t.Fatal(i, member)
		}
	}
	if set.MemberDigest() == ([32]byte{}) || set.ChildDigest() == ([32]byte{}) {
		t.Fatal("empty digest")
	}
	wantDigest, err := writer.TVSetMemberDigest(want)
	if err != nil || set.MemberDigest() != wantDigest {
		t.Fatal("member digest mismatch", set.MemberDigest(), wantDigest, err)
	}
	// Re-normalizing the same complete declaration is stable.
	again, err := NormalizeTVChildren(o)
	if err != nil || again.MemberDigest() != set.MemberDigest() ||
		again.ChildDigest() != set.ChildDigest() || !reflect.DeepEqual(again.Members, set.Members) {
		t.Fatal("normalization not stable", again, err)
	}
	// A season set publishes only its one season.
	season := TVChildObservation{Kind: enrich.Season, Numbering: TVAired, DeclaredSeasons: 1,
		Seasons: []TVChildSeason{{SeasonNumber: 2, DeclaredEpisodes: 1,
			Episodes: []TVChildEpisode{tvChildEpisodeFor("episode-3", 2, 1, 2, TVAired)}}}}
	seasonSet, err := NormalizeTVChildren(season)
	if err != nil || len(seasonSet.Members) != 1 || seasonSet.Members[0].EvidenceID != "episode-3" {
		t.Fatal(seasonSet, err)
	}
}

func TestIPTVChildrenDuplicateEpisodes(t *testing.T) {
	base := tvChildSeriesObservation(TVAired)
	cases := []struct {
		name   string
		mutate func(*TVChildObservation)
	}{
		{"duplicate_evidence", func(o *TVChildObservation) {
			o.Seasons[1].Episodes[0].EvidenceID = "episode-1"
		}},
		{"duplicate_aired_coordinate", func(o *TVChildObservation) {
			o.Seasons[1].Episodes[0].SeasonNumber, o.Seasons[1].Episodes[0].EpisodeNumber = 1, 1
		}},
		{"duplicate_season_number", func(o *TVChildObservation) {
			o.Seasons[1].SeasonNumber = 1
			o.Seasons[1].Episodes[0].SeasonNumber = 1
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := base
			o.Seasons = append([]TVChildSeason(nil), base.Seasons...)
			for i := range o.Seasons {
				o.Seasons[i].Episodes = append([]TVChildEpisode(nil), base.Seasons[i].Episodes...)
			}
			tc.mutate(&o)
			if set, err := NormalizeTVChildren(o); err == nil || !errors.Is(err, ErrTVChildren) ||
				!reflect.DeepEqual(set, TVChildSet{}) {
				t.Fatal("duplicate admitted", set, err)
			}
		})
	}
	absolute := TVChildObservation{Kind: enrich.Series, Numbering: TVAbsolute, DeclaredSeasons: 1,
		Seasons: []TVChildSeason{{SeasonNumber: 1, DeclaredEpisodes: 2, Episodes: []TVChildEpisode{
			tvChildEpisodeFor("episode-1", 1, 1, 4, TVAbsolute),
			tvChildEpisodeFor("episode-2", 1, 2, 4, TVAbsolute),
		}}}}
	if set, err := NormalizeTVChildren(absolute); err == nil || !errors.Is(err, ErrTVChildren) {
		t.Fatal("duplicate absolute coordinate admitted", set, err)
	}
}

func TestIPTVChildrenNumberingNamespaces(t *testing.T) {
	aired, err := NormalizeTVChildren(tvChildSeriesObservation(TVAired))
	if err != nil {
		t.Fatal(err)
	}
	absolute, err := NormalizeTVChildren(tvChildSeriesObservation(TVAbsolute))
	if err != nil {
		t.Fatal(err)
	}
	// Aired order is season then episode; absolute order is the absolute
	// coordinate. The same provider rows therefore publish different orders.
	if got := aired.EvidenceIDs(); !reflect.DeepEqual(got, []string{"episode-1", "episode-2", "episode-3"}) {
		t.Fatal(got)
	}
	if got := absolute.EvidenceIDs(); !reflect.DeepEqual(got, []string{"episode-2", "episode-3", "episode-1"}) {
		t.Fatal(got)
	}
	if aired.MemberDigest() == absolute.MemberDigest() || aired.ChildDigest() == absolute.ChildDigest() {
		t.Fatal("numbering namespaces collapsed")
	}
	// An absolute-labelled row cannot enter an aired set, and vice versa.
	mixed := tvChildSeriesObservation(TVAired)
	mixed.Seasons = append([]TVChildSeason(nil), mixed.Seasons...)
	mixed.Seasons[0].Episodes = append([]TVChildEpisode(nil), mixed.Seasons[0].Episodes...)
	mixed.Seasons[0].Episodes[0].Order = TVAbsolute
	if set, err := NormalizeTVChildren(mixed); err == nil || !errors.Is(err, ErrTVChildren) {
		t.Fatal("mixed numbering admitted", set, err)
	}
	// The chosen namespace coordinate must be present.
	missingAired := tvChildSeriesObservation(TVAired)
	missingAired.Seasons = append([]TVChildSeason(nil), missingAired.Seasons...)
	missingAired.Seasons[0].Episodes = append([]TVChildEpisode(nil), missingAired.Seasons[0].Episodes...)
	missingAired.Seasons[0].Episodes[0].EpisodePresent = false
	if set, err := NormalizeTVChildren(missingAired); err == nil || !errors.Is(err, ErrTVChildren) {
		t.Fatal("missing aired coordinate admitted", set, err)
	}
	missingAbsolute := tvChildSeriesObservation(TVAbsolute)
	missingAbsolute.Seasons = append([]TVChildSeason(nil), missingAbsolute.Seasons...)
	missingAbsolute.Seasons[0].Episodes = append([]TVChildEpisode(nil), missingAbsolute.Seasons[0].Episodes...)
	missingAbsolute.Seasons[0].Episodes[0].AbsolutePresent = false
	if set, err := NormalizeTVChildren(missingAbsolute); err == nil || !errors.Is(err, ErrTVChildren) {
		t.Fatal("missing absolute coordinate admitted", set, err)
	}
	// An alternative season coordinate must not disagree with its containing
	// season, but it never orders the set.
	mismatched := tvChildSeriesObservation(TVAbsolute)
	mismatched.Seasons = append([]TVChildSeason(nil), mismatched.Seasons...)
	mismatched.Seasons[0].Episodes = append([]TVChildEpisode(nil), mismatched.Seasons[0].Episodes...)
	mismatched.Seasons[0].Episodes[0].SeasonNumber = 2
	if set, err := NormalizeTVChildren(mismatched); err == nil || !errors.Is(err, ErrTVChildren) {
		t.Fatal("mismatched alternative season admitted", set, err)
	}
}

func TestIPTVChildrenNeverPartial(t *testing.T) {
	base := tvChildSeriesObservation(TVAired)
	cases := []struct {
		name   string
		mutate func(*TVChildObservation)
	}{
		{"no_declared_seasons", func(o *TVChildObservation) { o.DeclaredSeasons = 0 }},
		{"declared_seasons_mismatch", func(o *TVChildObservation) { o.DeclaredSeasons = 3 }},
		{"declared_episodes_short", func(o *TVChildObservation) { o.Seasons[0].DeclaredEpisodes = 1 }},
		{"declared_episodes_long", func(o *TVChildObservation) { o.Seasons[0].DeclaredEpisodes = 3 }},
		{"empty_season", func(o *TVChildObservation) {
			o.DeclaredSeasons = 2
			o.Seasons[0].DeclaredEpisodes = 0
			o.Seasons[0].Episodes = nil
			o.Seasons[1].DeclaredEpisodes = 0
			o.Seasons[1].Episodes = nil
		}},
		{"season_kind_multiple_seasons", func(o *TVChildObservation) { o.Kind = enrich.Season }},
		{"movie_kind", func(o *TVChildObservation) { o.Kind = enrich.Movie }},
		{"person_kind", func(o *TVChildObservation) { o.Kind = enrich.Person }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := base
			o.Seasons = append([]TVChildSeason(nil), base.Seasons...)
			for i := range o.Seasons {
				o.Seasons[i].Episodes = append([]TVChildEpisode(nil), base.Seasons[i].Episodes...)
			}
			tc.mutate(&o)
			if set, err := NormalizeTVChildren(o); err == nil || !errors.Is(err, ErrTVChildren) ||
				!reflect.DeepEqual(set, TVChildSet{}) {
				t.Fatal("partial admitted", set, err)
			}
		})
	}
	// A declared season with zero episodes is legal only when another season
	// carries the complete set.
	withEmpty := tvChildSeriesObservation(TVAired)
	withEmpty.Seasons = append(withEmpty.Seasons, TVChildSeason{SeasonNumber: 3, DeclaredEpisodes: 0})
	withEmpty.DeclaredSeasons = 3
	if set, err := NormalizeTVChildren(withEmpty); err != nil || len(set.Members) != 3 {
		t.Fatal("complete set with empty season refused", set, err)
	}
}

func TestIPTVChildrenBoundsAndMalformed(t *testing.T) {
	base := tvChildSeriesObservation(TVAired)
	longID := strings.Repeat("x", maxTVChildText+1)
	cases := []struct {
		name   string
		mutate func(*TVChildObservation)
	}{
		{"empty_evidence", func(o *TVChildObservation) { o.Seasons[0].Episodes[0].EvidenceID = "" }},
		{"long_evidence", func(o *TVChildObservation) { o.Seasons[0].Episodes[0].EvidenceID = longID }},
		{"invalid_utf8", func(o *TVChildObservation) { o.Seasons[0].Episodes[0].EvidenceID = string([]byte{0xff}) }},
		{"nul_evidence", func(o *TVChildObservation) { o.Seasons[0].Episodes[0].EvidenceID = "episode\x001" }},
		{"negative_season", func(o *TVChildObservation) {
			o.Seasons[0].SeasonNumber = -1
			o.Seasons[0].Episodes[0].SeasonNumber = -1
		}},
		{"season_over_max", func(o *TVChildObservation) {
			o.Seasons[0].SeasonNumber = maxTVSeasonNumber + 1
			o.Seasons[0].Episodes[0].SeasonNumber = maxTVSeasonNumber + 1
		}},
		{"negative_episode", func(o *TVChildObservation) { o.Seasons[0].Episodes[0].EpisodeNumber = -1 }},
		{"episode_over_max", func(o *TVChildObservation) {
			o.Seasons[0].Episodes[0].EpisodeNumber = maxTVEpisodeNumber + 1
		}},
		{"negative_declared_episodes", func(o *TVChildObservation) { o.Seasons[0].DeclaredEpisodes = -1 }},
		{"negative_declared_seasons", func(o *TVChildObservation) { o.DeclaredSeasons = -1 }},
		{"bad_numbering", func(o *TVChildObservation) { o.Numbering = "production" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := base
			o.Seasons = append([]TVChildSeason(nil), base.Seasons...)
			for i := range o.Seasons {
				o.Seasons[i].Episodes = append([]TVChildEpisode(nil), base.Seasons[i].Episodes...)
			}
			tc.mutate(&o)
			if set, err := NormalizeTVChildren(o); err == nil || !errors.Is(err, ErrTVChildren) {
				t.Fatal("malformed admitted", set, err)
			}
		})
	}
	// Absolute zero is not a coordinate.
	absoluteZero := TVChildObservation{Kind: enrich.Series, Numbering: TVAbsolute, DeclaredSeasons: 1,
		Seasons: []TVChildSeason{{SeasonNumber: 1, DeclaredEpisodes: 1, Episodes: []TVChildEpisode{
			tvChildEpisodeFor("episode-1", 1, 1, 0, TVAbsolute)}}}}
	if set, err := NormalizeTVChildren(absoluteZero); err == nil || !errors.Is(err, ErrTVChildren) {
		t.Fatal("absolute zero admitted", set, err)
	}
	// Too many seasons.
	seasons := make([]TVChildSeason, maxTVChildSeasons+1)
	for i := range seasons {
		seasons[i] = TVChildSeason{SeasonNumber: int64(i)}
	}
	if set, err := NormalizeTVChildren(TVChildObservation{Kind: enrich.Series, Numbering: TVAired,
		DeclaredSeasons: int64(len(seasons)), Seasons: seasons}); err == nil || !errors.Is(err, ErrTVChildren) {
		t.Fatal("too many seasons admitted", set, err)
	}
	// Too many episodes in one season.
	episodes := make([]TVChildEpisode, maxTVEpisodesPerSeason+1)
	for i := range episodes {
		episodes[i] = tvChildEpisodeFor(fmt.Sprintf("episode-%d", i), 1, int64(i), int64(i+1), TVAired)
	}
	if set, err := NormalizeTVChildren(TVChildObservation{Kind: enrich.Series, Numbering: TVAired,
		DeclaredSeasons: 1, Seasons: []TVChildSeason{{SeasonNumber: 1,
			DeclaredEpisodes: int64(len(episodes)), Episodes: episodes}}}); err == nil || !errors.Is(err, ErrTVChildren) {
		t.Fatal("too many episodes admitted", set, err)
	}
	// Too many total members across legal per-season counts.
	total := make([]TVChildSeason, 11)
	for season := range total {
		members := make([]TVChildEpisode, maxTVEpisodesPerSeason)
		for i := range members {
			members[i] = tvChildEpisodeFor(fmt.Sprintf("episode-%d-%d", season, i), int64(season), int64(i), int64(i+1), TVAired)
		}
		total[season] = TVChildSeason{SeasonNumber: int64(season), DeclaredEpisodes: int64(len(members)), Episodes: members}
	}
	if set, err := NormalizeTVChildren(TVChildObservation{Kind: enrich.Series, Numbering: TVAired,
		DeclaredSeasons: int64(len(total)), Seasons: total}); err == nil || !errors.Is(err, ErrTVChildren) {
		t.Fatal("over total members admitted", set, err)
	}
}

func TestIPTVChildrenScalarHeaderNeverCompletesChildren(t *testing.T) {
	// A complete scalar TV header is valid preparation but grants no child
	// completeness: there is no conversion into TVChildObservation, and an
	// empty/absent declaration is refused.
	body := `{"id":42,"name":"Series","overview":"Overview","first_air_date":"2024-02-29",` +
		`"original_name":"Original","tagline":"Tag"}`
	header, err := PrepareTMDBTVHeader(tvPolicy(t), tvObservation(enrich.Series), []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if header.Fields() != TVName|TVOverview|TVDate|TVOriginalName|TVTagline {
		t.Fatal("scalar header not complete", header.Fields())
	}
	for _, o := range []TVChildObservation{
		{Kind: enrich.Series, Numbering: TVAired},
		{Kind: enrich.Series, Numbering: TVAired, DeclaredSeasons: 1},
		{Kind: enrich.Season, Numbering: TVAired, DeclaredSeasons: 1,
			Seasons: []TVChildSeason{{SeasonNumber: 1, DeclaredEpisodes: 0}}},
	} {
		if set, err := NormalizeTVChildren(o); err == nil || !errors.Is(err, ErrTVChildren) ||
			!reflect.DeepEqual(set, TVChildSet{}) {
			t.Fatal("scalar header completed children", set, err)
		}
	}
}
