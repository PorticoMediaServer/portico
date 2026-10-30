package catalog

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// infoDiscMediaObservation assembles the timed facts a disc analysis produces
// for one exact title: the observed streams, the selected title's chapters and
// the consumed member manifest. The structure fingerprint is the one already
// accepted for this object, exactly as the accepting scan has it.
func infoDiscMediaObservation(t *testing.T, db *sql.DB, object, kind string, title disc.Title) assets.Facts {
	t.Helper()
	var fingerprint string
	if err := db.QueryRow(`SELECT fingerprint FROM inventory_disc_facts WHERE object_id=?`, object).Scan(&fingerprint); err != nil {
		t.Fatal(err)
	}
	chapters := make([]assets.Chapter, 0, len(title.Chapters))
	for i, chapter := range title.Chapters {
		end := title.DurationMs
		if i+1 < len(title.Chapters) {
			end = title.Chapters[i+1].StartMs
		}
		chapters = append(chapters, assets.Chapter{Title: "Chapter", Start: float64(chapter.StartMs) / 1000, End: float64(end) / 1000})
	}
	return assets.Facts{
		Container:   "mpeg2video",
		VideoCodec:  "mpeg2video",
		AudioCodec:  "ac3",
		Width:       720,
		Height:      480,
		Duration:    float64(title.DurationMs) / 1000,
		TimingKnown: true,
		Streams: []assets.Stream{
			{Index: 0, Type: "video", Codec: "mpeg2video"},
			{Index: 1, Type: "audio", Codec: "ac3", Language: "eng", Channels: 2, Default: true},
			{Index: 2, Type: "subtitle", Codec: "dvd_subtitle", Language: "eng", HearingImpaired: true},
		},
		Chapters: chapters,
		DiscSelection: &assets.DiscMediaSelection{
			TitleKey:             title.Key(kind),
			StructureFingerprint: fingerprint,
			Members: []storage.DiscMember{
				{Path: "VIDEO_TS/VTS_01_1.VOB", ObjectIdentity: "member-1", Size: 2048, ModifiedNS: 7, Required: true},
				{Path: "VIDEO_TS", ObjectIdentity: "member-2", Size: 4096, ModifiedNS: 7, Required: true, Directory: true},
			},
		},
	}
}

// infoDiscMediaBuild replays the builder inside one transaction and invokes
// every declared row ordinal, so a family that cannot reproduce its rows fails
// here rather than in the accepting writer.
func infoDiscMediaBuild(t *testing.T, db *sql.DB, job, object, revision string, policy int64, f assets.Facts, streamRevision, chapterRevision int64, alternate bool) []compactcatalog.InfoSourceFamily {
	t.Helper()
	families, err := infoDiscMediaBuildOrError(t, db, job, object, revision, policy, f, streamRevision, chapterRevision, alternate)
	if err != nil {
		t.Fatal(err)
	}
	return families
}

func infoDiscMediaBuildOrError(t *testing.T, db *sql.DB, job, object, revision string, policy int64, f assets.Facts, streamRevision, chapterRevision int64, alternate bool) ([]compactcatalog.InfoSourceFamily, error) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	frame, err := inventoryInfoFrameTx(ctx, tx, job, object, revision, policy, false)
	if err != nil {
		t.Fatal(err)
	}
	families, err := buildInfoDiscMediaFamilies(ctx, tx, frame, object, revision, f, streamRevision, chapterRevision, alternate)
	if err != nil {
		return nil, err
	}
	for _, family := range families {
		for i := int64(0); i < family.Count; i++ {
			if _, err = family.Row(ctx, tx, i); err != nil {
				t.Fatalf("family %d ordinal %d: %v", family.Number, i, err)
			}
		}
	}
	if err = checkInfoDiscMediaTx(ctx, tx, frame, object, revision, f, alternate); err != nil {
		t.Fatal(err)
	}
	return families, nil
}

func infoDiscMediaFamily(t *testing.T, families []compactcatalog.InfoSourceFamily, number int) compactcatalog.InfoSourceFamily {
	t.Helper()
	for _, family := range families {
		if family.Number == number {
			return family
		}
	}
	t.Fatalf("family %d is absent from %v", number, infoDiscMediaNumbers(families))
	return compactcatalog.InfoSourceFamily{}
}

func infoDiscMediaNumbers(families []compactcatalog.InfoSourceFamily) []int {
	out := make([]int, 0, len(families))
	for _, family := range families {
		out = append(out, family.Number)
	}
	return out
}

func infoDiscMediaNumbersEqual(families []compactcatalog.InfoSourceFamily, want ...int) bool {
	got := infoDiscMediaNumbers(families)
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// infoDiscMediaRow reproduces one staged row in its own short transaction. A
// shared transaction would keep SQLite's write lock open across the canonical
// writes and checks that follow, which is a fixture defect rather than a
// property of the builder.
func infoDiscMediaRow(t *testing.T, db *sql.DB, family compactcatalog.InfoSourceFamily, ordinal int64) compactcatalog.InfoStageRow {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	row, err := family.Row(context.Background(), tx, ordinal)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

// applyInfoDiscMediaCanonical installs the canonical rows the materializer would
// write for these facts, so a repeated identical observation must become a
// truthful no-op instead of restaging identical rows.
func applyInfoDiscMediaCanonical(t *testing.T, db *sql.DB, object, revision string, f assets.Facts, streamRevision, chapterRevision int64) {
	t.Helper()
	digest, err := discTitleMediaRevision(revision, f.DiscSelection.StructureFingerprint, f)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO inventory_disc_title_media(object_id,title_key,source_revision,fingerprint,revision,container,video_codec,audio_codec,width,height,duration_ms,origin_us,timing_known)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, object, f.DiscSelection.TitleKey, revision, f.DiscSelection.StructureFingerprint, digest, f.Container, f.VideoCodec, f.AudioCodec, f.Width, f.Height, int64(math.Round(f.Duration*1000)), f.OriginUS, f.TimingKnown); err != nil {
		t.Fatal(err)
	}
	for _, st := range f.Streams {
		if _, err = tx.Exec(`INSERT INTO inventory_disc_title_media_streams(object_id,title_key,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,detail_json,breadth_json)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, object, f.DiscSelection.TitleKey, st.Index, st.Type, st.Codec, st.Language, st.Title, st.Channels, st.ChannelLayout, st.Default, st.Forced, st.ColorTransfer, st.ColorPrimaries, st.EncodeDetail(), "{}"); err != nil {
			t.Fatal(err)
		}
	}
	for _, member := range f.DiscSelection.Members {
		if _, err = tx.Exec(`INSERT INTO inventory_disc_title_media_members(object_id,title_key,path,object_identity,size,modified_ns,required,directory)
 VALUES(?,?,?,?,?,?,?,?)`, object, f.DiscSelection.TitleKey, member.Path, member.ObjectIdentity, member.Size, member.ModifiedNS, member.Required, member.Directory); err != nil {
			t.Fatal(err)
		}
	}
	for _, member := range f.DiscSelection.Members {
		if _, err = tx.Exec(`INSERT INTO inventory_disc_media_members(object_id,path,object_identity,size,modified_ns,required,directory)
 VALUES(?,?,?,?,?,?,?)`, object, member.Path, member.ObjectIdentity, member.Size, member.ModifiedNS, member.Required, member.Directory); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(`UPDATE inventory_disc_facts SET media_title_key=?,media_stream_revision=?,media_chapter_revision=? WHERE object_id=?`, f.DiscSelection.TitleKey, streamRevision, chapterRevision, object); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// TestInfoDiscMediaSourceMainStagesTimedTitleAndStopsWhenUnchanged proves the
// main selection stages the fact association, the complete member list and the
// exact timed facts, then reports a truthful no-op once those rows are current.
func TestInfoDiscMediaSourceMainStagesTimedTitleAndStopsWhenUnchanged(t *testing.T) {
	s, db, _, job, object, policy, probe := discPublicationFixture(t)
	c := catalogtest.New(t, db)
	var revision string
	if err := db.QueryRow(`SELECT revision FROM inventory_objects WHERE id=?`, object).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	receipt, err := acceptDiscProbeForTest(t, s, context.Background(), job, object, revision, policy, probe)
	if err != nil || receipt.State != compactcatalog.InfoInputAcceptedPending {
		t.Fatal("structure source receipt", receipt, err)
	}
	c.Drain()

	titles := probe.OrderedTitles()
	f := infoDiscMediaObservation(t, db, object, probe.Kind, titles[0])
	families := infoDiscMediaBuild(t, db, job, object, revision, policy, f, 7, 4, false)
	if !infoDiscMediaNumbersEqual(families, 10, 11, 15, 16, 17, 18) {
		t.Fatal("main families", infoDiscMediaNumbers(families))
	}
	fact := infoDiscMediaFamily(t, families, 10)
	title := infoDiscMediaFamily(t, families, 11)
	if fact.Parent || !title.Parent {
		t.Fatal("main must change only the disc fact, not the title witness", fact.Parent, title.Parent)
	}
	ctx := context.Background()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	row, err := fact.Row(ctx, tx, 0)
	if err != nil {
		t.Fatal(err)
	}
	factRow, ok := row.(compactcatalog.InfoDiscFactRow)
	if !ok || factRow.MediaTitleKey != f.DiscSelection.TitleKey || factRow.MediaStreamRevision != 7 || factRow.MediaChapterRevision != 4 {
		t.Fatal("disc fact media association", row)
	}
	row, err = infoDiscMediaFamily(t, families, 16).Row(ctx, tx, 0)
	if err != nil {
		t.Fatal(err)
	}
	media, ok := row.(compactcatalog.InfoDiscTitleMediaRow)
	if !ok || media.Revision != mustInfoDiscMediaRevision(t, revision, f) || media.DurationMS != titles[0].DurationMs || media.SourceRevision != revision {
		t.Fatal("timed title row", row)
	}
	row, err = infoDiscMediaFamily(t, families, 17).Row(ctx, tx, 2)
	if err != nil {
		t.Fatal(err)
	}
	stream, ok := row.(compactcatalog.InfoDiscTitleMediaStreamRow)
	if !ok || stream.StreamIndex != 2 || stream.Type != "subtitle" || stream.IsForced != 0 {
		t.Fatal("timed stream ordinal", row)
	}
	row, err = infoDiscMediaFamily(t, families, 18).Row(ctx, tx, 1)
	if err != nil {
		t.Fatal(err)
	}
	member, ok := row.(compactcatalog.InfoDiscTitleMediaMemberRow)
	if !ok || member.Path != "VIDEO_TS" || member.Directory != 1 {
		t.Fatal("timed member ordinal", row)
	}

	// Every row probe above ran inside this read/write transaction, which holds
	// SQLite's write lock. Release it before the canonical writer opens its own
	// transaction, otherwise the second Begin blocks until busy_timeout and
	// fails with "database locked" instead of exercising the no-op path.
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	applyInfoDiscMediaCanonical(t, db, object, revision, f, 7, 4)
	if again := infoDiscMediaBuild(t, db, job, object, revision, policy, f, 7, 4, false); len(again) != 0 {
		t.Fatal("unchanged selection restaged", infoDiscMediaNumbers(again))
	}
	// A new technical revision with identical timed facts moves only the
	// association and the member list; the timed families stay untouched.
	moved := infoDiscMediaBuild(t, db, job, object, revision, policy, f, 8, 5, false)
	if !infoDiscMediaNumbersEqual(moved, 10, 11, 15) {
		t.Fatal("association-only families", infoDiscMediaNumbers(moved))
	}
	if infoDiscMediaFamily(t, moved, 10).Parent {
		t.Fatal("association change was staged as a parent witness")
	}
}

func mustInfoDiscMediaRevision(t *testing.T, revision string, f assets.Facts) string {
	t.Helper()
	digest, err := discTitleMediaRevision(revision, f.DiscSelection.StructureFingerprint, f)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func infoDiscMediaCheckError(t *testing.T, db *sql.DB, check compactcatalog.InfoSourceCheck) error {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	return check(context.Background(), tx)
}

// TestInfoDiscMediaSourceAlternateOnlyStagesRequestedTimedTitle proves an
// alternate publication keeps the main association and member list untouched,
// stages the requested title alone, and refuses to run without its original
// title request.
func TestInfoDiscMediaSourceAlternateOnlyStagesRequestedTimedTitle(t *testing.T) {
	s, db, _, job, object, policy, probe := discPublicationFixture(t)
	c := catalogtest.New(t, db)
	var revision string
	if err := db.QueryRow(`SELECT revision FROM inventory_objects WHERE id=?`, object).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	receipt, err := acceptDiscProbeForTest(t, s, context.Background(), job, object, revision, policy, probe)
	if err != nil || receipt.State != compactcatalog.InfoInputAcceptedPending {
		t.Fatal("structure source receipt", receipt, err)
	}
	c.Drain()

	titles := probe.OrderedTitles()
	main := infoDiscMediaObservation(t, db, object, probe.Kind, titles[0])
	alt := infoDiscMediaObservation(t, db, object, probe.Kind, titles[1])
	applyInfoDiscMediaCanonical(t, db, object, revision, main, 3, 2)

	if _, err = infoDiscMediaBuildOrError(t, db, job, object, revision, policy, alt, 0, 0, true); !errors.Is(err, ErrDiscSelectionChanged) {
		t.Fatal("alternate published without its title request", err)
	}
	if _, err = db.Exec(`INSERT INTO inventory_disc_title_requests(job_id,object_id,title_key,source_revision,fingerprint) VALUES(?,?,?,?,?)`, job, object, alt.DiscSelection.TitleKey, revision, alt.DiscSelection.StructureFingerprint); err != nil {
		t.Fatal(err)
	}
	families := infoDiscMediaBuild(t, db, job, object, revision, policy, alt, 0, 0, true)
	if !infoDiscMediaNumbersEqual(families, 10, 11, 16, 17, 18) {
		t.Fatal("alternate families", infoDiscMediaNumbers(families))
	}
	if err = infoDiscMediaCheckError(t, db, infoDiscMediaCheck(job, object, revision, policy, false, alt, true)); err != nil {
		t.Fatal("alternate check with its exact request job", err)
	}
	if !infoDiscMediaFamily(t, families, 10).Parent {
		t.Fatal("alternate changed the main disc fact")
	}
	ctx := context.Background()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	row, err := infoDiscMediaFamily(t, families, 10).Row(ctx, tx, 0)
	if err != nil {
		t.Fatal(err)
	}
	factRow, ok := row.(compactcatalog.InfoDiscFactRow)
	if !ok || factRow.MediaTitleKey != main.DiscSelection.TitleKey || factRow.MediaStreamRevision != 3 || factRow.MediaChapterRevision != 2 {
		t.Fatal("alternate witness fact", row)
	}
	row, err = infoDiscMediaFamily(t, families, 16).Row(ctx, tx, 0)
	if err != nil {
		t.Fatal(err)
	}
	media, ok := row.(compactcatalog.InfoDiscTitleMediaRow)
	if !ok || media.TitleKey != alt.DiscSelection.TitleKey || media.DurationMS != titles[1].DurationMs {
		t.Fatal("alternate timed title", row)
	}
	// The alternate's staged timed facts become a no-op once materialized,
	// while the main association is still exactly what it was.
	digest, err := discTitleMediaRevision(revision, alt.DiscSelection.StructureFingerprint, alt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO inventory_disc_title_media(object_id,title_key,source_revision,fingerprint,revision,container,video_codec,audio_codec,width,height,duration_ms,origin_us,timing_known) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, object, alt.DiscSelection.TitleKey, revision, alt.DiscSelection.StructureFingerprint, digest, alt.Container, alt.VideoCodec, alt.AudioCodec, alt.Width, alt.Height, titles[1].DurationMs, alt.OriginUS, alt.TimingKnown); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if again := infoDiscMediaBuild(t, db, job, object, revision, policy, alt, 0, 0, true); len(again) != 0 {
		t.Fatal("unchanged alternate restaged", infoDiscMediaNumbers(again))
	}
	var mainKey string
	var mainStreams, mainChapters int64
	if err = db.QueryRow(`SELECT media_title_key,media_stream_revision,media_chapter_revision FROM inventory_disc_facts WHERE object_id=?`, object).Scan(&mainKey, &mainStreams, &mainChapters); err != nil {
		t.Fatal(err)
	}
	if mainKey != main.DiscSelection.TitleKey || mainStreams != 3 || mainChapters != 2 {
		t.Fatal("alternate disturbed the main association", mainKey, mainStreams, mainChapters)
	}
	if _, err = db.Exec(`DELETE FROM inventory_disc_title_requests WHERE job_id=? AND object_id=?`, job, object); err != nil {
		t.Fatal(err)
	}
	if err = infoDiscMediaCheckError(t, db, infoDiscMediaCheck(job, object, revision, policy, false, alt, true)); !errors.Is(err, ErrDiscSelectionChanged) {
		t.Fatal("alternate check survived a missing request", err)
	}
}

// infoDiscMediaRequestJobCheckError runs the exact job request fence with a job
// that did not write the request row, against the real frame for job.
func infoDiscMediaRequestJobCheckError(t *testing.T, db *sql.DB, job, foreign, object, revision string, policy int64, f assets.Facts) error {
	t.Helper()
	ctx := context.Background()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	frame, err := inventoryInfoFrameTx(ctx, tx, job, object, revision, policy, false)
	if err != nil {
		t.Fatal(err)
	}
	return checkInfoDiscMediaRequestTx(ctx, tx, frame, foreign, object, revision, f)
}

// TestInfoDiscMediaSourceAlternateRespectsOwnerChoiceAndStagesRequestedTitle
// proves the owner-title clause is main-only, exactly as CommitDiscAlternate
// splits it: after the owner chose titles[0] as the main title, a job that
// requested titles[1] stages titles[1]'s timed facts while the owner's main
// association and member list stay untouched, a main write still refuses to
// speak for a title the owner did not choose, and the exact job request fence
// remains mandatory in every check.
func TestInfoDiscMediaSourceAlternateRespectsOwnerChoiceAndStagesRequestedTitle(t *testing.T) {
	s, db, _, job, object, policy, probe := discPublicationFixture(t)
	c := catalogtest.New(t, db)
	var revision string
	if err := db.QueryRow(`SELECT revision FROM inventory_objects WHERE id=?`, object).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	receipt, err := acceptDiscProbeForTest(t, s, context.Background(), job, object, revision, policy, probe)
	if err != nil || receipt.State != compactcatalog.InfoInputAcceptedPending {
		t.Fatal("structure source receipt", receipt, err)
	}
	c.Drain()

	titles := probe.OrderedTitles()
	owner := infoDiscMediaObservation(t, db, object, probe.Kind, titles[0])
	requested := infoDiscMediaObservation(t, db, object, probe.Kind, titles[1])
	applyInfoDiscMediaCanonical(t, db, object, revision, owner, 3, 2)
	// The owner picked titles[0]; the accepted main association is titles[0].
	if _, err = db.Exec(`UPDATE inventory_disc_facts SET owner_title_key=? WHERE object_id=?`, owner.DiscSelection.TitleKey, object); err != nil {
		t.Fatal(err)
	}
	if _, err = infoDiscMediaBuildOrError(t, db, job, object, revision, policy, requested, 0, 0, true); !errors.Is(err, ErrDiscSelectionChanged) {
		t.Fatal("alternate published without its title request", err)
	}
	if _, err = db.Exec(`INSERT INTO inventory_disc_title_requests(job_id,object_id,title_key,source_revision,fingerprint) VALUES(?,?,?,?,?)`, job, object, requested.DiscSelection.TitleKey, revision, requested.DiscSelection.StructureFingerprint); err != nil {
		t.Fatal(err)
	}
	// A main write may still speak for no title but the owner's, even with a
	// request row present for the other title.
	if _, err = infoDiscMediaBuildOrError(t, db, job, object, revision, policy, requested, 6, 4, false); !errors.Is(err, ErrDiscSelectionChanged) {
		t.Fatal("main write passed the owner choice", err)
	}
	// The owner's own main title is still admitted, so the clause was narrowed
	// for alternates rather than dropped.
	if moved := infoDiscMediaBuild(t, db, job, object, revision, policy, owner, 8, 5, false); !infoDiscMediaNumbersEqual(moved, 10, 11, 15) {
		t.Fatal("owner main write was refused", infoDiscMediaNumbers(moved))
	}
	families := infoDiscMediaBuild(t, db, job, object, revision, policy, requested, 0, 0, true)
	if !infoDiscMediaNumbersEqual(families, 10, 11, 16, 17, 18) {
		t.Fatal("owner-respecting alternate families", infoDiscMediaNumbers(families))
	}
	if !infoDiscMediaFamily(t, families, 10).Parent || !infoDiscMediaFamily(t, families, 11).Parent {
		t.Fatal("alternate changed the owner's canonical rows")
	}
	factRow, ok := infoDiscMediaRow(t, db, infoDiscMediaFamily(t, families, 10), 0).(compactcatalog.InfoDiscFactRow)
	if !ok || factRow.OwnerTitleKey != owner.DiscSelection.TitleKey || factRow.MediaTitleKey != owner.DiscSelection.TitleKey || factRow.MediaStreamRevision != 3 || factRow.MediaChapterRevision != 2 {
		t.Fatal("alternate witness fact did not preserve the owner association", factRow)
	}
	media, ok := infoDiscMediaRow(t, db, infoDiscMediaFamily(t, families, 16), 0).(compactcatalog.InfoDiscTitleMediaRow)
	if !ok || media.TitleKey != requested.DiscSelection.TitleKey || media.DurationMS != titles[1].DurationMs || media.SourceRevision != revision {
		t.Fatal("requested timed title", media)
	}
	for i := int64(0); i < int64(len(requested.Streams)); i++ {
		stream, ok := infoDiscMediaRow(t, db, infoDiscMediaFamily(t, families, 17), i).(compactcatalog.InfoDiscTitleMediaStreamRow)
		if !ok || stream.TitleKey != requested.DiscSelection.TitleKey {
			t.Fatal("requested timed stream", stream)
		}
	}
	for i := int64(0); i < int64(len(requested.DiscSelection.Members)); i++ {
		member, ok := infoDiscMediaRow(t, db, infoDiscMediaFamily(t, families, 18), i).(compactcatalog.InfoDiscTitleMediaMemberRow)
		if !ok || member.TitleKey != requested.DiscSelection.TitleKey {
			t.Fatal("requested timed member", member)
		}
	}
	var ownerKey, mediaKey string
	var mediaStreams, mediaChapters int64
	if err = db.QueryRow(`SELECT owner_title_key,media_title_key,media_stream_revision,media_chapter_revision FROM inventory_disc_facts WHERE object_id=?`, object).Scan(&ownerKey, &mediaKey, &mediaStreams, &mediaChapters); err != nil {
		t.Fatal(err)
	}
	if ownerKey != owner.DiscSelection.TitleKey || mediaKey != owner.DiscSelection.TitleKey || mediaStreams != 3 || mediaChapters != 2 {
		t.Fatal("alternate disturbed the owner association", ownerKey, mediaKey, mediaStreams, mediaChapters)
	}
	// The exact job request fence is still required by every check.
	if err = infoDiscMediaCheckError(t, db, infoDiscMediaCheck(job, object, revision, policy, false, requested, true)); err != nil {
		t.Fatal("alternate check with its exact request job", err)
	}
	if err = infoDiscMediaRequestJobCheckError(t, db, job, "foreign-job", object, revision, policy, requested); !errors.Is(err, ErrDiscSelectionChanged) {
		t.Fatal("a foreign job borrowed this job's title request", err)
	}
	if _, err = db.Exec(`DELETE FROM inventory_disc_title_requests WHERE job_id=? AND object_id=?`, job, object); err != nil {
		t.Fatal(err)
	}
	if err = infoDiscMediaRequestJobCheckError(t, db, job, job, object, revision, policy, requested); !errors.Is(err, ErrDiscSelectionChanged) {
		t.Fatal("alternate request fence survived a missing request", err)
	}
}

// TestInfoDiscMediaSourceRejectsUnfencedFacts proves the builder refuses a
// duration, chapter count, stream index or member identity the canonical
// structure does not support, instead of staging facts no reader can use.
func TestInfoDiscMediaSourceRejectsUnfencedFacts(t *testing.T) {
	s, db, _, job, object, policy, probe := discPublicationFixture(t)
	c := catalogtest.New(t, db)
	var revision string
	if err := db.QueryRow(`SELECT revision FROM inventory_objects WHERE id=?`, object).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	receipt, err := acceptDiscProbeForTest(t, s, context.Background(), job, object, revision, policy, probe)
	if err != nil || receipt.State != compactcatalog.InfoInputAcceptedPending {
		t.Fatal("structure source receipt", receipt, err)
	}
	c.Drain()

	title := probe.OrderedTitles()[0]
	base := infoDiscMediaObservation(t, db, object, probe.Kind, title)
	cases := []struct {
		name   string
		mutate func(*assets.Facts)
	}{
		{"duration", func(f *assets.Facts) { f.Duration += 1 }},
		{"chapters", func(f *assets.Facts) {
			if len(f.Chapters) > 0 {
				f.Chapters = f.Chapters[:len(f.Chapters)-1]
				return
			}
			f.Chapters = []assets.Chapter{{Start: 0, End: 1}}
		}},
		{"stream index", func(f *assets.Facts) { f.Streams[1].Index = f.Streams[0].Index }},
		{"stream type", func(f *assets.Facts) { f.Streams[0].Type = "data" }},
		{"member identity", func(f *assets.Facts) { f.DiscSelection.Members[0].ObjectIdentity = "" }},
	}
	for _, tc := range cases {
		f := base
		f.Streams = append([]assets.Stream(nil), base.Streams...)
		f.Chapters = append([]assets.Chapter(nil), base.Chapters...)
		f.DiscSelection = &assets.DiscMediaSelection{TitleKey: base.DiscSelection.TitleKey, StructureFingerprint: base.DiscSelection.StructureFingerprint, Members: append([]storage.DiscMember(nil), base.DiscSelection.Members...)}
		tc.mutate(&f)
		if _, err := infoDiscMediaBuildOrError(t, db, job, object, revision, policy, f, 6, 3, false); !errors.Is(err, disc.ErrProtocol) && !errors.Is(err, ErrDiscSelectionChanged) {
			t.Fatalf("%s was accepted: %v", tc.name, err)
		}
	}
}

// infoDiscMediaStructure publishes and drains the disc structure the timed
// publication is fenced against, exactly as the scan does before it probes a
// title.
func infoDiscMediaStructure(t *testing.T, s *Service, db *sql.DB, job, object string, policy int64, probe disc.Probe) string {
	t.Helper()
	var revision string
	if err := db.QueryRow(`SELECT revision FROM inventory_objects WHERE id=?`, object).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	receipt, err := acceptDiscProbeForTest(t, s, context.Background(), job, object, revision, policy, probe)
	if err != nil || receipt.State != compactcatalog.InfoInputAcceptedPending {
		t.Fatalf("structure source receipt: %+v %v", receipt, err)
	}
	catalogtest.New(t, db).Drain()
	return revision
}

// infoDiscMediaCanonicalFacts reads the accepted main association and the
// asset technical fact heads the disc fact must name.
func infoDiscMediaCanonicalFacts(t *testing.T, db *sql.DB, object, token string) (key string, streams, chapters int64, err error) {
	t.Helper()
	if err = db.QueryRow(`SELECT COALESCE(f.media_title_key,''),COALESCE(f.media_stream_revision,0),COALESCE(f.media_chapter_revision,0) FROM inventory_disc_facts f WHERE f.object_id=?`, object).Scan(&key, &streams, &chapters); err != nil {
		return "", 0, 0, err
	}
	if streams != 0 {
		var canonical int64
		if err = db.QueryRow(`SELECT revision FROM asset_stream_facts WHERE asset_id=?`, token).Scan(&canonical); err != nil {
			return "", 0, 0, err
		}
		if canonical != streams {
			t.Fatalf("disc fact names stream revision %d, canonical head is %d", streams, canonical)
		}
	}
	if chapters != 0 {
		var canonical int64
		if err = db.QueryRow(`SELECT revision FROM asset_chapter_facts WHERE asset_id=?`, token).Scan(&canonical); err != nil {
			return "", 0, 0, err
		}
		if canonical != chapters {
			t.Fatalf("disc fact names chapter revision %d, canonical head is %d", chapters, canonical)
		}
	}
	return key, streams, chapters, nil
}

// TestInfoInventorySourceDiscMediaPublishesSelectedTimedTitle proves the common
// analysis producer now accepts a selected disc title instead of rejecting it:
// one bounded input stages the asset streams/chapters, the disc association, the
// main member list and one exact title's timed media; the receipt is
// accepted-pending until the source publication applies them, and the accepted
// association names the technical revisions that were installed with it.
func TestInfoInventorySourceDiscMediaPublishesSelectedTimedTitle(t *testing.T) {
	s, db, _, job, object, policy, probe := discPublicationFixture(t)
	c := catalogtest.New(t, db)
	revision := infoDiscMediaStructure(t, s, db, job, object, policy, probe)
	var token string
	if err := db.QueryRow(`SELECT asset_id FROM inventory_objects WHERE id=?`, object).Scan(&token); err != nil {
		t.Fatal(err)
	}
	titles := probe.OrderedTitles()
	f := infoDiscMediaObservation(t, db, object, probe.Kind, titles[0])
	// The disc reader observes the selected title's chapters with its timed
	// media; the association names exactly those chapter facts.
	f.ChapterStatus = "known"
	receipt, err := acceptInventoryInfoForTest(t, s, context.Background(), job, object, revision, policy, f, false)
	if err != nil || receipt.State != compactcatalog.InfoInputAcceptedPending {
		t.Fatal("disc media source receipt", receipt, err)
	}
	// Accepted-pending is durable acceptance, not a claim that NEW is applied.
	var installed int
	if err := db.QueryRow(`SELECT count(*) FROM inventory_disc_title_media WHERE object_id=?`, object).Scan(&installed); err != nil || installed != 0 {
		t.Fatal("pending receipt published canonical timed media", installed, err)
	}
	c.Drain()
	key, streams, chapters, err := infoDiscMediaCanonicalFacts(t, db, object, token)
	if err != nil {
		t.Fatal(err)
	}
	if key != f.DiscSelection.TitleKey || streams <= 0 || chapters <= 0 {
		t.Fatal("accepted main association", key, streams, chapters)
	}
	var media, mediaStreams, mediaMembers, assetStreams, assetChapters, members int
	if err := db.QueryRow(`SELECT count(*) FROM inventory_disc_title_media WHERE object_id=? AND title_key=?`, object, f.DiscSelection.TitleKey).Scan(&media); err != nil || media != 1 {
		t.Fatal("timed title not installed", media, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM inventory_disc_title_media_streams WHERE object_id=? AND title_key=?`, object, f.DiscSelection.TitleKey).Scan(&mediaStreams); err != nil || mediaStreams != len(f.Streams) {
		t.Fatal("timed streams not installed", mediaStreams, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM inventory_disc_title_media_members WHERE object_id=? AND title_key=?`, object, f.DiscSelection.TitleKey).Scan(&mediaMembers); err != nil || mediaMembers != len(f.DiscSelection.Members) {
		t.Fatal("timed members not installed", mediaMembers, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM inventory_disc_media_members WHERE object_id=?`, object).Scan(&members); err != nil || members != len(f.DiscSelection.Members) {
		t.Fatal("main members not installed", members, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM asset_streams WHERE asset_id=?`, token).Scan(&assetStreams); err != nil || assetStreams != len(f.Streams) {
		t.Fatal("asset streams not installed", assetStreams, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM asset_chapters WHERE asset_id=?`, token).Scan(&assetChapters); err != nil || assetChapters != len(f.Chapters) {
		t.Fatal("asset chapters not installed", assetChapters, err)
	}
	// The same immutable observation is now a truthful accepted no-op.
	again, err := acceptInventoryInfoForTest(t, s, context.Background(), job, object, revision, policy, f, false)
	if err != nil || again.State != compactcatalog.InfoInputApplied || again.ChangeID != 0 {
		t.Fatal("unchanged disc media was not an applied no-op", again, err)
	}
}

// TestInfoInventorySourceClearsDiscMainAssociationWithoutSelection proves the
// retired canonical clear is a staged normalized family10 fact: an analysis with
// no disc selection removes the main association while leaving the accepted
// member list and timed title rows exactly as the canonical writer left them.
func TestInfoInventorySourceClearsDiscMainAssociationWithoutSelection(t *testing.T) {
	s, db, _, job, object, policy, probe := discPublicationFixture(t)
	c := catalogtest.New(t, db)
	revision := infoDiscMediaStructure(t, s, db, job, object, policy, probe)
	var token string
	if err := db.QueryRow(`SELECT asset_id FROM inventory_objects WHERE id=?`, object).Scan(&token); err != nil {
		t.Fatal(err)
	}
	main := infoDiscMediaObservation(t, db, object, probe.Kind, probe.OrderedTitles()[0])
	applyInfoDiscMediaCanonical(t, db, object, revision, main, 7, 4)
	plain := assets.Facts{
		Container:     "mkv",
		VideoCodec:    "h264",
		AudioCodec:    "aac",
		Duration:      60,
		Streams:       []assets.Stream{{Index: 0, Type: "video", Codec: "h264"}, {Index: 1, Type: "audio", Codec: "aac"}},
		ChapterStatus: "known",
	}
	receipt, err := acceptInventoryInfoForTest(t, s, context.Background(), job, object, revision, policy, plain, false)
	if err != nil || receipt.State != compactcatalog.InfoInputAcceptedPending {
		t.Fatal("clear source receipt", receipt, err)
	}
	c.Drain()
	key, streams, chapters, err := infoDiscMediaCanonicalFacts(t, db, object, token)
	if err != nil {
		t.Fatal(err)
	}
	if key != "" || streams != 0 || chapters != 0 {
		t.Fatal("main association survived a nil selection", key, streams, chapters)
	}
	var timed, members int
	if err := db.QueryRow(`SELECT count(*) FROM inventory_disc_title_media WHERE object_id=?`, object).Scan(&timed); err != nil || timed != 1 {
		t.Fatal("clear retired accepted timed title rows", timed, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM inventory_disc_media_members WHERE object_id=?`, object).Scan(&members); err != nil || members != len(main.DiscSelection.Members) {
		t.Fatal("clear retired accepted members", members, err)
	}
	// A second nil-selection analysis is already clear, so it is a no-op.
	again, err := acceptInventoryInfoForTest(t, s, context.Background(), job, object, revision, policy, plain, false)
	if err != nil || again.State != compactcatalog.InfoInputApplied || again.ChangeID != 0 {
		t.Fatal("already-clear association restaged", again, err)
	}
}

// TestAcceptDiscAlternateStagesRequestedTitleOnly proves the alternate producer
// keeps the receipt contract and the main-only owner clause: it refuses a title
// without its exact persisted request, stages only the requested title's timed
// media, never touches the main association or member list, and reports a
// truthful no-op once that title is current.
func TestAcceptDiscAlternateStagesRequestedTitleOnly(t *testing.T) {
	s, db, _, job, object, policy, probe := discPublicationFixture(t)
	c := catalogtest.New(t, db)
	revision := infoDiscMediaStructure(t, s, db, job, object, policy, probe)
	main := infoDiscMediaObservation(t, db, object, probe.Kind, probe.OrderedTitles()[0])
	alt := infoDiscMediaObservation(t, db, object, probe.Kind, probe.OrderedTitles()[1])
	alt.DiscAlternate = true
	alt.ChapterStatus = "known"
	applyInfoDiscMediaCanonical(t, db, object, revision, main, 3, 2)
	if _, err := s.AcceptDiscAlternate(context.Background(), job, object, revision, policy, alt); !errors.Is(err, ErrDiscSelectionChanged) {
		t.Fatal("alternate published without its title request", err)
	}
	if _, err := db.Exec(`INSERT INTO inventory_disc_title_requests(job_id,object_id,title_key,source_revision,fingerprint) VALUES(?,?,?,?,?)`, job, object, alt.DiscSelection.TitleKey, revision, alt.DiscSelection.StructureFingerprint); err != nil {
		t.Fatal(err)
	}
	receipt, err := acceptDiscAlternateForTest(t, s, context.Background(), job, object, revision, policy, alt)
	if err != nil || receipt.State != compactcatalog.InfoInputAcceptedPending {
		t.Fatal("alternate source receipt", receipt, err)
	}
	c.Drain()
	var timed int
	if err := db.QueryRow(`SELECT count(*) FROM inventory_disc_title_media WHERE object_id=? AND title_key=?`, object, alt.DiscSelection.TitleKey).Scan(&timed); err != nil || timed != 1 {
		t.Fatal("requested timed title not installed", timed, err)
	}
	var key string
	var streams, chapters int64
	if err := db.QueryRow(`SELECT media_title_key,media_stream_revision,media_chapter_revision FROM inventory_disc_facts WHERE object_id=?`, object).Scan(&key, &streams, &chapters); err != nil {
		t.Fatal(err)
	}
	if key != main.DiscSelection.TitleKey || streams != 3 || chapters != 2 {
		t.Fatal("alternate disturbed the main association", key, streams, chapters)
	}
	again, err := acceptDiscAlternateForTest(t, s, context.Background(), job, object, revision, policy, alt)
	if err != nil || again.State != compactcatalog.InfoInputApplied || again.ChangeID != 0 {
		t.Fatal("unchanged alternate was not an applied no-op", again, err)
	}
}
