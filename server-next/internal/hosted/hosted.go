// Package hosted is the one place the server may talk to Portico Services, the hosted service
// behind Portico Accounts. How the server and the service talk is not decided yet, so this
// package holds only the questions the server needs answered and NotConnected, which answers
// every one with ErrNotConnected. Nothing else in the server names a Portico address.
//
// A server set up with local sign-in never calls anything here: every caller asks only on
// behalf of a server that uses Portico Accounts, or of a setup or switch that is choosing them.
package hosted

import (
	"context"
	"errors"
	"time"
)

// ErrNotConnected: this build of the server cannot reach Portico Services. Callers answer it
// as "Portico Services can't be reached", which is also what a person sees when it is down.
var ErrNotConnected = errors.New("portico services are not connected")

// ErrDeclined: the person declined the approval, or it expired or was abandoned. The server
// stays exactly as it was.
var ErrDeclined = errors.New("the approval was declined or has expired")

// Purpose is what an approval is for.
type Purpose string

const (
	// PurposeSetup makes the approving Portico Account the owner of a new server.
	PurposeSetup Purpose = "setup"
)

// ApprovalRequest asks Portico Services for an address where a person approves this server.
type ApprovalRequest struct {
	ServerID   string
	ServerName string
	Purpose    Purpose
	// ReturnURL is where the person's browser comes back to with the approval.
	ReturnURL string
}

// Approval is where to send the person, and until when that address works.
type Approval struct {
	URL       string
	ExpiresAt time.Time
}

// Account is a Portico Account as the server sees it.
type Account struct {
	// ID is the account's id at Portico Services, stored as users.portico_account_id.
	ID          string
	Username    string
	DisplayName string
}

// Decision is an approval that came back, checked: who approved, for which server name, and
// for what. FinishApproval returns one only for an approval made for this server.
type Decision struct {
	Account    Account
	ServerName string
	Purpose    Purpose
}

// Service is what the server asks of Portico Services.
type Service interface {
	// StartApproval asks for an approval address. Nothing is saved on the server: the
	// approval that comes back carries everything the finish needs.
	StartApproval(ctx context.Context, req ApprovalRequest) (Approval, error)
	// FinishApproval checks an approval the person's browser or app brought back and says
	// who approved what. One made for another server, or for nothing, is ErrDeclined.
	FinishApproval(ctx context.Context, serverID, returned string) (Decision, error)
}

// NotConnected is the Service of this build: Portico Services is not reachable from it.
type NotConnected struct{}

func (NotConnected) StartApproval(context.Context, ApprovalRequest) (Approval, error) {
	return Approval{}, ErrNotConnected
}

func (NotConnected) FinishApproval(context.Context, string, string) (Decision, error) {
	return Decision{}, ErrNotConnected
}
