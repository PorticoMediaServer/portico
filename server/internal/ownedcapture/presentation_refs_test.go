//go:build linux || darwin

package ownedcapture

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Collector-only normalized SQL fixtures. They create no playback authority:
// actual snapshot publication/activation is exercised in playbackdisc tests.
// The source policy is the existing fixture's fake producer; bytes/artifact
// custody and the collector itself are real. All vectors are authored, UNRUN.
func seedPresentation(t *testing.T, s *Service, r Receipt, id, state string, sealed bool, expires time.Time, pin bool) {
	t.Helper()
	ctx := context.Background()
	until := expires.UnixMilli()
	if until > r.ExpiresAt.UnixMilli() {
		t.Fatal("fixture extends original capture")
	}
	err := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		exec := func(query string, args ...any) {
			t.Helper()
			if _, e := tx.ExecContext(ctx, query, args...); e != nil {
				t.Fatalf("seed: %v", e)
			}
		}
		exec(`INSERT OR IGNORE INTO libraries(id,name,kind,root) VALUES('lib','Library','movie','/library')`)
		// libraries owns the catalogue handle trigger; never insert a duplicate.
		exec(`INSERT OR IGNORE INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head) VALUES(1,randomblob(16),(SELECT id FROM catalog_libraries WHERE library_id='lib'),1,'Disc','disc','d')`)
		exec(`INSERT OR IGNORE INTO catalog_assets(token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,available) VALUES(?,'/library/disc.iso',?,1,'iso','','',0,0,0,1)`, r.Binding.AssetID, r.Object.Size)
		exec(`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration) VALUES(?,'hash','account','profile',1,?,1,'playing',?,?,'2099-01-01T00:00:00Z',?,60)`, "session-"+id, r.Binding.AssetID, "grant-"+id, "grant-token-"+id, "request-"+id)
		exec(`INSERT INTO playback_disc_snapshots(id,session_id,generation,input_kind) VALUES(?,?,1,'captured')`, id, "session-"+id)
		exec(`INSERT INTO playback_disc_lifecycle(snapshot_id,state,sealed,created_ms,expires_ms) VALUES(?,'pending',0,?,?)`, id, until-time.Hour.Milliseconds(), until)
		exec(`INSERT INTO playback_disc_snapshot_actor(snapshot_id,server_id,authority,account_id,profile_id,device_id,family_id,grant_id,session_hash,epoch,token_generation,original_horizon_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id, "server", "local", "account", "profile", "device", "family", "", "hash", 0, 1, until)
		exec(`INSERT INTO playback_disc_snapshot_operation(snapshot_id,legacy_request_id,start_key_digest,start_body_digest,queue_id,queue_entry_id,queue_owner_kind,queue_owner_id,queue_revision,queue_custody_generation) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, "request-"+id, strings.Repeat("a", 64), strings.Repeat("b", 64), "", "", "", "", 0, 0)
		exec(`INSERT INTO playback_disc_snapshot_source(snapshot_id,library_id,library_source_id,asset_id,inventory_object_id,inventory_revision,root_identity,incarnation,remote_source_id,effective_configuration,read_policy_witness,read_authority_witness,library_generation,raw_configuration_generation,profile_revision,size,modified_ns) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, "lib", "source", r.Binding.AssetID, r.Binding.ObjectID, r.Binding.ObjectRevision, "root", "incarnation", r.Binding.SourceID, r.Binding.ConfigGeneration, r.Binding.PolicyWitness, r.Binding.AuthorityWitness, 1, 1, 1, r.Object.Size, 0)
		exec(`INSERT INTO playback_disc_snapshot_disc(snapshot_id,probe_schema,kind,packaging,encryption,selected_title_key,structure_revision,stream_revision,chapter_revision,facts_revision) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, 1, "bluray", "iso", "none", "bd:00001", "structure", "streams", "chapters", "facts")
		exec(`INSERT INTO playback_disc_snapshot_media(snapshot_id,container,video_codec,audio_codec,width,height,duration_ms,origin_us,timing_known,chapters_present) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, "mpegts", "h264", "", 1280, 720, 60000, 0, 1, 1)
		exec(`INSERT INTO playback_disc_snapshot_tools(snapshot_id,disc_helper_digest,ffmpeg_digest,ffprobe_digest,runtime_identity) VALUES(?,?,?,?,?)`, id, strings.Repeat("c", 64), strings.Repeat("d", 64), strings.Repeat("e", 64), "runtime")
		exec(`INSERT INTO playback_disc_snapshot_streams(snapshot_id,stream_order,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,hearing_impaired,commentary,detail_max_bit_rate,detail_h_d_r10_plus,detail_profile,detail_level,detail_pixel_format,detail_bit_depth,detail_width,detail_height,detail_frame_rate,detail_interlaced,detail_rotation,detail_sample_aspect_ratio,detail_anamorphic,detail_ref_frames,detail_codec_tag,detail_bit_rate,detail_sample_rate,detail_bits_per_sample,detail_dynamic_range,detail_dolby_vision_profile,detail_dolby_vision_level,detail_dolby_vision_compatibility,detail_enhancement_layer,detail_master_display,detail_max_c_l_l,detail_object_audio,detail_audio_description,detail_audio_variant,detail_chroma_subsampling,detail_frame_rate_numerator,detail_frame_rate_denominator,detail_frame_rate_mode,detail_interlace_analysis,detail_telecine,detail_dolby_vision_r_p_u,detail_dolby_vision_base_layer,detail_dolby_vision_metadata_known,detail_dolby_vision_enhancement_type,detail_dolby_vision_c_m_version) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, 0, 0, "video", "h264", "", "", 0, "", 0, 0, "", "", 0, 0, 0, 0, "", 0, "", 0, 0, 0, 0, 0, 0, "", 0, 0, "", 0, 0, 0, "", 0, 0, 0, 0, "", "", "", 0, "", "", 0, 0, "", "", 0, 0, 0, 0, "", "")
		exec(`INSERT INTO playback_disc_snapshot_titles(snapshot_id,disc_order,title_key,playlist,playlist_file,dvd_title,vts,pgc,duration_ms,angle_count) VALUES(?,0,'bd:00001',1,'00001.mpls',0,0,0,60000,1)`, id)
		exec(`INSERT INTO playback_disc_snapshot_clips(snapshot_id,disc_order,sequence,file,in_time_90k,out_time_90k) VALUES(?,0,0,'00001.m2ts',0,5400000)`, id)
		exec(`INSERT INTO playback_disc_snapshot_disc_streams(snapshot_id,disc_order,stream_order,clip_sequence,pid,kind,coding,language,dv_enhancement,dv_known,dv_base_pid,video_role) VALUES(?,0,0,0,4113,'video',27,'',0,0,NULL,'primary')`, id)
		exec(`INSERT INTO playback_disc_snapshot_disc_chapters(snapshot_id,disc_order,chapter_order,start_ms) VALUES(?,0,0,0)`, id)
		exec(`INSERT INTO playback_disc_snapshot_chapters(snapshot_id,chapter_order,title,start_seconds,end_seconds) VALUES(?,0,'Opening',0,60)`, id)
		exec(`INSERT INTO playback_disc_capture_inputs(snapshot_id,capture_id,accepted_version_id) VALUES(?,?,?)`, id, r.ID, r.Version.ID())
		exec(`INSERT INTO owned_capture_presentation_refs(snapshot_id,capture_id,expires_ms) VALUES(?,?,?)`, id, r.ID, until)
		if sealed {
			exec(`UPDATE playback_disc_lifecycle SET sealed=1 WHERE snapshot_id=?`, id)
		}
		if state == "active" || state == "retired" {
			exec(`UPDATE playback_disc_lifecycle SET state='active' WHERE snapshot_id=?`, id)
		}
		if pin {
			exec(`INSERT INTO playback_disc_reader_pins(id,snapshot_id,expires_ms) VALUES(?,?,?)`, id+"-pin", id, until)
		}
		if state == "retired" {
			exec(`UPDATE playback_disc_lifecycle SET state='retired',retired_ms=? WHERE snapshot_id=?`, time.Now().UnixMilli(), id)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func releasePresentation(t *testing.T, s *Service, r Receipt, id string, releasePin bool) {
	t.Helper()
	ctx := context.Background()
	if e := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, `UPDATE playback_disc_lifecycle SET state='retired',retired_ms=? WHERE snapshot_id=?`, time.Now().UnixMilli(), id); e != nil {
			return e
		}
		if releasePin {
			if _, e := tx.ExecContext(ctx, `DELETE FROM playback_disc_reader_pins WHERE id=? AND snapshot_id=?`, id+"-pin", id); e != nil {
				return e
			}
		}
		_, e := tx.ExecContext(ctx, `DELETE FROM owned_capture_presentation_refs WHERE snapshot_id=? AND capture_id=?`, id, r.ID)
		return e
	}); e != nil {
		t.Fatal(e)
	}
}
func captureState(t *testing.T, s *Service, id string) string {
	t.Helper()
	var state string
	if e := s.db.QueryRow(`SELECT state FROM owned_capture_lifecycle WHERE capture_id=?`, id).Scan(&state); e != nil {
		t.Fatal(e)
	}
	return state
}
func retireCollect(t *testing.T, s *Service, r Receipt) {
	t.Helper()
	if e := s.Retire(context.Background(), r); e != nil {
		t.Fatal(e)
	}
	if e := s.Collect(context.Background()); e != nil {
		t.Fatal(e)
	}
}

func TestSealedPresentationRetainsOriginalCaptureAndFullCharge(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, state := range []string{"pending", "active"} {
		t.Run(state, func(t *testing.T) {
			s, p := fixture(t)
			r := capture(t, s)
			seedPresentation(t, s, r, "kept", state, true, r.ExpiresAt.Add(-time.Minute), false)
			retireCollect(t, s, r)
			if captureState(t, s, r.ID) == "deleted" || p.records[r.Binding.ReservationID].released {
				t.Fatal("original presentation lost physical custody or charge")
			}
			// Source retirement still refuses reads; retained bytes are not access.
			view, e := dbwork.BeginSnapshot(context.Background(), s.db)
			if e != nil {
				t.Fatal(e)
			}
			_, e = s.resolveTx(context.Background(), view.Tx(), r.ID, r.Binding)
			view.Rollback()
			if e == nil {
				t.Fatal("retired capture granted access through metadata")
			}
			releasePresentation(t, s, r, "kept", false)
			if e = s.Collect(context.Background()); e != nil {
				t.Fatal(e)
			}
			if captureState(t, s, r.ID) != "deleted" || !p.records[r.Binding.ReservationID].released {
				t.Fatal("released original reference remained charged")
			}
		})
	}
}
func TestRetiredPresentationReaderKeepsOnlyItsOwnCaptureReference(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, p := fixture(t)
	r := capture(t, s)
	until := r.ExpiresAt.Add(-time.Minute)
	seedPresentation(t, s, r, "first", "active", true, until, false)
	seedPresentation(t, s, r, "reader", "retired", true, until, true)
	retireCollect(t, s, r)
	releasePresentation(t, s, r, "first", false)
	if e := s.Collect(context.Background()); e != nil {
		t.Fatal(e)
	}
	if captureState(t, s, r.ID) == "deleted" || p.records[r.Binding.ReservationID].released {
		t.Fatal("another original reader pin was dropped")
	}
	releasePresentation(t, s, r, "reader", true)
	if e := s.Collect(context.Background()); e != nil {
		t.Fatal(e)
	}
	if captureState(t, s, r.ID) != "deleted" {
		t.Fatal("own last released reference was not cleaned")
	}
}
func TestExpiredRetiredAndUnsealedReferencesDoNotGrantRetention(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, name := range []string{"expired", "retired", "unsealed"} {
		t.Run(name, func(t *testing.T) {
			s, p := fixture(t)
			r := capture(t, s)
			until := r.ExpiresAt.Add(-time.Minute)
			state := "active"
			sealed := true
			if name == "expired" {
				until = time.Now().Add(-time.Minute)
			}
			if name == "retired" {
				state = "retired"
			}
			if name == "unsealed" {
				state = "pending"
				sealed = false
			}
			seedPresentation(t, s, r, "old", state, sealed, until, false)
			if p.records[r.Binding.ReservationID].released {
				t.Fatal("metadata alone uncharged bytes")
			}
			retireCollect(t, s, r)
			if captureState(t, s, r.ID) != "deleted" || !p.records[r.Binding.ReservationID].released {
				t.Fatal("non-live metadata reference retained custody")
			}
		})
	}
}
func TestPresentationCleanupLeavesAnotherAcquisitionUntouched(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, p := fixture(t)
	a := capture(t, s)
	b := capture(t, s)
	seedPresentation(t, s, b, "other", "active", true, b.ExpiresAt.Add(-time.Minute), false)
	retireCollect(t, s, a)
	if captureState(t, s, a.ID) != "deleted" || !p.records[a.Binding.ReservationID].released {
		t.Fatal("unreferenced acquisition was not released")
	}
	if captureState(t, s, b.ID) != "ready" || p.records[b.Binding.ReservationID].released {
		t.Fatal("another acquisition lost its own charge")
	}
	var state string
	if e := s.db.QueryRow(`SELECT state FROM playback_disc_lifecycle WHERE snapshot_id='other'`).Scan(&state); e != nil || state != "active" {
		t.Fatalf("other presentation changed: %s %v", state, e)
	}
}
func TestRealOwnedReaderDescriptorOutlivesMetadataReferences(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, p := fixture(t)
	r := capture(t, s)
	reader, e := s.Open(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	defer reader.Close()
	seedPresentation(t, s, r, "descriptor", "active", true, r.ExpiresAt.Add(-time.Minute), false)
	if e = s.Retire(context.Background(), r); e != nil {
		t.Fatal(e)
	}
	releasePresentation(t, s, r, "descriptor", false)
	// Fault injection: even loss of an advisory SQL pin must not bypass the real
	// retained descriptor/object lease. No running process may lose physical bytes.
	if _, e = s.db.Exec(`DELETE FROM owned_capture_reader_pins WHERE capture_id=?`, r.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.Collect(context.Background()); e != nil {
		t.Fatal(e)
	}
	if p.records[r.Binding.ReservationID].released || captureState(t, s, r.ID) == "deleted" {
		t.Fatal("physical descriptor custody was bypassed")
	}
	info, e := reader.File().Stat()
	if e != nil || info.Size() != r.Object.Size {
		t.Fatalf("original descriptor lost: %v", e)
	}
	if e = reader.Close(); e != nil {
		t.Fatal(e)
	}
	if e = s.Collect(context.Background()); e != nil {
		t.Fatal(e)
	}
	if captureState(t, s, r.ID) != "deleted" || !p.records[r.Binding.ReservationID].released {
		t.Fatal("closed original descriptor was not cleaned")
	}
	if descriptor, e := s.artifacts.OpenDescriptor(context.Background(), r.Object); e == nil {
		descriptor.Close()
		t.Fatal("collected artifact still has bytes")
	}
}
