package subtitles

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/localonly"
	"github.com/PorticoMediaServer/portico/server/internal/operations"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

type ProviderStatus struct {
	ID       string `json:"id"`
	Enabled  bool   `json:"enabled"`
	Reason   string `json:"reason,omitempty"`
	ResetsAt string `json:"resetsAt,omitempty"`
	RetryAt  string `json:"retryAt,omitempty"`
}
type ProviderCandidate struct {
	FileID                       int64
	Language, Title, Attribution string
}
type Provider interface {
	ID() string
	Search(context.Context, string, string, int) ([]ProviderCandidate, error)
	Download(context.Context, int64) ([]byte, error)
}

// Capture the durable settings revision before releasing the caller's snapshot.
// Every provider send then reads a fresh snapshot through WithOnlineAdmission.
// A committed credential change also blocks new work until the runtime adopts
// those exact settings, closing the commit-to-notification window.
func (s *Service) onlineAdmission(ctx context.Context, tx *sql.Tx, epoch int64, item string) (context.Context, error) {
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM metadata_runtime_settings WHERE singleton=1`).Scan(&revision); err != nil {
		return ctx, err
	}
	return localonly.WithOnlineAdmission(ctx, s.db, epoch, localonly.ItemAdmission(item), func(ctx context.Context, current *sql.Tx) error {
		var nowRevision int64
		var key, username, password string
		if err := current.QueryRowContext(ctx, `SELECT revision,opensubtitles_api_key,opensubtitles_username,opensubtitles_password FROM metadata_runtime_settings WHERE singleton=1`).Scan(&nowRevision, &key, &username, &password); err != nil {
			return err
		}
		if nowRevision != revision {
			return ErrConflict
		}
		if provider, ok := s.provider.(*OpenSubtitles); ok && !provider.matchesOwnerCredentials(key, username, password) {
			return ErrConflict
		}
		return nil
	}), nil
}

func (s *Service) ProviderStatus() ProviderStatus {
	if s.provider == nil {
		return ProviderStatus{ID: "opensubtitles", Reason: "provider_not_configured"}
	}
	if status, ok := s.provider.(interface{ Status() ProviderStatus }); ok {
		return status.Status()
	}
	return ProviderStatus{ID: s.provider.ID(), Enabled: true}
}

type SearchRequest struct {
	SourceID string `json:"sourceId"`
	Language string `json:"language"`
	Query    string `json:"query"`
	Page     int    `json:"page"`
}
type SearchCandidate struct {
	ID          string `json:"id"`
	Language    string `json:"language"`
	Title       string `json:"title"`
	Attribution string `json:"attribution"`
}
type SearchResult struct {
	Version    int               `json:"version"`
	Candidates []SearchCandidate `json:"candidates"`
	Page       int               `json:"page"`
	ExpiresAt  string            `json:"expiresAt"`
}

func (s *Service) Search(ctx context.Context, p identity.Principal, item string, m SearchRequest) (SearchResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := SearchResult{Version: 1, Candidates: []SearchCandidate{}, Page: m.Page}
	language, e := Language(m.Language)
	if e != nil || language == "und" && m.Language != "" || m.Page < 1 || m.Page > 20 || !textField(m.Query, 160) {
		return out, ErrInput
	}
	if s.provider == nil {
		return out, ErrUnavailable
	}
	gated, p, e := s.readTx(ctx, p, item)
	if e != nil {
		return out, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if m.Language == "" {
		values, _, _, err := operations.EffectivePreferences(tx, p.Viewer, "")
		if err != nil {
			return out, err
		}
		language = "en"
		for _, want := range values.List("playback.preferredSubtitleLanguages") {
			if normalized, err := Language(want); err == nil && normalized != "und" {
				language = normalized
				break
			}
		}
	}

	src, e := sourceQuery(ctx, tx, item, m.SourceID)
	if e != nil {
		return out, e
	}
	if local, err := localonly.Item(ctx, tx, item); err != nil || local {
		if err != nil {
			return out, err
		}
		return out, localonly.Err
	}
	if !src.Available {
		return out, ErrUnavailable
	}
	media, e := providerQuery(ctx, tx, item, strings.TrimSpace(m.Query), language, m.Page)
	if e != nil {
		return out, e
	}

	lookupEpoch, e := localonly.CaptureOnline(ctx, tx)
	if e != nil {
		return out, e
	}
	ctx, e = s.onlineAdmission(ctx, tx, lookupEpoch, item)
	if e != nil {
		return out, e
	}
	if e = gated.Commit(); e != nil {
		return out, e
	}
	if e = localonly.CheckOnline(ctx, dbwork.ReadHandle(ctx, s.db), lookupEpoch); e != nil {
		return out, e
	}
	var candidates []ProviderCandidate
	if advanced, ok := s.provider.(MediaSearchProvider); ok {
		candidates, e = advanced.SearchMedia(ctx, media)
	} else {
		candidates, e = s.provider.Search(ctx, safeTitle(media.Title), language, m.Page)
	}
	if e != nil {
		return out, e
	}
	var gated2 *dbwork.Write
	gated2, p, e = s.tx(ctx, p, item)
	if e != nil {
		return out, e
	}
	tx = gated2.Tx()
	defer gated2.Rollback()
	if e = localonly.CheckOnline(ctx, tx, lookupEpoch); e != nil {
		return out, e
	}
	if local, err := localonly.Item(ctx, tx, item); err != nil || local {
		if err != nil {
			return out, err
		}
		return out, localonly.Err
	}
	current, e := sourceQuery(ctx, tx, item, m.SourceID)
	if e != nil {
		return out, e
	}
	if !current.Available || current.size != src.size || current.modified != src.modified {
		return out, ErrConflict
	}
	// Bounded retained candidates per viewer; a later search deliberately retires
	// older un-applied capabilities. Publication receipts survive this retirement.
	if _, e = tx.ExecContext(ctx, `DELETE FROM subtitle_provider_candidates WHERE actor=? AND item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, actor(p), item); e != nil {
		return out, e
	}
	out.ExpiresAt = time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339)
	for i, c := range candidates {
		if i >= 40 {
			break
		}
		id := identity.Token()
		if _, e = tx.ExecContext(ctx, `INSERT INTO subtitle_provider_candidates(id,actor,item_id,source_id,source_size,source_modified_ns,provider,file_id,language,title,attribution,expires_at,lookup_epoch) SELECT ?,?,id,?,?,?,?,?,?,?,?,?,? FROM catalog_entities WHERE public_id=pid_blob(?)`, id, actor(p), src.ID, src.size, src.modified, s.provider.ID(), c.FileID, c.Language, safeTitle(c.Title), c.Attribution, out.ExpiresAt, lookupEpoch, item); e != nil {
			return out, e
		}
		out.Candidates = append(out.Candidates, SearchCandidate{id, c.Language, safeTitle(c.Title), c.Attribution})
	}
	return out, gated2.Commit()
}

type ApplyRequest struct {
	OperationID string `json:"operationId"`
	CandidateID string `json:"candidateId"`
	Scope       string `json:"scope"`
	Rights      string `json:"rights"`
}

func (s *Service) Apply(ctx context.Context, p identity.Principal, item string, m ApplyRequest) (Receipt, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if !validID(m.OperationID) || !validID(m.CandidateID) || (m.Scope != "personal" && m.Scope != "shared") || !textField(m.Rights, 500) {
		return Receipt{}, ErrInput
	}
	digest := requestDigest([]any{"provider", item, m})
	gated3, p, e := s.tx(ctx, p, item)
	if e != nil {
		return Receipt{}, e
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	old, e := receiptTx(ctx, tx, p, m.OperationID, digest, item)
	if e != nil {
		return Receipt{}, e
	}
	if old != nil {
		return *old, nil
	}
	if s.provider == nil {
		return Receipt{}, ErrUnavailable
	}
	if m.Scope == "shared" && !owner(p) {
		return Receipt{}, identity.ErrUnauthorized
	}
	var srcID, provider, language, title, attribution string
	var size, modified, file int64
	e = tx.QueryRowContext(ctx, `SELECT source_id,source_size,source_modified_ns,provider,file_id,language,title,attribution FROM subtitle_provider_candidates WHERE id=? AND actor=? AND item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND expires_at>? AND lookup_epoch=(SELECT lookup_epoch FROM metadata_runtime_settings WHERE singleton=1)`, m.CandidateID, actor(p), item, time.Now().UTC().Format(time.RFC3339)).Scan(&srcID, &size, &modified, &provider, &file, &language, &title, &attribution)
	if e != nil {
		return Receipt{}, ErrConflict
	}
	src, e := sourceQuery(ctx, tx, item, srcID)
	if e != nil {
		return Receipt{}, e
	}
	if !src.Available || src.size != size || src.modified != modified || provider != s.provider.ID() {
		return Receipt{}, ErrConflict
	}
	if local, err := localonly.Item(ctx, tx, item); err != nil || local {
		if err != nil {
			return Receipt{}, err
		}
		return Receipt{}, localonly.Err
	}
	lookupEpoch, e := localonly.CaptureOnline(ctx, tx)
	if e != nil {
		return Receipt{}, e
	}
	ctx, e = s.onlineAdmission(ctx, tx, lookupEpoch, item)
	if e != nil {
		return Receipt{}, e
	}
	if e = gated3.Commit(); e != nil {
		return Receipt{}, e
	}
	if e = localonly.CheckOnline(ctx, dbwork.ReadHandle(ctx, s.db), lookupEpoch); e != nil {
		return Receipt{}, e
	}
	raw, e := s.provider.Download(ctx, file)
	if e != nil {
		return Receipt{}, e
	}
	canonical, e := Canonical(raw, "srt", int64(src.duration*1e6))
	if e != nil {
		return Receipt{}, e
	}
	mutation := Mutation{OperationID: m.OperationID, SourceID: srcID, Scope: m.Scope, Format: "srt", Language: language, Title: title, Rights: m.Rights, OffsetUS: "0"}
	return s.publish(ctx, p, item, mutation, digest, publication{lookupEpoch: lookupEpoch, source: src, origin: "provider", provider: provider, attribution: attribution, originalDigest: digestBytes(raw)}, canonical)
}
