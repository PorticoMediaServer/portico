package httpapi

import (
	"slices"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// APILevel is this server's newest API major. Clients already in the field
// read it; newer clients negotiate from APIMajors and Capabilities instead
// (O-159). Bump it by hand only for a breaking change, in the same commit, and
// keep serving the old major; packages/contracts/src/api-level.ts carries the
// same numbers (TestAPILevelMatchesContracts).
const APILevel = 1

// APIMinor rises with each additive release of the newest major. It is for
// support and diagnostics only: clients gate features on Capabilities.
const APIMinor = 0

// APIMajors lists every major this server still serves, oldest first. Its
// first entry is the oldest client major the server accepts.
var APIMajors = []int{1}

// CapabilityEntry is one stable feature ID. Baseline entries exist at 1.0:
// always present, never gated and never advertised. Later entries name the
// release that added them and are advertised so clients may gate on them.
// IDs are never renamed or reused.
type CapabilityEntry struct {
	ID    string
	Since string // "1.0" for the baseline
}

// CapabilityRegistry records every feature ID (O-159). The 1.0 floor has no
// fallbacks: a server without negotiation facts is pre-release and refused.
var CapabilityRegistry = []CapabilityEntry{
	{"admin.certificate", "1.0"},
	{"admin.remote-access", "1.0"},
	{"connection.note", "1.0"},
	{"events.stream", "1.0"},
	{"file-info.windows", "1.0"},
	{"home.privacy-fence", "1.0"},
	{"links.server-scoped", "1.0"},
	{"music.lyrics", "1.0"},
	{"queue.track-entries", "1.0"},
	{"reading", "1.0"},
	{"reading.importing", "1.0"},
}

// Capabilities is what GET /v1/system advertises: the registry's features
// added after 1.0, sorted. Empty at 1.0.
var Capabilities = advertisedCapabilities(CapabilityRegistry)

func advertisedCapabilities(registry []CapabilityEntry) []string {
	out := []string{}
	for _, entry := range registry {
		if entry.Since != "1.0" {
			out = append(out, entry.ID)
		}
	}
	slices.Sort(out)
	return out
}

// SystemInfo is public and contains only the facts needed before sign-in.
type SystemInfo struct {
	ID     string               `json:"id"`
	Name   string               `json:"name"`
	SignIn identity.SignInKind  `json:"signIn"`
	Move   *identity.PublicMove `json:"move"`
	// MoveEndsAt is when the old sign-in offered during a move to Portico Accounts stops
	// working (RFC 3339, UTC); null when there is no such offer.
	MoveEndsAt       *string `json:"moveEndsAt"`
	SetupRequired    bool    `json:"setupRequired"`
	HostedConfigured bool    `json:"hostedConfigured"`
	HostedAttached   bool    `json:"hostedAttached"`
	Version          string  `json:"version"`
	APILevel         int     `json:"apiLevel"`
	// APIVersion is "major.minor" of the newest major (O-159).
	APIVersion string `json:"apiVersion"`
	// APIMajors lists the API majors this server serves, oldest first.
	APIMajors []int `json:"apiMajors"`
	// Capabilities lists features added after 1.0, sorted; empty at 1.0.
	Capabilities []string `json:"capabilities"`
}

// systemAPI returns the negotiation facts every GET /v1/system carries.
func systemAPI(info SystemInfo) SystemInfo {
	info.APILevel = APILevel
	info.APIVersion = strconv.Itoa(APILevel) + "." + strconv.Itoa(APIMinor)
	info.APIMajors = append([]int(nil), APIMajors...)
	info.Capabilities = append([]string{}, Capabilities...)
	return info
}
