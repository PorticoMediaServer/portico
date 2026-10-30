package httpapi

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// v1Presentation is the media session that presents a Playback v1 session.
//
// A v1 client knows only its v1 session id (ps_…) and the presentation
// generation. The offers, chapters and subtitle-plan routes read the media
// session that the v1 session's current presentation runs on (its row in
// playback_sessions, keyed by media_session_id). Those routes take the v1 id,
// resolve it here for the session's owner, and answer in the v1 id and
// generation, so a v1 client never sees or needs a media session id.
type v1Presentation struct {
	session    string // the v1 session id the client sent
	media      string // playback_sessions.id of its current presentation
	generation int    // the v1 presentation generation the client holds
	bound      string // the media session's token binding (session_hash)
}

// as is the caller as the media session's own reads know it. Ownership was
// decided by the v1 rule (resolveV1Presentation); the media session keeps the
// token binding its last write set (a v1 timeline report rebinds it to the
// reporting token, legacy Progress), so a read under a renewed token reaches
// the presentation without writing anything (NEW-38).
func (v v1Presentation) as(p identity.Principal) identity.Principal {
	if v.bound != "" {
		p.Hash = v.bound
	}
	return p
}

// resolveV1Presentation answers ok=false when id isn't a live v1 session of
// this viewer (the route then treats it as a legacy media session id, which
// answers not found for anything else). Ownership is the v1 rule: the same
// authority, account and profile. It only reads: it never takes the write
// gate, so the player's reads never wait for a background write.
func (d Dependencies) resolveV1Presentation(ctx context.Context, p identity.Principal, id string) (v1Presentation, bool, error) {
	if d.DB == nil || id == "" {
		return v1Presentation{}, false, nil
	}
	var out v1Presentation
	var bound sql.NullString
	err := dbwork.ReadHandle(ctx, d.DB).QueryRowContext(ctx, `SELECT v.media_session_id,v.generation,s.session_hash FROM playback_v1_sessions v LEFT JOIN playback_sessions s ON s.id=v.media_session_id AND s.account_id=v.account_id AND s.profile_id=v.profile_id WHERE v.id=? AND v.authority=? AND v.account_id=? AND v.profile_id=? AND v.ended_ms=0 AND COALESCE(v.media_session_id,'')<>''`,
		id, p.Authority, p.AccountID, p.ProfileID).Scan(&out.media, &out.generation, &bound)
	if errors.Is(err, sql.ErrNoRows) {
		return v1Presentation{}, false, nil
	}
	if err != nil {
		return v1Presentation{}, false, err
	}
	out.session, out.bound = id, bound.String
	return out, true, nil
}

// mediaGeneration is the media session's own generation, which the legacy
// subtitle selection fences on.
func (d Dependencies) mediaGeneration(ctx context.Context, media string) (int, error) {
	var generation int
	err := d.DB.QueryRowContext(ctx, `SELECT generation FROM playback_sessions WHERE id=?`, media).Scan(&generation)
	return generation, err
}
