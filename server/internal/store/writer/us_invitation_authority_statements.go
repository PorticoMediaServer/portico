package writer

import (
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	usInvitationAuthorityFamily       stmt.ID = "us.invitation.authority.family"
	usInvitationAuthorityPrincipal    stmt.ID = "us.invitation.authority.principal"
	usInvitationAuthorityPrincipalPIN stmt.ID = "us.invitation.authority.principal_pin"
	usInvitationAuthorityDevice       stmt.ID = "us.invitation.authority.device"
)

// Reuse unchanged US-01 point definitions byte-for-byte. Every changed SQL
// statement has its own ID; composing both recipes cannot drop a stricter arm.
// The only text returned is a role restricted by literal SQL to owner/admin.
func usInvitationAuthorityDefinitions() []stmt.Definition {
	defs := usAuthorityDefinitions()
	for n := range defs {
		d := &defs[n]
		switch d.ID {
		case usAuthorityFamily:
			d.ID = usInvitationAuthorityFamily
			d.SQL = strings.Replace(d.SQL, "SELECT f.control_variable_bytes,f.pin_gate", "SELECT f.control_variable_bytes,f.pin_gate,f.role", 1)
			d.SQL = strings.Replace(d.SQL, "f.role='owner'", "f.role IN('owner','admin')", 1)
		case usAuthorityPrincipal, usAuthorityPrincipalPIN:
			protected := d.ID == usAuthorityPrincipalPIN
			d.ID = usInvitationAuthorityPrincipal
			if protected {
				d.ID = usInvitationAuthorityPrincipalPIN
			}
			d.SQL = strings.Replace(d.SQL, "m.role='owner'", "m.role=?", 1)
			// The parameter occurs before the profile bindings in the inherited point.
			d.Params = append(append(append([]stmt.Param(nil), d.Params[:3]...), stmt.Param{Name: "verified_role", Type: stmt.Text}), d.Params[3:]...)
			for c := range d.Cohorts {
				old := d.Cohorts[c].Bindings
				d.Cohorts[c].Bindings = append(append(append([]any(nil), old[:3]...), "owner"), old[3:]...)
			}
		case usAuthorityDevice:
			d.ID = usInvitationAuthorityDevice
			d.SQL += " AND platform<>'api'"
		default:
			continue
		}
		d.Owner = "store/writer US closed invitation-manager authority"
		d.CallSites = []string{"store/writer/us_invitation_authority.go"}
	}
	return defs
}
