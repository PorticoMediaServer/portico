package operations

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/configfile"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// ConfigurationBinding connects an approved row to its one runtime authority.
// It contains no UI copy and does not register a setting without a consumer.
type ConfigurationBinding struct {
	ID, Key, Field string
	Scale          int
	ConfigOnly     bool
}

func ConfigurationBindings() []ConfigurationBinding {
	return []ConfigurationBinding{
		{"storage.missingRule", "storage.remove_missing_titles", "library.missingTitles", 1, false},
		{"storage.missingDaysExact", "storage.remove_missing_titles_days", "library.missingTitlesDays", 1, false},
		{"segments.shortestEpisode", "segments.shortest_episode_minutes", "segments.shortestEpisode", 1, false},
		{"segments.shortestFilm", "segments.shortest_film_minutes", "segments.shortestFilm", 1, false},
		{"segments.introWindow", "segments.intro_window", "segments.introWindow", 1, false},
		{"segments.creditsWindow", "segments.credits_window", "segments.creditsWindow", 1, false},
		{"segments.filmCreditsWindow", "segments.film_credits_window", "segments.filmCreditsWindow", 1, false},
		{"segments.tvMode", "segments.tv_mode", "segments.tvMode", 1, false},
		{"segments.animeMode", "segments.anime_mode", "segments.animeMode", 1, false},
		{"segments.movieMode", "segments.movie_mode", "segments.movieMode", 1, false},
		{"segments.chapters", "segments.use_chapter_names", "segments.useChapterNames", 1, false},
		{"segments.viewerFloor", "segments.viewer_confidence_percent", "segments.viewerConfidencePercent", 1, false},
		{"segments.autoFloor", "segments.automatic_confidence_percent", "segments.automaticConfidencePercent", 1, false},
		{"segments.mode", "library.segment_detection", "library.segmentDetection", 1, false},
		{"people.maxStreamsEach", "streaming.max_streams_per_account", "perAccountCap", 1, false},
		{"people.communityActivity", "people.community_activity", "home.communityActivityEnabled", 1, false},
		{"integrations.compatibility", "integrations.compatibility_enabled", "integrations.compatibilityEnabled", 1, false},
		{"metadata.titleDataset", "recommendations.title_dataset", "recommendations.titleDataset", 1, false},
		{"integrations.requestsPerMinute", "integrations.api_requests_per_minute", "apiRequestsPerMinute", 1, false},
		{"streaming.whenCantPlay", "streaming.when_cant_play", "planningPolicy", 1, false},
		{"remote.who", "remote.who_can_connect", "remoteSignInPolicy", 1, false},
		{"general.name", "general.name", "name", 1, false},
		{"general.updateFeed", "general.update_feed_url", "updates.feedUrl", 1, false},
		{"updates.channel", "updates.channel", "updates.releaseChannel", 1, true},
		{"streaming.allowConversion", "streaming.allow_conversion", "conversionMode", 1, false},
		{"streaming.outputFormat", "streaming.output_format", "outputFormat", 1, false},
		{"streaming.musicAwayQuality", "streaming.music_away_quality", "musicAwayQuality", 1, false},
		{"streaming.downloadQuality", "downloads.max_quality", "downloadMaxQuality", 1, false},
		{"streaming.uploadSpeed", "streaming.upload_speed", "uploadCapacityKbps", 1000, false},
		{"streaming.awayExact", "streaming.away_limit_mbps", "remoteBitrateLimitKbps", 1000, false},
		{"streaming.toneMapping", "streaming.tone_mapping", "hdrToneMapping", 1, false},
		{"streaming.toneMappingMethod", "streaming.tone_mapping_method", "hdrToneMappingAlgorithm", 1, false},
		{"streaming.encoderSpeed", "streaming.encoder_speed", "x264Preset", 1, false},
		{"streaming.repackage", "streaming.direct_stream_remux", "directStreamRemux", 1, false},
		{"streaming.ffmpegPath", "streaming.ffmpeg_path", "ffmpegPath", 1, false},
		{"streaming.hardware", "streaming.hardware_device", "hardwareSelection", 1, false},
		{"streaming.hardwareDecode", "streaming.hardware_decode", "hardwareDecode", 1, false},
		{"streaming.convertAhead", "streaming.convert_ahead_seconds", "throttleBufferMs", 1000, false},
		{"streaming.rewindKeep", "streaming.rewind_keep_seconds", "playedRetentionMs", 1000, false},
		{"streaming.sessionSpace", "streaming.session_space_mb", "sessionStorageMiB", 1, false},
		{"streaming.transcodeLocation", "streaming.transcode_location", "transcodeLocation", 1, false},
		{"streaming.memoryCap", "streaming.memory_cap", "memoryCap", 1, false},
		{"streaming.maxConversions", "streaming.max_conversions", "maxConcurrentSessions", 1, false},
		{"streaming.maxHardwareConversions", "streaming.max_hardware_conversions", "maxHardwareSessions", 1, false},
		{"streaming.maxSoftwareConversions", "streaming.max_software_conversions", "maxSoftwareSessions", 1, false},
		{"streaming.maxBackgroundConversions", "streaming.max_background_conversions", "maxBackgroundSessions", 1, false},
		{"streaming.maxStreams", "streaming.max_streams", "serverCap", 1, false},
		{"streaming.pausedTimeout", "streaming.paused_timeout_minutes", "pausedSessionTimeoutMinutes", 1, false},
		{"storage.analysisSpace", "storage.analysis_space_gb", "analysisStorageGiB", 1, false},
		{"storage.analysisStage", "storage.analysis_stage_limit_mb", "analysisStageStorageMiB", 1, false},
		{"storage.subtitleTracks", "storage.subtitle_tracks_per_file", "subtitleTrackLimit", 1, false},
		{"storage.subtitleFile", "storage.subtitle_file_limit_mb", "subtitleFileLimitMiB", 1, false},
		{"storage.keepNotifications", "storage.keep_notifications_days", "notificationDays", 1, false},
		{"history.keepHistory", "history.keep_play_history", "playHistoryDays", 1, false},
		{"remote.ownCertificate", "remote.own_certificate", "ownAddressEnabled", 1, false},
		{"remote.externalAddress", "remote.external_address", "customCertificateDomain", 1, false},
		{"remote.certFile", "remote.certificate_path", "customCertificatePath", 1, false},
		{"remote.certKey", "remote.certificate_key_path", "customCertificateKeyPath", 1, false},
		{"remote.homeNets", "remote.home_networks", "lanNetworks", 1, false},
		{"remote.discovery", "remote.discovery", "lanDiscoveryEnabled", 1, false},
		{"remote.dlna", "remote.dlna", "dlnaEnabled", 1, false},
		{"remote.interface", "network.advertised_interface", "advertisedInterface", 1, false},
		{"remote.connectionLimit", "network.connection_limit", "connectionLimit", 1, false},
		{"remote.extraAddresses", "remote.extra_addresses", "accessUrls", 1, false},
		{"remote.trustedProxies", "remote.trusted_proxies", "trustedProxies", 1, false},
		{"troubleshooting.logLevel", "logging.level", "logLevel", 1, false},
		{"troubleshooting.keepLogs", "logging.keep_days", "diagnosticDays", 1, false},
		{"troubleshooting.keepLogsByKind", "logging.keep_days_by_kind", "logRetention", 1, false},
	}
}

type ConfigurationValue struct {
	ID              string `json:"id"`
	Key             string `json:"key"`
	Current         *any   `json:"current"`
	Effective       *any   `json:"effective"`
	Default         *any   `json:"default"`
	Source          string `json:"source"`
	ConfigOnly      bool   `json:"configOnly"`
	ApplicationMode string `json:"applicationMode"`
}
type SettingsConfiguration struct {
	Revision         int64                `json:"revision"`
	RegistryRevision string               `json:"registryRevision"`
	Path             string               `json:"path"`
	Fields           []ConfigurationValue `json:"fields"`
}

// A required nullable pointer declares an intentionally unset value to the wire
// contract. A nil interface would be an accidental null and is refused.
func configurationAtom(v any) *any {
	if v == nil {
		return nil
	}
	return &v
}

func configurationValue(b ConfigurationBinding, v any) any {
	if b.Scale > 1 {
		if n, ok := v.(int); ok {
			if b.Field == "uploadCapacityKbps" && n == 0 {
				return nil
			}
			return float64(n) / float64(b.Scale)
		}
	}
	if enabled, ok := v.(*bool); ok {
		if enabled == nil {
			return nil
		}
		return *enabled
	}
	if n, ok := v.(*int); ok {
		if n == nil {
			return nil
		}
		return *n
	}
	if rows, ok := v.([]LogRetention); ok {
		out := []map[string]any{}
		for _, entry := range rows {
			out = append(out, map[string]any{"category": entry.Category, "days": entry.Days})
		}
		return out
	}
	return v
}
func (s *Store) Configuration(ctx context.Context, auth Authorize) (out SettingsConfiguration, err error) {
	out = SettingsConfiguration{RegistryRevision: RegistryRevision, Fields: []ConfigurationValue{}}
	if s.ConfigFile != nil {
		out.Path = s.ConfigPath
	}
	err = s.snapshot(ctx, auth, "", func(tx *sql.Tx) error {
		document, e := readSettings(tx)
		if e != nil {
			return e
		}
		s.projectAppliedSettings(&document)
		out.Revision = document.Revision
		current, effective, defaults := SettingsValues(document.Requested), SettingsValues(document.Effective), SettingsValues(DefaultSettings())
		definitions := map[string]Field{}
		for _, f := range Registry() {
			definitions[f.ID] = f
		}
		sources := map[string]string{}
		rows, e := tx.QueryContext(ctx, `SELECT field,source FROM server_setting_sources`)
		if e != nil {
			return e
		}
		for rows.Next() {
			var k, v string
			if e = rows.Scan(&k, &v); e != nil {
				rows.Close()
				return e
			}
			sources[k] = v
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, b := range ConfigurationBindings() {
			source := sources[b.Field]
			if source == "" {
				source = "default"
				// Existing normalized domain writers can predate this file bridge.
				// A non-factory database value is still a stored server policy.
				if !reflect.DeepEqual(current[b.Field], defaults[b.Field]) {
					source = "server"
				}
			}
			out.Fields = append(out.Fields, ConfigurationValue{b.ID, b.Key, configurationAtom(configurationValue(b, current[b.Field])), configurationAtom(configurationValue(b, effective[b.Field])), configurationAtom(configurationValue(b, defaults[b.Field])), source, b.ConfigOnly, definitions[b.Field].Apply})
		}
		for _, domain := range s.ConfigurationDomains {
			fields, err := domain.Read(ctx, tx, sources)
			if err != nil {
				return err
			}
			out.Fields = append(out.Fields, fields...)
		}
		return nil
	})
	return
}

// InitializeConfiguration drains an interrupted DB-to-file projection before
// accepting external file edits. The database never stores a serialized graph.
func (s *Store) InitializeConfiguration(ctx context.Context, path string) error {
	s.ConfigPath = path
	s.ConfigFile = configfile.New(path)
	if _, err := s.ConfigFile.Reload(); err != nil {
		return err
	}
	var pending int64
	if err := s.DB.QueryRowContext(ctx, `SELECT pending_revision FROM server_configuration_state WHERE singleton=1`).Scan(&pending); err != nil {
		return err
	}
	if pending > 0 {
		if err := s.FlushConfiguration(ctx); err != nil {
			return err
		}
	}
	return s.ImportConfiguration(ctx)
}

func configurationField(v *Settings, key string) reflect.Value {
	value := reflect.ValueOf(v).Elem()
	typ := value.Type()
	for i := 0; i < typ.NumField(); i++ {
		if strings.Split(typ.Field(i).Tag.Get("json"), ",")[0] == key {
			return value.Field(i)
		}
	}
	return reflect.Value{}
}
func assignConfiguration(v *Settings, b ConfigurationBinding, raw any) error {
	target := configurationField(v, b.Field)
	if !target.IsValid() {
		return ErrInvalid
	}
	if raw == nil {
		if target.Kind() == reflect.Pointer {
			target.SetZero()
			return nil
		}
		if b.Field == "uploadCapacityKbps" {
			target.SetInt(0)
			return nil
		}
		return ErrInvalid
	}
	if target.Kind() == reflect.Pointer && target.Type().Elem().Kind() == reflect.Bool {
		enabled, ok := raw.(bool)
		if !ok {
			return ErrInvalid
		}
		target.Set(reflect.ValueOf(&enabled))
		return nil
	}
	switch target.Kind() {
	case reflect.Int, reflect.Pointer:
		var number float64
		switch n := raw.(type) {
		case int64:
			number = float64(n)
		case float64:
			number = n
		case int:
			number = float64(n)
		default:
			return ErrInvalid
		}
		number *= float64(b.Scale)
		if number < 0 || number > 1e12 || float64(int64(number)) != number {
			return ErrInvalid
		}
		if target.Kind() == reflect.Pointer && number == 0 {
			target.SetZero()
			return nil
		}
		if target.Kind() == reflect.Pointer {
			n := int(number)
			target.Set(reflect.ValueOf(&n))
		} else {
			target.SetInt(int64(number))
		}
	case reflect.Bool:
		x, ok := raw.(bool)
		if !ok {
			return ErrInvalid
		}
		target.SetBool(x)
	case reflect.String:
		x, ok := raw.(string)
		if !ok {
			return ErrInvalid
		}
		target.SetString(x)
	case reflect.Slice:
		if b.Field == "logRetention" {
			var values []LogRetention
			items, ok := raw.([]any)
			if !ok {
				if maps, valid := raw.([]map[string]any); valid {
					items = []any{}
					for _, entry := range maps {
						items = append(items, entry)
					}
				} else {
					return ErrInvalid
				}
			}
			for _, item := range items {
				m, ok := item.(map[string]any)
				if !ok {
					return ErrInvalid
				}
				category, ok := m["category"].(string)
				if !ok {
					return ErrInvalid
				}
				var days int
				switch n := m["days"].(type) {
				case int64:
					days = int(n)
				case int:
					days = n
				default:
					return ErrInvalid
				}
				values = append(values, LogRetention{category, days})
			}
			target.Set(reflect.ValueOf(values))
			break
		}
		items, ok := raw.([]any)
		if !ok {
			if strings, ok := raw.([]string); ok {
				target.Set(reflect.ValueOf(strings))
				return nil
			}
			return ErrInvalid
		}
		values := []string{}
		for _, item := range items {
			x, ok := item.(string)
			if !ok {
				return ErrInvalid
			}
			values = append(values, x)
		}
		target.Set(reflect.ValueOf(values))
	default:
		return ErrInvalid
	}
	return nil
}

// ImportConfiguration validates the entire registered document before applying
// any field. A partial editor write keeps the last committed runtime policy.
func (s *Store) ImportConfiguration(ctx context.Context) error {
	if s.ConfigFile == nil {
		return nil
	}
	s.configMu.Lock()
	changed, err := s.importConfigurationLocked(ctx)
	s.configMu.Unlock()
	if err == nil && changed && s.ConfigChanged != nil {
		s.ConfigChanged(context.WithoutCancel(ctx))
	}
	return err
}

// ValidateConfiguration prepares exactly the startup import against a read-only
// snapshot. It neither applies the plan nor drains the app-to-file outbox.
func (s *Store) ValidateConfiguration(ctx context.Context, path string) error {
	loader := configfile.New(path)
	if _, err := loader.Reload(); err != nil {
		return err
	}
	snapshot, err := dbwork.BeginSnapshot(ctx, s.DB)
	if err != nil {
		return err
	}
	defer snapshot.Rollback()
	_, err = s.prepareConfiguration(ctx, snapshot.Tx(), loader)
	return err
}

func (s *Store) importConfigurationLocked(ctx context.Context) (bool, error) {
	applied := false
	err := dbwork.WithWriteTx(ctx, s.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		apply, err := s.prepareConfiguration(ctx, tx, s.ConfigFile)
		if err != nil || apply == nil {
			return err
		}
		if err = apply(); err != nil {
			return err
		}
		applied = true
		return nil
	})
	return applied, err
}

func (s *Store) prepareConfiguration(ctx context.Context, tx *sql.Tx, loader *configfile.Loader) (ConfigurationApply, error) {
	bindings := ConfigurationBindings()
	raw := map[string]any{}
	allBindings := append([]ConfigurationBinding{}, bindings...)
	for _, domain := range s.ConfigurationDomains {
		allBindings = append(allBindings, domain.Bindings()...)
	}
	for _, b := range allBindings {
		value, err := loader.Resolve(configfile.Binding{Key: b.Key})
		if err != nil {
			return nil, err
		}
		if value.Source == configfile.File {
			raw[b.Key] = value.Value
		}
	}
	document, err := readSettings(tx)
	if err != nil {
		return nil, err
	}
	next := document.Requested
	sources := map[string]string{}
	rows, e := tx.QueryContext(ctx, `SELECT field,source FROM server_setting_sources`)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var k, v string
		if e = rows.Scan(&k, &v); e != nil {
			rows.Close()
			return nil, e
		}
		sources[k] = v
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	pending, e := configurationPending(ctx, tx)
	if e != nil {
		return nil, e
	}
	// A committed app edit wins until its file projection has completed.
	// Other registered file edits can still be imported independently.
	for _, b := range bindings {
		field := b.Field
		if _, exists := pending[field]; !exists {
			continue
		}
		delete(raw, b.Key)
	}
	domainSources := make(map[string]string, len(sources))
	for field, source := range sources {
		if _, queued := pending[field]; !queued {
			domainSources[field] = source
		}
	}
	var domainChanges []ConfigurationApply
	for _, domain := range s.ConfigurationDomains {
		domainRaw := make(map[string]any, len(raw))
		for key, value := range raw {
			domainRaw[key] = value
		}
		for _, b := range domain.Bindings() {
			if _, queued := pending[domain.SourceField(b.Field)]; queued {
				delete(domainRaw, b.Key)
			}
		}
		apply, err := domain.PrepareImport(ctx, tx, domainRaw, domainSources, s.now())
		if err != nil {
			return nil, err
		}
		if apply != nil {
			domainChanges = append(domainChanges, apply)
		}
	}
	defaults := SettingsValues(DefaultSettings())
	removed := map[string]bool{}
	changed := len(domainChanges) > 0
	touched := map[string]bool{}
	for _, b := range bindings {
		if _, queued := pending[b.Field]; queued {
			continue
		}
		if _, ok := raw[b.Key]; !ok && sources[b.Field] == "file" {
			raw[b.Key] = configurationValue(b, defaults[b.Field])
			removed[b.Field] = true
			changed = true
		}
	}

	for _, b := range bindings {
		if _, queued := pending[b.Field]; queued {
			continue
		}
		value, ok := raw[b.Key]
		if !ok {
			continue
		}
		if sources[b.Field] == "" {
			changed = true
			touched[b.Field] = true
		}
		before := configurationValue(b, SettingsValues(next)[b.Field])
		if err = assignConfiguration(&next, b, value); err != nil {
			return nil, fmt.Errorf("invalid config key %s", b.Key)
		}
		if !reflect.DeepEqual(before, configurationValue(b, SettingsValues(next)[b.Field])) {
			changed = true
			touched[b.Field] = true
		}
	}
	if err = validateSettings(&next); err != nil {
		return nil, err
	}
	if !changed {
		return nil, nil
	}
	return func() error {
		for _, apply := range domainChanges {
			if err := apply(); err != nil {
				return err
			}
		}
		if err = writeStoredSettings(tx, next, s.now()); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO configuration VALUES('name',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, next.Name); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE playback_owner_policy SET revision=revision+1,transcoding_enabled=?,per_account_cap=?,server_cap=? WHERE singleton=1`, next.TranscodingEnabled, next.PerAccountCap, next.ServerCap); err != nil {
			return err
		}
		for _, b := range bindings {
			if removed[b.Field] {
				if _, err = tx.ExecContext(ctx, `DELETE FROM server_setting_sources WHERE field=?`, b.Field); err != nil {
					return err
				}
				continue
			}
			if touched[b.Field] {
				if _, err = tx.ExecContext(ctx, `INSERT INTO server_setting_sources VALUES(?,'file') ON CONFLICT(field) DO UPDATE SET source=excluded.source`, b.Field); err != nil {
					return err
				}
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE console_settings_revision SET revision=revision+1 WHERE singleton=1`)
		return err
	}, nil
}

func (s *Store) FlushConfiguration(ctx context.Context) error {
	if s.ConfigFile == nil {
		return nil
	}
	s.configMu.Lock()
	defer s.configMu.Unlock()
	snapshot, err := dbwork.BeginSnapshot(ctx, s.DB)
	if err != nil {
		return err
	}
	var pending int64
	err = snapshot.Tx().QueryRowContext(ctx, `SELECT pending_revision FROM server_configuration_state WHERE singleton=1`).Scan(&pending)
	if err != nil || pending == 0 {
		snapshot.Rollback()
		return err
	}
	document, err := readSettings(snapshot.Tx())
	queued, queueErr := configurationPending(ctx, snapshot.Tx())
	if queueErr != nil {
		snapshot.Rollback()
		return queueErr
	}
	sources := map[string]string{}
	rows, sourceErr := snapshot.Tx().QueryContext(ctx, `SELECT field,source FROM server_setting_sources`)
	if sourceErr == nil {
		for rows.Next() {
			var field, source string
			if sourceErr = rows.Scan(&field, &source); sourceErr != nil {
				break
			}
			if _, ok := queued[field]; ok {
				sources[field] = source
			}
		}
		if sourceErr == nil {
			sourceErr = rows.Err()
		}
		rows.Close()
	}
	additionalPatch := map[string]any{}
	if sourceErr == nil {
		for _, domain := range s.ConfigurationDomains {
			values, err := domain.Projection(ctx, snapshot.Tx(), sources)
			if err != nil {
				sourceErr = err
				break
			}
			for key, value := range values {
				additionalPatch[key] = value
			}
		}
	}
	snapshot.Rollback()
	if sourceErr != nil {
		return sourceErr
	}
	if err != nil {
		return err
	}
	patch := map[string]any{}
	values := SettingsValues(document.Requested)
	for _, b := range ConfigurationBindings() {
		if sources[b.Field] == "" {
			continue
		}
		v := configurationValue(b, values[b.Field])
		if v != nil {
			patch[b.Key] = v
		} else if b.Field == "uploadCapacityKbps" {
			patch[b.Key] = int64(0)
		} else {
			patch[b.Key] = int64(0)
		}
	}
	for key, value := range additionalPatch {
		patch[key] = value
	}
	if err = s.ConfigFile.Patch(patch); err != nil {
		return err
	}
	return s.acknowledgeConfigurationProjection(ctx, queued)
}

func (s *Store) acknowledgeConfigurationProjection(ctx context.Context, queued map[string]int64) error {
	return dbwork.WithWriteTx(ctx, s.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		for field, revision := range queued {
			if _, e := tx.ExecContext(ctx, `DELETE FROM server_configuration_pending WHERE field=? AND revision=?`, field, revision); e != nil {
				return e
			}
		}
		// Domain revisions are independent. A newer queued domain write may have
		// a lower revision than this snapshot, so a max-revision CAS cannot prove
		// that the queue is empty. Recompute from the surviving rows atomically.
		_, e := tx.ExecContext(ctx, `UPDATE server_configuration_state SET pending_revision=coalesce((SELECT max(revision) FROM server_configuration_pending),0) WHERE singleton=1`)
		return e
	})
}

// QueueConfigurationProjectionTx joins a domain write to the durable file
// projection without mirroring that domain's policy or exposing any secret.
// The field uses the same qualified source name as the domain's Read/Projection.
func QueueConfigurationProjectionTx(ctx context.Context, tx *sql.Tx, field string, revision int64) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO server_configuration_pending(field,revision) VALUES(?,?) ON CONFLICT(field) DO UPDATE SET revision=excluded.revision`, field, revision); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE server_configuration_state SET pending_revision=max(pending_revision,?) WHERE singleton=1`, revision)
	return err
}

func configurationPending(ctx context.Context, tx *sql.Tx) (map[string]int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT field,revision FROM server_configuration_pending`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var field string
		var revision int64
		if err := rows.Scan(&field, &revision); err != nil {
			return nil, err
		}
		out[field] = revision
	}
	return out, rows.Err()
}

// ConfigurationApply is a validated import's write phase, bound to its caller's
// transaction. A nil plan means no change. Preparation must only read state.
type ConfigurationApply func() error

// ConfigurationDomain joins another normalized authority to this one file.
// All reads and imports share the caller's snapshot or write transaction.
type ConfigurationDomain interface {
	Bindings() []ConfigurationBinding
	SourceField(string) string
	Read(context.Context, *sql.Tx, map[string]string) ([]ConfigurationValue, error)
	PrepareImport(context.Context, *sql.Tx, map[string]any, map[string]string, int64) (ConfigurationApply, error)
	Projection(context.Context, *sql.Tx, map[string]string) (map[string]any, error)
}
