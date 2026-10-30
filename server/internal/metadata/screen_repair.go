package metadata

// The generic correction UI and provider review UI write one screen identity
// intent. Acquisition never happens in the interactive transaction.
import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

func repairScreenIdentity(ctx context.Context, tx *sql.Tx, t RepairTarget) (*RepairIdentity, error) {
	if t.Kind != "item" && t.Kind != "show" {
		return nil, nil
	}
	out := &RepairIdentity{Engine: "screen"}
	var providers string
	entity, err := resolveEntity(ctx, tx, t.ID)
	if err != nil {
		return nil, err
	}
	err = tx.QueryRowContext(ctx, `SELECT w.provider,w.provider_type,w.provider_id,w.episode_order,w.revision,w.status,p.providers FROM screen_metadata_work w JOIN screen_metadata_policies p ON p.library_id=w.library_id WHERE w.target_kind=? AND w.target_id=?`, t.Kind, entity).Scan(&out.Provider, &out.Type, &out.ID, &out.Order, &out.Revision, &out.Status, &providers)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// An unmatched item still needs a provider choice for Search, not an invented
	// accepted ID. The specific review can choose any currently enabled provider.
	if out.Provider == "" {
		var ps []string
		if err = json.Unmarshal([]byte(providers), &ps); err != nil {
			return nil, err
		}
		if len(ps) > 0 {
			out.Provider = ps[0]
		}
	}
	out.Locked, err = repairIdentityLocked(ctx, tx, t)
	return out, err
}

func repairScreenCandidates(ctx context.Context, tx *sql.Tx, t RepairTarget) ([]RepairCandidate, error) {
	out := []RepairCandidate{}
	previews, err := candidatePreviews(ctx, tx, t)
	if err != nil {
		return out, err
	}
	entity, err := resolveEntity(ctx, tx, t.ID)
	if err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT candidate_key,provider,payload,observed_at FROM screen_metadata_candidates WHERE target_kind=? AND target_id=? ORDER BY confidence DESC,candidate_key LIMIT 50`, t.Kind, entity)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var c RepairCandidate
		var raw string
		var r metadataprovider.ScreenRecord
		if err = rows.Scan(&c.ID, &c.Provider, &raw, &c.Observed); err != nil {
			return out, err
		}
		if err = json.Unmarshal([]byte(raw), &r); err != nil {
			return out, err
		}
		c.IdentityID = r.Identity.ID
		c.IdentityType = r.Identity.Type
		c.Title = r.Title
		c.Year = r.Year
		c.Subtitle = fmt.Sprintf("%s · %s", r.Identity.Type, r.Identity.ID)
		if r.Year > 0 {
			c.Subtitle = fmt.Sprintf("%d · %s", r.Year, c.Subtitle)
		}
		c.PreviewURL = previews[c.Provider+"\x00"+r.PosterPath]
		if t.Kind == "show" {
			c.Orders = screenRecordOrders(r)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func setScreenOwnerIntent(ctx context.Context, tx *sql.Tx, b screenBase, id metadataprovider.ScreenID, order string, actor MBActor, restoring bool) error {
	if !metadataprovider.ValidScreenID(id) || len(order) > 160 {
		return ErrRepairInput
	}
	if !restoring && (!b.Confirmed || !b.Enabled || !screenProviderEnabled(b, id.Provider)) {
		return ErrRepairInput
	}
	if b.TargetKind == "item" && b.ItemKind != "episode" && id.Type != "movie" {
		return ErrRepairInput
	}
	if b.TargetKind == "show" && id.Type != "show" && id.Type != "anime" {
		return ErrRepairInput
	}
	if err := retireScreenLinks(ctx, tx, b.TargetKind, b.Entity, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE screen_metadata_work SET provider=?,provider_type=?,provider_id=?,episode_order=?,selection_mode='owner',selection_revision=selection_revision+1,generation=generation+1,revision=revision+1,status='pending',requested_provider='',query_override='',year_override=0,lease='',lease_until='',attempts=0,next_attempt='',error='' WHERE target_kind=? AND target_id=?`, id.Provider, id.Type, id.ID, order, b.TargetKind, b.Entity); err != nil {
		return err
	}
	if b.TargetKind == "show" {
		if err := screenSupersedeChildren(ctx, tx, b.TargetID); err != nil {
			return err
		}
		if id.Provider != b.Provider || id.ID != b.ProviderID {
			if _, err := tx.ExecContext(ctx, `DELETE FROM screen_anime_seasons WHERE show_id=?`, b.Entity); err != nil {
				return err
			}
		}
		// The existing paged TVDB worker may already be holding a lease. Its
		// generation fence must change even when the new provider is not TVDB.
		if _, err := tx.ExecContext(ctx, `UPDATE tvdb_jobs SET revision=revision+1,generation=generation+1,lease='',lease_until='' WHERE show_id=?`, b.Entity); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tvdb_publication_operations SET status='stale',reason='owner_selection',finished_at=? WHERE show_id=? AND status IN('claimed','ready')`, time.Now().UTC().Format(time.RFC3339Nano), b.Entity); err != nil {
			return err
		}
	}
	who := actor.Authority + ":" + actor.AccountID + ":" + actor.ProfileID
	return setRelationshipLock(ctx, tx, RepairTarget{b.TargetKind, b.TargetID}, "identity", true, who, time.Now().UTC().Format(time.RFC3339Nano))
}

func queueScreenRepair(ctx context.Context, tx *sql.Tx, t RepairTarget, m RepairCommand) error {
	b, err := readScreenBase(ctx, tx, t.Kind, t.ID)
	if err != nil {
		return err
	}
	if m.Action == "search" {
		if b.ItemKind == "episode" {
			return requestinput.New("identify the parent show; episode assignment is separate")
		}
		provider := m.Provider
		if provider == "" {
			provider = b.Provider
		}
		if provider == "" && len(b.Providers) > 0 {
			provider = b.Providers[0]
		}
		if !b.Confirmed || !b.Enabled || !screenProviderEnabled(b, provider) {
			return requestinput.New("confirm and enable the selected provider first")
		}
		if len(m.Query) > 512 || !screenSearchSafe(m.Query) || m.Query != "" && strings.TrimSpace(m.Query) == "" {
			return ErrRepairInput
		}
		// A supplied year narrows this search only. It is an interactive override,
		// never a replacement for the scanned seed.
		if m.Year < 0 || m.Year > 9999 {
			return ErrRepairInput
		}
		_, err = tx.ExecContext(ctx, `UPDATE screen_metadata_work SET requested_provider=?,query_override=?,year_override=?,status='searching',generation=generation+1,revision=revision+1,lease='',lease_until='',attempts=0,next_attempt='',error='' WHERE target_kind=? AND target_id=?`, provider, strings.TrimSpace(m.Query), m.Year, t.Kind, b.Entity)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE screen_metadata_work SET requested_provider='',query_override='',year_override=0,status='pending',generation=generation+1,revision=revision+1,lease='',lease_until='',attempts=0,next_attempt='',error='' WHERE target_kind=? AND target_id=?`, t.Kind, b.Entity)
	}
	return err
}

func restoreScreenIdentity(ctx context.Context, tx *sql.Tx, t RepairTarget, saved *RepairIdentity, actor MBActor) error {
	b, err := readScreenBase(ctx, tx, t.Kind, t.ID)
	if err != nil {
		return err
	}
	if saved.ID != "" && saved.ID != "0" {
		typ := saved.Type
		if typ == "" {
			typ = b.ItemKind
			if saved.Provider == "anilist" {
				typ = "anime"
			}
		}
		return setScreenOwnerIntent(ctx, tx, b, metadataprovider.ScreenID{Provider: saved.Provider, Type: typ, ID: saved.ID}, saved.Order, actor, true)
	}
	// Explicit undo to an unmatched identity is not automatic rematching. Keep
	// canonical media, local observations and owner corrections; retire only the
	// accepted remote identity/field evidence. Existing artwork remains selected.
	if _, err = tx.ExecContext(ctx, `UPDATE screen_metadata_publications SET decision='superseded' WHERE id=?`, b.Accepted); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM screen_metadata_fields WHERE target_kind=? AND target_id=? AND source_kind NOT IN('nfo','filename')`, t.Kind, b.Entity); err != nil {
		return err
	}
	if err = clearScreenLinks(ctx, tx, b.Entity); err != nil {
		return err
	}
	if t.Kind == "show" {
		if err = syncShowPeople(ctx, tx, t.ID); err != nil {
			return err
		}
	}
	if t.Kind == "item" {
		for _, table := range []string{"provider_evidence", "metadata_details", "metadata_ratings"} {
			if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE item_id=? AND provider IN('tmdb','tvdb','anilist')`, b.Entity); err != nil {
				return err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE screen_metadata_work SET provider='',provider_type='',provider_id='',accepted_publication='',selection_mode='owner',selection_revision=selection_revision+1,generation=generation+1,revision=revision+1,status='manual_preserved',requested_provider='',query_override='',lease='',lease_until='',attempts=0,next_attempt='',error='' WHERE target_kind=? AND target_id=?`, t.Kind, b.Entity); err != nil {
		return err
	}
	if t.Kind == "show" {
		if _, err = tx.ExecContext(ctx, `UPDATE tvdb_jobs SET provider_id=0,status='needs_selection',revision=revision+1,generation=generation+1,lease='',lease_until='' WHERE show_id=?`, b.Entity); err != nil {
			return err
		}
		if err = screenSupersedeChildren(ctx, tx, t.ID); err != nil {
			return err
		}
	}
	return nil
}
