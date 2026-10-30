package playbackdisc

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
)

func writeActor(ctx context.Context, tx *sql.Tx, id string, v Actor) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO playback_disc_snapshot_actor(snapshot_id,server_id,authority,account_id,profile_id,device_id,family_id,grant_id,session_hash,epoch,token_generation,original_horizon_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id, v.ServerID, v.Authority, v.AccountID, v.ProfileID, v.DeviceID, v.FamilyID, v.Grant, v.SessionHash, v.Epoch, v.TokenGeneration, v.OriginalHorizonMs)
	return err
}
func readActor(ctx context.Context, tx *sql.Tx, id string) (v Actor, err error) {
	err = tx.QueryRowContext(ctx, `SELECT server_id,authority,account_id,profile_id,device_id,family_id,grant_id,session_hash,epoch,token_generation,original_horizon_ms FROM playback_disc_snapshot_actor WHERE snapshot_id=?`, id).Scan(&v.ServerID, &v.Authority, &v.AccountID, &v.ProfileID, &v.DeviceID, &v.FamilyID, &v.Grant, &v.SessionHash, &v.Epoch, &v.TokenGeneration, &v.OriginalHorizonMs)
	return
}
func writeOperation(ctx context.Context, tx *sql.Tx, id string, v Operation) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO playback_disc_snapshot_operation(snapshot_id,legacy_request_id,start_key_digest,start_body_digest,queue_id,queue_entry_id,queue_owner_kind,queue_owner_id,queue_revision,queue_custody_generation) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, v.LegacyRequestID, v.StartKeyDigest, v.StartBodyDigest, v.QueueID, v.QueueEntryID, v.QueueOwnerKind, v.QueueOwnerID, v.QueueRevision, v.QueueCustodyGeneration)
	return err
}
func readOperation(ctx context.Context, tx *sql.Tx, id string) (v Operation, err error) {
	err = tx.QueryRowContext(ctx, `SELECT legacy_request_id,start_key_digest,start_body_digest,queue_id,queue_entry_id,queue_owner_kind,queue_owner_id,queue_revision,queue_custody_generation FROM playback_disc_snapshot_operation WHERE snapshot_id=?`, id).Scan(&v.LegacyRequestID, &v.StartKeyDigest, &v.StartBodyDigest, &v.QueueID, &v.QueueEntryID, &v.QueueOwnerKind, &v.QueueOwnerID, &v.QueueRevision, &v.QueueCustodyGeneration)
	return
}
func writeSource(ctx context.Context, tx *sql.Tx, id string, v Source) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO playback_disc_snapshot_source(snapshot_id,library_id,library_source_id,asset_id,inventory_object_id,inventory_revision,root_identity,incarnation,remote_source_id,effective_configuration,read_policy_witness,read_authority_witness,library_generation,raw_configuration_generation,profile_revision,size,modified_ns) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, v.LibraryID, v.LibrarySourceID, v.AssetID, v.InventoryObjectID, v.InventoryRevision, v.RootIdentity, v.Incarnation, v.RemoteSourceID, v.EffectiveConfiguration, v.ReadPolicyWitness, v.ReadAuthorityWitness, v.LibraryGeneration, v.RawConfigurationGeneration, v.ProfileRevision, v.Size, v.ModifiedNS)
	return err
}
func readSource(ctx context.Context, tx *sql.Tx, id string) (v Source, err error) {
	err = tx.QueryRowContext(ctx, `SELECT library_id,library_source_id,asset_id,inventory_object_id,inventory_revision,root_identity,incarnation,remote_source_id,effective_configuration,read_policy_witness,read_authority_witness,library_generation,raw_configuration_generation,profile_revision,size,modified_ns FROM playback_disc_snapshot_source WHERE snapshot_id=?`, id).Scan(&v.LibraryID, &v.LibrarySourceID, &v.AssetID, &v.InventoryObjectID, &v.InventoryRevision, &v.RootIdentity, &v.Incarnation, &v.RemoteSourceID, &v.EffectiveConfiguration, &v.ReadPolicyWitness, &v.ReadAuthorityWitness, &v.LibraryGeneration, &v.RawConfigurationGeneration, &v.ProfileRevision, &v.Size, &v.ModifiedNS)
	return
}
func writeDisc(ctx context.Context, tx *sql.Tx, id string, v DiscFacts) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO playback_disc_snapshot_disc(snapshot_id,probe_schema,kind,packaging,encryption,selected_title_key,structure_revision,stream_revision,chapter_revision,facts_revision) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, v.Structure.Schema, v.Structure.Kind, v.Structure.Packaging, v.Structure.Encryption, v.SelectedTitleKey, v.StructureRevision, v.StreamRevision, v.ChapterRevision, v.FactsRevision)
	return err
}
func readDisc(ctx context.Context, tx *sql.Tx, id string) (v DiscFacts, err error) {
	err = tx.QueryRowContext(ctx, `SELECT probe_schema,kind,packaging,encryption,selected_title_key,structure_revision,stream_revision,chapter_revision,facts_revision FROM playback_disc_snapshot_disc WHERE snapshot_id=?`, id).Scan(&v.Structure.Schema, &v.Structure.Kind, &v.Structure.Packaging, &v.Structure.Encryption, &v.SelectedTitleKey, &v.StructureRevision, &v.StreamRevision, &v.ChapterRevision, &v.FactsRevision)
	return
}
func writeMedia(ctx context.Context, tx *sql.Tx, id string, v MediaFacts) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO playback_disc_snapshot_media(snapshot_id,container,video_codec,audio_codec,width,height,duration_ms,origin_us,timing_known,chapters_present) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, v.Container, v.VideoCodec, v.AudioCodec, v.Width, v.Height, v.DurationMs, v.OriginUS, v.TimingKnown, v.Chapters != nil)
	return err
}
func readMedia(ctx context.Context, tx *sql.Tx, id string) (v MediaFacts, present bool, err error) {
	err = tx.QueryRowContext(ctx, `SELECT container,video_codec,audio_codec,width,height,duration_ms,origin_us,timing_known,chapters_present FROM playback_disc_snapshot_media WHERE snapshot_id=?`, id).Scan(&v.Container, &v.VideoCodec, &v.AudioCodec, &v.Width, &v.Height, &v.DurationMs, &v.OriginUS, &v.TimingKnown, &present)
	return
}
func writeTools(ctx context.Context, tx *sql.Tx, id string, v ToolPins) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO playback_disc_snapshot_tools(snapshot_id,disc_helper_digest,ffmpeg_digest,ffprobe_digest,runtime_identity) VALUES(?,?,?,?,?)`, id, v.DiscHelperDigest, v.FFmpegDigest, v.FFprobeDigest, v.RuntimeIdentity)
	return err
}
func readTools(ctx context.Context, tx *sql.Tx, id string) (v ToolPins, err error) {
	err = tx.QueryRowContext(ctx, `SELECT disc_helper_digest,ffmpeg_digest,ffprobe_digest,runtime_identity FROM playback_disc_snapshot_tools WHERE snapshot_id=?`, id).Scan(&v.DiscHelperDigest, &v.FFmpegDigest, &v.FFprobeDigest, &v.RuntimeIdentity)
	return
}
func writeStreams(ctx context.Context, tx *sql.Tx, id string, values []assets.Stream) error {
	for i, v := range values {
		if _, err := tx.ExecContext(ctx, `INSERT INTO playback_disc_snapshot_streams(snapshot_id,stream_order,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,hearing_impaired,commentary,detail_max_bit_rate,detail_h_d_r10_plus,detail_profile,detail_level,detail_pixel_format,detail_bit_depth,detail_width,detail_height,detail_frame_rate,detail_interlaced,detail_rotation,detail_sample_aspect_ratio,detail_anamorphic,detail_ref_frames,detail_codec_tag,detail_bit_rate,detail_sample_rate,detail_bits_per_sample,detail_dynamic_range,detail_dolby_vision_profile,detail_dolby_vision_level,detail_dolby_vision_compatibility,detail_enhancement_layer,detail_master_display,detail_max_c_l_l,detail_object_audio,detail_audio_description,detail_audio_variant,detail_chroma_subsampling,detail_frame_rate_numerator,detail_frame_rate_denominator,detail_frame_rate_mode,detail_interlace_analysis,detail_telecine,detail_dolby_vision_r_p_u,detail_dolby_vision_base_layer,detail_dolby_vision_metadata_known,detail_dolby_vision_enhancement_type,detail_dolby_vision_c_m_version) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, i, v.Index, v.Type, v.Codec, v.Language, v.Title, v.Channels, v.ChannelLayout, v.Default, v.Forced, v.ColorTransfer, v.ColorPrimaries, v.HearingImpaired, v.Commentary, v.Detail.MaxBitRate, v.Detail.HDR10Plus, v.Detail.Profile, v.Detail.Level, v.Detail.PixelFormat, v.Detail.BitDepth, v.Detail.Width, v.Detail.Height, v.Detail.FrameRate, v.Detail.Interlaced, v.Detail.Rotation, v.Detail.SampleAspectRatio, v.Detail.Anamorphic, v.Detail.RefFrames, v.Detail.CodecTag, v.Detail.BitRate, v.Detail.SampleRate, v.Detail.BitsPerSample, v.Detail.DynamicRange, v.Detail.DolbyVisionProfile, v.Detail.DolbyVisionLevel, v.Detail.DolbyVisionCompatibility, v.Detail.EnhancementLayer, v.Detail.MasterDisplay, v.Detail.MaxCLL, v.Detail.ObjectAudio, v.Detail.AudioDescription, v.Detail.AudioVariant, v.Detail.ChromaSubsampling, v.Detail.FrameRateNumerator, v.Detail.FrameRateDenominator, v.Detail.FrameRateMode, v.Detail.InterlaceAnalysis, v.Detail.Telecine, v.Detail.DolbyVisionRPU, v.Detail.DolbyVisionBaseLayer, v.Detail.DolbyVisionMetadataKnown, v.Detail.DolbyVisionEnhancementType, v.Detail.DolbyVisionCMVersion); err != nil {
			return err
		}
	}
	return nil
}
func readStreams(ctx context.Context, tx *sql.Tx, id string) ([]assets.Stream, error) {
	rows, err := tx.QueryContext(ctx, `SELECT stream_order,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,hearing_impaired,commentary,detail_max_bit_rate,detail_h_d_r10_plus,detail_profile,detail_level,detail_pixel_format,detail_bit_depth,detail_width,detail_height,detail_frame_rate,detail_interlaced,detail_rotation,detail_sample_aspect_ratio,detail_anamorphic,detail_ref_frames,detail_codec_tag,detail_bit_rate,detail_sample_rate,detail_bits_per_sample,detail_dynamic_range,detail_dolby_vision_profile,detail_dolby_vision_level,detail_dolby_vision_compatibility,detail_enhancement_layer,detail_master_display,detail_max_c_l_l,detail_object_audio,detail_audio_description,detail_audio_variant,detail_chroma_subsampling,detail_frame_rate_numerator,detail_frame_rate_denominator,detail_frame_rate_mode,detail_interlace_analysis,detail_telecine,detail_dolby_vision_r_p_u,detail_dolby_vision_base_layer,detail_dolby_vision_metadata_known,detail_dolby_vision_enhancement_type,detail_dolby_vision_c_m_version FROM playback_disc_snapshot_streams WHERE snapshot_id=? ORDER BY stream_order LIMIT 129`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []assets.Stream{}
	for rows.Next() {
		var v assets.Stream
		var i int
		if err = rows.Scan(&i, &v.Index, &v.Type, &v.Codec, &v.Language, &v.Title, &v.Channels, &v.ChannelLayout, &v.Default, &v.Forced, &v.ColorTransfer, &v.ColorPrimaries, &v.HearingImpaired, &v.Commentary, &v.Detail.MaxBitRate, &v.Detail.HDR10Plus, &v.Detail.Profile, &v.Detail.Level, &v.Detail.PixelFormat, &v.Detail.BitDepth, &v.Detail.Width, &v.Detail.Height, &v.Detail.FrameRate, &v.Detail.Interlaced, &v.Detail.Rotation, &v.Detail.SampleAspectRatio, &v.Detail.Anamorphic, &v.Detail.RefFrames, &v.Detail.CodecTag, &v.Detail.BitRate, &v.Detail.SampleRate, &v.Detail.BitsPerSample, &v.Detail.DynamicRange, &v.Detail.DolbyVisionProfile, &v.Detail.DolbyVisionLevel, &v.Detail.DolbyVisionCompatibility, &v.Detail.EnhancementLayer, &v.Detail.MasterDisplay, &v.Detail.MaxCLL, &v.Detail.ObjectAudio, &v.Detail.AudioDescription, &v.Detail.AudioVariant, &v.Detail.ChromaSubsampling, &v.Detail.FrameRateNumerator, &v.Detail.FrameRateDenominator, &v.Detail.FrameRateMode, &v.Detail.InterlaceAnalysis, &v.Detail.Telecine, &v.Detail.DolbyVisionRPU, &v.Detail.DolbyVisionBaseLayer, &v.Detail.DolbyVisionMetadataKnown, &v.Detail.DolbyVisionEnhancementType, &v.Detail.DolbyVisionCMVersion); err != nil {
			return nil, err
		}
		if i != len(out) || len(out) >= 128 {
			return nil, ErrInput
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
