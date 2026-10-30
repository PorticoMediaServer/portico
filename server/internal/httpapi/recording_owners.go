package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/recordingaccess"
)

type recordingOwnerChoice struct {
	Owner       livechannels.Owner `json:"owner"`
	AccountName string             `json:"accountName"`
	ProfileName string             `json:"profileName"`
	// CanRecord is the profile's Recordings switch (Who can record) and Revision
	// its restriction document's, which a change to it is fenced by.
	CanRecord bool  `json:"canRecord"`
	Revision  int64 `json:"revision"`
}

// Candidates are this server's local profiles, including Portico Account
// members. Listing them creates no grant and rechecks current membership.
func (d Dependencies) recordingOwnersRoute(mux *http.ServeMux) {
	d.httpHandle(mux, "GET /v1/admin/dvr/recording-owners", d.administrationHandler(30*time.Second, func(ctx context.Context, w http.ResponseWriter, r *http.Request, p identity.Principal) (any, error) {
		q, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			return nil, administration.ErrInput
		}
		limit := 100
		var after [3]string
		for key, values := range q {
			if len(values) != 1 {
				return nil, administration.ErrInput
			}
			switch key {
			case "limit":
				limit, err = strconv.Atoi(values[0])
				if err != nil || limit < 1 || limit > 200 {
					return nil, administration.ErrInput
				}
			case "cursor":
				if len(values[0]) > 4096 {
					return nil, administration.ErrInput
				}
				if values[0] != "" {
					raw, e := base64.RawURLEncoding.DecodeString(values[0])
					if e != nil {
						return nil, administration.ErrInput
					}
					var parts []string
					if json.Unmarshal(raw, &parts) != nil || len(parts) != 3 {
						return nil, administration.ErrInput
					}
					copy(after[:], parts)
					if !(livechannels.Owner{Authority: after[0], AccountID: after[1], ProfileID: after[2]}).Valid() {
						return nil, administration.ErrInput
					}
				}
			default:
				return nil, administration.ErrInput
			}
		}
		g, err := dbwork.BeginSnapshot(ctx, d.DB)
		if err != nil {
			return nil, err
		}
		defer g.Rollback()
		if err = d.administrationAuthority(p)(ctx, g.Tx()); err != nil {
			return nil, err
		}
		rows, err := g.Tx().QueryContext(ctx, `SELECT authority,account_id,profile_id,account_name,profile_name,can_record,revision FROM (
 SELECT 'local' authority,a.id account_id,p.id profile_id,a.username account_name,p.name profile_name,`+identity.RecordingDefaultSQL+` can_record,COALESCE(r.revision,1) revision
 FROM accounts a JOIN direct_profiles p ON p.account_id=a.id JOIN direct_memberships m ON m.account_id=a.id LEFT JOIN profile_restrictions r ON r.profile_id=p.id
 WHERE p.deleted=0 AND m.disabled=0
 ) WHERE (authority,account_id,profile_id)>(?,?,?) ORDER BY authority,account_id,profile_id LIMIT ?`, after[0], after[1], after[2], limit+1)
		if err != nil {
			return nil, err
		}
		candidates := []recordingOwnerChoice{}
		for rows.Next() {
			var c recordingOwnerChoice
			if err = rows.Scan(&c.Owner.Authority, &c.Owner.AccountID, &c.Owner.ProfileID, &c.AccountName, &c.ProfileName, &c.CanRecord, &c.Revision); err != nil {
				rows.Close()
				return nil, err
			}
			candidates = append(candidates, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		out := struct {
			Items      []recordingOwnerChoice `json:"items"`
			NextCursor string                 `json:"nextCursor"`
		}{Items: []recordingOwnerChoice{}}
		if len(candidates) > limit {
			candidates = candidates[:limit]
			last := candidates[len(candidates)-1].Owner
			raw, _ := json.Marshal([3]string{last.Authority, last.AccountID, last.ProfileID})
			out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
		}
		policy := recordingaccess.Policy{Cached: d.Hosted}
		// A typed nil must not become an apparently configured CachedPolicy.
		if d.Hosted == nil {
			policy.Cached = nil
		}
		for _, c := range candidates {
			if _, err = policy.MemberTx(ctx, g.Tx(), c.Owner); errors.Is(err, livechannels.ErrDenied) {
				continue
			} else if err != nil {
				return nil, err
			}
			if c.AccountName == "" {
				c.AccountName = c.Owner.AccountID
			}
			if c.ProfileName == "" {
				c.ProfileName = c.Owner.ProfileID
			}
			out.Items = append(out.Items, c)
		}
		return out, nil
	}))
}
