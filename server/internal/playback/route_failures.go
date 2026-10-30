package playback

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// A capability document can be wrong: a browser says it decodes a level it then
// chokes on, a television firmware lies about a codec. When a device reports
// that the route it was given failed in the engine (not on the network), the
// server remembers it for that file on that sign-in and plans the next route up.
// A wrong claim therefore costs one retry, not the title.

// routeFailureTTL bounds how long a report keeps a route closed. It is long
// enough to cover the rest of a viewing and short enough that a fixed client,
// driver or file is tried again without anyone clearing anything.
//
// That is for a report that says what was wrong: the engine could not decode
// the picture or the sound, or could not read the source. A report that only
// says something went wrong (a generic engine error, a stall) may be a passing
// glitch, so it closes the route for an evening, not a week.
const (
	routeFailureTTL          = 7 * 24 * time.Hour
	uncertainRouteFailureTTL = 6 * time.Hour
)

// certainRouteFailures are the codes that name a fault of the stream itself.
const certainRouteFailures = `'decode_error','source_not_supported','audio_decode_error','manifest_incompatible'`

var routeFailureCode = regexp.MustCompile(`^[a-z0-9_]{1,48}$`)

var ErrRouteFailure = errors.New("That playback failure report is not valid.")

// RouteFailureResult tells the device what a failure report changed.
type RouteFailureResult struct {
	Route string `json:"route"`
	// Engine is the player engine the failed route was planned for.
	Engine string `json:"engine"`
	// Escalates is false when the failed route was already the last resort, so
	// the device knows that asking again will plan the same thing.
	Escalates bool `json:"escalates"`
}

// RecordRouteFailure records that the route of one presentation failed in the
// device's engine: code is the engine's classification (decode_error,
// source_not_supported, audio_decode_error, stalled_without_data and so on),
// recorded and not interpreted. media is the presentation's media session,
// whose ownership the caller (the v1 session layer) has already decided. The
// failure is recorded for the caller's own sign-in and for the engine the
// presentation was planned for, which is what the next plan reads. It never
// touches the presentation itself.
func (s *Service) RecordRouteFailure(ctx context.Context, p identity.Principal, media, code, detail string) (RouteFailureResult, error) {
	var out RouteFailureResult
	if !validControlID(media) || !routeFailureCode.MatchString(code) || len(detail) > 512 || p.Hash == "" {
		return out, ErrRouteFailure
	}
	var asset, strategy, engine string
	err := s.db.QueryRowContext(ctx, `SELECT asset_id,COALESCE((SELECT d.strategy FROM playback_delivery_plans d WHERE d.session_id=playback_sessions.id),''),COALESCE((SELECT json_extract(d.policy_json,'$.engine') FROM playback_delivery_plans d WHERE d.session_id=playback_sessions.id AND d.policy_json<>''),'') FROM playback_sessions WHERE id=? AND account_id=? AND profile_id=?`, media, p.AccountID, p.ProfileID).Scan(&asset, &strategy, &engine)
	if errors.Is(err, sql.ErrNoRows) {
		// Not this viewer's presentation, ended, or unknown: hidden, and never a
		// 401 (a valid sign-in must not be sent to refresh its token).
		return out, identity.ErrNotVisible
	}
	if err != nil {
		return out, err
	}
	if engine != EngineMPV {
		engine = EngineNative
	}
	out.Route, out.Engine = strategy, engine
	switch DeliveryStrategy(strategy) {
	case DeliveryOriginal, DeliveryCopyRemux, DeliveryAudioConversion:
		out.Escalates = true
	case DeliveryVideoConversion:
		// No further conversion route exists, but its failure remains visible.
	default:
		return out, ErrRouteFailure
	}
	_, err = dbwork.ExecWrite(ctx, s.db, dbwork.ClassEstablishedPlayback, `INSERT INTO playback_route_failures(session_hash,asset_id,engine,route,code,detail,reported_at_ms) VALUES(?,?,?,?,?,?,?) ON CONFLICT(session_hash,asset_id,engine,route) DO UPDATE SET code=excluded.code,detail=excluded.detail,reported_at_ms=excluded.reported_at_ms`, p.Hash, asset, engine, strategy, code, publicAudioText(detail, 200), time.Now().UnixMilli())
	return out, err
}

// routeFailuresTx reads the routes this device has reported broken for one
// file, per player engine: a route that failed in one engine says nothing
// about the other.
func routeFailuresTx(ctx context.Context, tx *sql.Tx, p identity.Principal, asset string) (map[string]map[DeliveryStrategy]RouteRejection, error) {
	if p.Hash == "" {
		return nil, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT engine,route,code FROM (SELECT engine,route,code,ROW_NUMBER() OVER(PARTITION BY engine,route ORDER BY reported_at_ms DESC) AS rank FROM playback_route_failures WHERE (session_hash=? OR session_hash IN(SELECT original.token_hash FROM authorization_family_tokens original JOIN authorization_family_tokens caller ON caller.family_id=original.family_id WHERE caller.token_hash=?)) AND asset_id=? AND route<>'video_conversion' AND reported_at_ms>CASE WHEN code IN(`+certainRouteFailures+`) THEN ? ELSE ? END) WHERE rank=1 LIMIT 8`, p.Hash, p.Hash, asset, time.Now().Add(-routeFailureTTL).UnixMilli(), time.Now().Add(-uncertainRouteFailureTTL).UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out map[string]map[DeliveryStrategy]RouteRejection
	for rows.Next() {
		var engine, route, code string
		if err = rows.Scan(&engine, &route, &code); err != nil {
			return nil, err
		}
		if out == nil {
			out = map[string]map[DeliveryStrategy]RouteRejection{}
		}
		if out[engine] == nil {
			out[engine] = map[DeliveryStrategy]RouteRejection{}
		}
		out[engine][DeliveryStrategy(route)] = RouteRejection{Code: code}
	}
	return out, rows.Err()
}

// RememberedRouteFailure contains no credential or session hash.
type RememberedRouteFailure struct {
	SourceID     string `json:"sourceId"`
	Engine       string `json:"engine"`
	Route        string `json:"route"`
	Code         string `json:"code"`
	Detail       string `json:"detail"`
	ReportedAtMS int64  `json:"reportedAtMs"`
	ExpiresAtMS  int64  `json:"expiresAtMs"`
	// ItemID and Title name the title the file belongs to, so the row can say
	// what failed and link to it; absent once the file has left the catalogue.
	ItemID string `json:"itemId,omitempty"`
	Title  string `json:"title,omitempty"`
	// Kind is the title's kind (movie, episode, track, ...), for its link.
	Kind string `json:"kind,omitempty"`
}
type RouteFailurePage struct {
	Items []RememberedRouteFailure `json:"items"`
	More  bool                     `json:"more"`
}

// RouteFailureMemory is a bounded owner diagnostic of active failure memory.
// HTTP admission supplies owner authorization; no viewer credential is exposed.
func (s *Service) RouteFailureMemory(ctx context.Context) (RouteFailurePage, error) {
	out := RouteFailurePage{Items: []RememberedRouteFailure{}}
	// The title is the file's first linked entity, found per row of a page of
	// at most 101 through the asset token and link indexes.
	rows, err := dbwork.Query(ctx, s.db, `SELECT f.asset_id,f.engine,f.route,f.code,f.detail,f.reported_at_ms,f.reported_at_ms+CASE WHEN f.code IN(`+certainRouteFailures+`) THEN ? ELSE ? END,COALESCE(pid(e.public_id),''),COALESCE(e.title,''),COALESCE(e.kind,0)
 FROM playback_route_failures f LEFT JOIN catalog_entities e ON e.id=(SELECT l.entity_id FROM catalog_assets a JOIN catalog_asset_links l INDEXED BY catalog_asset_links_asset ON l.asset_id=a.id WHERE a.token=f.asset_id ORDER BY l.entity_id LIMIT 1)
 WHERE f.reported_at_ms>CASE WHEN f.code IN(`+certainRouteFailures+`) THEN ? ELSE ? END ORDER BY f.reported_at_ms DESC,f.asset_id,f.engine,f.route LIMIT 101`, routeFailureTTL.Milliseconds(), uncertainRouteFailureTTL.Milliseconds(), time.Now().Add(-routeFailureTTL).UnixMilli(), time.Now().Add(-uncertainRouteFailureTTL).UnixMilli())
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var v RememberedRouteFailure
		var kind compactcatalog.Kind
		if err = rows.Scan(&v.SourceID, &v.Engine, &v.Route, &v.Code, &v.Detail, &v.ReportedAtMS, &v.ExpiresAtMS, &v.ItemID, &v.Title, &kind); err != nil {
			return out, err
		}
		if v.ItemID != "" {
			v.Kind, _ = kind.Name()
		}
		if len(out.Items) == 100 {
			out.More = true
			break
		}
		out.Items = append(out.Items, v)
	}
	return out, rows.Err()
}

// ClearRouteFailureMemory lets an owner retry a file after a client or driver fix.
func (s *Service) ClearRouteFailureMemory(ctx context.Context, source string) error {
	if !validControlID(source) {
		return ErrRouteFailure
	}
	_, err := dbwork.ExecWrite(ctx, s.db, dbwork.ClassInteractive, `DELETE FROM playback_route_failures WHERE asset_id=?`, source)
	return err
}
