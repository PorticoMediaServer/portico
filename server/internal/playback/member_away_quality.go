package playback

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/streamlimits"
)

// memberAwayCeilingsTx reads the account's normalized video ceiling in the
// transaction that accepts the plan. Source locality (including STRM) is not
// connection reach. A home presentation does not acquire an away ceiling.
func memberAwayCeilingsTx(ctx context.Context, tx *sql.Tx, account string, remote bool) (ReplanCeilings, error) {
	out := ReplanCeilings{Remote: remote}
	if !remote {
		return out, nil
	}
	limits, err := streamlimits.ReadTx(ctx, tx, account)
	if err != nil {
		return out, err
	}
	if limits.AwayQuality == streamlimits.AwayQualityServer {
		return out, nil
	}
	preset, ok := LadderPreset(string(limits.AwayQuality))
	if !ok {
		return out, ErrQualityUnavailable
	}
	out.MaxVideoBitrateBPS, out.MaxVideoHeight = preset.VideoBitrateBPS, preset.Height
	return out, nil
}

func narrowMemberTarget(target QualityTarget, ceiling ReplanCeilings) QualityTarget {
	target.MaxVideoBitrateBPS = narrowCeiling(target.MaxVideoBitrateBPS, ceiling.MaxVideoBitrateBPS)
	target.MaxVideoHeight = narrowCeiling(target.MaxVideoHeight, ceiling.MaxVideoHeight)
	return target
}

// A channel narrows the admitted decoder ceilings for each actual producer.
// Audio capabilities and the independent music policy stay intact.
func memberChannelProfile(profile ClientProfile, ceiling ReplanCeilings) ClientProfile {
	profile = WithVideoBitrateCeiling(profile, ceiling.MaxVideoBitrateBPS)
	if ceiling.MaxVideoHeight > 0 {
		profile.Video = append([]ClientVideoCodec(nil), profile.Video...)
		for i := range profile.Video {
			profile.Video[i].narrowGeometry(nil, &ceiling.MaxVideoHeight)
		}
	}
	return profile
}

func memberChannelCeilingsTx(ctx context.Context, tx *sql.Tx, account, session string) (ReplanCeilings, error) {
	var location string
	if err := tx.QueryRowContext(ctx, `SELECT location FROM playback_v1_sessions WHERE id=? AND account_id=?`, session, account).Scan(&location); err != nil {
		return ReplanCeilings{}, err
	}
	return memberAwayCeilingsTx(ctx, tx, account, location != "local")
}
