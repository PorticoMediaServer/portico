package remotesources

import (
	"context"
	"database/sql"
)

// The normalized immutable definition is the sole source of values. A missing
// exact version is absent; neither a newer version nor a code default replaces it.
func readSourceChoiceDefinitionTx(ctx context.Context, tx *sql.Tx, name string, revision int64) (SourceChoiceDefinition, error) {
	var out SourceChoiceDefinition
	err := tx.QueryRowContext(ctx, `SELECT name,definition_revision,scan_depth,whole_file_reads,connections,requests_per_second,bytes_per_second,scan_interval_seconds,watch_for_changes,scan_on_start,full_recheck_days,double_check_folders,seek_previews,chapter_images,waveforms,loudness,checksums,intros_mode,cache_bytes,cache_floor_bytes,dir_cache_seconds,read_buffer_bytes,connect_timeout_seconds,idle_timeout_seconds,retries,low_level_retries FROM remote_source_choice_definitions WHERE name=? AND definition_revision=?`, name, revision).Scan(
		&out.Name, &out.DefinitionRevision, &out.Reading.ScanDepth, &out.Reading.WholeFileReads, &out.Reading.Connections, &out.Reading.RequestsPerSecond, &out.Reading.BytesPerSecond,
		&out.Advanced.ScanIntervalSeconds, &out.Advanced.WatchForChanges, &out.Advanced.ScanOnStart, &out.Advanced.FullRecheckDays, &out.Advanced.DoubleCheckFolders,
		&out.Advanced.SeekPreviews, &out.Advanced.ChapterImages, &out.Advanced.Waveforms, &out.Advanced.Loudness, &out.Advanced.Checksums, &out.Advanced.IntrosMode,
		&out.Advanced.CacheBytes, &out.Advanced.CacheFloorBytes, &out.Advanced.DirCacheSeconds, &out.Advanced.ReadBufferBytes, &out.Advanced.ConnectTimeoutSeconds, &out.Advanced.IdleTimeoutSeconds, &out.Advanced.Retries, &out.Advanced.LowLevelRetries)
	return out, err
}
