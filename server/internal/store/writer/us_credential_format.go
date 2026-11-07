package writer

import "fmt"

func (usCredentialTarget) String() string   { return "usCredentialTarget{redacted}" }
func (usCredentialTarget) GoString() string { return "usCredentialTarget{redacted}" }
func (usCredentialTarget) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usCredentialTarget")
}

func (usPasswordReplaceRequest) String() string   { return "usPasswordReplaceRequest{redacted}" }
func (usPasswordReplaceRequest) GoString() string { return "usPasswordReplaceRequest{redacted}" }
func (usPasswordReplaceRequest) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usPasswordReplaceRequest")
}

func (usPasswordReplacePrepared) String() string   { return "usPasswordReplacePrepared{redacted}" }
func (usPasswordReplacePrepared) GoString() string { return "usPasswordReplacePrepared{redacted}" }
func (usPasswordReplacePrepared) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usPasswordReplacePrepared")
}

func (usCredentialIssueRequest) String() string   { return "usCredentialIssueRequest{redacted}" }
func (usCredentialIssueRequest) GoString() string { return "usCredentialIssueRequest{redacted}" }
func (usCredentialIssueRequest) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usCredentialIssueRequest")
}

func (usCredentialIssuePrepared) String() string   { return "usCredentialIssuePrepared{redacted}" }
func (usCredentialIssuePrepared) GoString() string { return "usCredentialIssuePrepared{redacted}" }
func (usCredentialIssuePrepared) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usCredentialIssuePrepared")
}

func (usCredentialProtectedManifest) String() string {
	return "usCredentialProtectedManifest{redacted}"
}
func (usCredentialProtectedManifest) GoString() string {
	return "usCredentialProtectedManifest{redacted}"
}
func (usCredentialProtectedManifest) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usCredentialProtectedManifest")
}

func (usCredentialRevokeRequest) String() string   { return "usCredentialRevokeRequest{redacted}" }
func (usCredentialRevokeRequest) GoString() string { return "usCredentialRevokeRequest{redacted}" }
func (usCredentialRevokeRequest) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usCredentialRevokeRequest")
}

func (usCredentialRevokePrepared) String() string   { return "usCredentialRevokePrepared{redacted}" }
func (usCredentialRevokePrepared) GoString() string { return "usCredentialRevokePrepared{redacted}" }
func (usCredentialRevokePrepared) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usCredentialRevokePrepared")
}
