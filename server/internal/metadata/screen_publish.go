package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/metadatalanguage"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

// screenPublishPlan bounds one title's publication: its provider evidence (up
// to 75 candidates) is replaced and its accepted record applied together.
var screenPublishPlan = dbwork.WritePlan{Name: "screen-publish", Rows: 2048, Pages: 512, VMOps: 1_000_000, Why: "a title's candidates and accepted record replace the previous evidence atomically; splitting record application is writer-discipline item H"}

func (s *Service) publishScreen(ctx context.Context, p *screenClaim, cs []screenCandidate, winner int, status string) error {
	ctx = dbwork.WithPlan(ctx, screenPublishPlan)
	if len(cs) > 75 || winner >= len(cs) {
		return s.failScreen(ctx, p, &metadataprovider.Error{Code: "invalid_screen_evidence"})
	}
	total := 0
	for _, c := range cs {
		if err := metadataprovider.ValidateScreenRecord(c.Record); err != nil {
			return s.failScreen(ctx, p, err)
		}
		total += len(screenRaw(c.Record))
	}
	if total > 16<<20 {
		return s.failScreen(ctx, p, &metadataprovider.Error{Code: "evidence_capacity"})
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	ok, err := s.screenCurrent(ctx, tx, p)
	if err != nil {
		return err
	}
	if !ok {
		if err = s.screenStale(ctx, tx, p); err != nil {
			return err
		}
		return gated.Commit()
	}
	b := p.Base
	stamp := tvdbStamp(s.publicationTime())
	query := screenQueryReceipt(b)
	if _, err = tx.ExecContext(ctx, `DELETE FROM screen_metadata_candidates WHERE target_kind=? AND target_id=?`, b.TargetKind, b.Entity); err != nil {
		return err
	}
	for i := range cs {
		c := &cs[i]
		c.InputDigest = p.Digest
		c.QueryDigest = query
		c.ObservedAt = stamp
		c.Key = screenDigest([]any{b.TargetKind, b.TargetID, p.Digest, query, c.Record, c.SourceKind})
		_, err = tx.ExecContext(ctx, `INSERT INTO screen_metadata_candidates VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, c.Key, b.TargetKind, b.Entity, c.Record.Identity.Provider, c.Record.Identity.Type, c.Record.Identity.ID, screenRaw(c.Record), c.Confidence, screenRaw(c.Reasons), c.StrongSignals, c.Contradiction, p.Digest, query, c.SourceKind, stamp, b.Language, b.Region)
		if err != nil {
			return err
		}
	}
	if winner >= 0 {
		c := cs[winner]
		if b.ProviderID != "" && (b.Provider != c.Record.Identity.Provider || b.ProviderType != c.Record.Identity.Type || b.ProviderID != c.Record.Identity.ID) {
			return errors.New("accepted identity changed outside owner selection")
		}
		if b.ItemKind == "episode" && b.Mode != "owner" && b.LocalIdentity == "manual" {
			status = "manual_preserved"
		} else {
			if err = s.applyScreenRecord(ctx, tx, p, c); err != nil {
				return err
			}
			if b.TargetKind == "show" {
				status = "pending_children"
			}
		}
	}
	if b.TargetKind == "show" {
		match := status
		if status == "pending_children" {
			match = "matched"
		}
		if err = compactcatalog.SetFieldsTx(ctx, tx, b.Entity, compactcatalog.Automatic, map[string]any{"provider_match_status": match}); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE screen_metadata_work SET status=?,force_refresh=0,error='',attempts=0,next_attempt='',revision=revision+1,lease='',lease_until='' WHERE target_kind=? AND target_id=? AND lease=?`, status, b.TargetKind, b.Entity, p.Token)
	if err != nil {
		return err
	}
	if winner >= 0 {
		current, readErr := readScreenBase(ctx, tx, b.TargetKind, b.TargetID)
		if readErr != nil {
			return readErr
		}
		if _, err = tx.ExecContext(ctx, `UPDATE screen_metadata_candidates SET input_digest=? WHERE target_kind=? AND target_id=?`, screenInputDigest(current), b.TargetKind, b.Entity); err != nil {
			return err
		}
	}
	// Receipts retain recent superseded evidence, not an unbounded provider cache.
	_, err = tx.ExecContext(ctx, `DELETE FROM screen_metadata_publications WHERE target_kind=? AND target_id=? AND decision='superseded' AND id NOT IN(SELECT id FROM screen_metadata_publications WHERE target_kind=? AND target_id=? ORDER BY observed_at DESC,id DESC LIMIT 32)`, b.TargetKind, b.Entity, b.TargetKind, b.Entity)
	if err != nil {
		return err
	}
	return gated.Commit()
}

func (s *Service) applyScreenRecord(ctx context.Context, tx *sql.Tx, p *screenClaim, c screenCandidate) error {
	b := p.Base
	r := c.Record
	r.Language = metadatalanguage.Primary(r.Language)
	id := r.Identity
	if b.TargetKind == "show" && b.ProviderID == "" {
		if id.Provider == "anilist" {
			b.Order = "seasonal"
			if b.Numbering == "absolute" {
				b.Order = "absolute"
			}
		} else if id.Provider == "tvdb" && b.Numbering == "absolute" {
			b.Order = "absolute"
		}
	}
	superseding := b.ItemKind == "episode" && b.Mode == "inherited" && b.ProviderID == "" && b.Accepted != ""
	if b.Mode == "owner" {
		previousSelection := int64(0)
		if b.Accepted != "" {
			if err := tx.QueryRowContext(ctx, `SELECT selection_revision FROM screen_metadata_publications WHERE id=?`, b.Accepted).Scan(&previousSelection); err != nil {
				return err
			}
		}
		superseding = previousSelection != b.Selection
		if superseding && b.TargetKind == "show" {
			if err := screenSupersedeChildren(ctx, tx, b.TargetID); err != nil {
				return err
			}
		}
	}
	// An episode TheTVDB named carries no publication of its own, only that
	// provider's evidence: another provider's record supersedes it the same way.
	if !superseding && b.TargetKind == "item" {
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM provider_evidence WHERE item_id=? AND provider IN('tmdb','tvdb','anilist') AND provider<>?)`, b.Entity, id.Provider).Scan(&superseding); err != nil {
			return err
		}
	}
	// A different identity supersedes the last one's facts, however the title
	// came to be matched again (the library's provider changed, for one).
	if !superseding && b.Accepted != "" {
		var last metadataprovider.ScreenID
		err := tx.QueryRowContext(ctx, `SELECT provider,provider_type,provider_id FROM screen_metadata_publications WHERE id=?`, b.Accepted).Scan(&last.Provider, &last.Type, &last.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && last != id {
			superseding = true
			if b.TargetKind == "show" {
				if err := screenSupersedeChildren(ctx, tx, b.TargetID); err != nil {
					return err
				}
			}
		}
	}
	stamp := tvdbStamp(s.publicationTime())
	publication := identity.Token()
	selection := b.Selection
	changed := b.ProviderID == ""
	if changed {
		selection++
	}
	mode := b.Mode
	if mode == "none" {
		mode = "automatic"
	}
	if b.ItemKind == "episode" && mode != "owner" {
		mode = "inherited"
	}
	if b.Accepted != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE screen_metadata_publications SET decision='superseded' WHERE id=?`, b.Accepted); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO screen_metadata_publications(id,target_kind,target_id,provider,provider_type,provider_id,payload,confidence,reasons,source_kind,input_digest,query_digest,observed_at,language,region,selection_revision,generation,previous_id,decision) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'accepted')`, publication, b.TargetKind, b.Entity, id.Provider, id.Type, id.ID, screenRaw(r), c.Confidence, screenRaw(c.Reasons), c.SourceKind, p.Digest, screenQueryReceipt(b), stamp, b.Language, b.Region, selection, b.Generation, b.Accepted)
	if err != nil {
		return err
	}
	// Explicit identification supersedes screen-provider facts, not owner overrides,
	// physical identities, local episode coordinates, files, or progress.
	if superseding && (b.TargetKind == "item" || b.TargetKind == "show") {
		if b.TargetKind == "item" {
			for _, table := range []string{"provider_evidence", "metadata_details", "metadata_ratings"} {
				if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE item_id=? AND provider IN('tmdb','tvdb','anilist')`, b.Entity); err != nil {
					return err
				}
			}
			for _, class := range []string{"genre", "credit"} {
				locked, e := relationshipClassLocked(ctx, tx, b.TargetID, class)
				if e != nil {
					return e
				}
				if locked && class == "credit" {
					normal, e := normalizedCreditFamily(ctx, tx, b.Entity)
					if e != nil {
						return e
					}
					locked = !normal
				}
				if locked {
					continue
				}
				for _, provider := range []string{"tmdb", "tvdb", "anilist"} {
					if class == "genre" {
						e = compactcatalog.SetTermsTx(ctx, tx, b.Entity, compactcatalog.VocabGenre, provider, nil)
					} else {
						e = compactcatalog.SetCreditsTx(ctx, tx, b.Entity, provider, nil)
					}
					if e != nil {
						return e
					}
				}
			}
			if err = compactcatalog.TouchEntityTx(ctx, tx, b.Entity); err != nil {
				return err
			}
		}
	}
	relationOnly := b.ItemKind == "episode" && id.Type == "anime"
	fields := screenRecordFields(r, b.Region)
	// Unknown original language withdraws the prior fact even during a
	// fill-missing refresh; it is not an editable descriptive field.
	fields["language"] = r.Language
	if relationOnly {
		fields = map[string]any{"workRelationship": r.Identity, "relations": r.Relations, "ordering": r.Coordinates}
	}
	// Superseded typed facts are retired BEFORE the payload's own writes land,
	// so the projection ends this transaction holding only the accepted
	// payload's facts and never a stale mixture.
	if !relationOnly {
		if err = s.screenSupersedeTypedFacts(ctx, tx, b, fields, superseding, id.Provider); err != nil {
			return err
		}
	}
	if superseding {
		if _, err = tx.ExecContext(ctx, `DELETE FROM screen_metadata_fields WHERE target_kind=? AND target_id=? AND source_kind NOT IN('nfo','filename')`, b.TargetKind, b.Entity); err != nil {
			return err
		}
	}
	b.CreditRecordReference = id.Provider + ":" + id.Type + ":" + id.ID
	for field, value := range fields {
		provider := id.Provider
		fieldBase := b
		fieldBase.Language = r.FieldLanguages[field]
		written, err := s.screenField(ctx, tx, fieldBase, field, value, provider, c.SourceKind, c.SourceURL, c.Confidence, stamp, selection, publication)
		if err != nil {
			return err
		}
		if written {
			if err = s.screenCatalogField(ctx, tx, fieldBase, field, value, provider, c.SourceURL, stamp); err != nil {
				return err
			}
		}
		if credits, ok := value.([]metadataprovider.ScreenCredit); ok && field == "credits" {
			if err = s.screenCreditsBesideLocal(ctx, tx, b, provider, c.SourceKind, c.SourceURL, credits, stamp, publication); err != nil {
				return err
			}
		}
	}
	// A successful accepted empty record is still a current source head.
	// Legacy unproven locks keep their old path; normalized locks capture this
	// evidence while their effective owner sequence remains authoritative.
	if b.TargetKind == "item" && !relationOnly && len(r.Credits) == 0 {
		legacyLocked, e := relationshipClassLocked(ctx, tx, b.TargetID, "credit")
		if e != nil {
			return e
		}
		normal, e := normalizedCreditFamily(ctx, tx, b.Entity)
		if e != nil {
			return e
		}
		if !legacyLocked || normal {
			if e = compactcatalog.SetCreditsFromRecordTx(ctx, tx, b.Entity, id.Provider, b.CreditRecordReference, stamp, nil); e != nil {
				return e
			}
		}
	}
	if b.TargetKind == "item" {
		if !relationOnly {
			n, _ := strconv.ParseInt(id.ID, 10, 64)
			_, err = tx.ExecContext(ctx, `INSERT INTO provider_evidence(item_id,provider,provider_id,payload,observed_at) VALUES(?,?,?,?,?) ON CONFLICT(item_id,provider) DO UPDATE SET provider_id=excluded.provider_id,payload=excluded.payload,observed_at=excluded.observed_at`, b.Entity, id.Provider, n, screenRaw(r), stamp)
			if err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO metadata_details(item_id,provider,provider_id,source_url,observed_at) VALUES(?,?,?,?,?) ON CONFLICT(item_id,provider) DO UPDATE SET provider_id=excluded.provider_id,source_url=excluded.source_url,observed_at=excluded.observed_at`, b.Entity, id.Provider, id.ID, c.SourceURL, stamp)
		if err != nil {
			return err
		}
		if b.ItemKind == "episode" && b.LocalIdentity != "manual" {
			if err = compactcatalog.SetFieldsTx(ctx, tx, b.Entity, compactcatalog.Automatic, map[string]any{"ordering_basis": b.ParentOrder}); err != nil {
				return err
			}
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE screen_metadata_work SET provider=?,provider_type=?,provider_id=?,selection_mode=?,selection_revision=?,accepted_publication=?,episode_order=?,child_cursor='',requested_provider='',query_override='',parent_publication=? WHERE target_kind=? AND target_id=?`, id.Provider, id.Type, id.ID, mode, selection, publication, b.Order, b.ParentPublication, b.TargetKind, b.Entity)
	if err != nil {
		return err
	}
	if b.TargetKind == "show" && id.Provider == "tvdb" {
		// TheTVDB names this show's episodes through its own publication, which
		// leaves an episode alone while another provider's identity is on it.
		if err = screenRetireEpisodeFactsTx(ctx, tx, b.Entity, "tvdb"); err != nil {
			return err
		}
		if err = s.bridgeScreenTVDB(ctx, tx, b, r); err != nil {
			return err
		}
	}
	// The accepted work's similar titles and external ids (screen_links.go).
	if !relationOnly && screenWorkLinked(b) {
		if err = publishScreenLinks(ctx, tx, b.Entity, r); err != nil {
			return err
		}
	}
	// A superseding record without credits leaves none: rebuild either way.
	if b.TargetKind == "show" {
		if err = syncShowPeople(ctx, tx, b.TargetID); err != nil {
			return err
		}
	}
	// Only durable intent: the artwork runner handles consent, cancellation,
	// current accepted identity and any provider IO outside this transaction.
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO artwork_dirty VALUES(?,?)`, b.TargetKind, b.Entity); err != nil {
		return err
	}

	return nil
}
func screenRecordFields(r metadataprovider.ScreenRecord, region string) map[string]any {
	out := map[string]any{}
	if len(r.Skipped) > 0 {
		out["providerIssues"] = r.Skipped
	}
	for k, v := range map[string]string{"title": r.Title, "originalTitle": r.OriginalTitle, "overview": r.Overview, "tagline": r.Tagline, "date": r.Date, "language": r.Language, "status": r.Status, "format": r.Format, "sourceMaterial": r.SourceMaterial, "season": r.Season} {
		if v != "" {
			out[k] = v
		}
	}
	if r.Year > 0 {
		out["year"] = r.Year
	}
	if len(r.Aliases) > 0 {
		out["aliases"] = r.Aliases
	}
	if len(r.Genres) > 0 {
		out["genres"] = r.Genres
	}
	if len(r.Tags) > 0 {
		out["tags"] = r.Tags
	}
	if len(r.Credits) > 0 {
		out["credits"] = r.Credits
	}
	if len(r.Relations) > 0 {
		out["relations"] = r.Relations
	}
	if len(r.Crosswalk) > 0 {
		out["crosswalk"] = r.Crosswalk
	}
	if r.Rating != nil {
		out["rating"] = r.Rating
	}
	if len(r.Certifications) > 0 {
		out["certifications"] = r.Certifications
	}
	// The projected rating is the one the configured region issued. An
	// unconfigured or unclassified region publishes nothing rather than letting
	// another region's certification stand in (PC-METADATA §5.2).
	if certification := screenRegionalCertification(r.Certifications, region); certification != "" {
		out["certification"] = certification
	}
	// Company and network facts ride the typed relations. The scalar studio /
	// network columns and the browse attributes take the leading names; the
	// typed relations stay in the field record for owner detail.
	studios, networks := screenCompanyFacts(r.Relations)
	if len(studios) > 0 {
		out["studios"] = studios
	}
	if len(networks) > 0 {
		out["network"] = networks[0]
	}
	if r.EpisodeCount != nil {
		out["episodeCount"] = *r.EpisodeCount
	}
	if r.AdvisoryRuntimeMinutes != nil {
		out["advisoryRuntimeMinutes"] = *r.AdvisoryRuntimeMinutes
	}
	if r.Adult != nil {
		out["adult"] = *r.Adult
	}
	if r.Coordinates != nil {
		out["ordering"] = r.Coordinates
	}
	// Images are evidence, not selected artwork. No downloader or URL dereference.
	return out
}

// screenRegionalCertification selects the certification the configured region
// issued. The region must match exactly, case-insensitively; anything else is
// unknown and is never substituted with a foreign rating.
func screenRegionalCertification(certifications []metadataprovider.ScreenCertification, region string) string {
	region = strings.TrimSpace(region)
	if region == "" {
		return ""
	}
	for _, c := range certifications {
		if strings.EqualFold(strings.TrimSpace(c.Region), region) && strings.TrimSpace(c.Rating) != "" {
			return strings.TrimSpace(c.Rating)
		}
	}
	return ""
}

// screenCompanyFacts splits the typed relations a screen record carries into
// studio facts (production companies, animation studios) and network facts.
// Both are bounded by the record's relation cap before they get here.
func screenCompanyFacts(relations []metadataprovider.ScreenRelation) (studios, networks []string) {
	for _, r := range relations {
		name := strings.TrimSpace(r.Name)
		if name == "" {
			continue
		}
		switch r.Kind {
		case "production_company", "studio":
			studios = append(studios, name)
		case "network":
			networks = append(networks, name)
		}
	}
	return studios, networks
}
func (s *Service) screenField(ctx context.Context, tx *sql.Tx, b screenBase, field string, value any, provider, sourceKind, url string, confidence float64, stamp string, selection int64, publication string) (bool, error) {
	if field == "language" {
		if provider != "tmdb" && provider != "tvdb" && provider != "anilist" {
			return false, nil
		}
		text, _ := value.(string)
		value = metadatalanguage.Primary(text)
		if value == "" {
			_, err := tx.ExecContext(ctx, `DELETE FROM screen_metadata_fields WHERE target_kind=? AND target_id=? AND field='language'`, b.TargetKind, b.Entity)
			return false, err
		}
	}
	var prior, oldKind string
	err := tx.QueryRowContext(ctx, `SELECT value,source_kind FROM screen_metadata_fields WHERE target_kind=? AND target_id=? AND field=?`, b.TargetKind, b.Entity, field).Scan(&prior, &oldKind)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if field != "language" && err == nil && sourceKind != "nfo" && (oldKind == "nfo" || b.RefreshMode == "fill_missing") {
		return false, nil
	}
	raw := screenRaw(value)
	// Do not mutate canonical descriptions on every read/claim of identical NFO.
	if err == nil && prior == raw && oldKind == sourceKind && sourceKind == "nfo" {
		return false, nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO screen_metadata_fields VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(target_kind,target_id,field) DO UPDATE SET value=excluded.value,provider=excluded.provider,source_kind=excluded.source_kind,source_url=excluded.source_url,confidence=excluded.confidence,observed_at=excluded.observed_at,language=excluded.language,region=excluded.region,selection_revision=excluded.selection_revision,publication_id=excluded.publication_id`, b.TargetKind, b.Entity, field, raw, provider, sourceKind, url, confidence, stamp, b.Language, b.Region, selection, publication)
	return err == nil, err
}
func (s *Service) screenCatalogField(ctx context.Context, tx *sql.Tx, b screenBase, field string, value any, provider, url, stamp string) error {
	if b.TargetKind == "show" && field == "credits" {
		// The show's cast as people (show_people.go).
		if err := syncShowPeople(ctx, tx, b.TargetID); err != nil {
			return err
		}
	}
	fill := b.RefreshMode == "fill_missing" && provider != "nfo"
	if field == "title" || field == "overview" || field == "year" {
		if b.TargetKind == "show" && field == "overview" {
			return nil
		} // normalized field read by show consumers
		if field == "title" {
			fields := map[string]any{"title": value}
			var locked bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM metadata_owner_fields WHERE kind=? AND entity_id=? AND field='metadataLanguage' AND locked=1)`, b.TargetKind, b.Entity).Scan(&locked); err != nil {
				return err
			}
			if !locked {
				fields["metadata_language"] = b.Language
			}
			if fill {
				if cur := screenCurrentText(ctx, tx, b, "title"); cur != "" {
					return nil
				}
			}
			return compactcatalog.SetFieldsTx(ctx, tx, b.Entity, compactcatalog.Automatic, fields)
		}
		column := field
		if field == "overview" {
			column = "overview"
		}
		if fill {
			if field == "year" {
				var y int
				if err := tx.QueryRowContext(ctx, `SELECT year FROM catalog_entities WHERE id=?`, b.Entity).Scan(&y); err != nil {
					return err
				}
				if y != 0 {
					return nil
				}
			} else if cur := screenCurrentText(ctx, tx, b, column); cur != "" {
				return nil
			}
		}
		return compactcatalog.SetFieldsTx(ctx, tx, b.Entity, compactcatalog.Automatic, map[string]any{column: value})
	}
	if column, ok := screenCatalogColumn(b.TargetKind, field); ok {
		return s.screenDescriptiveField(ctx, tx, b, column, value, fill)
	}
	// The typed tag projection is entity-scoped, so a show target reaches it;
	// the item-scoped fact families stay item-only. Nothing here may fall back
	// to reading a show's tags from an item row.
	// A show's genres are its own terms as well, so a Shows grid filters by
	// genre through the term index (derive_facet_counts.go counts them).
	if b.TargetKind != "item" && field != "tags" && !(b.TargetKind == "show" && field == "genres") {
		return nil
	}
	class := map[string]string{"genres": "genre", "credits": "credit"}[field]
	if class != "" {
		locks, err := relationshipLocks(ctx, tx, RepairTarget{b.TargetKind, b.TargetID})
		locked := locks[class]
		if err != nil {
			return err
		}
		if locked && class == "credit" && b.TargetKind == "item" {
			normal, e := normalizedCreditFamily(ctx, tx, b.Entity)
			if e != nil {
				return e
			}
			locked = !normal
		}
		if locked {
			return nil
		}
	}
	if fill {
		var count int
		switch field {
		case "tags":
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM metadata_tags WHERE entity_kind=? AND entity_id=?`, b.TargetKind, b.Entity).Scan(&count); err != nil {
				return err
			}
		case "genres":
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM catalog_term_sources WHERE entity_id=? AND provider=? AND term_id IN(SELECT id FROM catalog_terms WHERE vocab=?)`, b.Entity, provider, int(compactcatalog.VocabGenre)).Scan(&count); err != nil {
				return err
			}
		case "credits":
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM catalog_credits WHERE entity_id=? AND provider=?`, b.Entity, provider).Scan(&count); err != nil {
				return err
			}
		case "rating":
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM metadata_ratings WHERE item_id=? AND provider=?`, b.Entity, provider).Scan(&count); err != nil {
				return err
			}
		default:
			return nil
		}
		if count > 0 {
			return nil
		}
	}
	switch field {
	case "genres":
		genres, ok := value.([]metadataprovider.ScreenName)
		if !ok {
			return errors.New("invalid normalized genres")
		}
		terms := make([]compactcatalog.Term, 0, len(genres))
		for _, g := range genres {
			terms = append(terms, compactcatalog.Term{SourceID: g.ID, Name: g.Name})
		}
		return compactcatalog.SetTermsTx(ctx, tx, b.Entity, compactcatalog.VocabGenre, provider, terms)
	case "credits":
		credits, ok := value.([]metadataprovider.ScreenCredit)
		if !ok {
			return errors.New("invalid normalized credits")
		}
		if b.CreditRecordReference != "" {
			return compactcatalog.SetCreditsFromRecordTx(ctx, tx, b.Entity, provider, b.CreditRecordReference, stamp, screenCatalogCredits(provider, credits))
		}
		return compactcatalog.SetCreditsTx(ctx, tx, b.Entity, provider, screenCatalogCredits(provider, credits))
	case "tags":
		tags, ok := value.([]metadataprovider.ScreenName)
		if !ok {
			return errors.New("invalid normalized tags")
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM metadata_tags WHERE entity_kind=? AND entity_id=? AND provider=?`, b.TargetKind, b.Entity, provider); err != nil {
			return err
		}
		for _, tag := range tags {
			name := strings.TrimSpace(tag.Name)
			if name == "" || len(name) > 200 || len(tag.ID) > 128 || tag.ID == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO metadata_tags VALUES(?,?,?,?,?)`, b.TargetKind, b.Entity, provider, tag.ID, name); err != nil {
				return err
			}
		}
	case "rating":
		r, ok := value.(*metadataprovider.ScreenRating)
		if !ok {
			return errors.New("invalid normalized rating")
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM metadata_ratings WHERE item_id=? AND provider=?`, b.Entity, provider); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO metadata_ratings VALUES(?,?,?,?,?,?,?)`, b.Entity, provider, r.Value, r.Scale, r.Votes, url, stamp); err != nil {
			return err
		}
	}
	return nil
}

// screenCatalogCredits is a provider's credits as catalogue credit rows.
func screenCatalogCredits(provider string, credits []metadataprovider.ScreenCredit) []compactcatalog.Credit {
	out := make([]compactcatalog.Credit, 0, len(credits))
	for i, c := range credits {
		credit := c.ID
		if credit == "" {
			credit = screenDigest([]string{c.Name, c.Role, c.Department})
		}
		credit = credit + ":" + strconv.Itoa(i)
		person := compactcatalog.Credit{
			CreditID:         credit,
			CreditedName:     c.Name,
			PersonName:       c.Name,
			ProviderPersonID: c.ID,
			Role:             c.Role,
			Department:       c.Department,
			Ordinal:          c.Ordinal,
		}
		if c.ID != "" {
			// Every credited person a provider identified is a canonical
			// people link keyed by provider person id, so a guest star is
			// one person everywhere.
			person.PersonKey = provider + ":" + c.ID
		} else if strings.TrimSpace(c.Name) != "" {
			// Someone named only by local metadata still has a page, under
			// the name key a show's credits use; a provider that later names
			// them on the same title makes the two one person.
			person.PersonKey = compactcatalog.LocalPersonKey(c.Name)
		}
		out = append(out, person)
	}
	return out
}

// screenCreditsBesideLocal keeps a local NFO and the library's provider from
// fighting over the credits. The NFO owns the departments it lists (in practice
// the cast: an NFO names actors); the provider supplies the departments it
// leaves out, so a title whose NFO lists ten actors still has its director and
// writers. Nothing happens while the provider owns the credits field itself.
//
// A film's credits are rows per provider, so the provider's rows become exactly
// its share. A show's credits are one field, so the share is the creditsOnline
// field, which the show's people are built from beside credits.
func (s *Service) screenCreditsBesideLocal(ctx context.Context, tx *sql.Tx, b screenBase, provider, sourceKind, url string, credits []metadataprovider.ScreenCredit, stamp, publication string) error {
	if b.TargetKind != "item" && b.TargetKind != "show" {
		return nil
	}
	var local, owner string
	err := tx.QueryRowContext(ctx, `SELECT value,source_kind FROM screen_metadata_fields WHERE target_kind=? AND target_id=? AND field='credits'`, b.TargetKind, b.Entity).Scan(&local, &owner)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err != nil || owner != "nfo" {
		_, err = tx.ExecContext(ctx, `DELETE FROM screen_metadata_fields WHERE target_kind=? AND target_id=? AND field='creditsOnline'`, b.TargetKind, b.Entity)
		return err
	}
	locked, err := relationshipClassLocked(ctx, tx, b.TargetID, "credit")
	if err != nil {
		return err
	}
	if locked && b.TargetKind == "item" {
		normal, e := normalizedCreditFamily(ctx, tx, b.Entity)
		if e != nil {
			return e
		}
		locked = !normal
	}
	if locked {
		return nil
	}
	var listed []metadataprovider.ScreenCredit
	if err = json.Unmarshal([]byte(local), &listed); err != nil {
		return nil // an unreadable local list owns nothing the provider could sit beside
	}
	owned := map[string]bool{}
	for _, c := range listed {
		owned[strings.ToLower(strings.TrimSpace(c.Department))] = true
	}
	if b.TargetKind == "item" {
		// The provider names the people the local list names only by name.
		for _, c := range credits {
			if err = compactcatalog.AdoptLocalPersonTx(ctx, tx, b.Entity, provider, c.ID, c.Name); err != nil {
				return err
			}
		}
	}
	share := make([]metadataprovider.ScreenCredit, 0, len(credits))
	for _, c := range credits {
		if !owned[strings.ToLower(strings.TrimSpace(c.Department))] {
			share = append(share, c)
		}
	}
	if b.TargetKind == "item" {
		if b.CreditRecordReference != "" {
			return compactcatalog.SetCreditsFromRecordTx(ctx, tx, b.Entity, provider, b.CreditRecordReference, stamp, screenCatalogCredits(provider, share))
		}
		return compactcatalog.SetCreditsTx(ctx, tx, b.Entity, provider, screenCatalogCredits(provider, share))
	}
	if len(share) == 0 {
		_, err = tx.ExecContext(ctx, `DELETE FROM screen_metadata_fields WHERE target_kind='show' AND target_id=? AND field='creditsOnline'`, b.Entity)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO screen_metadata_fields VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(target_kind,target_id,field) DO UPDATE SET value=excluded.value,provider=excluded.provider,source_kind=excluded.source_kind,source_url=excluded.source_url,confidence=excluded.confidence,observed_at=excluded.observed_at,language=excluded.language,region=excluded.region,selection_revision=excluded.selection_revision,publication_id=excluded.publication_id`, b.TargetKind, b.Entity, "creditsOnline", screenRaw(share), provider, sourceKind, url, 1, stamp, b.Language, b.Region, b.Selection, publication)
	}
	if err != nil {
		return err
	}
	return syncShowPeople(ctx, tx, b.TargetID)
}

// screenAcceptedCreditsBesideLocal reapplies the split after the local NFO's
// credits changed, from the accepted provider record, so the two never overlap
// until the next refresh.
func (s *Service) screenAcceptedCreditsBesideLocal(ctx context.Context, tx *sql.Tx, b screenBase, stamp string) error {
	if b.Accepted == "" || (b.ItemKind == "episode" && b.ProviderType == "anime") {
		return nil
	}
	var provider, sourceKind, payload string
	err := tx.QueryRowContext(ctx, `SELECT provider,source_kind,payload FROM screen_metadata_publications WHERE id=? AND decision='accepted'`, b.Accepted).Scan(&provider, &sourceKind, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var record metadataprovider.ScreenRecord
	if err = json.Unmarshal([]byte(payload), &record); err != nil {
		return nil
	}
	b.CreditRecordReference = record.Identity.Provider + ":" + record.Identity.Type + ":" + record.Identity.ID
	return s.screenCreditsBesideLocal(ctx, tx, b, provider, sourceKind, "", record.Credits, stamp, b.Accepted)
}

// screenCurrentText reads the catalogue text a fill_missing publication would
// replace. Empty means the field is still missing.
func screenCurrentText(ctx context.Context, tx *sql.Tx, b screenBase, column string) string {
	var v string
	var err error
	switch {
	case column == "title" || column == "sort_title":
		err = tx.QueryRowContext(ctx, `SELECT `+column+` FROM catalog_entities WHERE id=?`, b.Entity).Scan(&v)
	case b.TargetKind == "show" && (column == "original_title" || column == "tagline" || column == "content_rating" || column == "studio" || column == "network"):
		err = tx.QueryRowContext(ctx, `SELECT `+column+` FROM catalog_shows WHERE entity_id=?`, b.Entity).Scan(&v)
	default:
		err = tx.QueryRowContext(ctx, `SELECT `+column+` FROM catalog_item_details WHERE entity_id=?`, b.Entity).Scan(&v)
	}
	if err != nil {
		return ""
	}
	return v
}

// screenRetireEpisodeFactsTx retires what other screen providers published on a
// show's episodes once the show itself belongs to keep. It is the episode half
// of an identity change for a provider whose episodes are not published one by
// one (TheTVDB): identity, details, ratings, typed tags, provider fields, and
// the genres and credits no owner has locked. Local NFO facts, owner edits,
// files and progress stay; an episode the owner placed by hand is left alone.
func screenRetireEpisodeFactsTx(ctx context.Context, tx *sql.Tx, show int64, keep string) error {
	rows, err := tx.QueryContext(ctx, `SELECT e.entity_id,pid(i.public_id) FROM catalog_episodes e JOIN catalog_entities i ON i.id=e.entity_id
 WHERE e.show_id=?1 AND e.local_identity_status<>'manual' AND (
  EXISTS(SELECT 1 FROM provider_evidence x WHERE x.item_id=e.entity_id AND x.provider IN('tmdb','tvdb','anilist') AND x.provider<>?2)
  OR EXISTS(SELECT 1 FROM metadata_details x WHERE x.item_id=e.entity_id AND x.provider IN('tmdb','tvdb','anilist') AND x.provider<>?2)
  OR EXISTS(SELECT 1 FROM catalog_credits x WHERE x.entity_id=e.entity_id AND x.provider IN('tmdb','tvdb','anilist') AND x.provider<>?2))`, show, keep)
	if err != nil {
		return err
	}
	type episode struct {
		id     int64
		public string
	}
	var episodes []episode
	for rows.Next() {
		var e episode
		if err = rows.Scan(&e.id, &e.public); err != nil {
			rows.Close()
			return err
		}
		episodes = append(episodes, e)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, e := range episodes {
		for _, table := range []string{"provider_evidence", "metadata_details", "metadata_ratings"} {
			if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE item_id=? AND provider IN('tmdb','tvdb','anilist') AND provider<>?`, e.id, keep); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM metadata_tags WHERE entity_kind='item' AND entity_id=? AND provider IN('tmdb','tvdb','anilist') AND provider<>?`, e.id, keep); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM screen_metadata_fields WHERE target_kind='item' AND target_id=? AND source_kind NOT IN('nfo','filename')`, e.id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE screen_metadata_publications SET decision='superseded' WHERE target_kind='item' AND target_id=? AND decision='accepted'`, e.id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE screen_metadata_work SET accepted_publication='' WHERE target_kind='item' AND target_id=?`, e.id); err != nil {
			return err
		}
		for _, class := range []string{"genre", "credit"} {
			locked, lockErr := relationshipClassLocked(ctx, tx, e.public, class)
			if lockErr != nil {
				return lockErr
			}
			if locked && class == "credit" {
				normal, e2 := normalizedCreditFamily(ctx, tx, e.id)
				if e2 != nil {
					return e2
				}
				locked = !normal
			}
			if locked {
				continue
			}
			for _, provider := range []string{"tmdb", "tvdb", "anilist"} {
				if provider == keep {
					continue
				}
				if class == "genre" {
					err = compactcatalog.SetTermsTx(ctx, tx, e.id, compactcatalog.VocabGenre, provider, nil)
				} else {
					err = compactcatalog.SetCreditsTx(ctx, tx, e.id, provider, nil)
				}
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *Service) bridgeScreenTVDB(ctx context.Context, tx *sql.Tx, b screenBase, r metadataprovider.ScreenRecord) error {
	id, _ := strconv.ParseInt(r.Identity.ID, 10, 64)
	_, err := tx.ExecContext(ctx, `INSERT INTO tvdb_series_candidates(show_id,provider_id,name,year,overview,payload,observed_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(show_id,provider_id) DO UPDATE SET name=excluded.name,year=excluded.year,overview=excluded.overview,payload=excluded.payload,observed_at=excluded.observed_at`, b.Entity, id, r.Title, r.Year, r.Overview, screenRaw(r), tvdbStamp(s.publicationTime()))
	if err != nil {
		return err
	}
	var oldID, revision int64
	var order string
	if err = tx.QueryRowContext(ctx, `SELECT provider_id,episode_order,revision FROM tvdb_jobs WHERE show_id=?`, b.Entity).Scan(&oldID, &order, &revision); err != nil {
		return err
	}
	if !tvdbOrder(b.Order) {
		return errors.New("TVDB ordering selection required")
	}
	if oldID != id || order != b.Order || b.Accepted == "" {
		return s.selectTVDBTx(tx, b.TargetID, TVDBSelection{ExpectedRevision: revision, ProviderID: id, Order: b.Order, Automatic: b.Mode != "owner"})
	}
	// Existing TVDB source/hierarchy triggers schedule fresh target projections.
	// Explicit refresh also reacquires pages, preserving canonical values until
	// their replacement passes the existing publication fence.
	_, err = tx.ExecContext(ctx, `DELETE FROM tvdb_publication_sets WHERE show_id=?`, b.Entity)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE tvdb_jobs SET status='pending_episodes',page=0,apply_after='',generation=generation+1,revision=revision+1,attempts=0,next_attempt='',error='',lease='',lease_until='' WHERE show_id=?`, b.Entity)
	return err
}

// screenSupersedeTypedFacts keeps the typed tag projection and the regional
// certification scalar projections of the accepted payload instead of letting
// superseded facts linger as current values.
//
//   - An identity change supersedes every provider's typed tags for every
//     supported entity kind (items and shows; shows are not items).
//   - A same-accepted-identity refresh whose payload no longer carries tags
//     supersedes this provider's previously typed tags: an empty keyword/theme
//     list is a fact, not an omission. fill_missing preserves accepted
//     non-empty state instead.
//   - A payload with no certification for the configured region supersedes the
//     provider-derived automatic certification, so an old foreign rating is
//     never served as the current value. NFO/local evidence (source 'nfo') and
//     owner-locked values are preserved, and the browse projection is refreshed
//     in the same transaction.
func (s *Service) screenSupersedeTypedFacts(ctx context.Context, tx *sql.Tx, b screenBase, fields map[string]any, superseding bool, provider string) error {
	switch {
	case superseding:
		if _, err := tx.ExecContext(ctx, `DELETE FROM metadata_tags WHERE entity_kind=? AND entity_id=? AND provider IN('tmdb','tvdb','anilist')`, b.TargetKind, b.Entity); err != nil {
			return err
		}
		// The superseded payload's typed tag evidence goes with it; the
		// accepted payload re-establishes its own rows in the loop below.
		if _, err := tx.ExecContext(ctx, `DELETE FROM screen_metadata_fields WHERE target_kind=? AND target_id=? AND field='tags' AND source_kind NOT IN('nfo','filename')`, b.TargetKind, b.Entity); err != nil {
			return err
		}
	case b.RefreshMode != "fill_missing":
		if _, ok := fields["tags"]; !ok {
			if _, err := tx.ExecContext(ctx, `DELETE FROM metadata_tags WHERE entity_kind=? AND entity_id=? AND provider=?`, b.TargetKind, b.Entity, provider); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM screen_metadata_fields WHERE target_kind=? AND target_id=? AND field='tags' AND provider=? AND source_kind<>'nfo'`, b.TargetKind, b.Entity, provider); err != nil {
				return err
			}
		}
	}
	if _, ok := fields["certification"]; !ok && b.RefreshMode != "fill_missing" {
		var source string
		err := tx.QueryRowContext(ctx, `SELECT source_kind FROM screen_metadata_fields WHERE target_kind=? AND target_id=? AND field='certification'`, b.TargetKind, b.Entity).Scan(&source)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && source != "nfo" {
			// Provider-derived certification: the accepted payload carries none
			// for the configured region, so the obsolete automatic value and
			// its field row go. Owner-locked scalars survive through the write
			// API; NFO evidence is local-first and stays.
			if _, err = tx.ExecContext(ctx, `DELETE FROM screen_metadata_fields WHERE target_kind=? AND target_id=? AND field='certification' AND source_kind<>'nfo'`, b.TargetKind, b.Entity); err != nil {
				return err
			}
			if err = compactcatalog.SetFieldsTx(ctx, tx, b.Entity, compactcatalog.Automatic, map[string]any{"content_rating": ""}); err != nil {
				return err
			}
			if err = syncCatalogAttributes(ctx, tx, RepairTarget{Kind: b.TargetKind, ID: b.TargetID}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) applyScreenLocal(ctx context.Context, tx *sql.Tx, b screenBase) error {
	b.CreditRecordReference = ""
	// Per-field unanimity is required across duplicate sidecars. Conflicts stay
	// evidence; arrival order does not decide an identity or title.
	values := map[string]any{}
	conflict := map[string]bool{}
	put := func(k string, v any) {
		raw := screenRaw(v)
		if old, exists := values[k]; exists && screenRaw(old) != raw {
			conflict[k] = true
		} else {
			values[k] = v
		}
	}
	for _, n := range b.NFO {
		for k, v := range map[string]string{"title": n.Title, "originalTitle": n.OriginalTitle, "sortTitle": n.SortTitle, "overview": n.Overview, "tagline": n.Tagline, "date": n.Date, "edition": n.Edition, "collectionName": n.Collection, "certification": n.Certification} {
			if v != "" {
				put(k, v)
			}
		}
		if len(n.Tags) > 0 {
			tags := []metadataprovider.ScreenName{}
			for _, tag := range n.Tags {
				tags = append(tags, metadataprovider.ScreenName{ID: screenDigest(tag), Name: tag})
			}
			put("tags", tags)
		}
		if n.Rating != nil {
			put("rating", n.Rating)
		}
		if len(n.Countries) > 0 {
			put("countries", n.Countries)
		}
		if n.Status != "" {
			put("status", n.Status)
		}
		if n.Trailer != "" {
			put("trailer", n.Trailer)
		}
		if n.Year > 0 {
			put("year", n.Year)
		}
		if len(n.Genres) > 0 {
			genres := []metadataprovider.ScreenName{}
			for _, name := range n.Genres {
				genres = append(genres, metadataprovider.ScreenName{ID: screenDigest(name), Name: name})
			}
			put("genres", genres)
		}
		if len(n.Credits) > 0 {
			reference := "nfo:" + n.Locator
			if n.Locator != "" && len(reference) <= compactcatalog.CreditSourceRecordReferenceMax && (b.CreditRecordReference == "" || reference < b.CreditRecordReference) {
				b.CreditRecordReference = reference
			}
			put("credits", n.Credits)
		}
		if len(n.Studios) > 0 {
			put("studios", n.Studios)
		}
		if n.AdvisoryRuntimeMinutes != nil {
			put("advisoryRuntimeMinutes", *n.AdvisoryRuntimeMinutes)
		}
	}
	// Remove only ownership for fields no longer supplied. Last-good display
	// values survive until the provider can replace them; owner locks still win.
	rows, err := tx.QueryContext(ctx, `SELECT field FROM screen_metadata_fields WHERE target_kind=? AND target_id=? AND source_kind='nfo'`, b.TargetKind, b.Entity)
	if err != nil {
		return err
	}
	retired := []string{}
	for rows.Next() {
		var field string
		if err = rows.Scan(&field); err != nil {
			rows.Close()
			return err
		}
		if _, ok := values[field]; !ok && field != "localConflicts" {
			retired = append(retired, field)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	retirementLocks, err := relationshipLocks(ctx, tx, RepairTarget{b.TargetKind, b.TargetID})
	if err != nil {
		return err
	}
	for _, field := range retired {
		class := map[string]string{"genres": "genre", "credits": "credit"}[field]
		creditNormalized := false
		if class == "credit" && b.TargetKind == "item" {
			creditNormalized, err = normalizedCreditFamily(ctx, tx, b.Entity)
			if err != nil {
				return err
			}
		}
		if class != "" && retirementLocks[class] && !creditNormalized {
			continue
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM screen_metadata_fields WHERE target_kind=? AND target_id=? AND field=? AND source_kind='nfo'`, b.TargetKind, b.Entity, field); err != nil {
			return err
		}
		if field == "credits" {
			if err = compactcatalog.SetCreditsTx(ctx, tx, b.Entity, "nfo", nil); err != nil {
				return err
			}
		}
		if field == "genres" {
			if err = compactcatalog.SetTermsTx(ctx, tx, b.Entity, compactcatalog.VocabGenre, "nfo", nil); err != nil {
				return err
			}
		}
		if field == "tags" {
			if _, err = tx.ExecContext(ctx, `DELETE FROM metadata_tags WHERE entity_kind=? AND entity_id=? AND provider='nfo'`, b.TargetKind, b.Entity); err != nil {
				return err
			}
		}
	}
	stamp := tvdbStamp(s.publicationTime())
	localChanged := false
	for field, value := range values {
		if conflict[field] {
			continue
		}
		written, err := s.screenField(ctx, tx, b, field, value, "nfo", "nfo", "", 1, stamp, b.Selection, "")
		if err != nil {
			return err
		}
		creditReferenceChanged := false
		if field == "credits" && b.CreditRecordReference != "" {
			head, e := compactcatalog.ReadCreditSourceHead(ctx, tx, b.Entity, "nfo")
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			creditReferenceChanged = errors.Is(e, sql.ErrNoRows) || head.RecordReference != b.CreditRecordReference
		}
		if written || creditReferenceChanged {
			localChanged = localChanged || written
			if err = s.screenCatalogField(ctx, tx, b, field, value, "nfo", "", stamp); err != nil {
				return err
			}
			if field == "credits" {
				if err = s.screenAcceptedCreditsBesideLocal(ctx, tx, b, stamp); err != nil {
					return err
				}
			}
		}
	}
	if localChanged && b.TargetKind == "item" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO metadata_details(item_id,provider,provider_id,source_url,observed_at) VALUES(?,'nfo','local','',?) ON CONFLICT(item_id,provider) DO UPDATE SET observed_at=excluded.observed_at`, b.Entity, stamp); err != nil {
			return err
		}
	}
	if len(conflict) == 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM screen_metadata_fields WHERE target_kind=? AND target_id=? AND field='localConflicts'`, b.TargetKind, b.Entity); err != nil {
			return err
		}
	}
	if len(conflict) > 0 {
		names := []string{}
		for k := range conflict {
			names = append(names, k)
		}
		_, err := s.screenField(ctx, tx, b, "localConflicts", names, "nfo", "nfo", "", 1, stamp, b.Selection, "")
		if err != nil {
			return err
		}
	}
	editions := []string{}
	seen := map[string]bool{}
	for _, e := range b.Editions {
		if e.Label != "" && !seen[e.Label] {
			editions = append(editions, e.Label)
			seen[e.Label] = true
		}
	}
	for _, name := range append([]string{b.Title}, b.Names...) {
		_, _, edition := screenFilename(name)
		if edition != "" && !seen[edition] {
			editions = append(editions, edition)
			seen[edition] = true
		}
	}
	if len(editions) == 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM screen_metadata_fields WHERE target_kind=? AND target_id=? AND field='filenameEditions' AND source_kind='filename'`, b.TargetKind, b.Entity); err != nil {
			return err
		}
	}
	if len(editions) > 0 {
		_, err := s.screenField(ctx, tx, b, "filenameEditions", editions, "local", "filename", "", 1, stamp, b.Selection, "")
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) screenEpisodeStep(ctx context.Context, p *screenClaim) error {
	b := p.Base
	if b.LocalIdentity == "manual" {
		return s.finishScreen(ctx, p, "manual_preserved", "")
	}
	if b.IdentityLocked && b.Mode != "owner" {
		return s.finishScreen(ctx, p, "manual_preserved", "")
	}
	// No parent identity: never matched, or back to unmatched after the library's
	// provider changed (its last publication is kept for display only).
	if b.ParentPublication == "" || b.ParentProvider == "" {
		return s.finishScreen(ctx, p, "needs_parent_match", "")
	}
	if b.ParentProvider == "tvdb" {
		return s.finishScreen(ctx, p, "delegated_tvdb", "")
	}
	requestCtx, cancel := s.screenRequestContext(ctx, p)
	defer cancel()
	if err := s.screenBeforeRequest(requestCtx, p, b.ParentProvider); err != nil {
		return s.failScreen(ctx, p, err)
	}
	var record metadataprovider.ScreenRecord
	var err error
	source, status := "parent_coordinates", "matched"
	if b.ParentProvider == "tmdb" {
		if b.Numbering != "seasonal" && b.Numbering != "absolute" {
			return s.finishScreen(ctx, p, "needs_order", "tmdb_seasonal_coordinates_required")
		}
		provider, ok := s.screenProviders["tmdb"].(screenEpisodeProvider)
		if !ok {
			return s.failScreen(ctx, p, &metadataprovider.Error{Provider: "tmdb", Code: "provider_not_configured"})
		}
		record, err = s.screenEpisodeRecord(requestCtx, p, provider)
	} else if b.ParentProvider == "anilist" {
		id := b.AnimeSeasonID
		if b.Numbering == "absolute" {
			id = b.ParentProviderID
		} else if id == "" {
			var count int
			err = s.db.QueryRowContext(requestCtx, `SELECT count(*) FROM catalog_seasons WHERE show_id=?`, b.ParentEntity).Scan(&count)
			if err != nil {
				return s.failScreen(ctx, p, err)
			}
			if count != 1 || b.Season != 1 {
				return s.finishScreen(ctx, p, "needs_season_mapping", "choose_anilist_work_for_local_season")
			}
			id = b.ParentProviderID
		}
		parent, e := s.screenProviders["anilist"].ScreenDetails(requestCtx, "anime", id, b.Language, b.Region)
		err = e
		if err == nil {
			if parent.EpisodeCount != nil && b.Number > *parent.EpisodeCount {
				return s.finishScreen(ctx, p, "needs_season_mapping", "episode_outside_anilist_work")
			}
			coord := &metadataprovider.ScreenCoordinates{ShowID: id, Episode: b.Number, Order: b.ParentOrder}
			if b.Numbering == "absolute" {
				n := b.Number
				coord.Absolute = &n
			} else {
				n := b.Season
				coord.Season = &n
			}
			record = metadataprovider.ScreenRecord{Identity: parent.Identity, Title: b.Title, Coordinates: coord, Relations: []metadataprovider.ScreenRelation{{Kind: "part_of", Target: parent.Identity, Name: parent.Title}}}
			source = "parent_work"
			status = "matched_work"
		}
	} else {
		return s.finishScreen(ctx, p, "needs_parent_match", "")
	}
	if err != nil {
		var providerError *metadataprovider.Error
		if b.Numbering == "absolute" && errors.As(err, &providerError) {
			switch providerError.Code {
			case "order_unavailable", "order_not_in_series", "invalid_episode_order", "episode_outside_order":
				return s.finishScreen(ctx, p, "needs_order", providerError.Code)
			}
		}
		return s.failScreen(ctx, p, err)
	}
	if err = metadataprovider.ValidateScreenRecord(record); err != nil {
		return s.failScreen(ctx, p, err)
	}
	// Never silently replace an existing episode identity, including a migrated
	// one, when the series itself has not been explicitly re-identified.
	for _, old := range b.Existing {
		if b.Mode == "inherited" && b.ProviderID == "" && !b.IdentityLocked {
			continue
		}
		if old.Provider == record.Identity.Provider && old != record.Identity {
			return s.finishScreen(ctx, p, "identity_conflict", "accepted_episode_identity_preserved")
		}
	}
	for _, n := range b.NFO {
		for _, id := range n.IDs {
			if id.Provider == record.Identity.Provider && id.Type == "episode" && id != record.Identity {
				return s.finishScreen(ctx, p, "identity_conflict", "nfo_episode_identity_conflicts")
			}
		}
	}
	c := scoreScreen(record, nil, 0, true)
	c.SourceKind = source
	c.Reasons = []string{"accepted_parent_and_explicit_local_coordinates"}
	if record.Identity.Type == "anime" {
		c.Reasons = []string{"accepted_anilist_work_relationship", "episode_title_not_supplied_by_provider"}
	}
	// Work identity comes from the accepted parent; a prior inherited work may be
	// superseded only through that parent's explicit selection (dispatch below).
	if b.ProviderID != "" && (b.Provider != record.Identity.Provider || b.ProviderID != record.Identity.ID) {
		return s.finishScreen(ctx, p, "identity_conflict", "accepted_episode_identity_preserved")
	}
	return s.publishScreen(ctx, p, []screenCandidate{c}, 0, status)
}

func screenExplain(status string) string {
	switch status {
	case "needs_consent":
		return "Remote metadata is waiting for the owner’s provider disclosure confirmation."
	case "needs_selection":
		return "Candidates are ambiguous or below the automatic confidence/margin threshold."
	case "unmatched":
		return "No compatible provider candidate was found."
	case "source_unavailable":
		return "Source observations are unavailable; existing metadata is preserved."
	case "matched_work":
		return "Linked to an AniList work. AniList does not supply an episode identity or episode title."
	case "needs_season_mapping":
		return "Choose an AniList work for this local season; local episode numbers will not change."
	case "identity_conflict":
		return "An accepted identity or local identifier conflicts; an owner decision is required."
	case "provider_disabled":
		return "The selected provider is disabled in this library’s metadata settings."
	case "unavailable":
		return "The provider could not supply validated evidence. Existing metadata is preserved."
	case "delegated_tvdb":
		return "Episode matching uses the selected TVDB ordering and its paged publication worker."
	case "manual_preserved":
		return "The owner’s local episode assignment was preserved."
	case "needs_parent_match":
		return "Identify the parent show before matching this episode."
	case "needs_order":
		return "The local numbering does not resolve in the selected provider order."
	}
	return strings.ReplaceAll(status, "_", " ")
}

// screenCatalogColumn maps a published screen fact to the editable column that
// carries it. The editor, the browse projection and the provider publication
// then agree on one value per fact instead of three private copies.
func screenCatalogColumn(targetKind, field string) (string, bool) {
	shared := map[string]string{"originalTitle": "original_title", "sortTitle": "sort_title", "tagline": "tagline", "certification": "content_rating", "studios": "studio", "network": "network", "countries": "country"}
	if column, ok := shared[field]; ok {
		return column, true
	}
	if targetKind == "item" {
		switch field {
		case "date":
			return "release_date", true
		case "edition":
			return "edition", true
		}
	}
	return "", false
}

// screenDescriptiveText reduces a published screen value to the single text the
// column stores. A list contributes its first entry; anything else is ignored.
func screenDescriptiveText(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case []string:
		if len(v) > 0 {
			return strings.TrimSpace(v[0])
		}
	}
	return ""
}

func (s *Service) screenDescriptiveField(ctx context.Context, tx *sql.Tx, b screenBase, column string, value any, fill bool) error {
	text := screenDescriptiveText(value)
	spec := RepairFieldSpec{Field: column, Type: "text", MaxLength: textLimit}
	if column == "release_date" {
		spec.Type = "date"
	}
	if text == "" || !validFieldValue("item", spec, text) {
		return nil
	}
	if fill {
		if cur := screenCurrentText(ctx, tx, b, column); cur != "" {
			return nil
		}
	}
	// Identifiers are a closed set from screenCatalogColumn, never owner input.
	if err := compactcatalog.SetFieldsTx(ctx, tx, b.Entity, compactcatalog.Automatic, map[string]any{column: text}); err != nil {
		return err
	}
	return syncCatalogAttributes(ctx, tx, RepairTarget{Kind: b.TargetKind, ID: b.TargetID})
}
