package writer

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// Image mapping pinned to 74837e5d63b0490003984e0ae9aaee1f4ab535a7:
// identity/direct_accounts.go: DirectSnapshot and DirectAccount; directAccountTx,
// directCallerTx and passwordAccount populate account facts; directSnapshotTx
// supplies HostedAccountID. DirectMembers also fills disabled history fields;
// identity/direct_profiles.go: DirectProfile and directSnapshotTx;
// identity/sign_in_kind.go: ManagementCapabilities.
// These are historical public DTO data, INCLUDING captured permission flags,
// never authentication, current permission evidence, a receipt or result authority.
// Optional omitempty scalars have no separate presence in the native DTO.
// No DirectSignIn session, challenge, credentials or internal hashes belong here.
// Staging still needs an independently authorized same-snapshot carrier, numeric
// octet/cardinality preflight and memory accounting. Source reads are unbounded
// here: input validation caps do not establish stored source invariants.
// directSnapshotTx normalizes nil library lists and Profiles to empty; this image
// preserves the supplied public shape instead of guessing raw SQL inheritance.
// Raw profile SQL NULL inheritance cannot be recovered from that public DTO.
type USDirectSnapshotImage struct {
	TwoStepSetupRequired bool
	Authority            string
	ServerID             string
	Account              USDirectAccountImage
	Profiles             []USDirectProfileImage
	CanManage            bool
	Capabilities         USDirectCapabilitiesImage
	CustodyPending       bool
	RememberProfiles     bool
	ManagePINRequired    bool
}

func (USDirectSnapshotImage) String() string   { return "[direct snapshot image redacted]" }
func (USDirectSnapshotImage) GoString() string { return "[direct snapshot image redacted]" }
func (USDirectSnapshotImage) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte("[direct snapshot image redacted]"))
}

type USDirectAccountImage struct {
	ID               string
	Username         string
	PrimaryProfileID string
	Role             string
	Revision         int64
	Disabled         bool
	AllowedLibraries []string
	DisabledAt       string
	DisabledReason   string
	HostedAccountID  string
}

func (USDirectAccountImage) String() string   { return "[direct snapshot image redacted]" }
func (USDirectAccountImage) GoString() string { return "[direct snapshot image redacted]" }
func (USDirectAccountImage) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte("[direct snapshot image redacted]"))
}

type USDirectProfileImage struct {
	ID               string
	Name             string
	Primary          bool
	Position         int
	Art              string
	Revision         int64
	PINRevision      int64
	PINRequired      bool
	AllowedLibraries []string
}

func (USDirectProfileImage) String() string   { return "[direct snapshot image redacted]" }
func (USDirectProfileImage) GoString() string { return "[direct snapshot image redacted]" }
func (USDirectProfileImage) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte("[direct snapshot image redacted]"))
}

type USDirectCapabilitiesImage struct {
	ManageAccount bool
	ManagePeople  bool
	ManageServer  bool
	ManageSignIn  bool
}

func (USDirectCapabilitiesImage) String() string   { return "[direct snapshot image redacted]" }
func (USDirectCapabilitiesImage) GoString() string { return "[direct snapshot image redacted]" }
func (USDirectCapabilitiesImage) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte("[direct snapshot image redacted]"))
}

// Clone takes ownership of all mutable containers without normalization.
func (v USDirectSnapshotImage) Clone() USDirectSnapshotImage {
	v.Authority = strings.Clone(v.Authority)
	v.ServerID = strings.Clone(v.ServerID)
	v.Account = v.Account.Clone()
	if v.Profiles != nil {
		p := make([]USDirectProfileImage, len(v.Profiles))
		for i := range v.Profiles {
			p[i] = v.Profiles[i].Clone()
		}
		v.Profiles = p
	}
	return v
}
func cloneUSDirectImageStrings(v []string) []string {
	if v == nil {
		return nil
	}
	out := make([]string, len(v))
	for i := range v {
		out[i] = strings.Clone(v[i])
	}
	return out
}
func (v USDirectAccountImage) Clone() USDirectAccountImage {
	v.ID = strings.Clone(v.ID)
	v.Username = strings.Clone(v.Username)
	v.PrimaryProfileID = strings.Clone(v.PrimaryProfileID)
	v.Role = strings.Clone(v.Role)
	v.AllowedLibraries = cloneUSDirectImageStrings(v.AllowedLibraries)
	v.DisabledAt = strings.Clone(v.DisabledAt)
	v.DisabledReason = strings.Clone(v.DisabledReason)
	v.HostedAccountID = strings.Clone(v.HostedAccountID)
	return v
}
func (v USDirectProfileImage) Clone() USDirectProfileImage {
	v.ID = strings.Clone(v.ID)
	v.Name = strings.Clone(v.Name)
	v.Art = strings.Clone(v.Art)
	v.AllowedLibraries = cloneUSDirectImageStrings(v.AllowedLibraries)
	return v
}

// ManifestSHA256 is an image-only manual LP/integer manifest, version 1.
// Ordinals are local to each fixed-count container; slices include presence,
// length and element ordinals. Integers use signed two's-complement uint64.
// The global codec budget is an encoding refusal, never truncation or a product
// user/profile ceiling. No purpose registration or acceptance is performed.
func (v USDirectSnapshotImage) ManifestSHA256() ([32]byte, error) {
	e := requestEncoder{h: sha256.New()}
	e.text("portico-us-direct-snapshot-image")
	e.number(1)
	encodeUSDirectSnapshotImage(&e, v)
	if e.err != nil {
		return [32]byte{}, e.err
	}
	var out [32]byte
	copy(out[:], e.h.Sum(nil))
	return out, nil
}

// Native DTO strings are length-prefixed bytes. A legal stored TEXT value may
// contain invalid UTF-8; a manifest must preserve it before any JSON repair.
func encodeUSDirectImageText(e *requestEncoder, v string) {
	e.number(uint64(len(v)))
	e.bytes([]byte(v))
}
func encodeUSDirectImageStrings(e *requestEncoder, v []string) {
	e.flag(v != nil)
	if v == nil {
		return
	}
	e.number(uint64(len(v)))
	for i, s := range v {
		if e.err != nil {
			return
		}
		e.number(uint64(i))
		encodeUSDirectImageText(e, s)
	}
}
func encodeUSDirectSnapshotImage(e *requestEncoder, v USDirectSnapshotImage) {
	e.number(10)
	e.number(0) // TwoStepSetupRequired
	e.flag(v.TwoStepSetupRequired)
	e.number(1) // Authority
	encodeUSDirectImageText(e, v.Authority)
	e.number(2) // ServerID
	encodeUSDirectImageText(e, v.ServerID)
	e.number(3) // Account
	encodeUSDirectAccountImage(e, v.Account)
	e.number(4) // Profiles
	e.flag(v.Profiles != nil)
	if v.Profiles != nil {
		e.number(uint64(len(v.Profiles)))
		for i, p := range v.Profiles {
			if e.err != nil {
				return
			}
			e.number(uint64(i))
			encodeUSDirectProfileImage(e, p)
		}
	}
	e.number(5) // CanManage
	e.flag(v.CanManage)
	e.number(6) // Capabilities
	encodeUSDirectCapabilitiesImage(e, v.Capabilities)
	e.number(7) // CustodyPending
	e.flag(v.CustodyPending)
	e.number(8) // RememberProfiles
	e.flag(v.RememberProfiles)
	e.number(9) // ManagePINRequired
	e.flag(v.ManagePINRequired)
}
func encodeUSDirectAccountImage(e *requestEncoder, v USDirectAccountImage) {
	e.number(10)
	e.number(0) // ID
	encodeUSDirectImageText(e, v.ID)
	e.number(1) // Username
	encodeUSDirectImageText(e, v.Username)
	e.number(2) // PrimaryProfileID
	encodeUSDirectImageText(e, v.PrimaryProfileID)
	e.number(3) // Role
	encodeUSDirectImageText(e, v.Role)
	e.number(4) // Revision
	e.number(uint64(v.Revision))
	e.number(5) // Disabled
	e.flag(v.Disabled)
	e.number(6) // AllowedLibraries
	encodeUSDirectImageStrings(e, v.AllowedLibraries)
	e.number(7) // DisabledAt
	encodeUSDirectImageText(e, v.DisabledAt)
	e.number(8) // DisabledReason
	encodeUSDirectImageText(e, v.DisabledReason)
	e.number(9) // HostedAccountID
	encodeUSDirectImageText(e, v.HostedAccountID)
}
func encodeUSDirectProfileImage(e *requestEncoder, v USDirectProfileImage) {
	e.number(9)
	e.number(0) // ID
	encodeUSDirectImageText(e, v.ID)
	e.number(1) // Name
	encodeUSDirectImageText(e, v.Name)
	e.number(2) // Primary
	e.flag(v.Primary)
	e.number(3) // Position
	e.number(uint64(v.Position))
	e.number(4) // Art
	encodeUSDirectImageText(e, v.Art)
	e.number(5) // Revision
	e.number(uint64(v.Revision))
	e.number(6) // PINRevision
	e.number(uint64(v.PINRevision))
	e.number(7) // PINRequired
	e.flag(v.PINRequired)
	e.number(8) // AllowedLibraries
	encodeUSDirectImageStrings(e, v.AllowedLibraries)
}
func encodeUSDirectCapabilitiesImage(e *requestEncoder, v USDirectCapabilitiesImage) {
	e.number(4)
	e.number(0) // ManageAccount
	e.flag(v.ManageAccount)
	e.number(1) // ManagePeople
	e.flag(v.ManagePeople)
	e.number(2) // ManageServer
	e.flag(v.ManageServer)
	e.number(3) // ManageSignIn
	e.flag(v.ManageSignIn)
}
