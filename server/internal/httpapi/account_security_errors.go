package httpapi

import "github.com/PorticoMediaServer/portico/apikit/apierror"

// Metadata for unchanged legacy password/factor outcomes used by the recovery
// code producer. These statuses, messages and retryability match failure().
func accountSecurityErrorDefinitions() []apierror.Definition {
	return []apierror.Definition{
		{Code: "owner_reset_expired", Status: 410, Message: "This code has expired. Codes last 15 minutes. Write a new file to get another.", Retry: apierror.Never},
		{Code: "owner_reset_file_unavailable", Status: 503, Message: "Portico could not write the reset file. Check that the server's data folder can be written to, then try again.", Retry: apierror.Never},
		{Code: "authentication_busy", Status: 503, Message: "The request could not be completed.", Retry: apierror.SameRequest},
		{Code: "current_password_incorrect", Status: 403, Message: "The request could not be completed.", Retry: apierror.Never},
		{Code: "account_locked", Status: 429, Message: "The request could not be completed.", Retry: apierror.Never},
		{Code: "source_blocked", Status: 429, Message: "Too many failed attempts from this network. Try again later.", Retry: apierror.SameRequest},
		{Code: "two_factor_missing", Status: 409, Message: "The request could not be completed.", Retry: apierror.Never},
		{Code: "two_factor_required", Status: 401, Message: publicErrorMessage("unauthorized"), Retry: apierror.Never},
		{Code: "two_factor_invalid", Status: 403, Message: "The request could not be completed.", Retry: apierror.Never},
	}
}
