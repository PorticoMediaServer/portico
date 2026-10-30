package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"strings"
	"time"

	"golang.org/x/text/language"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

const ScreenDisclosureVersion = "screen-metadata-v1"
const ScreenDisclosure = "Portico can send cleaned movie or show titles, release years, typed provider identifiers, selected episode numbers and your metadata language/region to the providers you enable. It does not send media files, full source paths, account/profile details or playback history. These requests are made by your server using Portico’s application credentials (AniList public queries need no account). Local NFO reading stays under the library’s scan policy. You can choose a provider, pause remote metadata or withdraw confirmation here."

type ScreenPolicy struct {
	LibraryID       string `json:"libraryId"`
	LibraryKind     string `json:"libraryKind"`
	Revision        int64  `json:"revision"`
	ConsentRevision int64  `json:"consentRevision"`
	Confirmed       bool   `json:"confirmed"`
	// Status is the library's effective online-lookup state: "enabled",
	// "needs_consent" (server-wide consent never given), "declined" (an owner
	// withdrew it server-wide) or "disabled" (turned off for this library).
	Status string `json:"status"`
	// Agent is the library's metadata source (library_agent.go).
	Agent              string   `json:"agent"`
	Enabled            bool     `json:"enabled"`
	Providers          []string `json:"providers"`
	AvailableProviders []string `json:"availableProviders"`
	Language           string   `json:"language"`
	Region             string   `json:"region"`
	RefreshMode        string   `json:"refreshMode"`
	Disclosure         string   `json:"disclosure"`
	DisclosureVersion  string   `json:"disclosureVersion"`
}
type ScreenPolicyUpdate struct {
	ExpectedRevision        int64    `json:"expectedRevision"`
	ExpectedConsentRevision int64    `json:"expectedConsentRevision"`
	ConfirmRemote           *bool    `json:"confirmRemote,omitempty"`
	DisclosureVersion       string   `json:"disclosureVersion"`
	Enabled                 bool     `json:"enabled"`
	Providers               []string `json:"providers"`
	Language                string   `json:"language"`
	Region                  string   `json:"region"`
	RefreshMode             string   `json:"refreshMode"`
}
type ScreenReviewCandidate struct {
	Key           string                         `json:"key"`
	Provider      string                         `json:"provider"`
	Type          string                         `json:"type"`
	ProviderID    string                         `json:"providerId"`
	Title         string                         `json:"title"`
	Year          int                            `json:"year"`
	Overview      string                         `json:"overview"`
	Format        string                         `json:"format"`
	Confidence    float64                        `json:"confidence"`
	StrongSignals int                            `json:"strongSignals"`
	Contradiction bool                           `json:"contradiction"`
	Reasons       []string                       `json:"reasons"`
	ObservedAt    string                         `json:"observedAt"`
	SourceKind    string                         `json:"sourceKind"`
	SourceURL     string                         `json:"sourceUrl"`
	Attribution   string                         `json:"attribution"`
	Orders        []metadataprovider.ScreenOrder `json:"orders"`
}
type ScreenSeasonMapping struct {
	Number    int    `json:"number"`
	AniListID string `json:"anilistId"`
	Revision  int64  `json:"revision"`
}
type ScreenField struct {
	Name              string          `json:"name"`
	Value             json.RawMessage `json:"value"`
	Provider          string          `json:"provider"`
	SourceKind        string          `json:"sourceKind"`
	SourceURL         string          `json:"sourceUrl"`
	Confidence        float64         `json:"confidence"`
	ObservedAt        string          `json:"observedAt"`
	Language          string          `json:"language"`
	Region            string          `json:"region"`
	SelectionRevision int64           `json:"selectionRevision"`
}
type ScreenState struct {
	ServerID           string                         `json:"serverId"`
	ViewerFence        string                         `json:"viewerFence"`
	LibraryID          string                         `json:"libraryId"`
	EntityID           string                         `json:"entityId"`
	Kind               string                         `json:"kind"`
	Revision           int64                          `json:"revision"`
	SelectionRevision  int64                          `json:"selectionRevision"`
	Status             string                         `json:"status"`
	Explanation        string                         `json:"explanation"`
	Selected           metadataprovider.ScreenID      `json:"selected"`
	SelectionMode      string                         `json:"selectionMode"`
	Order              string                         `json:"order"`
	Accepted           *metadataprovider.ScreenRecord `json:"accepted,omitempty"`
	AcceptedAt         string                         `json:"acceptedAt"`
	AcceptedConfidence float64                        `json:"acceptedConfidence"`
	Candidates         []ScreenReviewCandidate        `json:"candidates"`
	Actions            []string                       `json:"actions"`
	Attempts           int                            `json:"attempts"`
	NextAttempt        string                         `json:"nextAttempt"`
	Error              string                         `json:"error"`
	Attribution        string                         `json:"attribution"`
	Policy             ScreenPolicy                   `json:"policy"`
	Fields             []ScreenField                  `json:"fields"`
	LocalIssues        []string                       `json:"localIssues"`
	Seasons            []ScreenSeasonMapping          `json:"seasons"`
	ParentID           string                         `json:"parentId"`
	EpisodeStatus      string                         `json:"episodeStatus"`
}

func readScreenPolicy(ctx context.Context, tx *sql.Tx, library string) (ScreenPolicy, error) {
	out := ScreenPolicy{LibraryID: library, Providers: []string{}, AvailableProviders: []string{"tmdb", "tvdb"}, Disclosure: ScreenDisclosure, DisclosureVersion: ScreenDisclosureVersion}
	var raw, decided string
	err := tx.QueryRowContext(ctx, `SELECT l.kind,p.revision,c.revision,c.confirmed,c.confirmed_at,p.enabled,p.providers,p.language,p.region,p.refresh_mode FROM screen_metadata_policies p JOIN libraries l ON l.id=p.library_id CROSS JOIN screen_metadata_consent c WHERE p.library_id=? AND c.singleton=1`, library).Scan(&out.LibraryKind, &out.Revision, &out.ConsentRevision, &out.Confirmed, &decided, &out.Enabled, &raw, &out.Language, &out.Region, &out.RefreshMode)
	if err != nil {
		return out, err
	}
	out.Status = screenLookupStatus(out.Confirmed, decided, out.Enabled)
	if err = tx.QueryRowContext(ctx, `SELECT agent FROM library_metadata_agents WHERE library_id=?`, library).Scan(&out.Agent); err != nil {
		return out, err
	}
	if out.LibraryKind == "anime" {
		out.AvailableProviders = metadataprovider.ProvidersFor(out.LibraryKind)
	}
	err = json.Unmarshal([]byte(raw), &out.Providers)
	return out, err
}

// screenLookupStatus follows the worker's blocking order (screen_worker.go):
// missing consent parks a target before a disabled library does.
func screenLookupStatus(confirmed bool, decidedAt string, enabled bool) string {
	switch {
	case !confirmed && decidedAt == "":
		return "needs_consent"
	case !confirmed:
		return "declined"
	case !enabled:
		return "disabled"
	}
	return "enabled"
}

// defaultScreenProviders is what an enabled library with no provider chosen
// uses. A library has one provider, never a chain: TMDB, for anime too (it
// names every episode, which AniList does not). TheTVDB, and AniList for an
// anime library, are choices an owner makes.
func defaultScreenProviders(string) []string {
	return []string{"tmdb"}
}

func (s *Service) ScreenPolicy(ctx context.Context, library string) (ScreenPolicy, error) {
	gated, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return ScreenPolicy{}, err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	return readScreenPolicy(ctx, tx, library)
}
func (s *Service) UpdateScreenPolicy(ctx context.Context, library string, body ScreenPolicyUpdate, authorize func(*sql.Tx) error) error {
	if body.ExpectedRevision < 1 || body.ExpectedConsentRevision < 1 || len(body.Providers) > 3 || len(body.Language) > 64 || len(body.Region) > 2 {
		return requestinput.New("invalid screen metadata policy")
	}
	if body.RefreshMode != "replace_unlocked" && body.RefreshMode != "fill_missing" {
		return requestinput.New("invalid metadata refresh mode")
	}
	if _, err := language.Parse(body.Language); err != nil {
		return requestinput.New("invalid metadata language")
	}
	if body.Region != "" && (len(body.Region) != 2 || body.Region[0] < 'A' || body.Region[0] > 'Z' || body.Region[1] < 'A' || body.Region[1] > 'Z') {
		return requestinput.New("metadata region must be a two-letter uppercase code")
	}
	if body.ConfirmRemote != nil && *body.ConfirmRemote && body.DisclosureVersion != ScreenDisclosureVersion {
		return requestinput.New("review the current provider disclosure before confirming")
	}
	if _, _, protocol := usermutation.RequestFrom(ctx); protocol && authorize == nil {
		return ErrScreenConflict
	}
	gated2, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	if authorize != nil {
		if err = authorize(tx); err != nil {
			return err
		}
	}
	r, err := usermutation.CheckContext(ctx, tx, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	if r != nil {
		return nil
	}
	policy, err := readScreenPolicy(ctx, tx, library)
	if err != nil {
		return err
	}
	if policy.Revision != body.ExpectedRevision || policy.ConsentRevision != body.ExpectedConsentRevision {
		return ErrScreenConflict
	}
	consentChanged := body.ConfirmRemote != nil && *body.ConfirmRemote != policy.Confirmed
	seen := map[string]bool{}
	for _, p := range body.Providers {
		allowed := false
		for _, a := range policy.AvailableProviders {
			allowed = allowed || a == p
		}
		if !allowed || seen[p] {
			return requestinput.New("choose each available provider at most once")
		}
		seen[p] = true
	}
	if len(body.Providers) > 1 {
		return requestinput.New("choose one metadata provider for a library")
	}
	if body.Providers == nil {
		body.Providers = []string{}
	}
	if body.Enabled && len(body.Providers) == 0 {
		body.Providers = defaultScreenProviders(policy.LibraryKind)
		for _, p := range body.Providers {
			seen[p] = true
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE screen_metadata_policies SET revision=revision+1,enabled=?,providers=?,language=?,region=?,refresh_mode=? WHERE library_id=?`, body.Enabled, screenRaw(body.Providers), body.Language, body.Region, body.RefreshMode, library)
	if err != nil {
		return err
	}
	if consentChanged {
		_, err = tx.ExecContext(ctx, `UPDATE screen_metadata_consent SET revision=revision+1,confirmed=?,confirmed_at=? WHERE singleton=1`, *body.ConfirmRemote, tvdbStamp(s.publicationTime()))
		if err != nil {
			return err
		}
	}
	// Mirror the existing TVDB publication policy fence. Even cached episode pages
	// cannot publish after a provider/locale/consent configuration changes.
	_, err = tx.ExecContext(ctx, `UPDATE tvdb_provider_policies SET enabled=?,refresh_mode=?,revision=revision+1 WHERE library_id=?`, body.Enabled && seen["tvdb"], body.RefreshMode, library)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE metadata_provider_policies SET enabled=?,language=?,region=?,refresh_mode=? WHERE library_id=? AND provider='tmdb'`, body.Enabled && seen["tmdb"], body.Language, body.Region, body.RefreshMode, library)
	if err != nil {
		return err
	}
	// The per-library switch is the library's metadata source: on is the
	// online agent, off is local metadata only.
	if err = syncLibraryAgentTx(ctx, tx, library, body.Enabled && len(body.Providers) > 0); err != nil {
		return err
	}
	if body.Enabled {
		if err = screenRequestRematchTx(ctx, tx, library, body.Providers); err != nil {
			return err
		}
	}
	// The policy/consent triggers own the durable fanout generation. Explicit
	// paths only OR rematch intent; rows transition in bounded worker turns.
	if err = usermutation.SaveContext(ctx, tx, usermutation.Result{Status: "applied", TargetID: library, Revision: policy.Revision + 1}, time.Now().UnixMilli()); err != nil {
		return err
	}
	return gated2.Commit()
}
func (s *Service) ScreenState(ctx context.Context, kind, id string) (ScreenState, error) {
	out := ScreenState{EntityID: id, Kind: kind, Candidates: []ScreenReviewCandidate{}, Actions: []string{"policy"}, Fields: []ScreenField{}, LocalIssues: []string{}, Seasons: []ScreenSeasonMapping{}}
	gated3, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return out, err
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	entity, err := resolveEntity(ctx, tx, id)
	if err != nil {
		return out, err
	}
	var raw string
	var parent int64
	err = tx.QueryRowContext(ctx, `SELECT library_id,revision,selection_revision,status,provider,provider_type,provider_id,selection_mode,episode_order,attempts,next_attempt,error,parent_id,accepted_publication FROM screen_metadata_work WHERE target_kind=? AND target_id=?`, kind, entity).Scan(&out.LibraryID, &out.Revision, &out.SelectionRevision, &out.Status, &out.Selected.Provider, &out.Selected.Type, &out.Selected.ID, &out.SelectionMode, &out.Order, &out.Attempts, &out.NextAttempt, &out.Error, &parent, &raw)
	if err != nil {
		return out, err
	}
	out.ParentID = entityPublic(ctx, tx, parent)
	out.Explanation = screenExplain(out.Status)
	out.Attribution = "Local NFO and provider evidence are kept separate from source/playback facts."
	out.Policy, err = readScreenPolicy(ctx, tx, out.LibraryID)
	if err != nil {
		return out, err
	}
	if raw != "" {
		var r metadataprovider.ScreenRecord
		var payload string
		if err = tx.QueryRowContext(ctx, `SELECT payload,observed_at,confidence FROM screen_metadata_publications WHERE id=?`, raw).Scan(&payload, &out.AcceptedAt, &out.AcceptedConfidence); err != nil {
			return out, err
		}
		if err = json.Unmarshal([]byte(payload), &r); err != nil {
			return out, err
		}
		out.Accepted = &r
		out.LocalIssues = append(out.LocalIssues, r.Skipped...)
		out.Attribution = metadataprovider.ScreenAttribution(r.Identity.Provider)
	}
	rows, err := tx.QueryContext(ctx, `SELECT candidate_key,provider,provider_type,provider_id,payload,confidence,strong_signals,contradiction,reasons,observed_at,source_kind FROM screen_metadata_candidates WHERE target_kind=? AND target_id=? ORDER BY confidence DESC,candidate_key LIMIT 75`, kind, entity)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var c ScreenReviewCandidate
		var payload, reasons string
		var r metadataprovider.ScreenRecord
		if err = rows.Scan(&c.Key, &c.Provider, &c.Type, &c.ProviderID, &payload, &c.Confidence, &c.StrongSignals, &c.Contradiction, &reasons, &c.ObservedAt, &c.SourceKind); err != nil {
			rows.Close()
			return out, err
		}
		if err = json.Unmarshal([]byte(payload), &r); err != nil {
			rows.Close()
			return out, err
		}
		if err = json.Unmarshal([]byte(reasons), &c.Reasons); err != nil {
			rows.Close()
			return out, err
		}
		c.Title = r.Title
		c.Year = r.Year
		c.Format = r.Format
		c.Overview = r.Overview
		if len([]rune(c.Overview)) > 2000 {
			c.Overview = string([]rune(c.Overview)[:2000]) + "…"
		}
		c.Orders = screenRecordOrders(r)
		c.SourceURL = metadataprovider.ScreenSourceURL(r.Identity)
		c.Attribution = metadataprovider.ScreenAttribution(c.Provider)
		out.Candidates = append(out.Candidates, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT field,value,provider,source_kind,source_url,confidence,observed_at,language,region,selection_revision FROM screen_metadata_fields WHERE target_kind=? AND target_id=? ORDER BY field LIMIT 64`, kind, entity)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v ScreenField
		var raw string
		if err = rows.Scan(&v.Name, &raw, &v.Provider, &v.SourceKind, &v.SourceURL, &v.Confidence, &v.ObservedAt, &v.Language, &v.Region, &v.SelectionRevision); err != nil {
			rows.Close()
			return out, err
		}
		v.Value = json.RawMessage(raw)
		out.Fields = append(out.Fields, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT DISTINCT issue FROM video_nfo_status WHERE asset_id IN(`+screenTargetAssets(kind)+`) AND issue<>'' ORDER BY issue LIMIT 16`, entity)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var issue string
		if err = rows.Scan(&issue); err != nil {
			rows.Close()
			return out, err
		}
		if len(out.LocalIssues) < 16 {
			out.LocalIssues = append(out.LocalIssues, issue)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if kind == "show" {
		rows, err = tx.QueryContext(ctx, `SELECT s.number,COALESCE(m.anilist_id,''),COALESCE(m.revision,0) FROM catalog_seasons s LEFT JOIN screen_anime_seasons m ON m.show_id=s.show_id AND m.season_number=s.number WHERE s.show_id=? ORDER BY s.number LIMIT 100`, entity)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var v ScreenSeasonMapping
			if err = rows.Scan(&v.Number, &v.AniListID, &v.Revision); err != nil {
				rows.Close()
				return out, err
			}
			out.Seasons = append(out.Seasons, v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
		if out.Selected.Provider == "tvdb" {
			err = tx.QueryRowContext(ctx, `SELECT status FROM tvdb_jobs WHERE show_id=?`, entity).Scan(&out.EpisodeStatus)
			if err != nil {
				return out, err
			}
		}
	}
	if out.Policy.Confirmed && out.Policy.Enabled && len(out.Policy.Providers) > 0 {
		out.Actions = append(out.Actions, "retry")
		if out.ParentID == "" {
			out.Actions = append(out.Actions, "search")
			if len(out.Candidates) > 0 {
				out.Actions = append(out.Actions, "select")
			}
		}
		if kind == "show" && out.Accepted != nil && out.Accepted.Identity.Provider == "anilist" {
			out.Actions = append(out.Actions, "map_season")
		}
	}
	return out, nil
}

type ScreenSelection struct {
	Actor            MBActor `json:"-"`
	ExpectedRevision int64   `json:"expectedRevision"`
	CandidateKey     string  `json:"candidateKey"`
	Order            string  `json:"order"`
}
type ScreenSearch struct {
	ExpectedRevision int64  `json:"expectedRevision"`
	Provider         string `json:"provider"`
	Query            string `json:"query"`
}

func (s *Service) SelectScreen(ctx context.Context, kind, id string, body ScreenSelection, authorize func(*sql.Tx) error) error {
	if len(body.CandidateKey) != 64 || len(body.Order) > 160 || body.ExpectedRevision < 1 {
		return requestinput.New("select an observed candidate and valid ordering")
	}
	gated4, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	tx := gated4.Tx()
	defer gated4.Rollback()
	if authorize != nil {
		if err = authorize(tx); err != nil {
			return err
		}
	}
	before, ent, err := readRepairSnapshot(ctx, tx, RepairTarget{kind, id})
	if err != nil {
		return err
	}
	base, err := repairRevision(ctx, tx, RepairTarget{kind, id}, before, ent)
	if err != nil {
		return err
	}
	if err = s.selectScreenTx(ctx, tx, kind, id, body); err != nil {
		return err
	}
	who := body.Actor.Authority + ":" + body.Actor.AccountID + ":" + body.Actor.ProfileID
	if err = recordRepair(ctx, tx, RepairTarget{kind, id}, before, base, "identify", who); err != nil {
		return err
	}
	if authorize != nil {
		if err = authorize(tx); err != nil {
			return err
		}
	}
	return gated4.Commit()
}

func (s *Service) selectScreenTx(ctx context.Context, tx *sql.Tx, kind, id string, body ScreenSelection) error {
	if len(body.CandidateKey) != 64 || len(body.Order) > 160 || body.ExpectedRevision < 1 {
		return ErrRepairInput
	}
	b, err := readScreenBase(ctx, tx, kind, id)
	if err != nil {
		return err
	}
	if b.Revision != body.ExpectedRevision {
		return ErrScreenConflict
	}
	if !b.Confirmed || !b.Enabled {
		return requestinput.New("confirm and enable remote metadata before identifying")
	}
	if b.ItemKind == "episode" {
		return requestinput.New("identify the parent show; local episode assignment is separate")
	}
	var payload, digest string
	entity, err := resolveEntity(ctx, tx, id)
	if err != nil {
		return err
	}
	err = tx.QueryRowContext(ctx, `SELECT payload,input_digest FROM screen_metadata_candidates WHERE candidate_key=? AND target_kind=? AND target_id=?`, body.CandidateKey, kind, entity).Scan(&payload, &digest)
	if err != nil {
		return err
	}
	if digest != screenInputDigest(b) {
		return ErrScreenConflict
	}
	var r metadataprovider.ScreenRecord
	if err = json.Unmarshal([]byte(payload), &r); err != nil {
		return err
	}
	if !screenProviderEnabled(b, r.Identity.Provider) {
		return requestinput.New("the chosen provider is disabled")
	}
	order := body.Order
	if kind == "show" {
		valid := false
		valid = screenOrderSupported(r, order)
		if !valid {
			return requestinput.New("select a supported explicit provider ordering")
		}
	} else {
		if order != "" && order != "official" {
			return requestinput.New("movie selection has no episode ordering")
		}
		order = "official"
	}
	return setScreenOwnerIntent(ctx, tx, b, r.Identity, order, body.Actor, false)
}

func screenSupersedeChildren(ctx context.Context, tx *sql.Tx, show string) error {
	// Deliberately no LIMIT: this is a set-based invalidation, not a capped fan-out
	// acquisition. Acquisition/publication itself remains keyset-paged.
	showID, err := resolveEntity(ctx, tx, show)
	if err != nil {
		return err
	}
	predicate := `SELECT e.entity_id FROM catalog_episodes e JOIN screen_metadata_work w ON w.target_kind='item' AND w.target_id=e.entity_id WHERE e.show_id=? AND e.local_identity_status<>'manual' AND w.selection_mode<>'owner' AND NOT EXISTS(SELECT 1 FROM metadata_relationship_decisions d WHERE d.kind='item' AND d.entity_id=e.entity_id AND d.relationship='identity' AND d.locked=1)`
	// Last-good child fields remain visible until that child's replacement succeeds.
	// A cleared desired identity authorizes the next inherited publication, not a
	// synchronous deletion of the old evidence during parent publication.

	_, err = tx.ExecContext(ctx, `UPDATE screen_metadata_work SET provider='',provider_type='',provider_id='',selection_mode='inherited',selection_revision=selection_revision+1,generation=generation+1,revision=revision+1,status='needs_parent_match',lease='',lease_until='',next_attempt='',error='' WHERE target_kind='item' AND target_id IN(`+predicate+`)`, showID)
	return err
}
func (s *Service) SearchScreen(ctx context.Context, kind, id string, body ScreenSearch, authorize func(*sql.Tx) error) error {
	if body.ExpectedRevision < 1 || len(body.Query) > 512 || !screenSearchSafe(body.Query) || strings.TrimSpace(body.Query) == "" && body.Query != "" {
		return requestinput.New("invalid provider search")
	}
	gated5, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	tx := gated5.Tx()
	defer gated5.Rollback()
	if authorize != nil {
		if err = authorize(tx); err != nil {
			return err
		}
	}
	b, err := readScreenBase(ctx, tx, kind, id)
	if err != nil {
		return err
	}
	if b.Revision != body.ExpectedRevision {
		return ErrScreenConflict
	}
	if local, e := LibraryLocalOnlyTx(ctx, tx, b.Library); e != nil || local {
		if e != nil {
			return e
		}
		return ErrLocalMetadataOnly
	}
	if !b.Confirmed || !b.Enabled || !screenProviderEnabled(b, body.Provider) {
		return requestinput.New("confirm and enable the selected provider first")
	}
	if b.ItemKind == "episode" {
		return requestinput.New("search for the parent show")
	}
	_, err = tx.ExecContext(ctx, `UPDATE screen_metadata_work SET requested_provider=?,query_override=?,status='searching',generation=generation+1,revision=revision+1,lease='',lease_until='',attempts=0,next_attempt='',error='' WHERE target_kind=? AND target_id=?`, body.Provider, strings.TrimSpace(body.Query), kind, b.Entity)
	if err != nil {
		return err
	}
	return gated5.Commit()
}
func (s *Service) RetryScreen(ctx context.Context, kind, id string, revision int64, authorize func(*sql.Tx) error) error {
	gated6, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	tx := gated6.Tx()
	defer gated6.Rollback()
	if authorize != nil {
		if err = authorize(tx); err != nil {
			return err
		}
	}
	var current int64
	entity, err := resolveEntity(ctx, tx, id)
	if err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, `SELECT revision FROM screen_metadata_work WHERE target_kind=? AND target_id=?`, kind, entity).Scan(&current); err != nil {
		return err
	}
	if current != revision {
		return ErrScreenConflict
	}
	_, err = tx.ExecContext(ctx, `UPDATE screen_metadata_work SET status='pending',generation=generation+1,revision=revision+1,lease='',lease_until='',attempts=0,next_attempt='',error='' WHERE target_kind=? AND target_id=?`, kind, entity)
	if err != nil {
		return err
	}
	return gated6.Commit()
}

type ScreenSeasonSelection struct {
	ExpectedRevision int64  `json:"expectedRevision"`
	Season           int    `json:"season"`
	AniListID        string `json:"anilistId"`
}

func (s *Service) SelectAnimeSeason(ctx context.Context, show string, body ScreenSeasonSelection, authorize func(*sql.Tx) error) error {
	if body.Season < 0 || body.Season > 99 || !metadataprovider.ValidScreenID(metadataprovider.ScreenID{Provider: "anilist", Type: "anime", ID: body.AniListID}) {
		return requestinput.New("invalid AniList season mapping")
	}
	gated7, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	tx := gated7.Tx()
	defer gated7.Rollback()
	if authorize != nil {
		if err = authorize(tx); err != nil {
			return err
		}
	}
	b, err := readScreenBase(ctx, tx, "show", show)
	if err != nil {
		return err
	}
	if b.Revision != body.ExpectedRevision {
		return ErrScreenConflict
	}
	if b.Accepted == "" || b.Provider != "anilist" {
		return requestinput.New("identify the show with AniList first")
	}
	var raw string
	if err = tx.QueryRowContext(ctx, `SELECT payload FROM screen_metadata_publications WHERE id=?`, b.Accepted).Scan(&raw); err != nil {
		return err
	}
	var r metadataprovider.ScreenRecord
	if err = json.Unmarshal([]byte(raw), &r); err != nil {
		return err
	}
	allowed := r.Identity.ID == body.AniListID
	for _, relation := range r.Relations {
		if relation.Target.Provider == "anilist" && relation.Target.Type == "anime" && relation.Target.ID == body.AniListID {
			allowed = true
		}
	}
	// A separately searched/observed work is also selectable; never accept an
	// arbitrary client-supplied identifier without provider evidence.
	if !allowed {
		var count int
		err = tx.QueryRowContext(ctx, `SELECT count(*) FROM screen_metadata_candidates WHERE target_kind='show' AND target_id=? AND provider='anilist' AND provider_type='anime' AND provider_id=?`, b.Entity, body.AniListID).Scan(&count)
		if err != nil {
			return err
		}
		allowed = count > 0
	}
	if !allowed {
		return requestinput.New("choose an observed AniList work or an attributed related work")
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM catalog_seasons WHERE show_id=? AND number=?`, b.Entity, body.Season).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return errors.New("local season not found")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO screen_anime_seasons(show_id,season_number,anilist_id,source_publication) VALUES(?,?,?,?) ON CONFLICT(show_id,season_number) DO UPDATE SET anilist_id=excluded.anilist_id,source_publication=excluded.source_publication,revision=revision+1`, b.Entity, body.Season, body.AniListID, b.Accepted)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE screen_metadata_work SET provider='',provider_type='',provider_id='',selection_mode='inherited',selection_revision=selection_revision+1,generation=generation+1,revision=revision+1,status='pending',lease='',lease_until='',next_attempt='',error='' WHERE target_kind='item' AND target_id IN(SELECT e.entity_id FROM catalog_episodes e JOIN catalog_seasons s ON s.entity_id=e.season_id WHERE e.show_id=? AND s.number=? AND e.local_identity_status<>'manual') AND selection_mode<>'owner'`, b.Entity, body.Season)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE screen_metadata_work SET revision=revision+1 WHERE target_kind='show' AND target_id=?`, b.Entity)
	if err != nil {
		return err
	}
	return gated7.Commit()
}

func screenRecordOrders(r metadataprovider.ScreenRecord) []metadataprovider.ScreenOrder {
	if len(r.Orders) > 0 {
		return r.Orders
	}
	out := []metadataprovider.ScreenOrder{}
	if r.Identity.Type == "show" {
		if r.Identity.Provider == "tvdb" {
			for _, v := range []string{"official", "dvd", "absolute", "default", "alternate", "regional"} {
				out = append(out, metadataprovider.ScreenOrder{ID: v, Name: v})
			}
		} else {
			out = append(out, metadataprovider.ScreenOrder{ID: "official", Name: "Official aired order"})
		}
	}
	if r.Identity.Type == "anime" {
		out = append(out, metadataprovider.ScreenOrder{ID: "absolute", Name: "Absolute numbering"}, metadataprovider.ScreenOrder{ID: "seasonal", Name: "Local seasonal numbering"})
	}
	return out
}

func screenOrderSupported(r metadataprovider.ScreenRecord, order string) bool {
	base := order
	if r.Identity.Provider == "tmdb" {
		var ok bool
		base, _, ok = metadataprovider.TMDBAbsoluteOrder(order)
		if !ok {
			return false
		}
	}
	for _, o := range screenRecordOrders(r) {
		if o.ID == base {
			return true
		}
	}
	return false
}
