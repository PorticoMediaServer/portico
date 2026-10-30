package compactcatalog_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// This file exercises the bounded per-holder disc subtitle projection against
// the real fresh-install schema and the real normalized source publication.
// The disc rows are staged through PublishInfoSource exactly as
// catalog.acceptInventoryInfo/AcceptDiscAlternate stage them; the fixture
// supplies the already-accepted observation and title rows those producers
// require, and never calls a retired all-holder helper.
//
// Acceptance now freezes A5's coherent object directory in the same writer as
// the typed body. A sealed frame can therefore observe a truthful
// ErrInfoInputDeferred while source41 is still walking its finite pass, and the
// fixture drives the real compact Worker between attempts just as ingestion
// does. It never forges the phase-1 owner pin: the accepted frame itself must
// carry the lowest raw ID chosen before any probe test.

type discProjectionFixture struct {
	t           *testing.T
	c           *catalogtest.Catalog
	lib         int64
	asset       int64
	token       string
	holders     []int64
	object      string
	revision    string
	fingerprint string
	source      string
	incarnation string
	generation  int64
	policy      int64
	job         string
	frame       int
}

const discProjectionDurationMS = 5400000

func newDiscProjectionFixture(t *testing.T, holders int) *discProjectionFixture {
	t.Helper()
	c := catalogtest.Open(t)
	lib := c.Library("discproj", "Discs", "movie", "/discproj")
	sum := sha256.Sum256([]byte("disc projection fingerprint"))
	f := &discProjectionFixture{
		t:           t,
		c:           c,
		lib:         lib,
		source:      "discproj",
		object:      "disc-observation",
		revision:    "revision-1",
		fingerprint: hex.EncodeToString(sum[:]),
		job:         "disc-projection-job",
	}
	for i := 0; i < holders; i++ {
		item := c.Entity(compactcatalog.Entity{Library: lib, Kind: compactcatalog.Movie, Key: fmt.Sprintf("discproj:%d", i), Title: "Disc"}, nil)
		f.holders = append(f.holders, item.ID)
		if i == 0 {
			f.asset, f.token = c.File(item.ID, "/discproj/VIDEO_TS", 5400)
			continue
		}
		asset := f.asset
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			return compactcatalog.LinkAssetTx(ctx, tx, item.ID, asset, compactcatalog.Link{})
		})
	}
	var size, modified int64
	if err := c.DB.QueryRow(`SELECT size,modified_ns FROM catalog_assets WHERE id=?`, f.asset).Scan(&size, &modified); err != nil {
		t.Fatal(err)
	}
	if err := c.DB.QueryRow(`SELECT incarnation,generation FROM library_sources WHERE id=?`, f.source).Scan(&f.incarnation, &f.generation); err != nil {
		t.Fatal(err)
	}
	if err := c.DB.QueryRow(`SELECT revision FROM library_scan_policies WHERE library_id=?`, "discproj").Scan(&f.policy); err != nil {
		t.Fatal(err)
	}
	// The disc witness is a live producer: the real job/run pair and the seen
	// object keep recovery from treating the staged frame as a dead witness
	// while source41 catches up to its coherent pass.
	c.Exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES(?,?,'running','now')`, f.job, f.source)
	c.Exec(`INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision)
 SELECT ?,id,generation,incarnation,root_identity,? FROM library_sources WHERE id=?`, f.job, f.policy, f.source)
	c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,state)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,'available')`, f.object, f.source, f.token, f.incarnation, "VIDEO_TS", "object-identity", f.revision, "{}", size, modified, f.job)
	c.Exec(`INSERT INTO asset_stream_facts(asset_id,revision,size,modified_ns,fingerprint,detail_version) VALUES(?,3,?,?,?,1)`, f.token, size, modified, "stream-facts")
	c.Drain()
	return f
}

// publish accepts one normalized source frame through the real public writer.
// Acceptance freezes A5's coherent owner observation in the same writer as the
// typed body. If source41 has not finished its finite pass yet the writer
// truthfully returns ErrInfoInputDeferred and this fixture drives the real
// compact Worker between attempts, exactly as ingestion does. The fixture then
// proves the accepted frame already carries its own phase-1 pin; it never
// writes one by hand.
func (f *discProjectionFixture) publish(mask int64, families []compactcatalog.InfoSourceFamily) int64 {
	f.t.Helper()
	f.frame++
	var epoch int64
	if err := f.c.DB.QueryRow(`SELECT info_epoch FROM catalog_assets WHERE id=?`, f.asset).Scan(&epoch); err != nil {
		f.t.Fatal(err)
	}
	fingerprint := sha256.Sum256([]byte(fmt.Sprintf("disc projection frame %d", f.frame)))
	spec := compactcatalog.InfoInputSpec{
		AssetID:       f.asset,
		Token:         f.token,
		ExpectedEpoch: epoch,
		FamilyMask:    mask,
		Fingerprint:   fingerprint,
		Witnesses: []compactcatalog.InfoWitness{{
			Kind:             compactcatalog.InfoWitnessDisc,
			JobID:            f.job,
			SourceID:         f.source,
			SourceGeneration: f.generation,
			RootIncarnation:  f.incarnation,
			ObjectID:         f.object,
			ObjectRevision:   f.revision,
			PolicyRevision:   f.policy,
			Fingerprint:      fingerprint,
		}},
	}
	accept := func() (compactcatalog.InfoAcceptReceipt, error) {
		return compactcatalog.PublishInfoSource(context.Background(), f.c.DB,
			func(context.Context, *sql.Tx) error { return nil },
			func(context.Context, *sql.Tx) (compactcatalog.InfoInputSpec, []compactcatalog.InfoSourceFamily, error) {
				return spec, families, nil
			})
	}
	receipt, err := accept()
	for attempt := 0; errors.Is(err, compactcatalog.ErrInfoInputDeferred) || errors.Is(err, compactcatalog.ErrInfoInputBusy); attempt++ {
		if attempt >= 1000 {
			f.t.Fatalf("accept disc source frame: %v", err)
		}
		f.coherentSource()
		receipt, err = accept()
	}
	if err != nil {
		f.t.Fatalf("accept disc source frame: %v", err)
	}
	if receipt.State != compactcatalog.InfoInputAcceptedPending && receipt.State != compactcatalog.InfoInputApplied {
		f.t.Fatalf("disc source receipt state %v", receipt.State)
	}
	// A5 pins the lowest eligible raw object before testing its probe. The
	// accepted header must already hold exactly that point choice.
	var pinned bool
	var owner, revision sql.NullString
	if err := f.c.DB.QueryRow(`SELECT copy_family=1,target_owner_object,target_owner_revision FROM catalog_info_changes WHERE id=?`, receipt.ChangeID).
		Scan(&pinned, &owner, &revision); err != nil {
		f.t.Fatal(err)
	}
	if !pinned || owner.String != f.object || revision.String != f.revision {
		f.t.Fatalf("accepted disc frame pinned %q/%q (pinned=%v), want %q/%q", owner.String, revision.String, pinned, f.object, f.revision)
	}
	return receipt.ChangeID
}

// coherentSource runs the real compact Worker until source41 reaches the exact
// coherent phase 3 that freezeInfoInputOwnerTx accepts: the pass revision is
// current, the frame is not a rerun, and the captured availability epoch still
// matches. It never installs a source row or a pin itself.
func (f *discProjectionFixture) coherentSource() {
	f.t.Helper()
	worker := compactcatalog.NewWorker(f.c.DB)
	for i := 0; i < 1000; i++ {
		var ready bool
		if err := f.c.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_upstream_sources w
 JOIN catalog_facet_source_state s ON s.singleton=1
 WHERE w.domain=41 AND w.source_id=? AND w.token=? AND w.phase=3
 AND w.pass_revision=w.revision AND w.after_key<>'rerun' AND w.source_epoch=s.availability_epoch)`, f.asset, f.token).Scan(&ready); err != nil {
			f.t.Fatal(err)
		}
		if ready {
			return
		}
		if _, err := worker.Step(context.Background(), 8); err != nil {
			f.t.Fatal(err)
		}
	}
	f.t.Fatal("A5 coherent source pass never reached phase 3")
}

func (f *discProjectionFixture) advance(change int64) bool {
	f.t.Helper()
	done := false
	err := dbwork.WithWriteTxContext(context.Background(), f.c.DB, dbwork.ClassBackgroundMedia, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		done, err = compactcatalog.AdvanceInfoPublicationTx(ctx, tx, f.asset, f.token, compactcatalog.NewInfoMaterializer())
		return err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return done
}

type discCursorState struct {
	phase, family, copyPhase, copyAux, item int64
}

// drive runs the real publication owner until the accepted frame retires and
// records the durable copy header after every bounded transaction.
func (f *discProjectionFixture) drive(change int64) []discCursorState {
	f.t.Helper()
	var states []discCursorState
	for i := 0; i < 500; i++ {
		done := f.advance(change)
		var s discCursorState
		err := f.c.DB.QueryRow(`SELECT phase,copy_family,copy_phase,copy_aux,copy_id FROM catalog_info_changes WHERE id=?`, change).
			Scan(&s.phase, &s.family, &s.copyPhase, &s.copyAux, &s.item)
		if err == nil {
			states = append(states, s)
		} else if err != sql.ErrNoRows {
			f.t.Fatal(err)
		}
		if done {
			return states
		}
	}
	f.t.Fatal("accepted disc source frame never retired")
	return nil
}

func (f *discProjectionFixture) facetStream(object, token, revision, factsRevision string, streams []compactcatalog.InfoDiscTitleMediaStreamRow) (int64, []compactcatalog.InfoSourceFamily) {
	f.t.Helper()
	title := compactcatalog.InfoDiscTitleRow{
		ObjectID: object, TitleKey: "title-a", SelectionToken: "selection-title-a",
		TitleNumber: 1, DiscOrder: 0, DurationMS: discProjectionDurationMS, AngleCount: 1,
	}
	fact := compactcatalog.InfoDiscFactRow{
		ObjectID: object, AssetID: token, SourceRevision: revision, SourceGeneration: 1,
		RootIncarnation: f.incarnation, Kind: "dvd", Packaging: "folder", Encryption: "none",
		Fingerprint: f.fingerprint, ChoiceRevision: 1, MediaTitleKey: "title-a",
		MediaStreamRevision: 3, MediaChapterRevision: 3,
	}
	media := compactcatalog.InfoDiscTitleMediaRow{
		ObjectID: object, TitleKey: "title-a", SourceRevision: revision, Fingerprint: f.fingerprint,
		Revision: factsRevision, Container: "mpeg2video", VideoCodec: "mpeg2video", AudioCodec: "ac3",
		Width: 720, Height: 480, DurationMS: discProjectionDurationMS, TimingKnown: 1,
	}
	for i := range streams {
		streams[i].ObjectID = object
		streams[i].TitleKey = "title-a"
	}
	families := []compactcatalog.InfoSourceFamily{
		{Number: 10, Count: 1, Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			row := fact
			row.InputOrdinal = i
			return row, nil
		}},
		{Number: 11, Count: 1, Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			row := title
			row.InputOrdinal = i
			return row, nil
		}},
		{Number: 16, Count: 1, Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			row := media
			row.InputOrdinal = i
			return row, nil
		}},
		{Number: 17, Count: int64(len(streams)), Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			if i < 0 || i >= int64(len(streams)) {
				return nil, compactcatalog.ErrInfoInvariant
			}
			row := streams[i]
			row.InputOrdinal = i
			return row, nil
		}},
	}
	return int64(1<<9 | 1<<10 | 1<<15 | 1<<16), families
}

func discProjectionStreams() []compactcatalog.InfoDiscTitleMediaStreamRow {
	return []compactcatalog.InfoDiscTitleMediaStreamRow{
		{StreamIndex: 2, Type: "subtitle", Codec: "dvd_subtitle", Language: "eng", Title: "", IsDefault: 1, IsForced: 0, DetailJSON: `{"hearingImpaired":true}`},
		{StreamIndex: 3, Type: "subtitle", Codec: "unsupported_subtitle_codec", Language: "eng", Title: "Unsupported"},
		{StreamIndex: 4, Type: "subtitle", Codec: "hdmv_pgs_subtitle", Language: "fra", Title: "Signs", IsDefault: 0, IsForced: 1},
	}
}

func infoDiscResourceID(item int64, token, object, title, revision string, policy int64, stream int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("disc-subtitle-v1:%d:%s:%s:%s:%s:%d:%d", item, token, object, title, revision, policy, stream)))
	return hex.EncodeToString(sum[:])
}

func infoDiscoveredResourceID(item int64, discovery string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("discovered:%d:%s", item, discovery)))
	return hex.EncodeToString(sum[:])
}

func TestInfoDiscSubtitleProjectionEmitsExactAcceptedTitleResources(t *testing.T) {
	f := newDiscProjectionFixture(t, 2)
	mask, families := f.facetStream(f.object, f.token, f.revision, "facts-a", discProjectionStreams())
	change := f.publish(mask, families)
	states := f.drive(change)

	// The accepted timed title installs its canonical rows only during phase 2.
	var mediaRevision, mediaSource, mediaFingerprint string
	var timing int64
	if err := f.c.DB.QueryRow(`SELECT revision,source_revision,fingerprint,timing_known FROM inventory_disc_title_media WHERE object_id=? AND title_key='title-a'`, f.object).
		Scan(&mediaRevision, &mediaSource, &mediaFingerprint, &timing); err != nil {
		t.Fatal(err)
	}
	if mediaRevision != "facts-a" || mediaSource != f.revision || mediaFingerprint != f.fingerprint || timing != 1 {
		t.Fatalf("accepted timed title %q/%q/%q timing%d", mediaRevision, mediaSource, mediaFingerprint, timing)
	}

	for _, item := range f.holders {
		for _, stream := range []struct {
			index  int64
			format string
			lang   string
			title  string
			render string
			def    int64
			forced int64
			hears  int64
		}{
			{2, "vobsub", "en", "Subtitles SDH", "burn_in", 1, 0, 1},
			{4, "pgs", "fr", "Signs", "burn_in", 0, 1, 0},
		} {
			id := infoDiscResourceID(item, f.token, f.object, "title-a", mediaRevision, f.policy, int(stream.index))
			var sourceID, scope, owner, discObject, discTitle, discRevision, discSource, discFingerprint string
			var current, discovered, discPolicy, discStream, originUS, discTiming int64
			if err := f.c.DB.QueryRow(`SELECT source_id,scope,owner,current_revision,discovered FROM subtitle_resources WHERE id=?`, id).
				Scan(&sourceID, &scope, &owner, &current, &discovered); err != nil {
				t.Fatalf("resource %s: %v", id, err)
			}
			if sourceID != f.token || scope != "shared" || owner != "" || current != 1 || discovered != 0 {
				t.Fatalf("resource shape %q/%q/%q/%d/%d", sourceID, scope, owner, current, discovered)
			}
			var format, language, title, origin string
			var sourceSize, sourceModified, sourceFacts int64
			if err := f.c.DB.QueryRow(`SELECT format,language,title,origin,source_size,source_modified_ns,source_facts_revision FROM subtitle_revisions WHERE resource_id=? AND revision=1`, id).
				Scan(&format, &language, &title, &origin, &sourceSize, &sourceModified, &sourceFacts); err != nil {
				t.Fatal(err)
			}
			if format != stream.format || language != stream.lang || title != stream.title || origin != "embedded" {
				t.Fatalf("revision %q/%q/%q/%q", format, language, title, origin)
			}
			if sourceFacts != 3 {
				t.Fatalf("source facts revision %d", sourceFacts)
			}
			var assetSize, assetModified int64
			if err := f.c.DB.QueryRow(`SELECT size,modified_ns FROM catalog_assets WHERE id=?`, f.asset).Scan(&assetSize, &assetModified); err != nil {
				t.Fatal(err)
			}
			if sourceSize != assetSize || sourceModified != assetModified {
				t.Fatalf("source scalar %d/%d, want %d/%d", sourceSize, sourceModified, assetSize, assetModified)
			}
			var renderer string
			var isDefault, isForced, hearing int64
			if err := f.c.DB.QueryRow(`SELECT renderer,is_default,is_forced,hearing_impaired FROM subtitle_render_revisions WHERE resource_id=? AND revision=1`, id).
				Scan(&renderer, &isDefault, &isForced, &hearing); err != nil {
				t.Fatal(err)
			}
			if renderer != stream.render || isDefault != stream.def || isForced != stream.forced || hearing != stream.hears {
				t.Fatalf("render %q/%d/%d/%d", renderer, isDefault, isForced, hearing)
			}
			if err := f.c.DB.QueryRow(`SELECT object_id,title_key,title_facts_revision,stream_index,source_revision,fingerprint,policy_revision,origin_us,timing_known FROM subtitle_disc_resources WHERE resource_id=?`, id).
				Scan(&discObject, &discTitle, &discRevision, &discStream, &discSource, &discFingerprint, &discPolicy, &originUS, &discTiming); err != nil {
				t.Fatal(err)
			}
			if discObject != f.object || discTitle != "title-a" || discRevision != mediaRevision || discStream != stream.index ||
				discSource != f.revision || discFingerprint != f.fingerprint || discPolicy != f.policy || originUS != 0 || discTiming != 1 {
				t.Fatalf("disc facts %q/%q/%q/%d/%q/%q/%d/%d/%d", discObject, discTitle, discRevision, discStream, discSource, discFingerprint, discPolicy, originUS, discTiming)
			}
		}
	}

	// One raw stream per committed quantum: the durable cursor is initialised
	// at phase 0/aux 0, then visits each sparse index exactly once, including
	// the unsupported stream that emits no resource. No negative copy_aux value
	// (a phase-0 recovery sentinel) is ever written by the apply phase.
	perHolder := map[int64][]discCursorState{}
	for _, s := range states {
		if s.phase == 4 && s.family == 2 {
			perHolder[s.item] = append(perHolder[s.item], s)
		}
	}
	for _, item := range f.holders {
		got := perHolder[item]
		want := []discCursorState{{copyPhase: 0, copyAux: 0}, {copyPhase: 2, copyAux: 2}, {copyPhase: 2, copyAux: 3}, {copyPhase: 2, copyAux: 4}}
		if len(got) != len(want) {
			t.Fatalf("holder %d disc cursor %+v", item, got)
		}
		for i := range want {
			if got[i].copyPhase != want[i].copyPhase || got[i].copyAux != want[i].copyAux {
				t.Fatalf("holder %d disc cursor %+v", item, got)
			}
		}
	}
}

func TestInfoDiscSubtitleProjectionPreservesAlternateTitlesAndClear(t *testing.T) {
	f := newDiscProjectionFixture(t, 1)
	item := f.holders[0]
	mask, families := f.facetStream(f.object, f.token, f.revision, "facts-a", discProjectionStreams())
	f.drive(f.publish(mask, families))
	mainResources := 2
	if n := countDiscResources(t, f.c.DB, f.token); n != mainResources {
		t.Fatalf("main title resources %d", n)
	}

	// The structure publication already installed every title row; an alternate
	// timed accept stages only its own title's media and resources.
	f.c.Exec(`INSERT INTO inventory_disc_titles(object_id,title_key,selection_token,title_number,disc_order,duration_ms,angle_count) VALUES(?,?,?,?,?,?,?)`,
		f.object, "title-b", "selection-title-b", 2, 1, discProjectionDurationMS, 1)
	alternateTitle := compactcatalog.InfoDiscTitleRow{
		ObjectID: f.object, TitleKey: "title-b", SelectionToken: "selection-title-b",
		TitleNumber: 2, DiscOrder: 1, DurationMS: discProjectionDurationMS, AngleCount: 1,
	}
	parentFact := compactcatalog.InfoDiscFactRow{
		ObjectID: f.object, AssetID: f.token, SourceRevision: f.revision, SourceGeneration: 1,
		RootIncarnation: f.incarnation, Kind: "dvd", Packaging: "folder", Encryption: "none",
		Fingerprint: f.fingerprint, ChoiceRevision: 1, MediaTitleKey: "title-a",
		MediaStreamRevision: 3, MediaChapterRevision: 3,
	}
	alternateMedia := compactcatalog.InfoDiscTitleMediaRow{
		ObjectID: f.object, TitleKey: "title-b", SourceRevision: f.revision, Fingerprint: f.fingerprint,
		Revision: "facts-b", Container: "mpeg2video", VideoCodec: "mpeg2video", AudioCodec: "ac3",
		Width: 720, Height: 480, DurationMS: discProjectionDurationMS, TimingKnown: 1,
	}
	alternateStream := compactcatalog.InfoDiscTitleMediaStreamRow{
		ObjectID: f.object, TitleKey: "title-b", StreamIndex: 5, Type: "subtitle", Codec: "subrip",
		Language: "deu", Title: "Alternate",
	}
	alternateFamilies := []compactcatalog.InfoSourceFamily{
		{Number: 10, Count: 1, Parent: true, Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			row := parentFact
			row.InputOrdinal = i
			return row, nil
		}},
		{Number: 11, Count: 1, Parent: true, Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			row := alternateTitle
			row.InputOrdinal = i
			return row, nil
		}},
		{Number: 16, Count: 1, Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			row := alternateMedia
			row.InputOrdinal = i
			return row, nil
		}},
		{Number: 17, Count: 1, Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			row := alternateStream
			row.InputOrdinal = i
			return row, nil
		}},
	}
	f.drive(f.publish(int64(1<<15|1<<16), alternateFamilies))
	if n := countDiscResources(t, f.c.DB, f.token); n != mainResources+1 {
		t.Fatalf("alternate resources %d, want %d", n, mainResources+1)
	}
	alternateID := infoDiscResourceID(item, f.token, f.object, "title-b", "facts-b", f.policy, 5)
	if n := infoTestCount(t, f.c.DB, `SELECT count(*) FROM subtitle_resources WHERE id=?`, alternateID); n != 1 {
		t.Fatal("accepted alternate resource absent", n)
	}
	// The alternate accept updated only its own title; the accepted main title's
	// resources keep their exact identities.
	mainID := infoDiscResourceID(item, f.token, f.object, "title-a", "facts-a", f.policy, 2)
	if err := f.c.DB.QueryRow(`SELECT count(*) FROM subtitle_resources WHERE id=?`, mainID).Scan(new(int)); err != nil {
		t.Fatal(err)
	}
	if got := infoTestText(t, f.c.DB, `SELECT media_title_key FROM inventory_disc_facts WHERE object_id=?`, f.object); got != "title-a" {
		t.Fatalf("alternate accept replaced the main association %q", got)
	}

	// The main association clear (family10 only, empty family16) removes no
	// resource: currentness hides the old title and alternate resources remain.
	clearFact := parentFact
	clearFact.MediaTitleKey = ""
	clearFact.MediaStreamRevision = 0
	clearFact.MediaChapterRevision = 0
	clearFamilies := []compactcatalog.InfoSourceFamily{
		{Number: 10, Count: 1, Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			row := clearFact
			row.InputOrdinal = i
			return row, nil
		}},
	}
	f.drive(f.publish(int64(1<<9), clearFamilies))
	if got := infoTestText(t, f.c.DB, `SELECT media_title_key FROM inventory_disc_facts WHERE object_id=?`, f.object); got != "" {
		t.Fatalf("main association was not cleared: %q", got)
	}
	if n := countDiscResources(t, f.c.DB, f.token); n != mainResources+1 {
		t.Fatalf("clear removed resources: %d", n)
	}

	// An empty family16 manifest is the structure-wide timed-title clear. The
	// materializer removes the accepted timed rows with its bounded cursor and
	// the projection emits nothing, while already-created resources are kept.
	timedClear := []compactcatalog.InfoSourceFamily{
		{Number: 10, Count: 1, Parent: true, Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			row := clearFact
			row.InputOrdinal = i
			return row, nil
		}},
		{Number: 11, Count: 1, Parent: true, Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			row := alternateTitle
			row.InputOrdinal = i
			return row, nil
		}},
		{Number: 16, Count: 0},
		{Number: 17, Count: 0},
	}
	f.drive(f.publish(int64(1<<15|1<<16), timedClear))
	if n := infoTestCount(t, f.c.DB, `SELECT count(*) FROM inventory_disc_title_media WHERE object_id=?`, f.object); n != 0 {
		t.Fatalf("empty family16 left %d accepted timed titles", n)
	}
	if n := countDiscResources(t, f.c.DB, f.token); n != mainResources+1 {
		t.Fatalf("timed clear removed resources: %d", n)
	}
}

func TestInfoDiscSubtitleProjectionSequencesDiscoveredBeforeDiscPerHolder(t *testing.T) {
	f := newDiscProjectionFixture(t, 2)
	var size, modified int64
	if err := f.c.DB.QueryRow(`SELECT size,modified_ns FROM catalog_assets WHERE id=?`, f.asset).Scan(&size, &modified); err != nil {
		t.Fatal(err)
	}
	f.c.Exec(`INSERT INTO asset_subtitle_facts(asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint) VALUES(?,1,?,?,0,1,'known','subtitle-facts')`, f.token, size, modified)

	fact := compactcatalog.InfoDiscFactRow{
		ObjectID: f.object, AssetID: f.token, SourceRevision: f.revision, SourceGeneration: 1,
		RootIncarnation: f.incarnation, Kind: "dvd", Packaging: "folder", Encryption: "none",
		Fingerprint: f.fingerprint, ChoiceRevision: 1, MediaTitleKey: "title-c",
		MediaStreamRevision: 3, MediaChapterRevision: 3,
	}
	title := compactcatalog.InfoDiscTitleRow{
		ObjectID: f.object, TitleKey: "title-c", SelectionToken: "selection-title-c",
		TitleNumber: 3, DiscOrder: 2, DurationMS: discProjectionDurationMS, AngleCount: 1,
	}
	media := compactcatalog.InfoDiscTitleMediaRow{
		ObjectID: f.object, TitleKey: "title-c", SourceRevision: f.revision, Fingerprint: f.fingerprint,
		Revision: "facts-c", Container: "mpeg2video", VideoCodec: "mpeg2video", AudioCodec: "ac3",
		Width: 720, Height: 480, DurationMS: discProjectionDurationMS, TimingKnown: 1,
	}
	stream := compactcatalog.InfoDiscTitleMediaStreamRow{
		ObjectID: f.object, TitleKey: "title-c", StreamIndex: 6, Type: "subtitle", Codec: "webvtt",
		Language: "eng", Title: "Combined",
	}
	subtitleFact := compactcatalog.InfoSubtitleFactRow{
		AssetID: f.token, Revision: 1, Size: size, ModifiedNS: modified, TimingKnown: 1,
		Status: "known", Fingerprint: "subtitle-facts",
	}
	subtitle := compactcatalog.InfoSubtitleRow{
		ID: "subtitle-combined", AssetID: f.token, Origin: "embedded", Locator: "6",
		StreamIndex: 6, Format: "srt", Language: "eng", Title: "Combined",
	}
	families := []compactcatalog.InfoSourceFamily{
		{Number: 6, Count: 1, Parent: true, Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			row := subtitleFact
			row.InputOrdinal = i
			return row, nil
		}},
		{Number: 8, Count: 1, Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			row := subtitle
			row.InputOrdinal = i
			return row, nil
		}},
		{Number: 10, Count: 1, Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			row := fact
			row.InputOrdinal = i
			return row, nil
		}},
		{Number: 11, Count: 1, Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			row := title
			row.InputOrdinal = i
			return row, nil
		}},
		{Number: 16, Count: 1, Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			row := media
			row.InputOrdinal = i
			return row, nil
		}},
		{Number: 17, Count: 1, Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			row := stream
			row.InputOrdinal = i
			return row, nil
		}},
	}
	change := f.publish(int64(1<<7|1<<9|1<<10|1<<15|1<<16), families)
	states := f.drive(change)

	for _, item := range f.holders {
		seenDisc := false
		for _, s := range states {
			if s.phase != 4 || s.item != item {
				continue
			}
			if s.family == 2 {
				seenDisc = true
				// The discovered phase completed in an earlier or the same
				// transaction; its resource must already exist before the disc
				// cursor is published.
				if n := infoTestCount(t, f.c.DB, `SELECT count(*) FROM subtitle_resources WHERE id=?`, infoDiscoveredResourceID(item, "subtitle-combined")); n != 1 {
					t.Fatalf("holder %d disc phase began before the discovered resource existed", item)
				}
			}
			if s.family == 1 && seenDisc {
				t.Fatalf("holder %d restarted the discovered phase after the disc phase began", item)
			}
		}
		if !seenDisc {
			t.Fatalf("holder %d never reached the disc phase", item)
		}
		if n := infoTestCount(t, f.c.DB, `SELECT count(*) FROM subtitle_resources WHERE id=?`, infoDiscResourceID(item, f.token, f.object, "title-c", "facts-c", f.policy, 6)); n != 1 {
			t.Fatalf("holder %d had no combined disc resource", item)
		}
	}
}

func countDiscResources(t *testing.T, db *sql.DB, token string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM subtitle_resources r JOIN subtitle_disc_resources d ON d.resource_id=r.id WHERE r.source_id=?`, token).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func infoTestText(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	var value string
	if err := db.QueryRow(query, args...).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}
