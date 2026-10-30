package metadata

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

func (s *Service) musicCheckCurrent(ctx context.Context, j mbJob) error {
	gated, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	return s.activeMB(ctx, tx, j)
}
func (s *Service) musicSongSearch(ctx context.Context, j mbJob) error {
	ctx = s.lookupContext(ctx)
	scores := map[string]float64{}
	acoustid, acoustidConfig := s.acoustidState()
	j.fingerprintStatus = musicFingerprintStatus(j, acoustid != nil)
	if j.fingerprintStatus == "ready" {
		var reason string
		e := s.db.QueryRowContext(ctx, `SELECT reason FROM music_acoustid_health WHERE singleton=1 AND configuration_digest=?`, acoustidConfig).Scan(&reason)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if e == nil {
			j.fingerprintStatus = "provider_disabled"
			j.acousticError = reason
		}
	}
	if j.fingerprintStatus == "ready" {
		var cooldown string
		e := s.db.QueryRowContext(ctx, `SELECT next_attempt FROM metadata_provider_cooldowns WHERE provider='acoustid'`).Scan(&cooldown)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if cooldown > s.publicationTime().Format(time.RFC3339) {
			j.fingerprintStatus = "provider_backoff"
		} else {
			if e = s.musicCheckCurrent(ctx, j); e != nil {
				return e
			}
			matches, e := acoustid.Lookup(ctx, j.base.Music.Tags["acoustid_fingerprint"], j.base.Music.Duration)
			if e != nil {
				j.fingerprintStatus = "provider_unavailable"
				j.acousticError = "unavailable"
				var failure *metadataprovider.Error
				if errors.As(e, &failure) {
					j.acousticError = failure.Code
					if failure.Code == "authentication" || failure.Code == "rights_or_terms" {
						if _, e = dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `INSERT INTO music_acoustid_health VALUES(1,?,?,?) ON CONFLICT(singleton) DO UPDATE SET configuration_digest=excluded.configuration_digest,reason=excluded.reason,observed_at=excluded.observed_at`, acoustidConfig, failure.Code, s.publicationTime().Format(time.RFC3339)); e != nil {
							return e
						}
						j.fingerprintStatus = "provider_disabled"
					}
					if failure.Retryable() {
						until := s.publicationTime().Add(max(30*time.Second, failure.RetryAfter)).Format(time.RFC3339)
						_, e = dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `INSERT INTO metadata_provider_cooldowns(provider,next_attempt) VALUES('acoustid',?) ON CONFLICT(provider) DO UPDATE SET next_attempt=MAX(next_attempt,excluded.next_attempt)`, until)
						if e != nil {
							return e
						}
					}
				}
			} else {
				j.fingerprintStatus = "no_match"
				for _, m := range matches {
					if mbIdentifier.MatchString(m.RecordingID) && m.Score >= .90 && m.Score <= 1 {
						scores[strings.ToLower(m.RecordingID)] = m.Score
						j.fingerprintStatus = "matched"
					}
				}
			}
		}
	}
	values := []metadataprovider.Recording{}
	usable := strings.TrimSpace(j.title) != "" && strings.TrimSpace(j.artist) != "" && len(j.title) <= 512 && len(j.artist) <= 512
	if usable {
		if e := s.musicCheckCurrent(ctx, j); e != nil {
			return e
		}
		var e error
		values, e = s.mb.SearchRecordings(ctx, j.title, j.artist)
		if e != nil {
			return e
		}
	}
	seen := map[string]bool{}
	for _, v := range values {
		seen[strings.ToLower(v.ID)] = true
	}
	// A strict additional-lookup budget prevents a fingerprint result from turning
	// one durable job into an unbounded fanout. Incomplete competitors forbid auto.
	added := 0
	ids := sortedAcousticIDs(scores)
	for _, id := range ids {
		if seen[id] {
			continue
		}
		if added >= 2 || len(values) >= 25 {
			j.incompleteCandidates = true
			continue
		}
		if e := s.musicCheckCurrent(ctx, j); e != nil {
			return e
		}
		added++
		lookup, _, e := s.mbRecording(ctx, id)
		if e != nil {
			j.incompleteCandidates = true
			continue
		}
		canonical := strings.ToLower(lookup.Recording.ID)
		if !seen[canonical] {
			values = append(values, lookup.Recording)
		}
		seen[id], seen[canonical] = true, true
		scores[canonical] = max(scores[canonical], scores[id])
	}
	rows, e := stageMBSongCandidates(values, j.title, j.artist)
	if e != nil {
		return errPublicationInput
	}
	for n := range rows {
		rankMusicSong(&rows[n], j, musicCandidateRecording(rows[n]), scores[rows[n].id])
	}
	return s.mbStoreCandidates(ctx, j, rows)
}

func sortedAcousticIDs(scores map[string]float64) []string {
	ids := make([]string, 0, len(scores))
	for id := range scores {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if scores[ids[i]] != scores[ids[j]] {
			return scores[ids[i]] > scores[ids[j]]
		}
		return ids[i] < ids[j]
	})
	return ids
}
