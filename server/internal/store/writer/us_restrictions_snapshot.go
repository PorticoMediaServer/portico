package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const usRestrictionSourceOctets stmt.ID = "us.restrictions.source.octets"
const usRestrictionSourceOctetsSQL = `SELECT octet_length(r.profile_id),octet_length(r.rating_system),octet_length(r.maximum_age_rating),octet_length(r.blocked_labels) FROM profile_restrictions AS r INDEXED BY sqlite_autoindex_profile_restrictions_1 WHERE r.profile_id=?`

func usRestrictionSourceDefinitions() []stmt.Definition {
	d := usRestrictionsEffectDefinitions()[0]
	d.ID, d.SQL = usRestrictionSourceOctets, usRestrictionSourceOctetsSQL
	d.Owner = "store/writer US restriction source preflight"
	d.CallSites = []string{"store/writer/us_restrictions_snapshot.go"}
	return []stmt.Definition{d}
}

// Fresh-only source image; immutable receipt classification belongs before this
// read. Absence is a fact, not an inferred default or manager/DVR authorization.
func readUSRestrictionSource(ctx context.Context, turn *commandTurn, a *usPolicyAuthorization, profile string) (out usRestrictionsEffect, present bool, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), struct {
			Value   usRestrictionsEffect
			Present bool
		}{out, present}, errors.Join(err, a.check(turn)))
		out, present, err = v.Value, v.Present, e
	}()
	if err = a.check(turn); err != nil {
		return out, false, err
	}
	if !usIdentifier(profile) {
		return out, false, ErrInvalid
	}
	row, err := usPolicyRow(ctx, turn, usRestrictionSourceOctets, profile)
	if err != nil {
		return out, false, err
	}
	var lengths [4]int64
	if err = row.Scan(&lengths[0], &lengths[1], &lengths[2], &lengths[3]); errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	} else if err != nil {
		return out, false, err
	}
	var total int64
	for _, n := range lengths {
		if n < 0 || n > math.MaxInt64-total {
			return out, false, ErrResolveRequired
		}
		total += n
	}
	if err = a.requireMemory(ctx, turn, total); err != nil {
		return out, false, err
	}
	row, err = usPolicyRow(ctx, turn, usRestrictionsFacts, profile)
	if err != nil {
		return out, false, err
	}
	if err = row.Scan(out.destinations()...); err != nil {
		return out, false, errors.Join(ErrResolveRequired, err)
	}
	actual := [4]int64{int64(len(out.profile)), int64(len(out.ratingSystem)), int64(len(out.maximumAgeRating)), int64(len(out.labelsJSON))}
	if actual != lengths || out.profile != profile || out.revision < 1 {
		return out, false, ErrResolveRequired
	}
	return out, true, turn.checkLifetimes(ctx)
}
