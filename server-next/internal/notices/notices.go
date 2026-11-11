// Package notices is how the server tells people something: a notice in their Notifications.
// There is no email and no push. Any area sends one with Send; the owner's `notices` settings
// say who gets server notices and how long notices are kept.
package notices

import (
	"context"
	"encoding/json"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

// Settings is the owner's `notices` settings document.
type Settings struct {
	// ServerNoticesTo is who gets server notices (a backup failed, storage is low, a source
	// cannot be reached, a new version): "owner", or "owner-and-admins".
	ServerNoticesTo string `json:"serverNoticesTo"`
	// KeepDays is how long a notice is kept, 30 to 180 days.
	KeepDays int `json:"keepDays"`
}

// Document is the notices settings document.
var Document = settings.Define("notices", Settings{ServerNoticesTo: "owner", KeepDays: 180}, func(s *Settings) error {
	if s.ServerNoticesTo != "owner" && s.ServerNoticesTo != "owner-and-admins" {
		return &settings.Invalid{Field: "serverNoticesTo", Message: "Choose the owner, or the owner and administrators."}
	}
	if s.KeepDays < 30 || s.KeepDays > 180 {
		return &settings.Invalid{Field: "keepDays", Message: "Keep notices for 30 to 180 days."}
	}
	return nil
})

// To is who a notice goes to.
type To struct {
	audience  db.Audience
	userID    int64
	profileID int64
	server    bool
}

// Server is whoever the owner chose to get server notices.
var Server = To{server: true}

// Everyone is every account that may sign in.
var Everyone = To{audience: db.ToEveryone}

// Account is one account, all its profiles.
func Account(userID int64) To { return To{audience: db.ToAccount, userID: userID} }

// Profile is one profile of an account.
func Profile(userID, profileID int64) To {
	return To{audience: db.ToProfile, userID: userID, profileID: profileID}
}

// Notices sends notices.
type Notices struct {
	DB       *db.DB
	Settings *settings.Settings
	Now      func() time.Time
}

// Send gives a notice of a kind to its audience. params are the facts the app words it from
// (they are marshalled to JSON); ref names what it is about, for the app to open.
func (n *Notices) Send(ctx context.Context, to To, kind string, params any, ref string) error {
	notice := db.NewNotice{To: to.audience, UserID: to.userID, ProfileID: to.profileID, Kind: kind, Ref: ref, Now: n.Now()}
	if to.server {
		notice.To = db.ToOwner
		if Document.Get(n.Settings).ServerNoticesTo == "owner-and-admins" {
			notice.To = db.ToOwnerAndAdmins
		}
	}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return err
		}
		notice.Params = string(raw)
	}
	_, _, err := n.DB.SendNotice(ctx, notice)
	return err
}

// Tidy removes notices older than the owner's keeping time. The daily tidy-up runs it.
func (n *Notices) Tidy(ctx context.Context) error {
	keep := time.Duration(Document.Get(n.Settings).KeepDays) * 24 * time.Hour
	_, err := n.DB.RemoveOldNotices(ctx, n.Now().Add(-keep))
	return err
}
