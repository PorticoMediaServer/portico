package api

import (
	"context"
	"encoding/json"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

// Required two-step sign-in is a setting of servers with local sign-in. On a server that signs
// people in with Portico Accounts it could do nothing: nobody signs in with a password there,
// and setting two-step up here needs the server's own account password, which such a person
// does not have. A setting must mean what it says, so there it is not offered: the settings
// document says it does not apply, a save that turns it on is refused, and switching a server
// to Portico Accounts turns it off. (The lead's ruling; sign-in rule 11 is silent on Portico
// Accounts.)

// twoStepOnPortico is the one line the screens show in place of the setting.
const twoStepOnPortico = "Two-step verification is set on each person's Portico Account."

// settingsNotApplicable names the fields of a settings document that do not apply on this
// server, each with the line to show in its place. Nil when all of it applies.
func (s *Server) settingsNotApplicable(ctx context.Context, name string) (map[string]string, error) {
	if name != settings.SignIn.Name() {
		return nil, nil
	}
	kind, err := s.signInKind(ctx)
	if err != nil || kind != "portico" {
		return nil, err
	}
	return map[string]string{"requireTwoStep": twoStepOnPortico}, nil
}

// refuseNotApplicable refuses a save that turns on a setting which does not apply here.
func (s *Server) refuseNotApplicable(ctx context.Context, name string, change JSON) error {
	gone, err := s.settingsNotApplicable(ctx, name)
	if err != nil || gone == nil {
		return err
	}
	var sent struct {
		RequireTwoStep *string `json:"requireTwoStep"`
	}
	if json.Unmarshal(change, &sent) == nil && sent.RequireTwoStep != nil && *sent.RequireTwoStep != "nobody" {
		return errSetting(&settings.Invalid{Field: "requireTwoStep", Message: gone["requireTwoStep"]})
	}
	return nil
}

// twoStepOffForPortico turns required two-step off after a server has switched to Portico
// Accounts, and says so in the activity record. The switch has already happened, so a
// failure here is logged and does not undo it: the setting does nothing there either way.
func (s *Server) twoStepOffForPortico(ctx context.Context, actorID int64, now time.Time) {
	if settings.SignIn.Get(s.settings).RequireTwoStep == "nobody" {
		return
	}
	_, err := s.settings.Write(ctx, actorID, settings.SignIn.Name(), json.RawMessage(`{"requireTwoStep":"nobody"}`), -1, now)
	if err == nil {
		err = s.db.RecordActivity(ctx, actorID, "settings.two_step_not_required", settings.SignIn.Name(),
			"Required two-step sign-in was turned off because this server now uses Portico Accounts. "+twoStepOnPortico, now)
	}
	if err != nil {
		s.log.Error("required two-step sign-in could not be turned off after the switch to Portico Accounts", "error", err)
	}
}
