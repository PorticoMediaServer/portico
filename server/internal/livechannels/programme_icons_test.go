package livechannels

import (
	"context"
	"strings"
	"testing"
	"time"
)

func iconTestInput() SourceInput {
	return SourceInput{ID: strings.Repeat("ab", 24), RequestID: strings.Repeat("cd", 24), Name: "Icon broadcast", TunerCount: 1,
		Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\",Channel one\nhttps://provider.invalid/one\n",
		Guide: `<?xml version="1.0"?><tv><channel id="one"/>` +
			`<programme id="a" channel="one" start="20260905120000 +0000" stop="20260905130000 +0000"><title>First</title><icon src="https://img.invalid/a.png"/></programme>` +
			`<programme id="b" channel="one" start="20260905130000 +0000" stop="20260905140000 +0000"><title>Second</title><icon src="https://img.invalid/a.png"/></programme>` +
			`<programme id="c" channel="one" start="20260905140000 +0000" stop="20260905150000 +0000"><title>Third</title><icon src="not-a-locator"/></programme>` +
			`</tv>`}
}

func iconTestQuery() GuideQuery {
	start, _ := time.Parse(time.RFC3339, "2026-09-05T12:00:00Z")
	return GuideQuery{Start: start, End: start.Add(4 * time.Hour), Timezone: "UTC", Kind: LiveSource, Limit: 10}
}

// An XMLTV guide with one icon URL on two programmes and an invalid src on a
// third stores two icon rows and none for the third; the guide publishes an
// image only once the background import has stored the icon, and a restricted
// programme never shows it.
func TestProgrammeIconsStoredProjectedAndRestricted(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := testAuthority("viewer-a", true, true)
	saved, err := s.Save(ctx, a, iconTestInput())
	if err != nil {
		t.Fatal(err)
	}
	var total int
	if err = s.db.QueryRow(`SELECT count(*) FROM live_programme_icons WHERE generation_id=?`, saved.Generation).Scan(&total); err != nil || total != 2 {
		t.Fatalf("icon rows: %d %v", total, err)
	}
	var shared int
	if err = s.db.QueryRow(`SELECT count(*) FROM live_programme_icons WHERE generation_id=? AND url='https://img.invalid/a.png'`, saved.Generation).Scan(&shared); err != nil || shared != 2 {
		t.Fatalf("shared icon rows: %d %v", shared, err)
	}
	guide, err := s.Guide(ctx, a, iconTestQuery())
	if err != nil {
		t.Fatal(err)
	}
	if len(guide.Channels) != 1 || len(guide.Channels[0].Programmes) != 3 {
		t.Fatalf("guide: %+v", guide.Channels)
	}
	for _, p := range guide.Channels[0].Programmes {
		if p.Image != "" {
			t.Fatalf("image before import: %+v", p)
		}
	}
	digest := strings.Repeat("ab", 32)
	if _, err = s.db.Exec(`INSERT INTO live_programme_images(url,small_digest,large_digest,media_type,small_width,small_height,small_bytes,large_width,large_height,large_bytes,stored_ms) SELECT column1,column2,column2,column3,column4,column5,1,column4,column5,1,column6 FROM (VALUES(?,?,?,?,?,?))`, "https://img.invalid/a.png", digest, "image/png", 64, 64, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	guide, err = s.Guide(ctx, a, iconTestQuery())
	if err != nil {
		t.Fatal(err)
	}
	// Each image URL describes the two renditions made at import.
	imagePath := func(p Programme) string {
		return "/v1/guide/programme-images/" + saved.Generation + "/" + p.ID + "?sd=64x64.1." + digest[:16] + "&ld=64x64.1." + digest[:16]
	}
	byTitle := map[string]Programme{}
	for _, p := range guide.Channels[0].Programmes {
		byTitle[p.Title] = p
	}
	if byTitle["First"].Image != imagePath(byTitle["First"]) || byTitle["Second"].Image != imagePath(byTitle["Second"]) {
		t.Fatalf("shared image: %+v", byTitle)
	}
	if byTitle["Third"].Image != "" {
		t.Fatalf("invalid icon published: %+v", byTitle["Third"])
	}
	refused := iconTestQuery()
	refused.ProgrammeAllowed = func(string) bool { return false }
	guide, err = s.Guide(ctx, a, refused)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range guide.Channels[0].Programmes {
		if p.Image != "" || p.Title != RestrictedProgrammeTitle {
			t.Fatalf("restricted slot leaked image: %+v", p)
		}
	}
}

// Republishing a new generation, then running retention, removes the old
// generation's icon rows.
func TestProgrammeIconsPrunedWithSupersededGeneration(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := testAuthority("viewer-a", true, true)
	first, err := s.Save(ctx, a, iconTestInput())
	if err != nil {
		t.Fatal(err)
	}
	second := iconTestInput()
	second.ExpectedRevision = first.Revision
	second.RequestID = strings.Repeat("ef", 24)
	second.Guide = strings.Replace(second.Guide, "First", "First again", 1)
	if _, err = s.Save(ctx, a, second); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	if _, err = s.db.Exec(`UPDATE live_generations SET created_at=? WHERE id=?`, old, first.Generation); err != nil {
		t.Fatal(err)
	}
	found, err := s.PruneSupersededGeneration(ctx)
	if err != nil || !found {
		t.Fatalf("prune: %t %v", found, err)
	}
	var left int
	if err = s.db.QueryRow(`SELECT count(*) FROM live_programme_icons WHERE generation_id=?`, first.Generation).Scan(&left); err != nil || left != 0 {
		t.Fatalf("old icon rows remain: %d %v", left, err)
	}
}
