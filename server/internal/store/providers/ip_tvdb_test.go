package providers

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/enrich"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

const tvdbTestSeries int64 = 71663

func tvdbFixtureBody(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func tvdbEvidenceIDs(ids ...int64) map[int64]string {
	out := make(map[int64]string, len(ids))
	for _, id := range ids {
		out[id] = "tvdb-evidence-" + strconv.FormatInt(id, 10)
	}
	return out
}

func tvdbTargetFor(ids ...int64) TVDBSetTarget {
	return TVDBSetTarget{SeriesProviderID: tvdbTestSeries, Kind: enrich.Series, Order: TVDBOfficial,
		Language: "eng", Region: "US", PolicyRevision: 3, ConsentRevision: 9,
		EpisodeEvidenceIDs: tvdbEvidenceIDs(ids...)}
}

func tvdbNextURL(page int64) string {
	return "https://api4.thetvdb.com/v4/series/" + strconv.FormatInt(tvdbTestSeries, 10) +
		"/episodes/official?page=" + strconv.FormatInt(page, 10)
}

func tvdbJSONValue(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case string:
		raw, err := json.Marshal(v)
		if err != nil {
			panic(err)
		}
		return string(raw)
	case int64:
		return strconv.FormatInt(v, 10)
	case int:
		return strconv.Itoa(v)
	default:
		panic(fmt.Sprintf("unsupported fixture value %T", value))
	}
}

func tvdbEpisodeRaw(id, series int64, name, overview, season, number, absolute, aired, runtime any) string {
	return `{"id":` + strconv.FormatInt(id, 10) + `,"seriesId":` + strconv.FormatInt(series, 10) +
		`,"name":` + tvdbJSONValue(name) + `,"overview":` + tvdbJSONValue(overview) +
		`,"seasonNumber":` + tvdbJSONValue(season) + `,"number":` + tvdbJSONValue(number) +
		`,"absoluteNumber":` + tvdbJSONValue(absolute) + `,"aired":` + tvdbJSONValue(aired) +
		`,"runtime":` + tvdbJSONValue(runtime) + `}`
}

func tvdbBodyWithNext(series int64, nextJSON string, episodes ...string) []byte {
	return []byte(`{"status":"success","data":{"series":{"id":` + strconv.FormatInt(series, 10) +
		`},"episodes":[` + strings.Join(episodes, ",") + `]},"links":{"next":` + nextJSON + `}}`)
}

func tvdbBody(series int64, next string, episodes ...string) []byte {
	nextJSON := "null"
	if next != "" {
		nextJSON = strconv.Quote(next)
	}
	return tvdbBodyWithNext(series, nextJSON, episodes...)
}

func tvdbAccepted(t *testing.T, target TVDBSetTarget, pages []TVDBRecordedPage) TVDBSet {
	t.Helper()
	set, err := PrepareTVDBSet(target, pages)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func tvdbRefused(t *testing.T, target TVDBSetTarget, pages []TVDBRecordedPage) {
	t.Helper()
	set, err := PrepareTVDBSet(target, pages)
	if err == nil || !errors.Is(err, ErrTVDB) || !reflect.DeepEqual(set, TVDBSet{}) {
		t.Fatalf("accepted invalid observation: %+v, %v", set, err)
	}
}

func TestIPTVDBMultiPageCompleteness(t *testing.T) {
	target := tvdbTargetFor(9001, 9002, 9003, 9004, 9005, 9006)
	pages := []TVDBRecordedPage{
		{Page: 0, Body: tvdbFixtureBody(t, "tvdb-episodes-official-page0.json")},
		{Page: 1, Body: tvdbFixtureBody(t, "tvdb-episodes-official-page1.json")},
		{Page: 2, Body: tvdbFixtureBody(t, "tvdb-episodes-official-page2.json")},
	}
	set := tvdbAccepted(t, target, pages)
	if set.SeriesProviderID != tvdbTestSeries || set.Order != TVDBOfficial ||
		set.Numbering != TVAired || set.Language != "eng" || set.Region != "US" {
		t.Fatalf("capture fields lost: %+v", set)
	}
	if len(set.Pages) != 3 {
		t.Fatal(len(set.Pages))
	}
	for i, page := range set.Pages {
		if page.Page != int64(i) {
			t.Fatal(i, page.Page)
		}
		if i < 2 {
			if page.NextPage == nil || *page.NextPage != int64(i+1) {
				t.Fatalf("page %d next truth: %+v", i, page.NextPage)
			}
		} else if page.NextPage != nil {
			t.Fatalf("terminal page declared next: %+v", page.NextPage)
		}
	}
	if len(set.Pages[0].Episodes) != 2 || len(set.Pages[1].Episodes) != 2 || len(set.Pages[2].Episodes) != 2 {
		t.Fatalf("page row counts: %d %d %d", len(set.Pages[0].Episodes), len(set.Pages[1].Episodes), len(set.Pages[2].Episodes))
	}
	// Aired ordering is season then episode across the whole recorded set; the
	// special (season zero) comes first.
	wantIDs := []string{"tvdb-evidence-9006", "tvdb-evidence-9001", "tvdb-evidence-9002",
		"tvdb-evidence-9003", "tvdb-evidence-9004", "tvdb-evidence-9005"}
	if got := set.Set.EvidenceIDs(); !reflect.DeepEqual(got, wantIDs) {
		t.Fatalf("member order: %v", got)
	}
	if !set.Set.Complete() {
		t.Fatal("set not complete")
	}
	// The descriptor is the IP-08 shape and normalizes to the same manifest.
	if set.Observation.Kind != enrich.Series || set.Observation.Numbering != TVAired ||
		set.Observation.DeclaredSeasons != 3 {
		t.Fatalf("descriptor: %+v", set.Observation)
	}
	var seasonNumbers, seasonCounts []int64
	for _, season := range set.Observation.Seasons {
		seasonNumbers = append(seasonNumbers, season.SeasonNumber)
		seasonCounts = append(seasonCounts, season.DeclaredEpisodes)
	}
	if !reflect.DeepEqual(seasonNumbers, []int64{0, 1, 2}) || !reflect.DeepEqual(seasonCounts, []int64{1, 3, 2}) {
		t.Fatalf("seasons: %v %v", seasonNumbers, seasonCounts)
	}
	again, err := NormalizeTVChildren(set.Observation)
	if err != nil || again.MemberDigest() != set.Set.MemberDigest() || again.ChildDigest() != set.Set.ChildDigest() {
		t.Fatalf("descriptor mismatch: %v %v", again, err)
	}
	wantDigest, err := writer.TVSetMemberDigest(wantIDs)
	if err != nil || set.Set.MemberDigest() != wantDigest {
		t.Fatalf("member digest: %v %v", set.Set.MemberDigest(), err)
	}
	// The manifest projection keeps the recorded page boundaries and next truth
	// but walks the normalized set order, which is the IP-08 page order.
	manifest := set.ManifestPages()
	if len(manifest) != 3 {
		t.Fatalf("manifest pages: %d", len(manifest))
	}
	var manifestIDs []string
	for i, page := range manifest {
		if page.Page != int64(i) || len(page.Episodes) != len(set.Pages[i].Episodes) {
			t.Fatalf("manifest page %d: %+v", i, page)
		}
		if i < 2 && (page.NextPage == nil || *page.NextPage != int64(i+1)) {
			t.Fatalf("manifest next truth %d: %+v", i, page.NextPage)
		}
		for _, episode := range page.Episodes {
			manifestIDs = append(manifestIDs, episode.EvidenceID)
		}
	}
	if !reflect.DeepEqual(manifestIDs, wantIDs) {
		t.Fatalf("manifest order: %v", manifestIDs)
	}
	broken := set
	broken.Set.Members = append([]TVSetMember(nil), set.Set.Members...)
	broken.Set.Members = broken.Set.Members[:2]
	if broken.ManifestPages() != nil {
		t.Fatal("manifest accepted a short member set")
	}
	broken = set
	broken.Set.Members = append([]TVSetMember(nil), set.Set.Members...)
	broken.Set.Members[0].EvidenceID = "missing-evidence"
	if broken.ManifestPages() != nil {
		t.Fatal("manifest accepted an unknown member")
	}
	// A terminal page may declare an empty next string; that is still no next.
	emptyNext := []byte(strings.Replace(string(pages[2].Body), `"next": null`, `"next": ""`, 1))
	empty := tvdbAccepted(t, target, []TVDBRecordedPage{pages[0], pages[1], {Page: 2, Body: emptyNext}})
	if empty.Pages[2].NextPage != nil {
		t.Fatal("empty next not terminal")
	}

	cases := []struct {
		name  string
		pages []TVDBRecordedPage
	}{
		{"missing_last_page", pages[:2]},
		{"reordered_pages", []TVDBRecordedPage{pages[0], pages[2], pages[1]}},
		{"start_not_zero", []TVDBRecordedPage{pages[1], pages[2]}},
		{"duplicate_page", []TVDBRecordedPage{pages[0], pages[1], pages[1]}},
		{"middle_page_terminal", []TVDBRecordedPage{pages[0], {Page: 1, Body: []byte(strings.Replace(string(pages[1].Body), `"next": "https://api4.thetvdb.com/v4/series/71663/episodes/official?page=2"`, `"next": null`, 1))}, pages[2]}},
		{"middle_page_gap", []TVDBRecordedPage{{Page: 0, Body: []byte(strings.Replace(string(pages[0].Body), "?page=1", "?page=2", 1))}, pages[1], pages[2]}},
		{"final_page_declares_next", []TVDBRecordedPage{pages[0], pages[1], {Page: 2, Body: []byte(strings.Replace(string(pages[2].Body), `"next": null`, `"next": "https://api4.thetvdb.com/v4/series/71663/episodes/official?page=3"`, 1))}}},
		{"next_off_origin", []TVDBRecordedPage{{Page: 0, Body: []byte(strings.Replace(string(pages[0].Body), "https://api4.thetvdb.com", "https://api4.thetvdb.com.evil", 1))}, pages[1], pages[2]}},
		{"next_http", []TVDBRecordedPage{{Page: 0, Body: []byte(strings.Replace(string(pages[0].Body), "https://api4.thetvdb.com", "http://api4.thetvdb.com", 1))}, pages[1], pages[2]}},
		{"next_extra_query", []TVDBRecordedPage{{Page: 0, Body: []byte(strings.Replace(string(pages[0].Body), "?page=1", "?page=1&x=1", 1))}, pages[1], pages[2]}},
		{"next_duplicate_page_query", []TVDBRecordedPage{{Page: 0, Body: []byte(strings.Replace(string(pages[0].Body), "?page=1", "?page=1&page=1", 1))}, pages[1], pages[2]}},
		{"next_noncanonical_page", []TVDBRecordedPage{{Page: 0, Body: []byte(strings.Replace(string(pages[0].Body), "?page=1", "?page=01", 1))}, pages[1], pages[2]}},
		{"next_fragment", []TVDBRecordedPage{{Page: 0, Body: []byte(strings.Replace(string(pages[0].Body), "?page=1", "?page=1#frag", 1))}, pages[1], pages[2]}},
		{"next_opaque", []TVDBRecordedPage{{Page: 0, Body: []byte(strings.Replace(string(pages[0].Body), "https://api4.thetvdb.com/v4/series/71663/episodes/official?page=1", "https:opaque", 1))}, pages[1], pages[2]}},
		{"next_bad_port", []TVDBRecordedPage{{Page: 0, Body: []byte(strings.Replace(string(pages[0].Body), "api4.thetvdb.com", "api4.thetvdb.com:444", 1))}, pages[1], pages[2]}},
		{"next_wrong_order_route", []TVDBRecordedPage{{Page: 0, Body: []byte(strings.Replace(string(pages[0].Body), "/episodes/official?page=1", "/episodes/absolute?page=1", 1))}, pages[1], pages[2]}},
		{"next_userinfo", []TVDBRecordedPage{{Page: 0, Body: []byte(strings.Replace(string(pages[0].Body), "https://api4.thetvdb.com", "https://user@api4.thetvdb.com", 1))}, pages[1], pages[2]}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { tvdbRefused(t, target, tc.pages) })
	}
}

func TestIPTVDBNumberingMaps(t *testing.T) {
	official := tvdbAccepted(t, tvdbTargetFor(9001, 9002, 9003, 9004, 9005, 9006),
		[]TVDBRecordedPage{
			{Page: 0, Body: tvdbFixtureBody(t, "tvdb-episodes-official-page0.json")},
			{Page: 1, Body: tvdbFixtureBody(t, "tvdb-episodes-official-page1.json")},
			{Page: 2, Body: tvdbFixtureBody(t, "tvdb-episodes-official-page2.json")},
		})
	if official.Numbering != TVAired || official.Set.Numbering != TVAired {
		t.Fatal("official order did not map to aired")
	}
	target := tvdbTargetFor(9101, 9102, 9103, 9104)
	target.Order = TVDBAbsolute
	absolutePages := []TVDBRecordedPage{
		{Page: 0, Body: tvdbFixtureBody(t, "tvdb-episodes-absolute-page0.json")},
		{Page: 1, Body: tvdbFixtureBody(t, "tvdb-episodes-absolute-page1.json")},
	}
	absolute := tvdbAccepted(t, target, absolutePages)
	if absolute.Numbering != TVAbsolute || absolute.Set.Numbering != TVAbsolute {
		t.Fatal("absolute order did not map to absolute")
	}
	wantIDs := []string{"tvdb-evidence-9102", "tvdb-evidence-9104", "tvdb-evidence-9101", "tvdb-evidence-9103"}
	if got := absolute.Set.EvidenceIDs(); !reflect.DeepEqual(got, wantIDs) {
		t.Fatalf("absolute order: %v", got)
	}
	// The recorded pages are in provider order, so the manifest projection must
	// re-slice the normalized order into the same page boundaries.
	manifest := absolute.ManifestPages()
	var manifestIDs []string
	for _, page := range manifest {
		for _, episode := range page.Episodes {
			manifestIDs = append(manifestIDs, episode.EvidenceID)
		}
	}
	if !reflect.DeepEqual(manifestIDs, wantIDs) {
		t.Fatalf("absolute manifest: %v", manifestIDs)
	}
	if absolute.Pages[0].Episodes[0].EvidenceID != "tvdb-evidence-9101" ||
		manifest[0].Episodes[0].EvidenceID != "tvdb-evidence-9102" {
		t.Fatalf("provider order collapsed into set order")
	}
	// Aired and absolute are distinct namespaces over the exact same recorded
	// rows: the chosen order changes the member order and both digests.
	shared := tvdbBody(tvdbTestSeries, "",
		tvdbEpisodeRaw(9211, tvdbTestSeries, "One", "", int64(1), int64(1), int64(2), "", nil),
		tvdbEpisodeRaw(9212, tvdbTestSeries, "Two", "", int64(1), int64(2), int64(1), "", nil),
		tvdbEpisodeRaw(9213, tvdbTestSeries, "Three", "", int64(1), int64(3), int64(3), "", nil))
	sharedPage := []TVDBRecordedPage{{Page: 0, Body: shared}}
	airedShared := tvdbAccepted(t, tvdbTargetFor(9211, 9212, 9213), sharedPage)
	absoluteSharedTarget := tvdbTargetFor(9211, 9212, 9213)
	absoluteSharedTarget.Order = TVDBAbsolute
	absoluteShared := tvdbAccepted(t, absoluteSharedTarget, sharedPage)
	if !reflect.DeepEqual(airedShared.Set.EvidenceIDs(), []string{"tvdb-evidence-9211", "tvdb-evidence-9212", "tvdb-evidence-9213"}) ||
		!reflect.DeepEqual(absoluteShared.Set.EvidenceIDs(), []string{"tvdb-evidence-9212", "tvdb-evidence-9211", "tvdb-evidence-9213"}) {
		t.Fatalf("shared rows order: %v %v", airedShared.Set.EvidenceIDs(), absoluteShared.Set.EvidenceIDs())
	}
	if airedShared.Set.MemberDigest() == absoluteShared.Set.MemberDigest() ||
		airedShared.Set.ChildDigest() == absoluteShared.Set.ChildDigest() {
		t.Fatal("numbering namespaces collapsed over identical rows")
	}
	for _, order := range []TVDBOrder{TVDBDVD, TVDBDefault, TVDBAlternate, TVDBRegional, "", "production"} {
		bad := tvdbTargetFor(9001, 9002, 9003, 9004, 9005, 9006)
		bad.Order = order
		tvdbRefused(t, bad, []TVDBRecordedPage{
			{Page: 0, Body: tvdbFixtureBody(t, "tvdb-episodes-official-page0.json")},
			{Page: 1, Body: tvdbFixtureBody(t, "tvdb-episodes-official-page1.json")},
			{Page: 2, Body: tvdbFixtureBody(t, "tvdb-episodes-official-page2.json")},
		})
	}

	// A season set declares exactly one season.
	seasonBody := tvdbBody(tvdbTestSeries, "", tvdbEpisodeRaw(9201, tvdbTestSeries, "One", "", int64(1), int64(1), int64(1), "", nil),
		tvdbEpisodeRaw(9202, tvdbTestSeries, "Two", "", int64(1), int64(2), int64(2), "", nil))
	seasonTarget := tvdbTargetFor(9201, 9202)
	seasonTarget.Kind, seasonTarget.Season = enrich.Season, 1
	seasonSet := tvdbAccepted(t, seasonTarget, []TVDBRecordedPage{{Page: 0, Body: seasonBody}})
	if seasonSet.Observation.Kind != enrich.Season || seasonSet.Observation.DeclaredSeasons != 1 ||
		len(seasonSet.Set.EvidenceIDs()) != 2 {
		t.Fatalf("season descriptor: %+v", seasonSet.Observation)
	}
	wrongSeason := seasonTarget
	wrongSeason.Season = 2
	tvdbRefused(t, wrongSeason, []TVDBRecordedPage{{Page: 0, Body: seasonBody}})
	// A series-wide sequence is never silently filtered into one season.
	seriesWide := tvdbTargetFor(9001, 9002, 9003, 9004, 9005, 9006)
	seriesWide.Kind, seriesWide.Season = enrich.Season, 1
	tvdbRefused(t, seriesWide, []TVDBRecordedPage{
		{Page: 0, Body: tvdbFixtureBody(t, "tvdb-episodes-official-page0.json")},
		{Page: 1, Body: tvdbFixtureBody(t, "tvdb-episodes-official-page1.json")},
		{Page: 2, Body: tvdbFixtureBody(t, "tvdb-episodes-official-page2.json")},
	})
	// A season set needs an explicit season coordinate on every row.
	absentSeason := tvdbBody(tvdbTestSeries, "", tvdbEpisodeRaw(9203, tvdbTestSeries, "Unnumbered", "", nil, nil, int64(1), "", nil))
	absentTarget := tvdbTargetFor(9203)
	absentTarget.Order, absentTarget.Kind, absentTarget.Season = TVDBAbsolute, enrich.Season, 0
	tvdbRefused(t, absentTarget, []TVDBRecordedPage{{Page: 0, Body: absentSeason}})
}

func TestIPTVDBPreservesNullAndEmptyCoordinates(t *testing.T) {
	one := func(body []byte, id int64) (TVDBSetTarget, []TVDBRecordedPage) {
		target := tvdbTargetFor(id)
		return target, []TVDBRecordedPage{{Page: 0, Body: body}}
	}
	// A real zero season/episode is present, not absent.
	zeroTarget, zeroPages := one(tvdbBody(tvdbTestSeries, "", tvdbEpisodeRaw(9301, tvdbTestSeries,
		"Zero", "", int64(0), int64(0), int64(1), "", nil)), 9301)
	zero := tvdbAccepted(t, zeroTarget, zeroPages)
	member := zero.Observation.Seasons[0].Episodes[0]
	if !member.SeasonPresent || !member.EpisodePresent || !member.AbsolutePresent ||
		member.SeasonNumber != 0 || member.EpisodeNumber != 0 || member.AbsoluteNumber != 1 {
		t.Fatalf("zero coordinates lost: %+v", member)
	}
	// Nullable absolute coordinates survive as absent, never as zero.
	absentTarget := tvdbTargetFor(9302)
	absentTarget.Order = TVDBAbsolute
	absentBody := tvdbBody(tvdbTestSeries, "", tvdbEpisodeRaw(9302, tvdbTestSeries,
		"Absent", nil, nil, nil, int64(2), nil, nil))
	absent := tvdbAccepted(t, absentTarget, []TVDBRecordedPage{{Page: 0, Body: absentBody}})
	absentMember := absent.Observation.Seasons[0].Episodes[0]
	if absentMember.SeasonPresent || absentMember.EpisodePresent || !absentMember.AbsolutePresent ||
		absentMember.SeasonNumber != 0 || absentMember.EpisodeNumber != 0 {
		t.Fatalf("absent coordinates invented: %+v", absentMember)
	}
	// Empty text is distinct from an absent field only where the descriptor
	// declares presence; an omitted/null overview and aired are legal.
	accepted := []struct {
		id   int64
		body []byte
	}{
		{9303, tvdbBody(tvdbTestSeries, "", tvdbEpisodeRaw(9303, tvdbTestSeries, "Text", nil, int64(1), int64(1), int64(1), nil, nil))},
		{9304, tvdbBody(tvdbTestSeries, "", tvdbEpisodeRaw(9304, tvdbTestSeries, "Text", "", int64(1), int64(1), int64(1), "", int64(0)))},
		{9305, tvdbBody(tvdbTestSeries, "", tvdbEpisodeRaw(9305, tvdbTestSeries, "Text", "", int64(1), int64(1), int64(1), "2020-02-29", int64(maxTVDBRuntime)))},
		// The child descriptor carries no episode text; a null, empty or
		// whitespace-only name is retained by the episode's own evidence header,
		// not invented here.
		{9306, tvdbBody(tvdbTestSeries, "", tvdbEpisodeRaw(9306, tvdbTestSeries, nil, "", int64(1), int64(1), int64(1), "", nil))},
		{9307, tvdbBody(tvdbTestSeries, "", tvdbEpisodeRaw(9307, tvdbTestSeries, "", "", int64(1), int64(2), int64(2), "", nil))},
	}
	for _, tc := range accepted {
		target, pages := one(tc.body, tc.id)
		set := tvdbAccepted(t, target, pages)
		if set.Set.EvidenceIDs()[0] != "tvdb-evidence-"+strconv.FormatInt(tc.id, 10) {
			t.Fatalf("empty text changed identity: %v", set.Set.EvidenceIDs())
		}
	}
	refused := []struct {
		id   int64
		body []byte
	}{
		// Aired order requires both coordinates; null and absent both refuse.
		{9310, tvdbBody(tvdbTestSeries, "", tvdbEpisodeRaw(9310, tvdbTestSeries, "E", "", nil, int64(1), int64(1), "", nil))},
		{9311, tvdbBody(tvdbTestSeries, "", tvdbEpisodeRaw(9311, tvdbTestSeries, "E", "", int64(1), nil, int64(1), "", nil))},
		// Absolute zero is not a coordinate.
		{9312, tvdbBody(tvdbTestSeries, "", tvdbEpisodeRaw(9312, tvdbTestSeries, "E", "", int64(1), int64(1), int64(0), "", nil))},
		// Invalid calendar dates refuse.
		{9316, tvdbBody(tvdbTestSeries, "", tvdbEpisodeRaw(9316, tvdbTestSeries, "E", "", int64(1), int64(1), int64(1), "2023-02-29", nil))},
		{9317, tvdbBody(tvdbTestSeries, "", tvdbEpisodeRaw(9317, tvdbTestSeries, "E", "", int64(1), int64(1), int64(1), "01/02/2020", nil))},
	}
	for _, tc := range refused {
		target, pages := one(tc.body, tc.id)
		tvdbRefused(t, target, pages)
	}
	// Absolute order requires the absolute coordinate; null and absent refuse.
	missingAbsolute := tvdbTargetFor(9318)
	missingAbsolute.Order = TVDBAbsolute
	tvdbRefused(t, missingAbsolute, []TVDBRecordedPage{{Page: 0, Body: tvdbBody(tvdbTestSeries, "",
		tvdbEpisodeRaw(9318, tvdbTestSeries, "E", "", int64(1), int64(1), nil, "", nil))}})
	// Negative and over-bound coordinates refuse.
	bounds := []struct {
		id      int64
		episode string
	}{
		{9320, tvdbEpisodeRaw(9320, tvdbTestSeries, "E", "", int64(-1), int64(1), int64(1), "", nil)},
		{9321, tvdbEpisodeRaw(9321, tvdbTestSeries, "E", "", int64(1), int64(-1), int64(1), "", nil)},
		{9322, tvdbEpisodeRaw(9322, tvdbTestSeries, "E", "", int64(maxTVDBSeason+1), int64(1), int64(1), "", nil)},
		{9323, tvdbEpisodeRaw(9323, tvdbTestSeries, "E", "", int64(1), int64(maxTVDBNumber+1), int64(1), "", nil)},
		{9324, tvdbEpisodeRaw(9324, tvdbTestSeries, "E", "", int64(1), int64(1), int64(maxTVDBAbsolute+1), "", nil)},
		{9325, tvdbEpisodeRaw(9325, tvdbTestSeries, "E", "", int64(1), int64(1), int64(1), "", int64(-1))},
		{9326, tvdbEpisodeRaw(9326, tvdbTestSeries, "E", "", int64(1), int64(1), int64(1), "", int64(maxTVDBRuntime+1))},
	}
	for _, tc := range bounds {
		target := tvdbTargetFor(tc.id)
		tvdbRefused(t, target, []TVDBRecordedPage{{Page: 0, Body: tvdbBody(tvdbTestSeries, "", tc.episode)}})
	}
}

func TestIPTVDBLocaleConsentCapture(t *testing.T) {
	body := tvdbBody(tvdbTestSeries, "", tvdbEpisodeRaw(9401, tvdbTestSeries, "E", "", int64(1), int64(1), int64(1), "", nil))
	pages := []TVDBRecordedPage{{Page: 0, Body: body}}
	base := tvdbTargetFor(9401)
	base.Order = TVDBOfficial
	cases := []struct {
		name   string
		mutate func(*TVDBSetTarget)
	}{
		{"zero_policy", func(q *TVDBSetTarget) { q.PolicyRevision = 0 }},
		{"negative_policy", func(q *TVDBSetTarget) { q.PolicyRevision = -1 }},
		{"zero_consent", func(q *TVDBSetTarget) { q.ConsentRevision = 0 }},
		{"negative_consent", func(q *TVDBSetTarget) { q.ConsentRevision = -2 }},
		{"empty_language", func(q *TVDBSetTarget) { q.Language = "" }},
		{"padded_language", func(q *TVDBSetTarget) { q.Language = " eng" }},
		{"invalid_language", func(q *TVDBSetTarget) { q.Language = string([]byte{0xff}) }},
		{"long_language", func(q *TVDBSetTarget) { q.Language = strings.Repeat("l", maxTVDBLocale+1) }},
		{"long_region", func(q *TVDBSetTarget) { q.Region = strings.Repeat("r", maxTVDBLocale+1) }},
		{"padded_region", func(q *TVDBSetTarget) { q.Region = "US " }},
		{"invalid_region", func(q *TVDBSetTarget) { q.Region = string([]byte{0xff}) }},
		{"zero_nul_language", func(q *TVDBSetTarget) { q.Language = "en\x00" }},
		{"movie_kind", func(q *TVDBSetTarget) { q.Kind = enrich.Movie }},
		{"episode_kind", func(q *TVDBSetTarget) { q.Kind = enrich.Episode }},
		{"zero_series", func(q *TVDBSetTarget) { q.SeriesProviderID = 0 }},
		{"oversize_series", func(q *TVDBSetTarget) { q.SeriesProviderID = maxTVDBID + 1 }},
		{"negative_season", func(q *TVDBSetTarget) { q.Kind = enrich.Season; q.Season = -1 }},
		{"oversize_season", func(q *TVDBSetTarget) { q.Kind = enrich.Season; q.Season = maxTVDBSeason + 1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := base
			tc.mutate(&target)
			tvdbRefused(t, target, pages)
		})
	}
	// An empty region is the absent screen region, not an invented one.
	noRegion := base
	noRegion.Region = ""
	set := tvdbAccepted(t, noRegion, pages)
	if set.Region != "" || set.Language != base.Language || set.Order != TVDBOfficial ||
		set.SeriesProviderID != tvdbTestSeries {
		t.Fatalf("capture envelope changed: %+v", set)
	}
	// The recorded provider namespace must match the target exactly.
	wrongSeries := base
	wrongSeries.SeriesProviderID = tvdbTestSeries + 1
	tvdbRefused(t, wrongSeries, pages)
	wrongEpisode := tvdbBody(tvdbTestSeries, "", tvdbEpisodeRaw(9401, tvdbTestSeries+1, "E", "", int64(1), int64(1), int64(1), "", nil))
	tvdbRefused(t, base, []TVDBRecordedPage{{Page: 0, Body: wrongEpisode}})
}

func TestIPTVDBEvidenceIdentityMap(t *testing.T) {
	body := tvdbBody(tvdbTestSeries, "", tvdbEpisodeRaw(9501, tvdbTestSeries, "One", "", int64(1), int64(1), int64(1), "", nil),
		tvdbEpisodeRaw(9502, tvdbTestSeries, "Two", "", int64(1), int64(2), int64(2), "", nil))
	pages := []TVDBRecordedPage{{Page: 0, Body: body}}
	good := tvdbTargetFor(9501, 9502)
	set := tvdbAccepted(t, good, pages)
	if set.Pages[0].Episodes[0].EvidenceID != "tvdb-evidence-9501" ||
		set.Pages[0].Episodes[1].EvidenceID != "tvdb-evidence-9502" {
		t.Fatalf("page identities: %+v", set.Pages[0].Episodes)
	}
	cases := []struct {
		name string
		ids  map[int64]string
	}{
		{"nil_map", nil},
		{"missing_one", map[int64]string{9501: "a"}},
		{"extra_one", map[int64]string{9501: "a", 9502: "b", 9503: "c"}},
		{"duplicate_identity", map[int64]string{9501: "a", 9502: "a"}},
		{"empty_identity", map[int64]string{9501: "a", 9502: ""}},
		{"padded_identity", map[int64]string{9501: "a", 9502: " b"}},
		{"nul_identity", map[int64]string{9501: "a", 9502: "b\x00"}},
		{"invalid_identity", map[int64]string{9501: "a", 9502: string([]byte{0xff})}},
		{"long_identity", map[int64]string{9501: "a", 9502: strings.Repeat("x", maxTVDBEvidence+1)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := good
			target.EpisodeEvidenceIDs = tc.ids
			tvdbRefused(t, target, pages)
		})
	}
	// Swapping identities changes the complete member digest; the adapter
	// never chooses an identity itself.
	swapped := good
	swapped.EpisodeEvidenceIDs = map[int64]string{9501: "tvdb-evidence-9502", 9502: "tvdb-evidence-9501"}
	other := tvdbAccepted(t, swapped, pages)
	if other.Set.MemberDigest() == set.Set.MemberDigest() {
		t.Fatal("member digest ignored evidence identities")
	}
}

func TestIPTVDBMalformedBoundedResponses(t *testing.T) {
	valid := tvdbFixtureBody(t, "tvdb-episodes-official-page0.json")
	target := tvdbTargetFor(9001, 9002)
	cases := []struct {
		name string
		body []byte
	}{
		{"not_json", []byte("not json")},
		{"truncated", valid[:len(valid)/2]},
		{"trailing_data", append(append([]byte{}, valid...), []byte(`{}`)...)},
		{"failed_status", []byte(strings.Replace(string(valid), `"status": "success"`, `"status": "failure"`, 1))},
		{"missing_status", []byte(strings.Replace(string(valid), `"status": "success",`, ``, 1))},
		{"wrong_series", []byte(strings.Replace(string(valid), `"id": 71663`, `"id": 1`, 1))},
		{"wrong_episode_series", []byte(strings.Replace(string(valid), `"seriesId": 71663`, `"seriesId": 1`, 1))},
		{"duplicate_episode_id", tvdbBody(tvdbTestSeries, "", tvdbEpisodeRaw(9001, tvdbTestSeries, "A", "", int64(1), int64(1), int64(1), "", nil),
			tvdbEpisodeRaw(9001, tvdbTestSeries, "B", "", int64(1), int64(2), int64(2), "", nil))},
		{"name_over_bound", tvdbBody(tvdbTestSeries, "", tvdbEpisodeRaw(9001, tvdbTestSeries, strings.Repeat("n", maxTVDBName+1), "", int64(1), int64(1), int64(1), "", nil))},
		{"overview_over_bound", tvdbBody(tvdbTestSeries, "", tvdbEpisodeRaw(9001, tvdbTestSeries, "E", strings.Repeat("o", maxTVDBOverview+1), int64(1), int64(1), int64(1), "", nil))},
		{"zero_id", tvdbBody(tvdbTestSeries, "", tvdbEpisodeRaw(0, tvdbTestSeries, "E", "", int64(1), int64(1), int64(1), "", nil))},
		{"oversize_id", tvdbBody(tvdbTestSeries, "", tvdbEpisodeRaw(maxTVDBID+1, tvdbTestSeries, "E", "", int64(1), int64(1), int64(1), "", nil))},
		{"nul_name", tvdbBody(tvdbTestSeries, "", tvdbEpisodeRaw(9001, tvdbTestSeries, "bad\u0000name", "", int64(1), int64(1), int64(1), "", nil))},
		{"wrong_name_type", tvdbBody(tvdbTestSeries, "", `{"id":9001,"seriesId":71663,"name":5,"overview":"","seasonNumber":1,"number":1,"absoluteNumber":1,"aired":"","runtime":null}`)},
		{"wrong_id_type", tvdbBody(tvdbTestSeries, "", `{"id":"9001","seriesId":71663,"name":"E","overview":"","seasonNumber":1,"number":1,"absoluteNumber":1,"aired":"","runtime":null}`)},
		{"invalid_utf8_name", []byte(strings.Replace(string(tvdbBody(tvdbTestSeries, "", tvdbEpisodeRaw(9001, tvdbTestSeries, "NAME", "", int64(1), int64(1), int64(1), "", nil))), "NAME", string([]byte{0xff}), 1))},
		{"empty_set", tvdbBody(tvdbTestSeries, "")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tvdbRefused(t, target, []TVDBRecordedPage{{Page: 0, Body: tc.body}})
		})
	}
	// No recorded page and an empty body are both incomplete observations.
	tvdbRefused(t, target, nil)
	tvdbRefused(t, target, []TVDBRecordedPage{{Page: 0, Body: nil}})
	// A provider episode ID repeats across pages.
	duplicateAcross := []TVDBRecordedPage{
		{Page: 0, Body: tvdbBody(tvdbTestSeries, tvdbNextURL(1), tvdbEpisodeRaw(9001, tvdbTestSeries, "A", "", int64(1), int64(1), int64(1), "", nil))},
		{Page: 1, Body: tvdbBody(tvdbTestSeries, "", tvdbEpisodeRaw(9001, tvdbTestSeries, "B", "", int64(1), int64(2), int64(2), "", nil))},
	}
	tvdbRefused(t, tvdbTargetFor(9001), duplicateAcross)
	// Too many rows on one page.
	rows := make([]string, maxTVDBPageRows+1)
	for i := range rows {
		rows[i] = tvdbEpisodeRaw(int64(9600+i), tvdbTestSeries, "E", "", int64(1), int64(i), int64(i+1), "", nil)
	}
	manyIDs := make([]int64, len(rows))
	for i := range manyIDs {
		manyIDs[i] = int64(9600 + i)
	}
	tvdbRefused(t, tvdbTargetFor(manyIDs...), []TVDBRecordedPage{{Page: 0, Body: tvdbBody(tvdbTestSeries, "", rows...)}})
	// Too many recorded pages.
	pages := make([]TVDBRecordedPage, maxTVDBPages+1)
	for i := range pages {
		pages[i] = TVDBRecordedPage{Page: int64(i), Body: []byte(`{}`)}
	}
	tvdbRefused(t, tvdbTargetFor(1), pages)
	// One oversized page response is refused without a truncated prefix.
	oversized := TVDBRecordedPage{Page: 0, Body: make([]byte, maxTVDBPageBytes+1)}
	set, err := PrepareTVDBSet(target, []TVDBRecordedPage{oversized})
	var size *enrich.ResponseSizeError
	if err == nil || !errors.Is(err, ErrTVDB) || !errors.As(err, &size) ||
		size.LimitBytes != maxTVDBPageBytes || size.ObservedBytes != maxTVDBPageBytes+1 ||
		!reflect.DeepEqual(set, TVDBSet{}) {
		t.Fatalf("oversized page: %+v, %v", set, err)
	}
	// The whole set is bounded too: two legal pages plus a third exceed it.
	pad := maxTVDBSetBytes/3 + 1024
	padded := func(page int64, next string) TVDBRecordedPage {
		nextJSON := "null"
		if next != "" {
			nextJSON = strconv.Quote(next)
		}
		return TVDBRecordedPage{Page: page, Body: []byte(`{"status":"success","data":{"series":{"id":71663},` +
			`"episodes":[` + tvdbEpisodeRaw(int64(9700+page), tvdbTestSeries, "E", "", int64(1), int64(1+page), int64(1+page), "", nil) + `]},` +
			`"links":{"next":` + nextJSON + `},"pad":"` + strings.Repeat("p", pad) + `"}`)}
	}
	set, err = PrepareTVDBSet(tvdbTargetFor(9700, 9701, 9702),
		[]TVDBRecordedPage{padded(0, tvdbNextURL(1)), padded(1, tvdbNextURL(2)), {Page: 2, Body: make([]byte, pad)}})
	if err == nil || !errors.Is(err, ErrTVDB) || !errors.As(err, &size) ||
		size.LimitBytes != maxTVDBSetBytes || !reflect.DeepEqual(set, TVDBSet{}) {
		t.Fatalf("oversized set: %+v, %v", set, err)
	}
}

func TestIPTVDBBoundedDeclarations(t *testing.T) {
	// More seasons than the IP-08 descriptor can declare.
	seasons := make([]string, maxTVChildSeasons+1)
	seasonIDs := make([]int64, len(seasons))
	for i := range seasons {
		seasonIDs[i] = int64(9800 + i)
		seasons[i] = tvdbEpisodeRaw(seasonIDs[i], tvdbTestSeries, "E", "", int64(i), int64(1), int64(i+1), "", nil)
	}
	tvdbRefused(t, tvdbTargetFor(seasonIDs...), []TVDBRecordedPage{{Page: 0, Body: tvdbBody(tvdbTestSeries, "", seasons...)}})
	// More episodes in one season than IP-08 accepts.
	var pages []TVDBRecordedPage
	var ids []int64
	for page := int64(0); page < 3; page++ {
		count := 1000
		if page == 2 {
			count = 1
		}
		rows := make([]string, count)
		for i := 0; i < count; i++ {
			number := page*1000 + int64(i) + 1
			id := int64(9900) + number
			ids = append(ids, id)
			rows[i] = tvdbEpisodeRaw(id, tvdbTestSeries, "E", "", int64(1), number, number, "", nil)
		}
		next := ""
		if page < 2 {
			next = tvdbNextURL(page + 1)
		}
		pages = append(pages, TVDBRecordedPage{Page: page, Body: tvdbBody(tvdbTestSeries, next, rows...)})
	}
	tvdbRefused(t, tvdbTargetFor(ids...), pages)
	// More total members than IP-08 accepts, with legal per-page and per-season
	// counts.
	pages = pages[:0]
	ids = ids[:0]
	for page := int64(0); page < 21; page++ {
		count := 1000
		rows := make([]string, count)
		for i := 0; i < count; i++ {
			number := int64(i) + 1
			id := int64(12000) + page*1000 + int64(i)
			ids = append(ids, id)
			rows[i] = tvdbEpisodeRaw(id, tvdbTestSeries, "E", "", page, number, page*1000+number, "", nil)
		}
		next := ""
		if page < 20 {
			next = tvdbNextURL(page + 1)
		}
		pages = append(pages, TVDBRecordedPage{Page: page, Body: tvdbBody(tvdbTestSeries, next, rows...)})
	}
	tvdbRefused(t, tvdbTargetFor(ids...), pages)
	// Duplicate aired coordinates across pages refuse.
	first := tvdbBody(tvdbTestSeries, tvdbNextURL(1), tvdbEpisodeRaw(13001, tvdbTestSeries, "A", "", int64(1), int64(1), int64(1), "", nil))
	second := tvdbBody(tvdbTestSeries, "", tvdbEpisodeRaw(13002, tvdbTestSeries, "B", "", int64(1), int64(1), int64(2), "", nil))
	tvdbRefused(t, tvdbTargetFor(13001, 13002), []TVDBRecordedPage{{Page: 0, Body: first}, {Page: 1, Body: second}})
	// Duplicate absolute coordinates across pages refuse.
	absoluteTarget := tvdbTargetFor(13101, 13102)
	absoluteTarget.Order = TVDBAbsolute
	first = tvdbBody(tvdbTestSeries, tvdbNextURL(1), tvdbEpisodeRaw(13101, tvdbTestSeries, "A", "", int64(1), int64(1), int64(1), "", nil))
	second = tvdbBody(tvdbTestSeries, "", tvdbEpisodeRaw(13102, tvdbTestSeries, "B", "", int64(1), int64(2), int64(1), "", nil))
	tvdbRefused(t, absoluteTarget, []TVDBRecordedPage{{Page: 0, Body: first}, {Page: 1, Body: second}})
	// An empty middle page is legal; an empty terminal set is not.
	emptyMiddle := []TVDBRecordedPage{
		{Page: 0, Body: tvdbBody(tvdbTestSeries, tvdbNextURL(1))},
		{Page: 1, Body: tvdbBody(tvdbTestSeries, "", tvdbEpisodeRaw(13201, tvdbTestSeries, "E", "", int64(1), int64(1), int64(1), "", nil))},
	}
	if set := tvdbAccepted(t, tvdbTargetFor(13201), emptyMiddle); len(set.Pages[0].Episodes) != 0 {
		t.Fatalf("empty middle page: %+v", set.Pages[0])
	}
}
