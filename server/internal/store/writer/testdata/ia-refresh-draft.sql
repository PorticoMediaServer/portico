-- C019 adopted journal fragment, frozen packet c0ca22cf / candidate81d6971a.
-- Temporary component fixture only; remove after actual C embed. No Capture claim.
CREATE TABLE catalog_info_refresh_undo(
 refresh_id INTEGER PRIMARY KEY AUTOINCREMENT,
 change_id INTEGER NOT NULL CHECK(change_id>0),
 original_run_id TEXT NOT NULL REFERENCES inventory_runs(job_id) ON DELETE NO ACTION,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 asset_id INTEGER NOT NULL CHECK(asset_id>0),
 asset_token TEXT NOT NULL,
 expected_epoch INTEGER NOT NULL CHECK(expected_epoch>0),
 input_revision INTEGER NOT NULL CHECK(input_revision>0),
 fingerprint BLOB NOT NULL CHECK(length(fingerprint)=32),
 state TEXT NOT NULL CHECK(state IN('copying','restoring','accepted','restored','retired')),
 revision INTEGER NOT NULL CHECK(revision>0),
 last_command_sequence INTEGER NOT NULL DEFAULT 0 CHECK(last_command_sequence>=0),
 acknowledged_command_sequence INTEGER NOT NULL DEFAULT 0 CHECK(acknowledged_command_sequence BETWEEN 0 AND last_command_sequence),
 after_family INTEGER NOT NULL DEFAULT 0 CHECK(after_family BETWEEN 0 AND 7),
 after_key TEXT NOT NULL DEFAULT '',
 reclaim_command_sequence INTEGER CHECK(reclaim_command_sequence IS NULL OR reclaim_command_sequence>0),
 reclaim_operation_id BLOB CHECK(reclaim_operation_id IS NULL OR length(reclaim_operation_id)=16),
 reclaim_request_digest BLOB CHECK(reclaim_request_digest IS NULL OR length(reclaim_request_digest)=32),
 reclaim_result_digest BLOB CHECK(reclaim_result_digest IS NULL OR length(reclaim_result_digest)=32),
 reclaim_applied_ms INTEGER CHECK(reclaim_applied_ms IS NULL OR reclaim_applied_ms>=0),
 reclaim_after_family INTEGER CHECK(reclaim_after_family IS NULL OR reclaim_after_family BETWEEN 0 AND 7),
 reclaim_after_key TEXT,
 reclaim_removed_rows INTEGER CHECK(reclaim_removed_rows IS NULL OR reclaim_removed_rows>=0),
 reclaim_removed_bytes INTEGER CHECK(reclaim_removed_bytes IS NULL OR reclaim_removed_bytes>=0),
 reclaim_terminal INTEGER CHECK(reclaim_terminal IS NULL OR reclaim_terminal IN(0,1)),
 retained_rows INTEGER NOT NULL DEFAULT 0 CHECK(retained_rows>=0),
 retained_bytes INTEGER NOT NULL DEFAULT 0 CHECK(retained_bytes>=0),
 CHECK((reclaim_command_sequence IS NULL)=(reclaim_operation_id IS NULL) AND (reclaim_command_sequence IS NULL)=(reclaim_request_digest IS NULL) AND (reclaim_command_sequence IS NULL)=(reclaim_result_digest IS NULL) AND (reclaim_command_sequence IS NULL)=(reclaim_applied_ms IS NULL) AND (reclaim_command_sequence IS NULL)=(reclaim_after_family IS NULL) AND (reclaim_command_sequence IS NULL)=(reclaim_after_key IS NULL) AND (reclaim_command_sequence IS NULL)=(reclaim_removed_rows IS NULL) AND (reclaim_command_sequence IS NULL)=(reclaim_removed_bytes IS NULL) AND (reclaim_command_sequence IS NULL)=(reclaim_terminal IS NULL)),
 CHECK(reclaim_command_sequence IS NULL OR reclaim_command_sequence<=last_command_sequence),
 FOREIGN KEY(asset_id,asset_token) REFERENCES catalog_assets(id,token) ON DELETE NO ACTION,
 CHECK(last_command_sequence-acknowledged_command_sequence<=1)
) STRICT;
CREATE INDEX catalog_info_refresh_run ON catalog_info_refresh_undo(original_run_id,refresh_id);
CREATE INDEX catalog_info_refresh_token ON catalog_info_refresh_undo(asset_token,refresh_id);
CREATE UNIQUE INDEX catalog_info_refresh_command_owner ON catalog_info_refresh_undo(refresh_id,command_incarnation);
CREATE INDEX catalog_info_refresh_asset ON catalog_info_refresh_undo(asset_id,asset_token,refresh_id);
CREATE TABLE catalog_info_refresh_receipts(
 refresh_id INTEGER NOT NULL,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 command_sequence INTEGER NOT NULL CHECK(command_sequence>0),
 operation_id BLOB NOT NULL UNIQUE CHECK(length(operation_id)=16),
 request_digest BLOB NOT NULL CHECK(length(request_digest)=32),
 command_kind TEXT NOT NULL CHECK(command_kind IN('refresh_begin','refresh_step','refresh_rollback','refresh_finalize')),
 accepted_revision INTEGER NOT NULL CHECK(accepted_revision>0),
 applied_ms INTEGER NOT NULL CHECK(applied_ms>=0),
 result_state TEXT NOT NULL CHECK(result_state IN('copying','restoring','accepted','restored','retired')),
 after_family INTEGER NOT NULL CHECK(after_family BETWEEN 0 AND 7),
 after_key TEXT NOT NULL,
 result_rows INTEGER NOT NULL CHECK(result_rows>=0),
 result_bytes INTEGER NOT NULL CHECK(result_bytes>=0),
 result_digest BLOB NOT NULL CHECK(length(result_digest)=32),
 PRIMARY KEY(refresh_id,command_incarnation,command_sequence),
 FOREIGN KEY(refresh_id,command_incarnation) REFERENCES catalog_info_refresh_undo(refresh_id,command_incarnation) ON DELETE NO ACTION,
 CHECK((command_kind='refresh_begin' AND result_state='copying') OR (command_kind='refresh_step' AND result_state='copying') OR (command_kind='refresh_rollback' AND result_state IN('restoring','restored')) OR (command_kind='refresh_finalize' AND result_state IN('accepted','restored','retired')))
) WITHOUT ROWID, STRICT;

CREATE TABLE catalog_info_undo_asset_stream_facts(
 refresh_id INTEGER NOT NULL REFERENCES catalog_info_refresh_undo(refresh_id) ON DELETE NO ACTION,
 old_exists INTEGER NOT NULL CHECK(old_exists IN(0,1)),
 written_revision INTEGER NOT NULL CHECK(written_revision>=0),
 write_digest BLOB NOT NULL CHECK(length(write_digest)=32),
 asset_id TEXT NOT NULL,
 revision INTEGER,
 size INTEGER,
 modified_ns INTEGER,
 fingerprint TEXT,
 detail_version INTEGER,
 PRIMARY KEY(refresh_id,asset_id),
 CHECK(old_exists=1 OR (revision IS NULL AND size IS NULL AND modified_ns IS NULL AND fingerprint IS NULL AND detail_version IS NULL)),
 CHECK(old_exists=0 OR (asset_id IS NOT NULL AND revision IS NOT NULL AND size IS NOT NULL AND modified_ns IS NOT NULL AND fingerprint IS NOT NULL AND detail_version IS NOT NULL))
) WITHOUT ROWID, STRICT;

CREATE TABLE catalog_info_undo_asset_streams(
 refresh_id INTEGER NOT NULL REFERENCES catalog_info_refresh_undo(refresh_id) ON DELETE NO ACTION,
 old_exists INTEGER NOT NULL CHECK(old_exists IN(0,1)),
 written_revision INTEGER NOT NULL CHECK(written_revision>=0),
 write_digest BLOB NOT NULL CHECK(length(write_digest)=32),
 asset_id TEXT NOT NULL,
 stream_index INTEGER NOT NULL,
 type TEXT,
 codec TEXT,
 language TEXT,
 title TEXT,
 channels INTEGER,
 channel_layout TEXT,
 is_default INTEGER,
 is_forced INTEGER,
 color_transfer TEXT,
 color_primaries TEXT,
 detail_json TEXT,
 has_asset_stream_breadth INTEGER,
 breadth_audio_description INTEGER,
 breadth_audio_variant TEXT,
 breadth_chroma_subsampling TEXT,
 breadth_fps_numerator INTEGER,
 breadth_fps_denominator INTEGER,
 breadth_frame_rate_mode TEXT,
 breadth_interlace_analysis TEXT,
 breadth_telecine INTEGER,
 breadth_dovi_rpu INTEGER,
 breadth_dovi_base_layer INTEGER,
 breadth_dovi_metadata_known INTEGER,
 breadth_dovi_enhancement_type TEXT,
 breadth_dovi_cm_version TEXT,
 breadth_playback_frames_revision TEXT,
 breadth_packet_peak_revision TEXT,
 PRIMARY KEY(refresh_id,asset_id,stream_index),
 CHECK(old_exists=1 OR (type IS NULL AND codec IS NULL AND language IS NULL AND title IS NULL AND channels IS NULL AND channel_layout IS NULL AND is_default IS NULL AND is_forced IS NULL AND color_transfer IS NULL AND color_primaries IS NULL AND detail_json IS NULL AND has_asset_stream_breadth IS NULL AND breadth_audio_description IS NULL AND breadth_audio_variant IS NULL AND breadth_chroma_subsampling IS NULL AND breadth_fps_numerator IS NULL AND breadth_fps_denominator IS NULL AND breadth_frame_rate_mode IS NULL AND breadth_interlace_analysis IS NULL AND breadth_telecine IS NULL AND breadth_dovi_rpu IS NULL AND breadth_dovi_base_layer IS NULL AND breadth_dovi_metadata_known IS NULL AND breadth_dovi_enhancement_type IS NULL AND breadth_dovi_cm_version IS NULL AND breadth_playback_frames_revision IS NULL AND breadth_packet_peak_revision IS NULL)),
 CHECK(old_exists=0 OR (asset_id IS NOT NULL AND stream_index IS NOT NULL AND type IS NOT NULL AND codec IS NOT NULL AND language IS NOT NULL AND title IS NOT NULL AND channels IS NOT NULL AND channel_layout IS NOT NULL AND is_default IS NOT NULL AND is_forced IS NOT NULL AND color_transfer IS NOT NULL AND color_primaries IS NOT NULL AND detail_json IS NOT NULL AND has_asset_stream_breadth IS NOT NULL))
) WITHOUT ROWID, STRICT;

CREATE TABLE catalog_info_undo_asset_chapter_facts(
 refresh_id INTEGER NOT NULL REFERENCES catalog_info_refresh_undo(refresh_id) ON DELETE NO ACTION,
 old_exists INTEGER NOT NULL CHECK(old_exists IN(0,1)),
 written_revision INTEGER NOT NULL CHECK(written_revision>=0),
 write_digest BLOB NOT NULL CHECK(length(write_digest)=32),
 asset_id TEXT NOT NULL,
 revision INTEGER,
 size INTEGER,
 modified_ns INTEGER,
 status TEXT,
 fingerprint TEXT,
 PRIMARY KEY(refresh_id,asset_id),
 CHECK(old_exists=1 OR (revision IS NULL AND size IS NULL AND modified_ns IS NULL AND status IS NULL AND fingerprint IS NULL)),
 CHECK(old_exists=0 OR (asset_id IS NOT NULL AND revision IS NOT NULL AND size IS NOT NULL AND modified_ns IS NOT NULL AND status IS NOT NULL AND fingerprint IS NOT NULL))
) WITHOUT ROWID, STRICT;

CREATE TABLE catalog_info_undo_asset_chapters(
 refresh_id INTEGER NOT NULL REFERENCES catalog_info_refresh_undo(refresh_id) ON DELETE NO ACTION,
 old_exists INTEGER NOT NULL CHECK(old_exists IN(0,1)),
 written_revision INTEGER NOT NULL CHECK(written_revision>=0),
 write_digest BLOB NOT NULL CHECK(length(write_digest)=32),
 asset_id TEXT NOT NULL,
 chapter_index INTEGER NOT NULL,
 title TEXT,
 start_seconds REAL,
 end_seconds REAL,
 PRIMARY KEY(refresh_id,asset_id,chapter_index),
 CHECK(old_exists=1 OR (title IS NULL AND start_seconds IS NULL AND end_seconds IS NULL)),
 CHECK(old_exists=0 OR (asset_id IS NOT NULL AND chapter_index IS NOT NULL AND title IS NOT NULL AND start_seconds IS NOT NULL AND end_seconds IS NOT NULL))
) WITHOUT ROWID, STRICT;

CREATE TABLE catalog_info_undo_asset_subtitle_facts(
 refresh_id INTEGER NOT NULL REFERENCES catalog_info_refresh_undo(refresh_id) ON DELETE NO ACTION,
 old_exists INTEGER NOT NULL CHECK(old_exists IN(0,1)),
 written_revision INTEGER NOT NULL CHECK(written_revision>=0),
 write_digest BLOB NOT NULL CHECK(length(write_digest)=32),
 asset_id TEXT NOT NULL,
 revision INTEGER,
 size INTEGER,
 modified_ns INTEGER,
 origin_us INTEGER,
 timing_known INTEGER,
 status TEXT,
 fingerprint TEXT,
 has_subtitle_inventory_evidence INTEGER,
 inventory_revision INTEGER,
 inventory_evidence TEXT,
 PRIMARY KEY(refresh_id,asset_id),
 CHECK(old_exists=1 OR (revision IS NULL AND size IS NULL AND modified_ns IS NULL AND origin_us IS NULL AND timing_known IS NULL AND status IS NULL AND fingerprint IS NULL AND has_subtitle_inventory_evidence IS NULL AND inventory_revision IS NULL AND inventory_evidence IS NULL)),
 CHECK(old_exists=0 OR (asset_id IS NOT NULL AND revision IS NOT NULL AND size IS NOT NULL AND modified_ns IS NOT NULL AND origin_us IS NOT NULL AND timing_known IS NOT NULL AND status IS NOT NULL AND fingerprint IS NOT NULL AND has_subtitle_inventory_evidence IS NOT NULL))
) WITHOUT ROWID, STRICT;

CREATE TABLE catalog_info_undo_subtitle_sidecars(
 refresh_id INTEGER NOT NULL REFERENCES catalog_info_refresh_undo(refresh_id) ON DELETE NO ACTION,
 old_exists INTEGER NOT NULL CHECK(old_exists IN(0,1)),
 written_revision INTEGER NOT NULL CHECK(written_revision>=0),
 write_digest BLOB NOT NULL CHECK(length(write_digest)=32),
 id TEXT NOT NULL,
 path TEXT,
 size INTEGER,
 modified_ns INTEGER,
 digest TEXT,
 PRIMARY KEY(refresh_id,id),
 CHECK(old_exists=1 OR (path IS NULL AND size IS NULL AND modified_ns IS NULL AND digest IS NULL)),
 CHECK(old_exists=0 OR (id IS NOT NULL AND path IS NOT NULL AND size IS NOT NULL AND modified_ns IS NOT NULL AND digest IS NOT NULL))
) WITHOUT ROWID, STRICT;

CREATE TABLE catalog_info_undo_asset_subtitles(
 refresh_id INTEGER NOT NULL REFERENCES catalog_info_refresh_undo(refresh_id) ON DELETE NO ACTION,
 old_exists INTEGER NOT NULL CHECK(old_exists IN(0,1)),
 written_revision INTEGER NOT NULL CHECK(written_revision>=0),
 write_digest BLOB NOT NULL CHECK(length(write_digest)=32),
 id TEXT NOT NULL,
 asset_id TEXT,
 origin TEXT,
 locator TEXT,
 stream_index INTEGER,
 sidecar_id TEXT,
 format TEXT,
 language TEXT,
 title TEXT,
 is_default INTEGER,
 is_forced INTEGER,
 reason TEXT,
 hearing_impaired INTEGER,
 PRIMARY KEY(refresh_id,id),
 CHECK(old_exists=1 OR (asset_id IS NULL AND origin IS NULL AND locator IS NULL AND stream_index IS NULL AND sidecar_id IS NULL AND format IS NULL AND language IS NULL AND title IS NULL AND is_default IS NULL AND is_forced IS NULL AND reason IS NULL AND hearing_impaired IS NULL)),
 CHECK(old_exists=0 OR (id IS NOT NULL AND asset_id IS NOT NULL AND origin IS NOT NULL AND locator IS NOT NULL AND stream_index IS NOT NULL AND format IS NOT NULL AND language IS NOT NULL AND title IS NOT NULL AND is_default IS NOT NULL AND is_forced IS NOT NULL AND reason IS NOT NULL AND hearing_impaired IS NOT NULL))
) WITHOUT ROWID, STRICT;
CREATE INDEX catalog_info_undo_sidecar_key ON catalog_info_undo_subtitle_sidecars(id,refresh_id);

CREATE UNIQUE INDEX catalog_assets_id_token ON catalog_assets(id,token);
CREATE UNIQUE INDEX catalog_info_refresh_original ON catalog_info_refresh_undo(change_id,command_incarnation);
CREATE UNIQUE INDEX catalog_info_refresh_active_asset ON catalog_info_refresh_undo(asset_id) WHERE state IN('copying','restoring');
