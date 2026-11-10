package db

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
)

// SessionDevice is the device a sign-in is on.
type SessionDevice struct {
	ID       int64
	PublicID string
	Name     string
	// UserPublicID is the account the sign-in belongs to.
	UserPublicID string
}

// SessionDevice returns the device of a live sign-in.
func (d *DB) SessionDevice(ctx context.Context, sessionID int64) (SessionDevice, error) {
	var out SessionDevice
	err := d.read.QueryRowContext(ctx, `SELECT d.id, d.public_id, d.name, u.public_id FROM sessions s
		JOIN devices d ON d.id = s.device_id JOIN users u ON u.id = s.user_id WHERE s.id = ? AND s.revoked_at IS NULL`, sessionID).
		Scan(&out.ID, &out.PublicID, &out.Name, &out.UserPublicID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	return out, err
}

// PutDeviceCapabilities stores the capability document one of the user's devices published.
// The document is kept as it was sent: it can describe more than one player on the device.
func (d *DB) PutDeviceCapabilities(ctx context.Context, userID int64, devicePublicID, document string, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		id, err := userDevice(tx, userID, devicePublicID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO device_capabilities (device_id, document, updated_at) VALUES (?, ?, ?)
			ON CONFLICT (device_id) DO UPDATE SET document = excluded.document, updated_at = excluded.updated_at`, id, document, now.UnixMilli())
		return err
	})
}

// DeviceCapabilities returns the document one of the user's devices last published.
// A device that has published none is ErrNotFound.
func (d *DB) DeviceCapabilities(ctx context.Context, userID int64, devicePublicID string) (document string, updatedAt time.Time, err error) {
	var at int64
	err = d.read.QueryRowContext(ctx, `SELECT c.document, c.updated_at FROM device_capabilities c JOIN devices d ON d.id = c.device_id
		WHERE d.public_id = ? AND EXISTS (SELECT 1 FROM sessions s WHERE s.device_id = d.id AND s.user_id = ?
			AND ifnull(s.revoked_reason, '') <> 'device_forgotten')`, devicePublicID, userID).Scan(&document, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, ErrNotFound
	}
	return document, time.UnixMilli(at), err
}

// PlaybackItem is what starting playback of one title needs to know about it.
type PlaybackItem struct {
	ID         int64
	PublicID   string
	Kind       ItemKind
	Title      string
	DurationMS int64
	LibraryID  int64
	// Parent and Root are what the title sits in: an episode's season and show, a song's
	// album and artist.
	Parent *ItemRef
	Root   *ItemRef
	// PositionMS is where the viewer stopped, 0 for the start.
	PositionMS int64
	// AudioPick and SubtitlePick are the track choices remembered for this title and profile;
	// for an episode with none of its own, the one made on another episode of the show.
	// Empty means no choice was made. See RememberTracks for the notation.
	AudioPick    string
	SubtitlePick string
	Versions     []PlaybackVersion
}

// PlaybackVersion is one playable version with every track that can be chosen for it.
type PlaybackVersion struct {
	Version
	// FileID is the file of the version's first part: what a remembered playback failure is about.
	FileID int64
	Tracks []Track
}

// Track is one video, audio or subtitle track of a version: in its own file, or in a
// subtitle file beside it.
type Track struct {
	Stream
	FileID int64
	Index  int
	// Sidecar is true for a track in a file beside the video.
	Sidecar bool
}

// PlaybackItem returns a title the viewer may see with its versions and tracks, where the
// viewer stopped and the track choices remembered for it. Anything else is ErrNotFound.
func (d *DB) PlaybackItem(ctx context.Context, v Viewer, publicID string) (PlaybackItem, error) {
	var out PlaybackItem
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return out, err
	}
	var parentID, rootID int64
	err = tx.QueryRowContext(ctx, `SELECT i.id, i.public_id, i.kind, i.title, ifnull(i.duration_ms, 0), i.library_id,
		ifnull(i.parent_id, 0), ifnull(i.root_id, 0), ifnull(s.position_ms, 0), ifnull(s.audio_language, ''), ifnull(s.subtitle_language, '')
		FROM items i LEFT JOIN user_item_state s ON s.profile_id = @viewer_profile AND s.item_id = i.id
		WHERE i.public_id = @id AND `+acc.visible("i"), append(v.args(), sql.Named("id", publicID))...).
		Scan(&out.ID, &out.PublicID, &out.Kind, &out.Title, &out.DurationMS, &out.LibraryID, &parentID, &rootID,
			&out.PositionMS, &out.AudioPick, &out.SubtitlePick)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	for _, ref := range []struct {
		id   int64
		into **ItemRef
	}{{parentID, &out.Parent}, {rootID, &out.Root}} {
		if ref.id == 0 {
			continue
		}
		r := &ItemRef{}
		if err := tx.QueryRowContext(ctx, `SELECT public_id, kind, title, index_number FROM items WHERE id = ?`, ref.id).
			Scan(&r.PublicID, &r.Kind, &r.Title, &r.Index); err != nil {
			return out, err
		}
		*ref.into = r
	}
	// A pick made on another episode stands for the whole show, each kind of track on its own.
	if out.Kind == KindEpisode && rootID != 0 && (out.AudioPick == "" || out.SubtitlePick == "") {
		var audio, subtitle string
		err := tx.QueryRowContext(ctx, `SELECT ifnull(audio_language, ''), ifnull(subtitle_language, '') FROM user_item_state
			WHERE profile_id = ? AND item_id = ?`, v.ProfileID, rootID).Scan(&audio, &subtitle)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		if out.AudioPick == "" {
			out.AudioPick = audio
		}
		if out.SubtitlePick == "" {
			out.SubtitlePick = subtitle
		}
	}
	list, err := versions(ctx, tx, out.ID)
	if err != nil {
		return out, err
	}
	for _, version := range list {
		pv := PlaybackVersion{Version: version}
		if len(version.Parts) > 0 {
			pv.FileID = version.Parts[0].FileID
			if pv.Tracks, err = tracks(ctx, tx, pv.FileID, false); err != nil {
				return out, err
			}
		}
		sidecars, err := ids(ctx, tx, `SELECT id FROM files WHERE media_id = ? AND role = ? AND missing_since_at IS NULL ORDER BY name`,
			version.id, FileSubtitle)
		if err != nil {
			return out, err
		}
		for _, fileID := range sidecars {
			more, err := tracks(ctx, tx, fileID, true)
			if err != nil {
				return out, err
			}
			pv.Tracks = append(pv.Tracks, more...)
		}
		out.Versions = append(out.Versions, pv)
	}
	return out, nil
}

func tracks(ctx context.Context, tx *sql.Tx, fileID int64, sidecar bool) ([]Track, error) {
	return queryAll(ctx, tx, `SELECT stream_index, kind, ifnull(codec, ''), ifnull(profile, ''), ifnull(language, ''), ifnull(title, ''),
		is_default, is_forced, is_hearing_impaired, ifnull(bitrate, 0), ifnull(width, 0), ifnull(height, 0), ifnull(frame_rate, 0),
		ifnull(bit_depth, 0), ifnull(hdr, ''), ifnull(channels, 0), ifnull(channel_layout, ''), ifnull(sample_rate, 0)
		FROM media_streams WHERE file_id = ? ORDER BY stream_index`, []any{fileID},
		func(t *Track) []any {
			t.FileID, t.Sidecar = fileID, sidecar
			s := &t.Stream
			return []any{&t.Index, &s.Kind, &s.Codec, &s.Profile, &s.Language, &s.Title, &s.Default, &s.Forced, &s.HearingImpaired,
				&s.Bitrate, &s.Width, &s.Height, &s.FrameRate, &s.BitDepth, &s.HDR, &s.Channels, &s.ChannelLayout, &s.SampleRate}
		})
}

// SubtitlesOff is the remembered subtitle pick of someone who turned subtitles off.
const SubtitlesOff = "off"

// RememberTracks records the audio or subtitle track a person picked in the player, for this
// title and profile on every device. A pick is a language, optionally followed by a space and
// what set the track apart ("eng", "eng sdh", "eng commentary"), or SubtitlesOff. A pick made
// on an episode is also kept on its show, where it stands for the other episodes until a pick
// on another episode replaces it. A nil pick leaves that choice as it was.
func (d *DB) RememberTracks(ctx context.Context, v Viewer, itemID int64, audio, subtitle *string, now time.Time) error {
	if audio == nil && subtitle == nil {
		return nil
	}
	return d.write(ctx, User, func(tx *sql.Tx) error {
		var kind ItemKind
		var rootID int64
		err := tx.QueryRow(`SELECT kind, ifnull(root_id, 0) FROM items WHERE id = ?`, itemID).Scan(&kind, &rootID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil // the title was removed while it played
		}
		if err != nil {
			return err
		}
		targets := []int64{itemID}
		if kind == KindEpisode && rootID != 0 {
			targets = append(targets, rootID)
		}
		for _, id := range targets {
			if _, err := tx.Exec(`INSERT INTO user_item_state (profile_id, item_id, audio_language, subtitle_language, updated_at)
				VALUES (?1, ?2, ?3, ?4, ?5) ON CONFLICT (profile_id, item_id) DO UPDATE SET
				audio_language = ifnull(?3, audio_language), subtitle_language = ifnull(?4, subtitle_language), updated_at = ?5`,
				v.ProfileID, id, audio, subtitle, now.UnixMilli()); err != nil {
				return err
			}
		}
		return nil
	})
}

// Visible reports whether the viewer may see an item now.
func (d *DB) Visible(ctx context.Context, v Viewer, itemID int64) (bool, error) {
	titles, err := d.VisibleTitles(ctx, v, []int64{itemID})
	return len(titles) == 1, err
}

// VisibleTitles returns the titles of those of the items the viewer may see, by id. An
// episode or a song is named with what it belongs to ("Show: Episode").
func (d *DB) VisibleTitles(ctx context.Context, v Viewer, itemIDs []int64) (map[int64]string, error) {
	out := map[int64]string{}
	if len(itemIDs) == 0 {
		return out, nil
	}
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT i.id, i.title, i.kind, ifnull((SELECT r.title FROM items r WHERE r.id = i.root_id), '')
		FROM items i WHERE i.id IN `+intList(itemIDs)+` AND `+acc.visible("i"), v.args()...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var title, root string
		var kind ItemKind
		if err := rows.Scan(&id, &title, &kind, &root); err != nil {
			return nil, err
		}
		if root != "" && (kind == KindEpisode || kind == KindTrack) {
			title = root + ": " + title
		}
		out[id] = title
	}
	return out, rows.Err()
}

// Preferences returns a profile's settings for everywhere and those it saved for one device,
// as stored: key to JSON value. Where both hold a key, the device's wins. What the profile
// saved for a kind of device is not read here: nothing that decides playback is saved that way.
func (d *DB) Preferences(ctx context.Context, profileID, deviceID int64) (map[string]string, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT key, value FROM preferences
		WHERE profile_id = ?1 AND (device_id = ?2 OR device_id IS NULL AND device_class = '')
		ORDER BY device_id IS NOT NULL`, profileID, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, rows.Err()
}

// SetProfilePreference stores one of a profile's settings as a JSON value.
func (d *DB) SetProfilePreference(ctx context.Context, profileID int64, key, value string, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO preferences (profile_id, key, value, updated_at) VALUES (?, ?, ?, ?)
			ON CONFLICT (profile_id, device_class, ifnull(device_id, 0), key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			profileID, key, value, now.UnixMilli())
		return err
	})
}

// PlayRecord is one finished play as the owner's play record has it.
type PlayRecord struct {
	Title             string
	HiddenFromProfile bool
}

// PlayRecords lists a profile's finished plays, newest first, for the owner: it includes
// plays made while the profile had its history paused. Anything shown to the profile itself
// must leave out the rows with HiddenFromProfile.
func (d *DB) PlayRecords(ctx context.Context, profileID int64, limit int) ([]PlayRecord, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT title, hidden_from_profile FROM play_history WHERE profile_id = ?
		ORDER BY played_at DESC, id DESC LIMIT ?`, profileID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlayRecord
	for rows.Next() {
		var r PlayRecord
		if err := rows.Scan(&r.Title, &r.HiddenFromProfile); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RouteFailure is one remembered "this way of playing this file failed on this device".
type RouteFailure struct {
	PublicID  string
	Player    string
	Route     string
	Code      string
	Detail    string
	FailedAt  time.Time
	ExpiresAt time.Time
	// For the owner's list.
	DeviceName string
	ItemID     string
	ItemTitle  string
	FileName   string
}

// RecordRouteFailure remembers that a way of delivering a file failed on a device's player,
// until expires. A later failure of the same way replaces the earlier one.
func (d *DB) RecordRouteFailure(ctx context.Context, deviceID, fileID int64, f RouteFailure) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM playback_route_failures WHERE expires_at <= ?`, f.FailedAt.UnixMilli()); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO playback_route_failures (public_id, device_id, file_id, player, route, code, detail, failed_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, nullif(?, ''), ?, ?) ON CONFLICT (device_id, file_id, player, route) DO UPDATE SET
			code = excluded.code, detail = excluded.detail, failed_at = excluded.failed_at, expires_at = excluded.expires_at`,
			NewID("prf"), deviceID, fileID, f.Player, f.Route, f.Code, f.Detail, f.FailedAt.UnixMilli(), f.ExpiresAt.UnixMilli())
		return err
	})
}

// RouteFailures returns what is remembered now for one file on one device, for planning.
func (d *DB) RouteFailures(ctx context.Context, deviceID, fileID int64, now time.Time) ([]RouteFailure, error) {
	return d.routeFailures(ctx, `f.device_id = ? AND f.file_id = ? AND f.expires_at > ?`, deviceID, fileID, now.UnixMilli())
}

// AllRouteFailures lists everything remembered now, newest first, for the owner.
func (d *DB) AllRouteFailures(ctx context.Context, now time.Time) ([]RouteFailure, error) {
	return d.routeFailures(ctx, `f.expires_at > ?`, now.UnixMilli())
}

func (d *DB) routeFailures(ctx context.Context, where string, args ...any) ([]RouteFailure, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT f.public_id, f.player, f.route, f.code, ifnull(f.detail, ''), f.failed_at, f.expires_at,
		dev.name, fl.name, ifnull(i.public_id, ''), ifnull(i.title, '')
		FROM playback_route_failures f JOIN devices dev ON dev.id = f.device_id JOIN files fl ON fl.id = f.file_id
		LEFT JOIN items i ON i.id = (SELECT m.item_id FROM media_parts p JOIN media m ON m.id = p.media_id WHERE p.file_id = f.file_id LIMIT 1)
		WHERE `+where+` ORDER BY f.failed_at DESC, f.id DESC LIMIT 500`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RouteFailure
	for rows.Next() {
		var f RouteFailure
		var failed, expires int64
		if err := rows.Scan(&f.PublicID, &f.Player, &f.Route, &f.Code, &f.Detail, &failed, &expires, &f.DeviceName, &f.FileName,
			&f.ItemID, &f.ItemTitle); err != nil {
			return nil, err
		}
		f.FailedAt, f.ExpiresAt = time.UnixMilli(failed), time.UnixMilli(expires)
		out = append(out, f)
	}
	return out, rows.Err()
}

// ForgetRouteFailure removes one remembered failure, so the next play tries that way again.
// Forgetting one that is already gone changes nothing.
func (d *DB) ForgetRouteFailure(ctx context.Context, publicID string) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM playback_route_failures WHERE public_id = ?`, publicID)
		return err
	})
}

// Progress is where one playback had got to when its device last said.
type Progress struct {
	Viewer   Viewer
	ItemID   string
	DeviceID int64
	// PlayID names this playing of the title. A finished play is counted once per PlayID,
	// however often it is saved.
	PlayID     string
	PositionMS int64
	// At is when the device was at that position.
	At time.Time
	// WatchedMS is how long the title has actually played in this playing.
	WatchedMS int64
}

// Settings a profile can change that decide what a position means. Values are percentages.
const (
	PrefWatchedAt    = "playback.playedThresholdPercent"  // 50 to 100; the default is 95
	PrefStartedAt    = "playback.startedThresholdPercent" // 1 to 25; the default is 5
	PrefPauseHistory = "privacy.pauseWatchHistory"
)

// How a library decides a video is finished, in its options as "videoCompletion": at the
// percentage or when trusted credits start, whichever comes first (the default); at the
// percentage only; or when trusted credits start, falling back to the percentage for a
// title that has none.
const (
	VideoCompletionEarliest  = "earliest"
	VideoCompletionThreshold = "threshold"
	VideoCompletionCredits   = "credits"
)

// SaveProgress saves a batch of playback positions in one transaction, and reports for each
// whether that playing has now reached the end and been counted.
//
// A position past the profile's "Mark as watched at", or at trusted credits where the library
// goes by them, marks the title watched, clears the resume point, adds one to the play count
// and writes one history row: once per PlayID. An audiobook is finished when 30 seconds or 5%
// remains, whichever is less. Before that, the position becomes the resume point once the
// title counts as started. Songs keep no resume point and no watched mark, only plays. Every
// write goes through the state rules, so a position never undoes a watched or unwatched mark
// made after it. While a profile has history paused nothing of its own is saved, but a
// finished play is still recorded for the owner, hidden from the profile.
func (d *DB) SaveProgress(ctx context.Context, p Priority, batch []Progress) ([]bool, error) {
	finished := make([]bool, len(batch))
	if len(batch) == 0 {
		return finished, nil
	}
	err := d.write(ctx, p, func(tx *sql.Tx) error {
		writers := map[Viewer]*stateWriter{}
		defer func() {
			for _, w := range writers {
				w.close()
			}
		}()
		prefs := map[int64]map[string]string{}
		for n, sample := range batch {
			finished[n] = false
			w := writers[sample.Viewer]
			if w == nil {
				var err error
				if w, err = newStateWriter(ctx, tx, sample.Viewer); err != nil {
					return err
				}
				writers[sample.Viewer] = w
				if prefs[sample.Viewer.ProfileID], err = profilePreferences(tx, sample.Viewer.ProfileID); err != nil {
					return err
				}
			}
			done, err := w.saveProgress(sample, prefs[sample.Viewer.ProfileID])
			if err != nil {
				return err
			}
			finished[n] = done
		}
		return nil
	})
	return finished, err
}

func profilePreferences(tx *sql.Tx, profileID int64) (map[string]string, error) {
	rows, err := tx.Query(`SELECT key, value FROM preferences WHERE profile_id = ? AND device_class = '' AND device_id IS NULL AND key IN (?, ?, ?)`,
		profileID, PrefWatchedAt, PrefStartedAt, PrefPauseHistory)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = strings.Trim(value, `"`)
	}
	return out, rows.Err()
}

func percent(prefs map[string]string, key string, fallback, low, high int64) int64 {
	n, ok := int64(0), false
	for _, c := range prefs[key] {
		if c < '0' || c > '9' || n > 1000 {
			return fallback
		}
		n, ok = n*10+int64(c-'0'), true
	}
	if !ok || n < low || n > high {
		return fallback
	}
	return n
}

func (w *stateWriter) saveProgress(sample Progress, prefs map[string]string) (finished bool, err error) {
	paused := prefs[PrefPauseHistory] == "true"
	var id, duration int64
	var kind ItemKind
	var title, holder, completion string
	var resumeAt, credits sql.NullInt64
	// Only a trusted credits marker can finish a title: see MarkerSource.Trusted.
	err = w.queryRow(`SELECT i.id, i.kind, ifnull(i.duration_ms, 0), i.title,
		ifnull((SELECT r.title FROM items r WHERE r.id = ifnull(i.root_id, i.parent_id)), ''), s.position_ms,
		ifnull((SELECT json_extract(l.options, '$.videoCompletion') FROM libraries l WHERE l.id = i.library_id), ''),
		(SELECT min(k.start_ms) FROM media m JOIN media_markers k ON k.media_id = m.id
			WHERE m.item_id = i.id AND k.kind = `+itoa(int64(MarkerCredits))+` AND k.source <> `+itoa(int64(MarkerFromAnalysis))+`)
		FROM items i LEFT JOIN user_item_state s ON s.profile_id = @viewer_profile AND s.item_id = i.id
		WHERE i.public_id = @item AND `+w.access.visible("i"), w.named(sql.Named("item", sample.ItemID))...).
		Scan(&id, &kind, &duration, &title, &holder, &resumeAt, &completion, &credits)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil // removed, or no longer this profile's to see
	}
	if err != nil {
		return false, err
	}
	position := max(sample.PositionMS, 0)
	if duration > 0 {
		position = min(position, duration)
	}
	ago := max(time.Duration(w.now-sample.At.UnixMilli())*time.Millisecond, 0)

	done := duration > 0 && position >= duration*percent(prefs, PrefWatchedAt, 95, 50, 100)/100
	video := kind == KindMovie || kind == KindEpisode
	switch {
	case kind == KindAudiobook:
		done = duration > 0 && position > 0 && duration-position <= min(30_000, duration/20)
	case video && completion != VideoCompletionThreshold && credits.Valid:
		// The library finishes a video when its credits start: instead of the percentage
		// ("credits"), or whichever comes first (the default).
		atCredits := position >= credits.Int64
		done = atCredits || done && completion != VideoCompletionCredits
	}
	if done {
		res, err := w.exec(`INSERT INTO play_history (profile_id, item_id, device_id, client_event_id, played_at, watched_ms, item_kind, title,
			subtitle, hidden_from_profile) VALUES (?, ?, ?, ?, ?, ?, ?, ?, nullif(?, ''), ?) ON CONFLICT (profile_id, client_event_id) DO NOTHING`,
			w.viewer.ProfileID, id, sample.DeviceID, sample.PlayID, sample.At.UnixMilli(), sample.WatchedMS, kind, title, holder, paused)
		if err != nil {
			return false, err
		}
		if n, _ := res.RowsAffected(); n == 0 || paused {
			// Already counted; or history is paused, and the play is the owner's record only.
			return true, nil
		}
		if kind != KindTrack {
			yes := true
			if _, _, err := w.apply(sample.ItemID, StateEdit{Watched: &yes, Ago: ago}); err != nil && !errors.Is(err, ErrNotFound) {
				return false, err
			}
		}
		_, err = w.exec(`INSERT INTO user_item_state (profile_id, item_id, play_count, last_played_at, updated_at) VALUES (?1, ?2, 1, ?3, ?4)
			ON CONFLICT (profile_id, item_id) DO UPDATE SET play_count = play_count + 1,
			last_played_at = max(ifnull(last_played_at, 0), ?3), updated_at = ?4`, w.viewer.ProfileID, id, sample.At.UnixMilli(), w.now)
		if err != nil {
			return false, err
		}
		return true, appendChange(w.tx, Change{At: w.now, Topic: TopicState, Op: OpUpdated, RefID: id, ProfileID: w.viewer.ProfileID})
	}
	if kind == KindTrack || paused {
		return false, nil
	}
	// A resume point is kept once the title counts as started, and from then on is wherever
	// the person stopped, even after seeking back to the first minute. A title of unknown
	// length counts as started after thirty seconds, and an audiobook after one.
	started := int64(30_000)
	switch {
	case kind == KindAudiobook:
		started = 1000
	case duration > 0:
		started = duration * percent(prefs, PrefStartedAt, 5, 1, 25) / 100
	}
	if position < started && !resumeAt.Valid {
		return false, nil
	}
	_, _, err = w.apply(sample.ItemID, StateEdit{PositionMS: &position, Ago: ago})
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalid) {
		err = nil
	}
	return false, err
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
