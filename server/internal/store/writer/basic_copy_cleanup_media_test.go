package writer

import (
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func basicCleanupMediaTestDefinitions() []stmt.Definition {
	return append(basicCopyStepTestDefinitions(), basicCopyCleanupMediaDefinitions()...)
}

// All native setup precedes one actual FULL startup Capture. Prerequisites
// commit separately; every bounded page uses the existing basicHandlerTurn.
func basicCleanupMediaFixture(t *testing.T, family int64, defs []stmt.Definition) (*executor, *commandTurn, basicStreamCopyFence) {
	t.Helper()
	w, input := basicCompleteAcceptFixture(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := w.db.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO asset_stream_facts VALUES('basic-asset',31,42,-11,'probe-fingerprint',1)`)
	exec(`INSERT INTO asset_chapter_facts VALUES('basic-asset',37,42,-11,'known','chapters:37')`)
	exec(`INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced) SELECT asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced FROM catalog_info_new_streams WHERE change_id=51`)
	exec(`UPDATE asset_streams SET has_asset_stream_breadth=1,breadth_audio_variant='retained'`)
	exec(`INSERT INTO asset_chapters SELECT asset_id,chapter_index,title,start_seconds,end_seconds FROM catalog_info_new_chapters WHERE change_id=51`)
	for key := int64(9); key <= 17; key++ {
		exec(`INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,has_asset_stream_breadth,breadth_audio_variant) VALUES('basic-asset',?,'audio','aac','','obsolete',2,'stereo',0,0,1,'old')`, key)
		exec(`INSERT INTO asset_chapters VALUES('basic-asset',?,'obsolete',0,1)`, key)
	}
	// An absent breadth key still consumes the canonical-key budget.
	exec(`UPDATE asset_streams SET has_asset_stream_breadth=0,breadth_audio_variant='absent payload' WHERE stream_index=10`)
	exec(`INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,info_epoch) VALUES(42,'other-asset',7,'/media/Other.mkv',42,-11,'','','',0,0,0,17)`)
	exec(`INSERT INTO asset_chapter_facts VALUES('other-asset',37,42,-11,'known','chapters:37')`)
	exec(`INSERT INTO asset_chapters VALUES('other-asset',9,'other',0,1)`)
	var captured []basicMediaRow
	var rowsErr error
	if family == 3 {
		d := basicCopyStreamDefinition(t, basicCopyStreamDefinitions(), basicCopyBreadthStaged)
		q := strings.Replace(d.SQL, "input_ordinal=?", "input_ordinal>=? ORDER BY input_ordinal", 1)
		rows, e := w.db.QueryContext(t.Context(), q, int64(51), int64(0))
		if e != nil {
			t.Fatal(e)
		}
		for rows.Next() {
			var ordinal int64
			var token string
			var r basicStreamBreadth
			if e = rows.Scan(&ordinal, &token, &r.index, &r.audioDescription, &r.audioVariant, &r.chroma, &r.fpsNumerator, &r.fpsDenominator, &r.frameMode, &r.interlace, &r.telecine, &r.doviRPU, &r.doviBase, &r.doviKnown, &r.doviEnhancement, &r.doviCM, &r.playbackFramesRevision, &r.packetPeakRevision); e != nil {
				t.Fatal(e)
			}
			captured = append(captured, r)
		}
		rowsErr = errors.Join(rows.Err(), rows.Close())
	} else {
		rows, e := w.db.QueryContext(t.Context(), `SELECT chapter_index,title,start_seconds,end_seconds FROM catalog_info_new_chapters WHERE change_id=51 ORDER BY input_ordinal`)
		if e != nil {
			t.Fatal(e)
		}
		for rows.Next() {
			var r basicChapter
			if e = rows.Scan(&r.index, &r.title, &r.start, &r.end); e != nil {
				t.Fatal(e)
			}
			captured = append(captured, r)
		}
		rowsErr = errors.Join(rows.Err(), rows.Close())
	}
	if rowsErr != nil {
		t.Fatal(rowsErr)
	}
	f, e := prepareBasicMediaFamily(input.stream.source, family, 2, captured)
	if e != nil {
		t.Fatal(e)
	}
	registry := nativeInventoryFixtureRegistry(t, w, defs, "basic_copy_cleanup_media.go", "basic_copy_cleanup_media_statements.go", "basic_copy_cleanup_media_test.go")
	turn := basicHandlerTurn(t, w, BasicAccept, defs, registry)
	accepted, e := acceptBasicOrdinaryInput(t.Context(), turn, input, 2000)
	if e != nil {
		t.Fatal(e)
	}
	input.stream.changeRevision = accepted.changeRevision
	input.snapshot.changeRevision = accepted.changeRevision
	p, e := prefenceBasicOrdinary(t.Context(), turn, basicPrefenceEffects{input: input})
	if e != nil {
		t.Fatal(e)
	}
	next := p
	next.family = family
	next.copyPhase = 1
	p, e = advanceBasicCopiedPosition(t.Context(), turn, f.source, p, next, input.stream.shapeID)
	if e != nil {
		t.Fatal(e)
	}
	basicMediaCommit(t, turn)
	return w, basicHandlerTurn(t, w, BasicAccept, defs, registry), basicStreamCopyFence{family: f, position: p, shapeID: input.stream.shapeID, fileSizeRevision: input.snapshot.fileSizeRevision, invalidation: input.snapshot.invalidationRevision}
}

func TestBasicCleanupMediaBoundedRetained(t *testing.T) {
	for _, family := range []int64{3, 5} {
		t.Run(string(rune('0'+family)), func(t *testing.T) {
			defs := basicCleanupMediaTestDefinitions()
			w, turn, f := basicCleanupMediaFixture(t, family, defs)
			for page := 0; page < 2; page++ {
				got, e := cleanupBasicMedia(t.Context(), turn, f)
				if e != nil || !got.hasAfter || got.more != (page == 0) {
					t.Fatal(got, e)
				}
				if page == 0 && got.id != 14 {
					t.Fatal("native first-page key", got)
				}
				// Caller persists the true key on the very same turn, even at exhaustion;
				// publication/family completion is deliberately outside this component.
				if page == 0 {
					var untouched bool
					if e = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT (SELECT has_asset_stream_breadth=1 AND title='obsolete' FROM asset_streams WHERE asset_id='basic-asset' AND stream_index=15) AND (SELECT title='obsolete' FROM asset_chapters WHERE asset_id='basic-asset' AND chapter_index=15)`).Scan(&untouched); e != nil || !untouched {
						t.Fatal("lookahead changed", untouched, e)
					}
				}
				next := f.position
				next.id = got.id
				next.aux = 1
				next, e = advanceBasicCopiedPosition(t.Context(), turn, f.family.source, f.position, next, f.shapeID)
				if e != nil {
					t.Fatal(e)
				}
				basicMediaCommit(t, turn)
				f.position = next
				if page == 0 {
					turn = basicHandlerTurn(t, w, BasicAccept, defs, turn.registry)
				}
			}
			var exact bool
			q := `SELECT (SELECT count(*)=2 FROM asset_chapters WHERE asset_id='basic-asset')`
			if family == 3 {
				q = `SELECT (SELECT count(*)=11 FROM asset_streams WHERE asset_id='basic-asset') AND (SELECT count(*)=2 FROM asset_streams WHERE asset_id='basic-asset' AND has_asset_stream_breadth=1 AND breadth_audio_variant='retained') AND (SELECT breadth_audio_variant='absent payload' FROM asset_streams WHERE asset_id='basic-asset' AND stream_index=10) AND (SELECT count(*)=8 FROM asset_streams WHERE asset_id='basic-asset' AND stream_index>=9 AND stream_index<>10 AND has_asset_stream_breadth=0 AND breadth_audio_variant='')`
			}
			q += ` AND (SELECT count(*)=1 FROM asset_chapters WHERE asset_id='other-asset' AND chapter_index=9) AND (SELECT count(*)=2 FROM catalog_info_new_chapters WHERE change_id=51) AND (SELECT count(*)=2 FROM catalog_info_new_stream_breadth WHERE change_id=51) AND (SELECT revision=37 FROM asset_chapter_facts WHERE asset_id='basic-asset') AND (SELECT revision=31 FROM asset_stream_facts WHERE asset_id='basic-asset') AND (SELECT info_epoch=17 AND info_publication_pending=1 FROM catalog_assets WHERE id=41) AND (SELECT info_pending_change=51 FROM catalog_asset_links WHERE asset_id=41 AND entity_id=31) AND (SELECT count(*)=0 FROM scan_command_receipts)`
			if e := w.db.QueryRowContext(t.Context(), q).Scan(&exact); e != nil || !exact {
				t.Fatal(exact, e)
			}
		})
	}
}

func TestBasicCleanupMediaLateReturningRollback(t *testing.T) {
	for _, family := range []int64{3, 5} {
		t.Run(string(rune('0'+family)), func(t *testing.T) {
			defs := basicCleanupMediaTestDefinitions()
			for n := range defs {
				switch defs[n].ID {
				case basicCleanupBreadthClear:
					j := strings.LastIndex(defs[n].SQL, ",breadth_packet_peak_revision")
					defs[n].SQL = defs[n].SQL[:j] + ",title"
				case basicCleanupChapterDelete:
					defs[n].SQL = strings.Replace(defs[n].SQL, "RETURNING asset_id,chapter_index", "RETURNING asset_id,start_seconds", 1)
				}
			}
			w, turn, f := basicCleanupMediaFixture(t, family, defs)
			got, e := cleanupBasicMedia(t.Context(), turn, f)
			if !errors.Is(e, ErrConflict) || got != (basicStreamCleanupResult{}) {
				t.Fatal(got, e)
			}
			_ = turn.closeRows()
			_, _ = turn.transaction.rollback(nil)
			var exact bool
			if e = w.db.QueryRowContext(t.Context(), `SELECT (SELECT has_asset_stream_breadth=1 AND breadth_audio_variant='old' FROM asset_streams WHERE asset_id='basic-asset' AND stream_index=9) AND (SELECT count(*)=1 FROM asset_chapters WHERE asset_id='basic-asset' AND chapter_index=9)`).Scan(&exact); e != nil || !exact {
				t.Fatal(exact, e)
			}
		})
	}
}

func TestBasicCleanupMediaLiteralBounds(t *testing.T) {
	defs := basicCopyCleanupMediaDefinitions()
	if _, e := stmt.Describe(defs); e != nil {
		t.Fatal(e)
	}
	for _, d := range defs {
		if strings.Contains(d.SQL, "EXISTS") {
			t.Fatal(d.ID)
		}
		switch d.ID {
		case basicCleanupBreadthFirst, basicCleanupBreadthAfter:
			if !strings.HasSuffix(d.SQL, "ORDER BY stream_index LIMIT 9") || strings.Contains(d.SQL, "has_asset_stream_breadth=") {
				t.Fatal(d.SQL)
			}
		case basicCleanupChapterFirst, basicCleanupChapterAfter:
			if !strings.HasSuffix(d.SQL, "ORDER BY chapter_index LIMIT 9") {
				t.Fatal(d.SQL)
			}
		}
	}
}
