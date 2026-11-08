package read

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func TestRSTechnicalCompleteSelectedFactsAndPendingPrefix(t *testing.T) {
	f, r := rsTechnicalFixture(t)
	w := rsTechnicalBegin(t, f, r, RSSelectedTechnicalInput{})
	p := rsTechnicalFinish(t, r, f, w)
	if p.Preparing || p.StreamState != "ready" || p.ChapterState != "ready" || p.SubtitleState != "ready" {
		t.Fatalf("states %+v", p)
	}
	if p.File.Container != "matroska" || p.File.VideoCodec != "h264" || p.File.Width != 1920 || p.File.Height != 1080 || p.File.Duration != 120 {
		t.Fatalf("file %+v", p.File)
	}
	if len(p.Streams) != 2 || len(p.Chapters) != 2 || len(p.Subtitles) != 1 {
		t.Fatalf("payload streams=%d chapters=%d subtitles=%d", len(p.Streams), len(p.Chapters), len(p.Subtitles))
	}
	if p.Streams[0].Kind != "video" || p.Streams[1].Kind != "audio" || p.Streams[1].Language != "eng" || p.Streams[1].Channels != 2 {
		t.Fatalf("streams %+v", p.Streams)
	}
	// The accepted link frame is still exposed, but a member never receives the
	// accepted path or owner disc inventory.
	if p.Path != nil || p.Disc != nil || p.DiscState != "missing" || p.MediaState != "missing" || p.PartIndex != 0 || p.Edition != "" || p.StartSeconds != 0 || p.EndSeconds != nil {
		t.Fatalf("member-only fields %+v", p)
	}
	if p.Subtitles[0] != (RSTechnicalSubtitle{Origin: "embedded", Index: 2, Format: "srt", Language: "eng", Title: "English SDH", Default: 1, Forced: 0, HearingImpaired: 1}) {
		t.Fatalf("subtitle %+v", p.Subtitles[0])
	}
	// The stored detail is exposed only for the current decoder revision, and
	// breadth stays in its scalar relational copy.
	if p.Streams[0].DetailState != "ready" || p.Streams[0].Detail == nil || p.Streams[0].Detail.Profile != "High" || p.Streams[0].Detail.DynamicRange != "hdr10" {
		t.Fatalf("detail %+v", p.Streams[0])
	}
	if p.Streams[0].HearingImpaired == nil || *p.Streams[0].HearingImpaired || p.Streams[0].Commentary == nil || *p.Streams[0].Commentary {
		t.Fatalf("dispositions %+v", p.Streams[0])
	}
	if p.Streams[0].HasBreadth != 0 || p.Streams[0].AudioVariant != nil || p.Streams[0].DoviRpu != nil {
		t.Fatalf("unobserved breadth leaked %+v", p.Streams[0])
	}
	// Music facts are present for the exact observation revision that measured
	// them and stay typed pointers.
	if p.File.SampleRate == nil || *p.File.SampleRate != 48000 || p.File.BitDepth == nil || *p.File.BitDepth != 24 || p.File.Channels == nil || *p.File.Channels != 2 || p.File.Lossless == nil || *p.File.Lossless != 1 {
		t.Fatalf("music facts %+v", p.File)
	}
}

func TestRSTechnicalOwnerPathDiscAndSelectedMedia(t *testing.T) {
	f, r := rsTechnicalFixture(t)
	f.exec(t, `UPDATE direct_memberships SET role='owner' WHERE account_id='a'`)
	w := rsTechnicalBegin(t, f, r, RSSelectedTechnicalInput{})
	p := rsTechnicalFinish(t, r, f, w)
	if p.Path == nil || *p.Path != "/media/title.mkv" {
		t.Fatalf("owner path %+v", p.Path)
	}
	if p.DiscState != "ready" || p.Disc == nil || p.Disc.Kind != "bluray" || p.Disc.Packaging != "iso" || p.Disc.Encryption != "none" {
		t.Fatalf("disc %+v", p.Disc)
	}
	// Owner titles sort by stored disc_order with title-number tie break.
	if len(p.Disc.Titles) != 2 || p.Disc.Titles[0].Number != 2 || p.Disc.Titles[1].Number != 1 {
		t.Fatalf("title order %+v", p.Disc.Titles)
	}
	if p.Disc.Titles[0].DurationMS != 60000 || p.Disc.Titles[0].Angles != 1 || p.Disc.Titles[1].Angles != 2 {
		t.Fatalf("title facts %+v", p.Disc.Titles)
	}
	if p.Disc.Chapters[1] != 2 || p.Disc.Chapters[2] != 0 {
		t.Fatalf("chapter counts %+v", p.Disc.Chapters)
	}
	// Selected media is the scan's exact display selection with matching ready
	// head revisions. It is not a playback grant.
	if p.MediaState != "ready" || p.Disc.Media == nil || p.Disc.Media.Number != 1 || p.Disc.Media.key != "pl:1" {
		t.Fatalf("selected media %+v", p.Disc)
	}
}

func TestRSTechnicalMissingStaleAndPendingHeads(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		f, r := rsTechnicalFixture(t)
		f.exec(t, `DELETE FROM asset_streams`)
		f.exec(t, `DELETE FROM asset_stream_facts`)
		f.exec(t, `DELETE FROM asset_chapters`)
		f.exec(t, `DELETE FROM asset_chapter_facts`)
		f.exec(t, `DELETE FROM asset_subtitles`)
		f.exec(t, `DELETE FROM asset_subtitle_facts`)
		p := rsTechnicalFinish(t, r, f, rsTechnicalBegin(t, f, r, RSSelectedTechnicalInput{}))
		if p.StreamState != "missing" || p.ChapterState != "missing" || p.SubtitleState != "missing" || len(p.Streams) != 0 || len(p.Chapters) != 0 || len(p.Subtitles) != 0 {
			t.Fatalf("missing heads %+v", p)
		}
	})
	t.Run("stale", func(t *testing.T) {
		f, r := rsTechnicalFixture(t)
		// Each basic head keeps its own acceptance frame; a frame that is not the
		// exact accepted asset size/mtime is stale, not an older ready answer.
		f.exec(t, `UPDATE asset_stream_facts SET size=999 WHERE asset_id='asset'`)
		f.exec(t, `UPDATE asset_chapter_facts SET modified_ns=999 WHERE asset_id='asset'`)
		f.exec(t, `UPDATE asset_subtitle_facts SET size=999 WHERE asset_id='asset'`)
		p := rsTechnicalFinish(t, r, f, rsTechnicalBegin(t, f, r, RSSelectedTechnicalInput{}))
		if p.StreamState != "stale" || p.ChapterState != "stale" || p.SubtitleState != "stale" || len(p.Streams) != 0 || len(p.Chapters) != 0 || len(p.Subtitles) != 0 {
			t.Fatalf("stale heads %+v", p)
		}
	})
	t.Run("pending", func(t *testing.T) {
		f, r := rsTechnicalFixture(t)
		f.exec(t, `UPDATE asset_chapter_facts SET status='invalid' WHERE asset_id='asset'`)
		f.exec(t, `UPDATE asset_subtitle_facts SET status='capacity' WHERE asset_id='asset'`)
		p := rsTechnicalFinish(t, r, f, rsTechnicalBegin(t, f, r, RSSelectedTechnicalInput{}))
		if p.StreamState != "ready" || p.ChapterState != "pending" || p.SubtitleState != "pending" {
			t.Fatalf("pending heads %+v", p)
		}
		if len(p.Chapters) != 0 || len(p.Subtitles) != 0 {
			t.Fatal("pending heads published rows")
		}
	})
	t.Run("detail", func(t *testing.T) {
		f, r := rsTechnicalFixture(t)
		f.exec(t, `UPDATE asset_streams SET detail_json='' WHERE asset_id='asset' AND stream_index=0`)
		p := rsTechnicalFinish(t, r, f, rsTechnicalBegin(t, f, r, RSSelectedTechnicalInput{}))
		if p.Streams[0].DetailState != "missing" || p.Streams[0].Detail != nil {
			t.Fatalf("missing detail %+v", p.Streams[0])
		}
	})
	t.Run("detail-stale", func(t *testing.T) {
		f, r := rsTechnicalFixture(t)
		f.exec(t, `UPDATE asset_stream_facts SET detail_version=4 WHERE asset_id='asset'`)
		p := rsTechnicalFinish(t, r, f, rsTechnicalBegin(t, f, r, RSSelectedTechnicalInput{}))
		if p.Streams[0].DetailState != "stale" || p.Streams[0].Detail != nil {
			t.Fatalf("stale detail %+v", p.Streams[0])
		}
	})
	t.Run("detail-malformed", func(t *testing.T) {
		f, r := rsTechnicalFixture(t)
		f.exec(t, `UPDATE asset_streams SET detail_json='{"profile":' WHERE asset_id='asset' AND stream_index=0`)
		w := rsTechnicalBegin(t, f, r, RSSelectedTechnicalInput{})
		var p RSTechnicalFacts
		var e error
		for n := 0; n < 40; n++ {
			p, e = rsTechnicalPoll(t, r, f, w)
			if e != nil || !p.Preparing {
				break
			}
		}
		if !errors.Is(e, ErrUnqualified) || len(p.Streams) != 0 {
			t.Fatalf("malformed detail %+v %v", p, e)
		}
	})
}

func TestRSTechnicalChangedRevisionsFailClosed(t *testing.T) {
	for name, c := range map[string]struct {
		mutate string
		want   error
	}{
		"stream":   {`UPDATE asset_stream_facts SET revision=revision+1 WHERE asset_id='asset'`, ErrStale},
		"chapter":  {`UPDATE asset_chapter_facts SET revision=revision+1 WHERE asset_id='asset'`, ErrStale},
		"subtitle": {`UPDATE asset_subtitle_facts SET revision=revision+1 WHERE asset_id='asset'`, ErrStale},
		"disc":     {`UPDATE inventory_disc_facts SET source_revision='r2' WHERE object_id='tech-object'`, ErrStale},
		"object":   {`UPDATE inventory_objects SET revision='r2' WHERE id='tech-object'`, ErrDenied},
	} {
		t.Run(name, func(t *testing.T) {
			f, r := rsTechnicalFixture(t)
			f.exec(t, `UPDATE direct_memberships SET role='owner' WHERE account_id='a'`)
			w := rsTechnicalBegin(t, f, r, RSSelectedTechnicalInput{})
			if p, e := rsTechnicalPoll(t, r, f, w); e != nil || !p.Preparing {
				t.Fatalf("bind %+v %v", p, e)
			}
			f.exec(t, c.mutate)
			p, e := rsTechnicalPoll(t, r, f, w)
			if !errors.Is(e, c.want) || p.Preparing || len(p.Streams) != 0 {
				t.Fatalf("changed %s published %+v %v", name, p, e)
			}
		})
	}
}

func TestRSTechnicalMultiObservationAmbiguity(t *testing.T) {
	t.Run("ambiguous", func(t *testing.T) {
		f, r := rsTechnicalFixture(t)
		f.exec(t, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('tech-second','tech-source','asset','root-a','second.mkv','r1','{}',1000,1)`)
		p, e := rsTechnicalPoll(t, r, f, rsTechnicalBegin(t, f, r, RSSelectedTechnicalInput{}))
		if !errors.Is(e, ErrDenied) || p.Preparing || len(p.Streams) != 0 {
			t.Fatalf("ambiguous observation %+v %v", p, e)
		}
	})
	t.Run("retired-extra", func(t *testing.T) {
		f, r := rsTechnicalFixture(t)
		f.exec(t, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,retired) VALUES('tech-old','tech-source','asset','root-a','old.mkv','r0','{}',900,0,1)`)
		p := rsTechnicalFinish(t, r, f, rsTechnicalBegin(t, f, r, RSSelectedTechnicalInput{}))
		if p.StreamState != "ready" || len(p.Streams) != 2 {
			t.Fatalf("retired observation blocked the selected one %+v", p)
		}
	})
}

func TestRSTechnicalRetiredRootAndObject(t *testing.T) {
	for name, mutate := range map[string]string{
		"object":      `UPDATE inventory_objects SET retired=1 WHERE id='tech-object'`,
		"unavailable": `UPDATE inventory_objects SET state='missing' WHERE id='tech-object'`,
		"library":     `UPDATE catalog_libraries SET retired=1 WHERE id=1`,
		"root":        `UPDATE library_sources SET health='root_changed' WHERE id='tech-source'`,
		"disabled":    `UPDATE library_sources SET enabled=0 WHERE id='tech-source'`,
	} {
		t.Run(name, func(t *testing.T) {
			f, r := rsTechnicalFixture(t)
			f.exec(t, mutate)
			p, e := rsTechnicalPoll(t, r, f, rsTechnicalBegin(t, f, r, RSSelectedTechnicalInput{}))
			if !errors.Is(e, ErrDenied) || p.Preparing || len(p.Streams) != 0 || p.File.Container != "" {
				t.Fatalf("retired %s published %+v %v", name, p, e)
			}
		})
	}
}

func TestRSTechnicalTypedNullableFacts(t *testing.T) {
	t.Run("unobserved-music", func(t *testing.T) {
		f, r := rsTechnicalFixture(t)
		f.exec(t, `UPDATE catalog_assets SET music_sample_rate=NULL,music_bit_depth=NULL,music_channels=NULL,music_lossless=NULL WHERE id=1`)
		p := rsTechnicalFinish(t, r, f, rsTechnicalBegin(t, f, r, RSSelectedTechnicalInput{}))
		if p.File.SampleRate != nil || p.File.BitDepth != nil || p.File.Channels != nil || p.File.Lossless != nil {
			t.Fatalf("null music facts invented values %+v", p.File)
		}
	})
	t.Run("moved-music-source", func(t *testing.T) {
		f, r := rsTechnicalFixture(t)
		// Music facts measured from another observation revision must not stand
		// in for the selected one, even though the columns are populated.
		f.exec(t, `UPDATE catalog_assets SET music_technical_source_revision='r0' WHERE id=1`)
		p := rsTechnicalFinish(t, r, f, rsTechnicalBegin(t, f, r, RSSelectedTechnicalInput{}))
		if p.File.SampleRate != nil || p.File.Lossless != nil {
			t.Fatalf("stale music revision leaked %+v", p.File)
		}
	})
	t.Run("breadth", func(t *testing.T) {
		f, r := rsTechnicalFixture(t)
		f.exec(t, `UPDATE asset_streams SET has_asset_stream_breadth=1,breadth_audio_description=0,breadth_audio_variant='',breadth_chroma_subsampling='',breadth_fps_numerator=0,breadth_fps_denominator=0,breadth_frame_rate_mode='',breadth_interlace_analysis='',breadth_telecine=0,breadth_dovi_rpu=0,breadth_dovi_base_layer=0,breadth_dovi_metadata_known=0,breadth_dovi_enhancement_type='',breadth_dovi_cm_version='',breadth_playback_frames_revision='',breadth_packet_peak_revision='' WHERE asset_id='asset' AND stream_index=0`)
		p := rsTechnicalFinish(t, r, f, rsTechnicalBegin(t, f, r, RSSelectedTechnicalInput{}))
		if p.Streams[0].HasBreadth != 1 || p.Streams[0].AudioDescription == nil || *p.Streams[0].AudioDescription != 0 || p.Streams[0].FpsNumerator == nil || p.Streams[0].DoviRpu == nil || *p.Streams[0].DoviRpu != 0 {
			t.Fatalf("breadth pointers %+v", p.Streams[0])
		}
	})
}

func TestRSTechnicalRejectsReadingAndBadInput(t *testing.T) {
	for _, kind := range []int64{20, 21, 14, 99} {
		in := RSSelectedTechnicalInput{Item: 1, Asset: 1, View: BrowseInput{Kind: kind, Library: 1, Limit: 1, Database: "db", TokenHash: "token", Now: time.Now()}}
		if validRSTechnical(in) {
			t.Fatalf("kind %d entered the timed-media family", kind)
		}
	}
	base := BrowseInput{Kind: 1, Library: 1, Limit: 40, Database: "db", TokenHash: "token", Now: time.Now()}
	for name, in := range map[string]RSSelectedTechnicalInput{
		"no-item":        {Asset: 1, View: base},
		"no-asset":       {Item: 1, View: base},
		"after":          {Item: 1, Asset: 1, View: BrowseInput{Kind: 1, Library: 1, Limit: 40, Database: "db", TokenHash: "token", Now: base.Now, After: &BrowseCursor{}}},
		"descending":     {Item: 1, Asset: 1, View: BrowseInput{Kind: 1, Library: 1, Limit: 40, Database: "db", TokenHash: "token", Now: base.Now, Descending: true}},
		"no-library":     {Item: 1, Asset: 1, View: BrowseInput{Kind: 1, Limit: 40, Database: "db", TokenHash: "token", Now: base.Now}},
		"no-now":         {Item: 1, Asset: 1, View: BrowseInput{Kind: 1, Library: 1, Limit: 40, Database: "db", TokenHash: "token"}},
		"foreign-policy": {Item: 1, Asset: 1, View: BrowseInput{Kind: 1, Library: 1, Limit: 40, Database: "db", TokenHash: "token", Now: base.Now, policy: readPolicyBinding{recipe: rsDetailSubjectPolicy, subject: 1}}},
	} {
		if validRSTechnical(in) {
			t.Fatalf("%s was accepted", name)
		}
	}
	f, _ := rsTechnicalFixture(t)
	if w, ref, e := BeginRSTechnicalWork(t.Context(), f.authority, 1, governor.JobKey{}, time.Now().Add(time.Second), 8<<20, RSSelectedTechnicalInput{}); w != nil || ref != nil || !errors.Is(e, ErrInput) {
		t.Fatalf("empty key admitted %v %v", w, ref)
	}
	if w, ref, e := BeginRSTechnicalWork(nil, f.authority, 1, governor.JobKey{Domain: 79, ID: "x"}, time.Now().Add(time.Second), 8<<20, RSSelectedTechnicalInput{}); w != nil || ref != nil || !errors.Is(e, ErrInput) {
		t.Fatalf("nil context admitted %v %v", w, ref)
	}
}

func TestRSTechnicalOwnerMediaStates(t *testing.T) {
	t.Run("pending", func(t *testing.T) {
		f, r := rsTechnicalFixture(t)
		f.exec(t, `UPDATE direct_memberships SET role='owner' WHERE account_id='a'`)
		// The selection exists but its measured stream/chapter revisions are not
		// the current ready heads: RS reports pending, not an older title.
		f.exec(t, `UPDATE inventory_disc_facts SET media_chapter_revision=2 WHERE object_id='tech-object'`)
		p := rsTechnicalFinish(t, r, f, rsTechnicalBegin(t, f, r, RSSelectedTechnicalInput{}))
		if p.Disc == nil || p.MediaState != "pending" || p.Disc.Media != nil {
			t.Fatalf("pending media %+v", p.Disc)
		}
		if len(p.Disc.Titles) != 2 {
			t.Fatalf("owner titles lost with pending media %+v", p.Disc.Titles)
		}
	})
	t.Run("missing", func(t *testing.T) {
		f, r := rsTechnicalFixture(t)
		f.exec(t, `UPDATE direct_memberships SET role='owner' WHERE account_id='a'`)
		f.exec(t, `UPDATE inventory_disc_facts SET media_title_key='' WHERE object_id='tech-object'`)
		p := rsTechnicalFinish(t, r, f, rsTechnicalBegin(t, f, r, RSSelectedTechnicalInput{}))
		if p.Disc == nil || p.MediaState != "missing" || p.Disc.Media != nil {
			t.Fatalf("missing media %+v", p.Disc)
		}
	})
	t.Run("encrypted", func(t *testing.T) {
		f, r := rsTechnicalFixture(t)
		f.exec(t, `UPDATE direct_memberships SET role='owner' WHERE account_id='a'`)
		// Encrypted disc images have no selected playable media display here.
		f.exec(t, `UPDATE inventory_disc_facts SET encryption='aacs' WHERE object_id='tech-object'`)
		p := rsTechnicalFinish(t, r, f, rsTechnicalBegin(t, f, r, RSSelectedTechnicalInput{}))
		if p.Disc == nil || p.MediaState != "missing" || p.Disc.Media != nil || p.Disc.Encryption != "aacs" {
			t.Fatalf("encrypted media %+v", p.Disc)
		}
	})
}

func TestRSTechnicalPreparationLifecycle(t *testing.T) {
	f, r := rsTechnicalFixture(t)
	w := rsTechnicalBegin(t, f, r, RSSelectedTechnicalInput{})
	// A different owner, reader or output grant cannot attach to this job.
	other := foregroundRequest()
	other.Owner = 2
	if _, _, e := r.poll(t.Context(), other, time.Second, f.output, RSSelectedTechnicalInput{View: f.input, Item: 1, Asset: 1, Work: w}); !errors.Is(e, ErrInput) {
		t.Fatalf("foreign owner attached: %v", e)
	}
	p := rsTechnicalFinish(t, r, f, w)
	if p.StreamState != "ready" {
		t.Fatal(p)
	}
	// Exactly one value handoff: a second poll on the same finished job refuses.
	if _, _, e := r.poll(t.Context(), foregroundRequest(), time.Second, f.output, RSSelectedTechnicalInput{View: f.input, Item: 1, Asset: 1, Work: w}); !errors.Is(e, ErrInput) {
		t.Fatalf("second handoff admitted: %v", e)
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	if e := w.Close(); e != nil {
		t.Fatal("idempotent close", e)
	}
}

func TestRSTechnicalPublicPollCustody(t *testing.T) {
	f, r := rsTechnicalFixture(t)
	w := rsTechnicalBegin(t, f, r, RSSelectedTechnicalInput{})
	for n := 0; n < 60; n++ {
		q, request, output := liveRequests("rs-technical-live")
		var p RSTechnicalFacts
		_, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
			var e error
			p, _, e = r.Poll(ctx, request, time.Second, out, RSSelectedTechnicalInput{View: f.input, Item: 1, Asset: 1, Work: w})
			return e
		})
		if e != nil {
			t.Fatal("live poll", n, e)
		}
		if p.Preparing {
			if len(p.Streams) != 0 || len(p.Chapters) != 0 || p.Subtitles != nil {
				t.Fatalf("live pending prefix escaped %+v", p)
			}
			continue
		}
		if p.StreamState != "ready" || len(p.Streams) != 2 || len(p.Chapters) != 2 {
			t.Fatalf("live complete %+v", p)
		}
		if u := f.authority.ForegroundUsage(); u.Live != 0 || u.Waiters != 0 {
			t.Fatalf("live custody not retired %+v", u)
		}
		return
	}
	t.Fatal("live custody did not complete")
}

func TestRSTechnicalInventoryCapacityRefusal(t *testing.T) {
	f, r := rsTechnicalFixture(t)
	// 4097 additional streams exceed the explicit per-family key cap; the family
	// refuses rather than truncating to a plausible-looking exact answer.
	f.exec(t, `WITH RECURSIVE n(i) AS (SELECT 2 UNION ALL SELECT i+1 FROM n WHERE i<4096) INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced) SELECT 'asset',i,'audio','aac','eng','',2,'stereo',0,0 FROM n`)
	w := rsTechnicalBegin(t, f, r, RSSelectedTechnicalInput{})
	var e error
	for n := 0; n < 20; n++ {
		_, e = rsTechnicalPoll(t, r, f, w)
		if e != nil {
			break
		}
	}
	// The governor read budget may refuse the 4097-key window before the
	// family's own cap; either is an explicit refusal, never a truncated answer.
	if !errors.Is(e, ErrUnqualified) && !errors.Is(e, governor.ErrSnapshotBudget) {
		t.Fatalf("capacity refusal %v", e)
	}
}

func TestRSTechnicalSourceFenceAndFileFrame(t *testing.T) {
	f, r := rsTechnicalFixture(t)
	w := rsTechnicalBegin(t, f, r, RSSelectedTechnicalInput{})
	if p, e := rsTechnicalPoll(t, r, f, w); e != nil || !p.Preparing {
		t.Fatalf("bind %+v %v", p, e)
	}
	// A raw file stat change breaks the accepted link/object frame; the family
	// fails closed instead of publishing the older accepted facts.
	f.exec(t, `UPDATE catalog_assets SET size=1001 WHERE id=1`)
	p, e := rsTechnicalPoll(t, r, f, w)
	if !errors.Is(e, ErrDenied) || p.Preparing || len(p.Streams) != 0 {
		t.Fatalf("changed file frame published %+v %v", p, e)
	}
}
