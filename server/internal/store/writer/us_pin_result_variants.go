package writer

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// The CLOSED 532/533 result variant. SOL-USERS rules that the caller's own
// account keeps the complete post-effect DirectSnapshot, while an administrative
// clear of ANOTHER account's profile keeps the MemberProfile image the merged
// US-09 already builds. The variant is derived from the SAME validated entry
// (command purpose), same-frame authority stamp (issuer account) and validated
// target (target account). It is never a request field, a caller DTO selector,
// a stored flag or a caller-supplied boolean.
type usPINResultVariant uint8

const (
	usPINVariantCallerSnapshot usPINResultVariant = iota + 1
	usPINVariantAdministrativeProfile
)

// errUSPINResultArm is the named refusal for a loader handed the other variant.
// The two loaders are not interchangeable: the caller-snapshot loader builds
// only a DirectSnapshot and the administrative loader builds only a
// MemberProfile. A direct call with the wrong arm is refused before any source
// read or allocation instead of silently returning a shape that arm did not
// select.
var errUSPINResultArm = errors.New("us pin result image arm refused")

// codec is the closed image-codec tag bound into the result digest. The two
// arms can never share a digest: a saved original proves which response shape
// was captured at birth even when every scalar result field is identical.
func (v usPINResultVariant) codec() (string, error) {
	switch v {
	case usPINVariantCallerSnapshot:
		return "pin_own_snapshot_v1", nil
	case usPINVariantAdministrativeProfile:
		return "pin_admin_profile_v1", nil
	default:
		return "", ErrInvalid
	}
}

// deriveUSPINResultVariant is the ONLY selector. callerAccount must be the
// rechecked US-01 stamp's issuer account and targetAccount the validated target
// entry, so request input cannot force an arm. 532 and 533 share the rule: an
// own-account edit (including PINReset through EditDirectProfile) is the caller
// snapshot, and a cross-account edit is the administrative profile image.
func deriveUSPINResultVariant(kind CommandID, callerAccount, targetAccount string) (usPINResultVariant, error) {
	if kind != usPINSet && kind != usPINClear {
		return 0, ErrInvalid
	}
	if !usIdentifier(callerAccount) || !usIdentifier(targetAccount) {
		return 0, ErrInvalid
	}
	if targetAccount == callerAccount {
		return usPINVariantCallerSnapshot, nil
	}
	return usPINVariantAdministrativeProfile, nil
}

// variantOf derives this image's closed arm from its presence shape. Exactly one
// arm must be present; two arms or none is a programming error, never a caller
// choice.
func (v usPINPublicImage) variantOf() (usPINResultVariant, error) {
	switch {
	case v.Snapshot != nil && v.Profile == nil:
		return usPINVariantCallerSnapshot, nil
	case v.Profile != nil && v.Snapshot == nil:
		return usPINVariantAdministrativeProfile, nil
	default:
		return 0, ErrInvalid
	}
}

// usPINImageBuffer is a write-only hash.Hash collector for the shared manual
// LP/integer image encoders. The encoders were designed to feed a digest; here
// the exact same byte grammar is retained as the closed storage/replay codec.
type usPINImageBuffer struct{ buf []byte }

func (w *usPINImageBuffer) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	return len(p), nil
}
func (w *usPINImageBuffer) Sum(b []byte) []byte { return append(b, w.buf...) }
func (w *usPINImageBuffer) Reset()              { w.buf = w.buf[:0] }
func (w *usPINImageBuffer) Size() int           { return 32 }
func (w *usPINImageBuffer) BlockSize() int      { return 64 }

const (
	usPINImageCodecMagic   = "portico-us-pin-public-image"
	usPINImageCodecVersion = 1
)

// Encode is the closed, versioned original-image codec. The arm is written into
// the bytes, so a decode needs no caller selector and cannot be told to return
// the other shape. The shared image encoders preserve nil/empty, order and raw
// bytes, so the saved original is field-for-field the image captured at birth.
func (v usPINPublicImage) Encode() ([]byte, error) {
	variant, err := v.variantOf()
	if err != nil {
		return nil, err
	}
	collector := &usPINImageBuffer{}
	e := requestEncoder{h: collector}
	e.text(usPINImageCodecMagic)
	e.number(usPINImageCodecVersion)
	e.number(uint64(variant))
	switch variant {
	case usPINVariantCallerSnapshot:
		encodeUSDirectSnapshotImage(&e, *v.Snapshot)
	case usPINVariantAdministrativeProfile:
		encodeUSMemberProfileImage(&e, *v.Profile)
	}
	if e.err != nil {
		return nil, e.err
	}
	out := make([]byte, len(collector.buf))
	copy(out, collector.buf)
	return out, nil
}

// usPINImageReader is the bounded decoder. Every length is proved against the
// bytes that actually remain BEFORE any allocation: a hostile saved body can
// never make the decoder reserve more than it already holds. Ordinals and
// field counts are exact, and any trailing byte is refused.
type usPINImageReader struct {
	b   []byte
	err error
}

func (r *usPINImageReader) take(n int) []byte {
	if r.err != nil || n < 0 || n > len(r.b) {
		r.err = ErrResolveRequired
		return nil
	}
	out := r.b[:n]
	r.b = r.b[n:]
	return out
}
func (r *usPINImageReader) number() uint64 {
	raw := r.take(8)
	if raw == nil {
		return 0
	}
	var n uint64
	for _, b := range raw {
		n = n<<8 | uint64(b)
	}
	return n
}
func (r *usPINImageReader) flag() bool {
	raw := r.take(1)
	if raw == nil {
		return false
	}
	if raw[0] != 0 && raw[0] != 1 {
		r.err = ErrResolveRequired
		return false
	}
	return raw[0] == 1
}
func (r *usPINImageReader) text() string {
	n := r.number()
	if r.err != nil {
		return ""
	}
	// Size-before-allocation: the declared length must fit the remaining bytes.
	if n > uint64(len(r.b)) {
		r.err = ErrResolveRequired
		return ""
	}
	return string(r.take(int(n)))
}
func (r *usPINImageReader) strings() []string {
	if !r.flag() {
		return nil
	}
	n := r.number()
	if r.err != nil {
		return nil
	}
	// Each element carries an 8-byte ordinal and an 8-byte length, so a count
	// beyond len/16 is impossible without overrunning. Prove it before make().
	if n > uint64(len(r.b))/16 {
		r.err = ErrResolveRequired
		return nil
	}
	out := make([]string, 0, int(n))
	for i := uint64(0); i < n && r.err == nil; i++ {
		if r.number() != i {
			r.err = ErrResolveRequired
			return nil
		}
		out = append(out, r.text())
	}
	if r.err != nil {
		return nil
	}
	return out
}
func (r *usPINImageReader) field(want uint64) {
	if r.number() != want {
		r.err = ErrResolveRequired
	}
}

// usPINPublicImageDecode is the replay half of the codec. It reads ONLY the
// saved original bytes: no live source, no authority reconstruction and no
// statement. A saved MemberProfile is never adapted into a DirectSnapshot.
//
// DURABLE adoption is not yet wired. Before a saved image becomes durable, the
// decoder must take the expected arm (from the validated entry/authority/target
// as it is derived at capture time), replay must compare that expected arm with
// the arm encoded here, the result digest must bind the image bytes (the
// current digest covers only the variant tag and scalars, not the image), and
// the byte quota must count the image itself. Until then an original image is
// same-frame only.
func usPINPublicImageDecode(saved []byte) (out usPINPublicImage, err error) {
	if len(saved) == 0 || len(saved) > maxCanonicalResultBytes {
		return out, &Refusal{Code: RefusalCapacity}
	}
	r := &usPINImageReader{b: saved}
	if r.text() != usPINImageCodecMagic || r.number() != usPINImageCodecVersion {
		return out, ErrResolveRequired
	}
	// Validate the raw uint64 tag against the closed set BEFORE narrowing it to
	// usPINResultVariant. A truncating conversion would map a non-canonical tag
	// such as 257 back onto arm 1 and accept a saved body whose tag is not the
	// one the encoder writes.
	tag := r.number()
	if r.err != nil {
		return out, r.err
	}
	if tag == 0 || tag > uint64(usPINVariantAdministrativeProfile) {
		return out, ErrResolveRequired
	}
	switch variant := usPINResultVariant(tag); variant {
	case usPINVariantCallerSnapshot:
		image := decodeUSDirectSnapshotImage(r)
		if r.err == nil {
			out.Snapshot = &image
		}
	case usPINVariantAdministrativeProfile:
		image := decodeUSMemberProfileImage(r)
		if r.err == nil {
			out.Profile = &image
		}
	default:
		return out, ErrResolveRequired
	}
	if r.err != nil {
		return usPINPublicImage{}, r.err
	}
	if len(r.b) != 0 {
		return usPINPublicImage{}, ErrResolveRequired
	}
	return out, nil
}

func decodeUSDirectSnapshotImage(r *usPINImageReader) USDirectSnapshotImage {
	var v USDirectSnapshotImage
	if r.number() != 10 {
		r.err = ErrResolveRequired
		return v
	}
	r.field(0)
	v.TwoStepSetupRequired = r.flag()
	r.field(1)
	v.Authority = r.text()
	r.field(2)
	v.ServerID = r.text()
	r.field(3)
	v.Account = decodeUSDirectAccountImage(r)
	r.field(4)
	if r.flag() {
		n := r.number()
		if r.err == nil && n > uint64(len(r.b))/40 {
			r.err = ErrResolveRequired
		}
		if r.err == nil {
			v.Profiles = make([]USDirectProfileImage, 0, int(n))
			for i := uint64(0); i < n && r.err == nil; i++ {
				if r.number() != i {
					r.err = ErrResolveRequired
					break
				}
				v.Profiles = append(v.Profiles, decodeUSDirectProfileImage(r))
			}
		}
	}
	r.field(5)
	v.CanManage = r.flag()
	r.field(6)
	v.Capabilities = decodeUSDirectCapabilitiesImage(r)
	r.field(7)
	v.CustodyPending = r.flag()
	r.field(8)
	v.RememberProfiles = r.flag()
	r.field(9)
	v.ManagePINRequired = r.flag()
	return v
}

func decodeUSDirectAccountImage(r *usPINImageReader) USDirectAccountImage {
	var v USDirectAccountImage
	if r.number() != 10 {
		r.err = ErrResolveRequired
		return v
	}
	r.field(0)
	v.ID = r.text()
	r.field(1)
	v.Username = r.text()
	r.field(2)
	v.PrimaryProfileID = r.text()
	r.field(3)
	v.Role = r.text()
	r.field(4)
	v.Revision = int64(r.number())
	r.field(5)
	v.Disabled = r.flag()
	r.field(6)
	v.AllowedLibraries = r.strings()
	r.field(7)
	v.DisabledAt = r.text()
	r.field(8)
	v.DisabledReason = r.text()
	r.field(9)
	v.HostedAccountID = r.text()
	return v
}

func decodeUSDirectProfileImage(r *usPINImageReader) USDirectProfileImage {
	var v USDirectProfileImage
	if r.number() != 9 {
		r.err = ErrResolveRequired
		return v
	}
	r.field(0)
	v.ID = r.text()
	r.field(1)
	v.Name = r.text()
	r.field(2)
	v.Primary = r.flag()
	r.field(3)
	v.Position = int(int64(r.number()))
	r.field(4)
	v.Art = r.text()
	r.field(5)
	v.Revision = int64(r.number())
	r.field(6)
	v.PINRevision = int64(r.number())
	r.field(7)
	v.PINRequired = r.flag()
	r.field(8)
	v.AllowedLibraries = r.strings()
	return v
}

func decodeUSDirectCapabilitiesImage(r *usPINImageReader) USDirectCapabilitiesImage {
	var v USDirectCapabilitiesImage
	if r.number() != 4 {
		r.err = ErrResolveRequired
		return v
	}
	r.field(0)
	v.ManageAccount = r.flag()
	r.field(1)
	v.ManagePeople = r.flag()
	r.field(2)
	v.ManageServer = r.flag()
	r.field(3)
	v.ManageSignIn = r.flag()
	return v
}

func decodeUSMemberProfileImage(r *usPINImageReader) USMemberProfileImage {
	var v USMemberProfileImage
	if r.number() != 11 {
		r.err = ErrResolveRequired
		return v
	}
	r.field(0)
	v.ID = r.text()
	r.field(1)
	v.Name = r.text()
	r.field(2)
	v.Primary = r.flag()
	r.field(3)
	v.Position = int(int64(r.number()))
	r.field(4)
	v.Art = r.text()
	r.field(5)
	v.Revision = int64(r.number())
	r.field(6)
	v.PINRevision = int64(r.number())
	r.field(7)
	v.PINRequired = r.flag()
	r.field(8)
	v.AllowedLibraries = r.strings()
	r.field(9)
	v.Restrictions = decodeUSMemberProfileRestrictionsImage(r)
	r.field(10)
	if r.flag() {
		avatar := decodeUSMemberProfileAvatarImage(r)
		if r.err == nil {
			v.Avatar = &avatar
		}
	}
	return v
}

func decodeUSMemberProfileRestrictionsImage(r *usPINImageReader) USMemberProfileRestrictionsImage {
	var v USMemberProfileRestrictionsImage
	if r.number() != 11 {
		r.err = ErrResolveRequired
		return v
	}
	r.field(0)
	v.ProfileID = r.text()
	r.field(1)
	v.RatingSystem = r.text()
	r.field(2)
	v.MaximumAgeRating = r.text()
	r.field(3)
	v.MaximumAge = int(int64(r.number()))
	r.field(4)
	v.AllowUnrated = r.flag()
	r.field(5)
	v.BlockedLabels = r.strings()
	r.field(6)
	v.AllowDownloads = r.flag()
	r.field(7)
	v.AllowLiveTV = r.flag()
	r.field(8)
	v.AllowDVR = r.flag()
	r.field(9)
	v.AllowWatchTogether = r.flag()
	r.field(10)
	v.Revision = int64(r.number())
	return v
}

func decodeUSMemberProfileAvatarImage(r *usPINImageReader) USMemberProfileAvatarImage {
	var v USMemberProfileAvatarImage
	if r.number() != 5 {
		r.err = ErrResolveRequired
		return v
	}
	r.field(0)
	v.ProfileID = r.text()
	r.field(1)
	v.Version = int64(r.number())
	r.field(2)
	v.UpdatedAt = r.text()
	r.field(3)
	v.SourceMIME = r.text()
	r.field(4)
	v.URL = r.text()
	return v
}

// Statement inventory for the PIN-owned copy of the 541 caller-snapshot loader.
// Every variable (TEXT/BLOB) column first appears as an INTEGER-only octet
// point; the frame grant is charged from those exact octets before any TEXT
// scan, JSON decode, slice or DTO allocation. The statements stay owned by this
// file so the 541 dispatcher's inventory and call sites are untouched.
const (
	usPINSnapshotAccountOctets   stmt.ID = "us.pin.snapshot.account_octets"
	usPINSnapshotFamilyOctets    stmt.ID = "us.pin.snapshot.family_octets"
	usPINSnapshotPolicyOctets    stmt.ID = "us.pin.snapshot.policy_octets"
	usPINSnapshotHostedOctets    stmt.ID = "us.pin.snapshot.hosted_octets"
	usPINSnapshotCustodianOctets stmt.ID = "us.pin.snapshot.custodian_octets"
	usPINSnapshotProfilesOctets  stmt.ID = "us.pin.snapshot.profiles_octets"
	usPINSnapshotAccount         stmt.ID = "us.pin.snapshot.account"
	usPINSnapshotFamily          stmt.ID = "us.pin.snapshot.family"
	usPINSnapshotPolicy          stmt.ID = "us.pin.snapshot.policy"
	usPINSnapshotFactor          stmt.ID = "us.pin.snapshot.factor"
	usPINSnapshotHosted          stmt.ID = "us.pin.snapshot.hosted"
	usPINSnapshotCustodian       stmt.ID = "us.pin.snapshot.custodian"
	usPINSnapshotProfiles        stmt.ID = "us.pin.snapshot.profiles"
)

func usPINSnapshotDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	accountPlan := stmt.Plan{Hot: true,
		Indexes: []string{"sqlite_autoindex_accounts_1", "sqlite_autoindex_direct_memberships_1"},
		Seeks:   []string{"id=?", "account_id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{
			usPolicyPoint("accounts", "sqlite_autoindex_accounts_1", "pk", "id"),
			usPolicyPoint("direct_memberships", "sqlite_autoindex_direct_memberships_1", "pk", "account_id"),
		}}
	familyPlan := stmt.Plan{Hot: true,
		Indexes: []string{"sqlite_autoindex_authorization_session_families_1", "sqlite_autoindex_direct_profiles_2"},
		Seeks:   []string{"id=?", "account_id=? AND id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{
			usPolicyPoint("authorization_session_families", "sqlite_autoindex_authorization_session_families_1", "pk", "id"),
			usPolicyPoint("direct_profiles", "sqlite_autoindex_direct_profiles_2", "u", "account_id", "id"),
		}}
	policyPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"p", "s"}, Seeks: []string{"rowid=?"}}
	hostedPlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_account_portico_links_1"}, Seeks: []string{"account_id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{usPolicyPoint("account_portico_links", "sqlite_autoindex_account_portico_links_1", "pk", "account_id")}}
	custodianPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"hosted_membership_sync"}, Seeks: []string{"rowid=?"}}
	profilesPlan := stmt.Plan{Hot: true, Indexes: []string{"direct_profiles_account"}, Seeks: []string{"account_id=? AND deleted=?"}}
	defs := []stmt.Definition{
		{ID: usPINSnapshotAccountOctets, SQL: `SELECT octet_length(a.username),octet_length(a.profile_id),octet_length(m.role),octet_length(m.allowed_libraries),m.revision,m.disabled
FROM accounts AS a INDEXED BY sqlite_autoindex_accounts_1
CROSS JOIN direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 ON m.account_id=a.id
WHERE a.id=?`,
			Params:  []stmt.Param{text("account")},
			Cohorts: []stmt.Cohort{{Name: "caller", Bindings: []any{"issuer"}}}, Plan: accountPlan},
		{ID: usPINSnapshotFamilyOctets, SQL: `SELECT octet_length(f.role),octet_length(f.remember_until),octet_length(f.manage_until),octet_length(p.pin_hash),p.is_primary
FROM authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1
CROSS JOIN direct_profiles AS p INDEXED BY sqlite_autoindex_direct_profiles_2 ON p.account_id=f.account_id
WHERE f.id=? AND f.account_id=? AND p.id=?`,
			Params:  []stmt.Param{text("family"), text("account"), text("profile")},
			Cohorts: []stmt.Cohort{{Name: "caller", Bindings: []any{"issuer-family", "issuer", "issuer-profile"}}}, Plan: familyPlan},
		{ID: usPINSnapshotPolicyOctets, SQL: `SELECT octet_length(p.require_two_step),p.pin_remember_days,octet_length(s.sign_in_kind)
FROM identity_sign_in_policy AS p
CROSS JOIN identity_state AS s ON s.singleton=1
WHERE p.singleton=1`,
			Params:  []stmt.Param{},
			Cohorts: []stmt.Cohort{{Name: "single", Bindings: []any{}}}, Plan: policyPlan},
		{ID: usPINSnapshotHostedOctets, SQL: `SELECT octet_length(hosted_account_id) FROM account_portico_links INDEXED BY sqlite_autoindex_account_portico_links_1 WHERE account_id=?`,
			Params:  []stmt.Param{text("account")},
			Cohorts: []stmt.Cohort{{Name: "caller", Bindings: []any{"issuer"}}}, Plan: hostedPlan},
		{ID: usPINSnapshotCustodianOctets, SQL: `SELECT octet_length(custodian) FROM hosted_membership_sync WHERE singleton=1`,
			Params:  []stmt.Param{},
			Cohorts: []stmt.Cohort{{Name: "single", Bindings: []any{}}}, Plan: custodianPlan},
		{ID: usPINSnapshotProfilesOctets, SQL: `SELECT octet_length(p.id),octet_length(p.name),octet_length(p.art),coalesce(octet_length(p.allowed_libraries),0),p.position
FROM direct_profiles AS p INDEXED BY direct_profiles_account
WHERE p.account_id=? AND p.deleted=0 ORDER BY p.position,p.id LIMIT ?`,
			Params:  []stmt.Param{text("account"), integer("row_limit")},
			Cohorts: []stmt.Cohort{{Name: "caller", Bindings: []any{"issuer", int64(65)}}}, Plan: profilesPlan},
		{ID: usPINSnapshotAccount, SQL: `SELECT a.username,a.profile_id,m.role,m.revision,m.disabled,m.allowed_libraries
FROM accounts AS a INDEXED BY sqlite_autoindex_accounts_1
CROSS JOIN direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 ON m.account_id=a.id
WHERE a.id=?`,
			Params:  []stmt.Param{text("account")},
			Cohorts: []stmt.Cohort{{Name: "caller", Bindings: []any{"issuer"}}}, Plan: accountPlan},
		{ID: usPINSnapshotFamily, SQL: `SELECT f.role,f.remember_until,f.manage_until,length(p.pin_hash),p.is_primary
FROM authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1
CROSS JOIN direct_profiles AS p INDEXED BY sqlite_autoindex_direct_profiles_2 ON p.account_id=f.account_id
WHERE f.id=? AND f.account_id=? AND p.id=?`,
			Params:  []stmt.Param{text("family"), text("account"), text("profile")},
			Cohorts: []stmt.Cohort{{Name: "caller", Bindings: []any{"issuer-family", "issuer", "issuer-profile"}}}, Plan: familyPlan},
		{ID: usPINSnapshotPolicy, SQL: `SELECT p.require_two_step,p.pin_remember_days,s.sign_in_kind
FROM identity_sign_in_policy AS p
CROSS JOIN identity_state AS s ON s.singleton=1
WHERE p.singleton=1`,
			Params:  []stmt.Param{},
			Cohorts: []stmt.Cohort{{Name: "single", Bindings: []any{}}}, Plan: policyPlan},
		{ID: usPINSnapshotFactor, SQL: `SELECT 1 FROM identity_account_factors WHERE account_id=? AND confirmed=1`,
			Params:  []stmt.Param{text("account")},
			Cohorts: []stmt.Cohort{{Name: "caller", Bindings: []any{"issuer"}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_identity_account_factors_1"}, Seeks: []string{"account_id=?"}}},
		{ID: usPINSnapshotHosted, SQL: `SELECT hosted_account_id FROM account_portico_links INDEXED BY sqlite_autoindex_account_portico_links_1 WHERE account_id=?`,
			Params:  []stmt.Param{text("account")},
			Cohorts: []stmt.Cohort{{Name: "caller", Bindings: []any{"issuer"}}}, Plan: hostedPlan},
		{ID: usPINSnapshotCustodian, SQL: `SELECT custodian FROM hosted_membership_sync WHERE singleton=1`,
			Params:  []stmt.Param{},
			Cohorts: []stmt.Cohort{{Name: "single", Bindings: []any{}}}, Plan: custodianPlan},
		{ID: usPINSnapshotProfiles, SQL: `SELECT p.id,p.name,p.is_primary,p.position,p.art,p.revision,p.pin_revision,length(p.pin_hash),p.allowed_libraries
FROM direct_profiles AS p INDEXED BY direct_profiles_account
WHERE p.account_id=? AND p.deleted=0 ORDER BY p.position,p.id LIMIT ?`,
			Params:  []stmt.Param{text("account"), integer("row_limit")},
			Cohorts: []stmt.Cohort{{Name: "caller", Bindings: []any{"issuer", int64(65)}}}, Plan: profilesPlan},
	}
	for n := range defs {
		defs[n].Kind = stmt.Read
		defs[n].Owner = "store/writer US PIN snapshot"
		defs[n].CallSites = []string{"store/writer/us_pin_result_variants.go"}
	}
	return defs
}

// captureUSPINSelfSnapshot freezes the CALLER's complete POST-EFFECT
// DirectSnapshot for a fresh own-account 532/533 original (including a PINReset
// through EditDirectProfile), exactly the identity service's directSnapshotTx
// shape the edit response returns. It is the 541 caller-snapshot primitive,
// copied for this purpose; it never returns the edited target's MemberProfile
// and never runs on replay.
//
// Authority binding: every key comes from the SAME-FRAME rechecked US-01
// stamp's validated issuer (account/profile/lifetime/family), never the frozen
// command's claimed issuer and never a caller-supplied boolean. CanManage,
// Capabilities, ManagePINRequired, TwoStepSetupRequired and CustodyPending are
// derived from current native facts with directSnapshotTx's own rules.
//
// Resource order: INTEGER-only octet/count preflights over the exact selected
// keys (including the repeated 43-octet Token() ids), overflow-checked sum,
// funded-grant charge, and only then any TEXT/BLOB Scan, JSON decode, slice or
// DTO allocation. The live profile read uses the direct_profiles_account index
// (deleted=0, position,id) with cap+1 refusal, so retired rows never consume the
// cap and a live row is never silently dropped.
func captureUSPINSelfSnapshot(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, variant usPINResultVariant, target usPINFenceScope, v usPINSavedResult, nowMS int64) (out *USDirectSnapshotImage, charged int64, err error) {
	var done struct {
		image   *USDirectSnapshotImage
		charged int64
	}
	defer func() {
		value, e := finishCommand(ctx, commandLifetime(turn), done, err)
		out, charged, err = value.image, value.charged, e
	}()
	if turn == nil || turn.transaction == nil || a == nil || (v.kind != usPINSet && v.kind != usPINClear) {
		return nil, 0, ErrInvalid
	}
	issuer := a.proof.witness.issuer
	if !usIdentifier(issuer.account) || !usIdentifier(issuer.profile) || !usIdentifier(issuer.family) {
		return nil, 0, ErrInvalid
	}
	// This loader builds only the caller-snapshot arm. Refuse the administrative
	// arm by name rather than building a DirectSnapshot under the other arm.
	if variant != usPINVariantCallerSnapshot {
		return nil, 0, errUSPINResultArm
	}
	// The snapshot is the caller's own account. A cross-account image is the
	// administrative MemberProfile arm, not this one.
	if target.account != issuer.account || v.account != issuer.account || v.profile != target.profile || v.lifetime != target.lifetime {
		return nil, 0, ErrDisclosureDenied
	}
	want, err := deriveUSPINResultVariant(v.kind, issuer.account, target.account)
	if err != nil || want != usPINVariantCallerSnapshot {
		return nil, 0, ErrDisclosureDenied
	}
	if nowMS < 0 {
		return nil, 0, ErrInvalid
	}

	// Integer-only preflight. No variable column is materialized yet.
	var preflight struct {
		account  [4]int64
		family   [3]int64
		policy   [2]int64
		hosted   [2]int64
		profiles int64
		rows     int64
	}
	row, err := usPolicyRow(ctx, turn, usPINSnapshotAccountOctets, issuer.account)
	if err != nil {
		return nil, 0, err
	}
	var usernameN, primaryN, roleN, librariesN, revision, disabled sql.NullInt64
	if err = row.Scan(&usernameN, &primaryN, &roleN, &librariesN, &revision, &disabled); err != nil {
		return nil, 0, errors.Join(ErrResolveRequired, err)
	}
	for n, value := range []sql.NullInt64{usernameN, primaryN, roleN, librariesN} {
		if preflight.account[n], err = usPolicyOctet(value); err != nil {
			return nil, 0, err
		}
	}
	if !revision.Valid || revision.Int64 < 1 || !disabled.Valid || disabled.Int64 != 0 {
		return nil, 0, ErrResolveRequired
	}
	row, err = usPolicyRow(ctx, turn, usPINSnapshotFamilyOctets, issuer.family, issuer.account, issuer.profile)
	if err != nil {
		return nil, 0, err
	}
	var familyRoleN, rememberN, manageN, pinN, primaryInt sql.NullInt64
	if err = row.Scan(&familyRoleN, &rememberN, &manageN, &pinN, &primaryInt); err != nil {
		return nil, 0, errors.Join(ErrResolveRequired, err)
	}
	for n, value := range []sql.NullInt64{familyRoleN, rememberN, manageN} {
		if preflight.family[n], err = usPolicyOctet(value); err != nil {
			return nil, 0, err
		}
	}
	if !primaryInt.Valid || (primaryInt.Int64 != 0 && primaryInt.Int64 != 1) {
		return nil, 0, ErrResolveRequired
	}
	row, err = usPolicyRow(ctx, turn, usPINSnapshotPolicyOctets)
	if err != nil {
		return nil, 0, err
	}
	var twoStepN, rememberDays, kindN sql.NullInt64
	if err = row.Scan(&twoStepN, &rememberDays, &kindN); err != nil {
		return nil, 0, errors.Join(ErrResolveRequired, err)
	}
	if preflight.policy[0], err = usPolicyOctet(twoStepN); err != nil {
		return nil, 0, err
	}
	if preflight.policy[1], err = usPolicyOctet(kindN); err != nil {
		return nil, 0, err
	}
	if !rememberDays.Valid || rememberDays.Int64 < 0 {
		return nil, 0, ErrResolveRequired
	}
	hostedPresent := false
	hostedRow, err := usPolicyRow(ctx, turn, usPINSnapshotHostedOctets, issuer.account)
	if err != nil {
		return nil, 0, err
	}
	var hostedN sql.NullInt64
	switch scan := hostedRow.Scan(&hostedN); {
	case errors.Is(scan, sql.ErrNoRows):
		// No Hosted link: both facts are absent, exactly as directSnapshotTx.
	case scan != nil:
		return nil, 0, errors.Join(ErrResolveRequired, scan)
	default:
		if preflight.hosted[0], err = usPolicyOctet(hostedN); err != nil {
			return nil, 0, err
		}
		hostedPresent = true
	}
	if hostedPresent {
		custodianRow, e := usPolicyRow(ctx, turn, usPINSnapshotCustodianOctets)
		if e != nil {
			return nil, 0, e
		}
		var custodianN sql.NullInt64
		switch scan := custodianRow.Scan(&custodianN); {
		case errors.Is(scan, sql.ErrNoRows), scan == nil && !custodianN.Valid:
			// No custodian row/fact: NULL custodian, exactly as the product.
		case scan != nil:
			return nil, 0, errors.Join(ErrResolveRequired, scan)
		default:
			if preflight.hosted[1], err = usPolicyOctet(custodianN); err != nil {
				return nil, 0, err
			}
		}
	}
	rows, err := usPolicyRows(ctx, turn, usPINSnapshotProfilesOctets, issuer.account, usPolicyImageMaxProfiles+1)
	if err != nil {
		return nil, 0, err
	}
	for rows.Next() {
		var idN, nameN, artN, librariesN, position sql.NullInt64
		if err = rows.Scan(&idN, &nameN, &artN, &librariesN, &position); err != nil {
			_ = rows.Close()
			return nil, 0, err
		}
		preflight.rows++
		if preflight.rows > usPolicyImageMaxProfiles {
			_ = rows.Close()
			return nil, 0, &Refusal{Code: RefusalCapacity}
		}
		for _, value := range []sql.NullInt64{idN, nameN, artN, librariesN} {
			n, e := usPolicyOctet(value)
			if e != nil {
				_ = rows.Close()
				return nil, 0, e
			}
			if preflight.profiles, err = usPolicyOctetsSum(preflight.profiles, n); err != nil {
				_ = rows.Close()
				return nil, 0, err
			}
		}
		_ = position
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, 0, err
	}
	if err = rows.Close(); err != nil {
		return nil, 0, err
	}
	raw := int64(0)
	for _, n := range preflight.account {
		if raw, err = usPolicyOctetsSum(raw, n); err != nil {
			return nil, 0, err
		}
	}
	for _, n := range preflight.family {
		if raw, err = usPolicyOctetsSum(raw, n); err != nil {
			return nil, 0, err
		}
	}
	for _, n := range preflight.policy {
		if raw, err = usPolicyOctetsSum(raw, n); err != nil {
			return nil, 0, err
		}
	}
	for _, n := range preflight.hosted {
		if raw, err = usPolicyOctetsSum(raw, n); err != nil {
			return nil, 0, err
		}
	}
	if raw, err = usPolicyOctetsSum(raw, preflight.profiles); err != nil {
		return nil, 0, err
	}
	// The image also carries three fixed strings no source column supplies:
	// Authority is the literal "local", and ServerID/Account.ID are the validated
	// issuer's identity Token() values (32 random bytes in base64.RawURLEncoding,
	// so 43 octets each). Preflight their exact lengths on the raw side before
	// the comparison; otherwise an ordinary PIN edit would exceed its own source
	// octets and refuse after the effect.
	for _, fixed := range []string{"local", issuer.server, issuer.account} {
		if raw, err = usPolicyOctetsSum(raw, int64(len(fixed))); err != nil {
			return nil, 0, err
		}
	}
	// Both the retained native copy and the decoded DTO copy are resident while
	// the image is built; per-profile slice overhead and the image header are
	// additive. The charge is proved BEFORE any materialization.
	charged = raw
	if charged, err = usPolicyOctetsSum(charged, raw); err != nil {
		return nil, 0, err
	}
	for n := preflight.rows; n > 0; n-- {
		if charged, err = usPolicyOctetsSum(charged, usPolicyImageProfileBytes); err != nil {
			return nil, 0, err
		}
	}
	if charged, err = usPolicyOctetsSum(charged, usPolicyImageHeaderBytes); err != nil {
		return nil, 0, err
	}
	if err = a.budget.check(ctx, turn, charged); err != nil {
		return nil, 0, err
	}
	if err = usPINDispatchFund(a, charged); err != nil {
		return nil, 0, err
	}
	done.charged = charged

	// Charged. Only now may variable source become Go memory.
	row, err = usPolicyRow(ctx, turn, usPINSnapshotAccount, issuer.account)
	if err != nil {
		return nil, 0, err
	}
	var username, primary, role, libraries string
	if err = row.Scan(&username, &primary, &role, &revision, &disabled, &libraries); err != nil {
		return nil, 0, errors.Join(ErrResolveRequired, err)
	}
	if primary != issuer.profile || (role != "owner" && role != "admin" && role != "member") || !revision.Valid || revision.Int64 < 1 || !disabled.Valid || disabled.Int64 != 0 {
		return nil, 0, ErrResolveRequired
	}
	accountLibraries, err := decodeUSLibraryIDs(libraries)
	if err != nil {
		return nil, 0, err
	}
	if accountLibraries == nil {
		accountLibraries = []string{}
	}

	row, err = usPolicyRow(ctx, turn, usPINSnapshotFamily, issuer.family, issuer.account, issuer.profile)
	if err != nil {
		return nil, 0, err
	}
	var familyRole, rememberUntil, manageUntil string
	var pinLength sql.NullInt64
	if err = row.Scan(&familyRole, &rememberUntil, &manageUntil, &pinLength, &primaryInt); err != nil {
		return nil, 0, errors.Join(ErrResolveRequired, err)
	}
	if !usPolicyFamilyRole(familyRole) || !primaryInt.Valid || (primaryInt.Int64 != 0 && primaryInt.Int64 != 1) {
		return nil, 0, ErrResolveRequired
	}
	// directSnapshotTx tests PIN presence (`pin_hash IS NOT NULL`), never a
	// non-empty length: an empty stored hash still protects the profile.
	primaryPIN := pinLength.Valid

	row, err = usPolicyRow(ctx, turn, usPINSnapshotPolicy)
	if err != nil {
		return nil, 0, err
	}
	var requireTwoStep, signInKind string
	if err = row.Scan(&requireTwoStep, &rememberDays, &signInKind); err != nil {
		return nil, 0, errors.Join(ErrResolveRequired, err)
	}
	if (requireTwoStep != "nobody" && requireTwoStep != "administrators" && requireTwoStep != "everyone") ||
		(signInKind != "local" && signInKind != "portico") || !rememberDays.Valid || rememberDays.Int64 < 0 {
		return nil, 0, ErrResolveRequired
	}
	// missingRequiredFactorTx, from current native facts only.
	required := usPolicyTwoStepRequired(requireTwoStep, signInKind, role)
	factorConfirmed := false
	if familyRole == "account" && required {
		factor, e := usPolicyRow(ctx, turn, usPINSnapshotFactor, issuer.account)
		if e != nil {
			return nil, 0, e
		}
		var present int64
		switch scan := factor.Scan(&present); {
		case scan == nil && present == 1:
			factorConfirmed = true
		case scan == nil, errors.Is(scan, sql.ErrNoRows):
		default:
			return nil, 0, errors.Join(ErrResolveRequired, scan)
		}
	}
	// The US-01 stamp always mints familyRole='owner' (usAuthorityFamily
	// requires f.role='owner'), so the account-scope and two-step branches
	// below are unreachable through this loader. The PIN branch is NOT
	// unreachable: this loader runs AFTER the effect, and an own-primary SET
	// leaves pin_hash non-NULL even though the stamp required it NULL when the
	// turn was admitted (us_authority.go usAuthorityPrincipal). TestUSPIN-
	// PhysicalOriginalReplayAndACK checks the post-effect PIN facts against the
	// independent productSnapshot oracle; TestUSPolicyDispatchSnapshotManagement
	// exercises the pure helper's account-scope, two-step and manage-lock
	// branches directly.
	canManage, twoStep, managePINRequired := usPolicySnapshotManage(familyRole,
		primaryInt.Int64 == 1, primaryPIN, required, factorConfirmed, rememberUntil, manageUntil,
		time.UnixMilli(nowMS).UTC().Format(time.RFC3339))

	hostedAccount := ""
	custodian := sql.NullString{}
	hostedRow, err = usPolicyRow(ctx, turn, usPINSnapshotHosted, issuer.account)
	if err != nil {
		return nil, 0, err
	}
	switch scan := hostedRow.Scan(&hostedAccount); {
	case errors.Is(scan, sql.ErrNoRows):
		hostedPresent = false
	case scan != nil:
		return nil, 0, errors.Join(ErrResolveRequired, scan)
	}
	if hostedPresent {
		custodianRow, e := usPolicyRow(ctx, turn, usPINSnapshotCustodian)
		if e != nil {
			return nil, 0, e
		}
		switch scan := custodianRow.Scan(&custodian); {
		case errors.Is(scan, sql.ErrNoRows):
		case scan != nil:
			return nil, 0, errors.Join(ErrResolveRequired, scan)
		}
	}
	custodyPending := false
	if hostedPresent && role == "owner" {
		// The product reads custodian only for a Hosted owner. A NULL custodian
		// is NOT pending; an empty or different string is.
		custodyPending = custodian.Valid && custodian.String != hostedAccount
	}

	profiles := make([]USDirectProfileImage, 0, 8)
	rows, err = usPolicyRows(ctx, turn, usPINSnapshotProfiles, issuer.account, usPolicyImageMaxProfiles+1)
	if err != nil {
		return nil, 0, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	edited := false
	for rows.Next() {
		var p USDirectProfileImage
		var pinHash sql.NullInt64
		var libraries sql.NullString
		if err = rows.Scan(&p.ID, &p.Name, &p.Primary, &p.Position, &p.Art, &p.Revision, &p.PINRevision, &pinHash, &libraries); err != nil {
			return nil, 0, err
		}
		if len(profiles) >= int(usPolicyImageMaxProfiles) {
			return nil, 0, &Refusal{Code: RefusalCapacity}
		}
		p.PINRequired = pinHash.Valid
		if libraries.Valid {
			ids, e := decodeUSLibraryIDs(libraries.String)
			if e != nil {
				return nil, 0, e
			}
			p.AllowedLibraries = ids
		}
		if p.ID == v.profile {
			// The edited profile in the post-effect snapshot must be exactly the
			// source this result saved: an image from before the effect (or one
			// that disagrees with the receipt scalars) is never accepted.
			edited = true
			if p.Revision != v.revision || p.PINRevision != v.pinRevision || p.PINRequired != v.pinRequired {
				return nil, 0, ErrResolveRequired
			}
		}
		if p.AllowedLibraries == nil {
			p.AllowedLibraries = []string{}
		}
		profiles = append(profiles, p)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	if !edited {
		return nil, 0, ErrResolveRequired
	}

	out = &USDirectSnapshotImage{
		TwoStepSetupRequired: twoStep,
		Authority:            "local",
		ServerID:             issuer.server,
		Account: USDirectAccountImage{ID: issuer.account, Username: username, PrimaryProfileID: primary, Role: role,
			Revision: revision.Int64, Disabled: false, AllowedLibraries: accountLibraries,
			DisabledAt: "", DisabledReason: "", HostedAccountID: hostedAccount},
		Profiles:          profiles,
		CanManage:         canManage,
		Capabilities:      usPolicySnapshotCapabilities(canManage, role),
		CustodyPending:    custodyPending,
		RememberProfiles:  rememberDays.Int64 > 0,
		ManagePINRequired: managePINRequired,
	}
	// Frozen carrier octet contract on the fresh image: the image can never cost
	// more variable bytes than the preflighted source it was decoded from.
	imageOctets, e := USDirectSnapshotImageOctets(*out)
	if e != nil || imageOctets > raw {
		return nil, 0, errors.Join(ErrResolveRequired, e)
	}
	done.image = out
	return out, charged, nil
}
