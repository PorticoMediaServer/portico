package metadata

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
)

// EpisodeThumbnailState contains actual mode/publication/selection facts for
// the source authority owner. None of these fields is a permission grant.
type EpisodeThumbnailState struct {
	PolicyRevision                                    int64
	Mode                                              string
	ArtworkRevision                                   string
	ProviderResolved, ProviderStill, GenerationNeeded bool
}

func (s *Service) ReadEpisodeThumbnailStateTx(ctx context.Context, tx *sql.Tx, library, episode string) (EpisodeThumbnailState, error) {
	out, err := s.episodeThumbnailArtworkStateTx(ctx, tx, library, episode)
	if err != nil {
		return out, err
	}
	policy, err := catalog.EpisodeThumbnailPolicyTx(ctx, tx, library)
	if err == nil {
		out.PolicyRevision, out.Mode = policy.Revision, policy.Mode
	}
	return out, err
}

func (s *Service) episodeThumbnailArtworkStateTx(ctx context.Context, tx *sql.Tx, library, episode string) (EpisodeThumbnailState, error) {
	out := EpisodeThumbnailState{}
	target := RepairTarget{Kind: "item", ID: episode}
	before, ent, err := readRepairSnapshot(ctx, tx, target)
	if err != nil {
		return out, err
	}
	if ent.itemKind != "episode" || ent.library != library {
		return out, ErrRepairInput
	}
	entity, err := artworkEntity(ctx, tx, episode)
	if err != nil {
		return out, err
	}
	fence, err := artworkFence(ctx, tx, target)
	if err != nil {
		return out, err
	}
	repair, err := repairRevision(ctx, tx, target, before, ent)
	if err != nil {
		return out, err
	}
	var held bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM artwork_selections a JOIN artwork_candidates c ON c.id=a.candidate_id WHERE a.kind='item' AND a.entity_id=? AND a.role IN('still','thumbnail') AND a.subject='' AND (a.locked=1 OR c.provider IN('local','upload') OR c.source_fence=?)) OR EXISTS(SELECT 1 FROM artwork_jobs WHERE kind='item' AND entity_id=? AND role IN('still','thumbnail') AND subject='' AND (actor<>'' OR provider='local') AND status IN('pending','running','retry'))`, entity, fence, entity).Scan(&held)
	if err != nil {
		return out, err
	}
	out.GenerationNeeded = !held
	var status, problem, lease, publication string
	var revision int64
	var refreshing bool
	err = tx.QueryRowContext(ctx, `SELECT status,error,lease,accepted_publication,revision,force_refresh FROM screen_metadata_work WHERE target_kind='item' AND target_id=?`, entity).Scan(&status, &problem, &lease, &publication, &revision, &refreshing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	out.ArtworkRevision = publicationDigest([]any{fence, repair, status, problem, lease, publication, revision, refreshing})
	record, hasScreen, err := acceptedScreenArtwork(ctx, tx, target)
	if err != nil {
		return out, err
	}
	local, err := LibraryLocalOnlyTx(ctx, tx, library)
	if err != nil {
		return out, err
	}
	if local {
		out.ProviderResolved = true
		// Existing accepted provider art is not relabelled as a local absence.
		out.ProviderStill = record != nil && record.StillPath != ""
		return out, nil
	}
	if hasScreen {
		if record == nil || (status != "matched" && status != "manual_preserved") || problem != "" || lease != "" || refreshing {
			return out, nil
		}
		if record.StillPath == "" {
			out.ProviderResolved = true
			return out, nil
		}
		origin := record.StillPath
		if record.Identity.Provider == "tmdb" {
			origin = imageURL(origin, "original")
		}
		if record.Identity.Provider == "tvdb" && strings.HasPrefix(origin, "/banners/") {
			origin = "https://artworks.thetvdb.com" + origin
		}
		enabled, err := s.artworkProviderEnabled(ctx, tx, target, record.Identity.Provider)
		if err != nil {
			return out, err
		}
		if enabled && artworkURLAllowed(origin, record.Identity.Provider) {
			out.ProviderResolved = true
			out.ProviderStill = true
		}
		return out, nil
	}
	// Legacy TVDB evidence is usable only when its accepted show/episode link
	// is current and all provider work is settled. Missing rows are unknown.
	var image, jobStatus, jobError, linkStatus string
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(json_extract(p.payload,'$.image'),''),j.status,j.error,l.status FROM provider_evidence p JOIN catalog_episodes ep ON ep.entity_id=p.item_id JOIN tvdb_jobs j ON j.show_id=ep.show_id JOIN tvdb_episode_links l ON l.item_id=ep.entity_id WHERE p.item_id=? AND p.provider='tvdb' AND p.provider_id=l.provider_id`, entity).Scan(&image, &jobStatus, &jobError, &linkStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.ArtworkRevision = publicationDigest([]any{out.ArtworkRevision, image, jobStatus, jobError, linkStatus})
	if jobError != "" || jobStatus != "complete" || linkStatus != "matched" {
		return out, nil
	}
	if image == "" {
		out.ProviderResolved = true
		return out, nil
	}
	if strings.HasPrefix(image, "/banners/") {
		image = "https://artworks.thetvdb.com" + image
	}
	enabled, err := s.artworkProviderEnabled(ctx, tx, target, "tvdb")
	if err != nil {
		return out, err
	}
	if enabled && artworkURLAllowed(image, "tvdb") {
		out.ProviderResolved = true
		out.ProviderStill = true
	}
	return out, nil
}
