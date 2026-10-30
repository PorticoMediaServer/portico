package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

func storeMBCredits(ctx context.Context, tx *sql.Tx, table, revision, track string, values []metadataprovider.ArtistCredit) error {
	for n, v := range values {
		var e error
		if table == "mb_track_credits" {
			_, e = tx.ExecContext(ctx, `INSERT INTO mb_track_credits VALUES(?,?,?,?,?,?,?,?,?)`, revision, track, n, v.Artist.ID, v.Name, v.Artist.Name, v.Artist.SortName, v.Artist.Disambiguation, v.JoinPhrase)
		} else {
			_, e = tx.ExecContext(ctx, `INSERT INTO `+table+` VALUES(?,?,?,?,?,?,?,?)`, revision, n, v.Artist.ID, v.Name, v.Artist.Name, v.Artist.SortName, v.Artist.Disambiguation, v.JoinPhrase)
		}
		if e != nil {
			return e
		}
	}
	return nil
}
func loadMBCredits(ctx context.Context, tx *sql.Tx, table, revision, track string) ([]metadataprovider.ArtistCredit, error) {
	query := `SELECT artist_id,name,artist_name,sort_name,disambiguation,join_phrase FROM ` + table + ` WHERE revision_id=? ORDER BY ordinal`
	args := []any{revision}
	if table == "mb_track_credits" {
		query = `SELECT artist_id,name,artist_name,sort_name,disambiguation,join_phrase FROM mb_track_credits WHERE release_revision=? AND track_id=? ORDER BY ordinal`
		args = append(args, track)
	}
	rows, e := tx.QueryContext(ctx, query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []metadataprovider.ArtistCredit
	for rows.Next() {
		var v metadataprovider.ArtistCredit
		if e = rows.Scan(&v.Artist.ID, &v.Name, &v.Artist.Name, &v.Artist.SortName, &v.Artist.Disambiguation, &v.JoinPhrase); e != nil {
			return nil, e
		}
		if len(out) >= 128 {
			return nil, errPublicationInput
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func mbOptionalLength(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int64)
	return &n
}
func loadMBRecordingEvidence(ctx context.Context, tx *sql.Tx, revision string, allowOpen bool) (metadataprovider.Recording, string, error) {
	var out metadataprovider.Recording
	var digest, artist, payload string
	var sealed bool
	var length sql.NullInt64
	e := tx.QueryRowContext(ctx, `SELECT provider_id,digest,title,artist,length_millis,disambiguation,payload,sealed FROM mb_recording_evidence WHERE revision_id=?`, revision).Scan(&out.ID, &digest, &out.Title, &artist, &length, &out.Disambiguation, &payload, &sealed)
	if e != nil {
		return out, "", e
	}
	if !allowOpen && !sealed {
		return out, "", errPublicationInput
	}
	out.LengthMillis = mbOptionalLength(length)
	if out.ArtistCredit, e = loadMBCredits(ctx, tx, "mb_recording_credits", revision, ""); e != nil {
		return out, "", e
	}
	// Supplements remain inside the sealed, content-addressed payload. Indexed
	// identity/credits are still reconstructed independently and compared below.
	var supplement metadataprovider.Recording
	if json.Unmarshal([]byte(payload), &supplement) != nil {
		return out, "", errPublicationInput
	}
	out.ISRCs = supplement.ISRCs
	out.Relations = supplement.Relations
	staged, e := stageMBRecording(metadataprovider.RecordingLookup{RequestedID: out.ID, Recording: out})
	if e != nil || staged.digest != digest || string(staged.payload) != payload || mbArtist(out.ArtistCredit) != artist {
		return out, "", errPublicationInput
	}
	return out, digest, nil
}
func saveMBRecordingEvidence(ctx context.Context, tx *sql.Tx, staged mbRecordingEvidence, observed string) (string, error) {
	var revision string
	e := tx.QueryRowContext(ctx, `SELECT revision_id FROM mb_recording_evidence WHERE provider_id=? AND digest=?`, staged.value.ID, staged.digest).Scan(&revision)
	if e == nil {
		_, digest, e := loadMBRecordingEvidence(ctx, tx, revision, false)
		if e != nil {
			return "", e
		}
		if digest != staged.digest {
			return "", errPublicationInput
		}
		return revision, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return "", e
	}
	revision = identity.Token()
	r := staged.value
	if _, e = tx.ExecContext(ctx, `INSERT INTO mb_recording_evidence(revision_id,provider_id,digest,title,artist,length_millis,disambiguation,observed_at,payload) VALUES(?,?,?,?,?,?,?,?,?)`, revision, r.ID, staged.digest, r.Title, mbArtist(r.ArtistCredit), r.LengthMillis, r.Disambiguation, observed, string(staged.payload)); e != nil {
		return "", e
	}
	if e = storeMBCredits(ctx, tx, "mb_recording_credits", revision, "", r.ArtistCredit); e != nil {
		return "", e
	}
	if _, _, e = loadMBRecordingEvidence(ctx, tx, revision, true); e != nil {
		return "", e
	}
	_, e = tx.ExecContext(ctx, `UPDATE mb_recording_evidence SET sealed=1 WHERE revision_id=?`, revision)
	return revision, e
}
func loadMBReleaseEvidence(ctx context.Context, tx *sql.Tx, revision string, allowOpen bool) (metadataprovider.Release, string, error) {
	var out metadataprovider.Release
	var digest, artist, payload string
	var sealed bool
	e := tx.QueryRowContext(ctx, `SELECT provider_id,digest,title,artist,release_group_id,group_title,primary_type,date,country,barcode,disambiguation,payload,sealed FROM mb_release_evidence WHERE revision_id=?`, revision).Scan(&out.ID, &digest, &out.Title, &artist, &out.ReleaseGroup.ID, &out.ReleaseGroup.Title, &out.ReleaseGroup.PrimaryType, &out.Date, &out.Country, &out.Barcode, &out.Disambiguation, &payload, &sealed)
	if e != nil {
		return out, "", e
	}
	if !allowOpen && !sealed {
		return out, "", errPublicationInput
	}
	if out.ArtistCredit, e = loadMBCredits(ctx, tx, "mb_release_credits", revision, ""); e != nil {
		return out, "", e
	}
	rows, e := tx.QueryContext(ctx, `SELECT value FROM mb_release_group_types WHERE release_revision=? ORDER BY ordinal`, revision)
	if e != nil {
		return out, "", e
	}
	for rows.Next() {
		var value string
		if e = rows.Scan(&value); e != nil {
			rows.Close()
			return out, "", e
		}
		if len(out.ReleaseGroup.SecondaryTypes) >= 64 {
			rows.Close()
			return out, "", errPublicationInput
		}
		out.ReleaseGroup.SecondaryTypes = append(out.ReleaseGroup.SecondaryTypes, value)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, "", e
	}
	rows, e = tx.QueryContext(ctx, `SELECT position,format,title FROM mb_release_media WHERE release_revision=? ORDER BY position`, revision)
	if e != nil {
		return out, "", e
	}
	for rows.Next() {
		var m metadataprovider.Medium
		if e = rows.Scan(&m.Position, &m.Format, &m.Title); e != nil {
			rows.Close()
			return out, "", e
		}
		if len(out.Media) >= 200 {
			rows.Close()
			return out, "", errPublicationInput
		}
		out.Media = append(out.Media, m)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, "", e
	}
	type rowTrack struct {
		medium                    int
		recordingRevision, artist string
		track                     metadataprovider.ReleaseTrack
	}
	rows, e = tx.QueryContext(ctx, `SELECT medium_position,track_id,position,number,title,artist,length_millis,recording_revision FROM mb_release_tracks WHERE release_revision=? ORDER BY medium_position,position`, revision)
	if e != nil {
		return out, "", e
	}
	tracks := []rowTrack{}
	for rows.Next() {
		var v rowTrack
		var length sql.NullInt64
		if e = rows.Scan(&v.medium, &v.track.ID, &v.track.Position, &v.track.Number, &v.track.Title, &v.artist, &length, &v.recordingRevision); e != nil {
			rows.Close()
			return out, "", e
		}
		if len(tracks) >= 10000 {
			rows.Close()
			return out, "", errPublicationInput
		}
		v.track.LengthMillis = mbOptionalLength(length)
		tracks = append(tracks, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, "", e
	}
	records := map[string]metadataprovider.Recording{}
	for _, v := range tracks {
		r, ok := records[v.recordingRevision]
		if !ok {
			r, _, e = loadMBRecordingEvidence(ctx, tx, v.recordingRevision, false)
			if e != nil {
				return out, "", e
			}
			records[v.recordingRevision] = r
		}
		v.track.Recording = r
		if v.track.ArtistCredit, e = loadMBCredits(ctx, tx, "mb_track_credits", revision, v.track.ID); e != nil {
			return out, "", e
		}
		if trackArtist(v.track) != v.artist {
			return out, "", errPublicationInput
		}
		found := false
		for n := range out.Media {
			if out.Media[n].Position == v.medium {
				out.Media[n].Tracks = append(out.Media[n].Tracks, v.track)
				found = true
				break
			}
		}
		if !found {
			return out, "", errPublicationInput
		}
	}
	var supplement metadataprovider.Release
	if json.Unmarshal([]byte(payload), &supplement) != nil {
		return out, "", errPublicationInput
	}
	out.Status = supplement.Status
	out.Packaging = supplement.Packaging
	out.LabelInfo = supplement.LabelInfo
	out.Genres = supplement.Genres
	out.TextRepresentation = supplement.TextRepresentation
	staged, e := stageMBRelease(metadataprovider.ReleaseLookup{RequestedID: out.ID, Release: out})
	if e != nil || staged.digest != digest || string(staged.payload) != payload || mbArtist(out.ArtistCredit) != artist {
		return out, "", errPublicationInput
	}
	return out, digest, nil
}
func saveMBReleaseEvidence(ctx context.Context, tx *sql.Tx, staged mbReleaseEvidence, observed string) (string, error) {
	var revision string
	e := tx.QueryRowContext(ctx, `SELECT revision_id FROM mb_release_evidence WHERE provider_id=? AND digest=?`, staged.value.ID, staged.digest).Scan(&revision)
	if e == nil {
		_, digest, e := loadMBReleaseEvidence(ctx, tx, revision, false)
		if e != nil {
			return "", e
		}
		if digest != staged.digest {
			return "", errPublicationInput
		}
		return revision, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return "", e
	}
	revision = identity.Token()
	r := staged.value
	if _, e = tx.ExecContext(ctx, `INSERT INTO mb_release_evidence(revision_id,provider_id,digest,title,artist,release_group_id,group_title,primary_type,date,country,barcode,disambiguation,observed_at,payload) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, revision, r.ID, staged.digest, r.Title, mbArtist(r.ArtistCredit), r.ReleaseGroup.ID, r.ReleaseGroup.Title, r.ReleaseGroup.PrimaryType, r.Date, r.Country, r.Barcode, r.Disambiguation, observed, string(staged.payload)); e != nil {
		return "", e
	}
	if e = storeMBCredits(ctx, tx, "mb_release_credits", revision, "", r.ArtistCredit); e != nil {
		return "", e
	}
	for n, v := range r.ReleaseGroup.SecondaryTypes {
		if _, e = tx.ExecContext(ctx, `INSERT INTO mb_release_group_types VALUES(?,?,?)`, revision, n, v); e != nil {
			return "", e
		}
	}
	for _, m := range r.Media {
		if _, e = tx.ExecContext(ctx, `INSERT INTO mb_release_media VALUES(?,?,?,?)`, revision, m.Position, m.Format, m.Title); e != nil {
			return "", e
		}
		for _, t := range m.Tracks {
			record, e := stageMBRecording(metadataprovider.RecordingLookup{RequestedID: t.Recording.ID, Recording: t.Recording})
			if e != nil {
				return "", errPublicationInput
			}
			recordRevision, e := saveMBRecordingEvidence(ctx, tx, record, observed)
			if e != nil {
				return "", e
			}
			if _, e = tx.ExecContext(ctx, `INSERT INTO mb_release_tracks VALUES(?,?,?,?,?,?,?,?,?)`, revision, t.ID, m.Position, t.Position, t.Number, t.Title, trackArtist(t), t.LengthMillis, recordRevision); e != nil {
				return "", e
			}
			if e = storeMBCredits(ctx, tx, "mb_track_credits", revision, t.ID, t.ArtistCredit); e != nil {
				return "", e
			}
		}
	}
	if _, _, e = loadMBReleaseEvidence(ctx, tx, revision, true); e != nil {
		return "", e
	}
	_, e = tx.ExecContext(ctx, `UPDATE mb_release_evidence SET sealed=1 WHERE revision_id=?`, revision)
	return revision, e
}
