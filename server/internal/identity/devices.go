package identity

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrDeviceInput = requestinput.New("invalid device record")
var ErrDeviceUnknown = errors.New("This device is not registered on this account.")

// A device record is what a person recognises on a sign-out screen: "the TV in the
// living room", not a token family id. Families remain the unit of revocation, so a
// device record owns the families issued to it and signing a device out revokes
// exactly those. The record is keyed by an installation id the client generates
// once and keeps; it is not a fingerprint and carries no identifying power of its
// own, which is why an unknown installation id is registered rather than rejected.

// Installation ids are random installation secrets, never device fingerprints.
func validInstallationID(v string) bool { return len(v) >= 32 && validFamilyID(v) }

// ValidInstallationID applies the device registry's existing installation grammar
// at protocol boundaries. Installation ids are opaque; they need not encode 32 bytes.
func ValidInstallationID(v string) bool { return validInstallationID(v) }

// Device is the record a person sees and manages.
type Device struct {
	ID              string `json:"id"`
	Authority       string `json:"authority"`
	InstallationID  string `json:"installationId"`
	Name            string `json:"name"`
	Platform        string `json:"platform"`
	App             string `json:"app"`
	AppVersion      string `json:"appVersion"`
	IP              string `json:"ip"`
	FirstSeen       string `json:"firstSeen"`
	LastSeen        string `json:"lastSeen"`
	LastProfileID   string `json:"lastProfileId,omitempty"`
	RememberAccount bool   `json:"rememberAccount"`
	Current         bool   `json:"current"`
	Sessions        int    `json:"sessions"`
}

// DeviceRegistration is what a client sends at sign-in. Everything in it is a
// client assertion; none of it grants anything.
type DeviceRegistration struct {
	InstallationID string `json:"installationId"`
	Name           string `json:"name"`
	Platform       string `json:"platform"`
	App            string `json:"app"`
	AppVersion     string `json:"appVersion"`
}

func deviceText(v string, max int) (string, bool) {
	if !utf8.ValidString(v) || strings.ContainsFunc(v, func(r rune) bool { return r < 32 || r == 127 }) {
		return "", false
	}
	v = strings.TrimSpace(v)
	if len(v) > max {
		return "", false
	}
	return v, true
}

// clientIP keeps only the address, never the port, and never a proxy header the
// caller supplied. The caller resolves the peer; this function only normalises it.
func clientIP(raw string) string {
	if raw == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	if parsed := net.ParseIP(raw); parsed != nil {
		return parsed.String()
	}
	return ""
}

func (q DeviceRegistration) valid() bool {
	if !validInstallationID(q.InstallationID) {
		return false
	}
	for _, pair := range [][2]any{{q.Name, 120}, {q.Platform, 64}, {q.App, 64}, {q.AppVersion, 64}} {
		if _, ok := deviceText(pair[0].(string), pair[1].(int)); !ok {
			return false
		}
	}
	return true
}

// RegisterDevice records or refreshes the device behind an account session and
// returns it. A device the owner has denied is refused here, before any viewing
// session can be minted for it.
func (s *Service) RegisterDevice(ctx context.Context, bearer string, q DeviceRegistration, peer string) (Device, error) {
	if !q.valid() {
		return Device{}, ErrDeviceInput
	}
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return Device{}, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	c, e := s.deviceCallerTx(ctx, tx, bearer, false)
	if e != nil {
		return Device{}, e
	}
	device, e := s.registerDeviceTx(ctx, tx, c, q, peer)
	if e != nil {
		return device, e
	}
	// A pending or denied registration is still recorded: the owner cannot approve
	// a device whose arrival was rolled back, and a denied device that vanished
	// would come back as a stranger on its next attempt.
	if commitErr := gated.Commit(); commitErr != nil {
		return device, commitErr
	}
	return device, e
}

func (s *Service) registerDeviceTx(ctx context.Context, tx *sql.Tx, c deviceCaller, q DeviceRegistration, peer string) (Device, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	name, _ := deviceText(q.Name, 120)
	if name == "" {
		name = "Portico"
	}
	platform, _ := deviceText(q.Platform, 64)
	app, _ := deviceText(q.App, 64)
	version, _ := deviceText(q.AppVersion, 64)
	if _, e := tx.ExecContext(ctx, `INSERT INTO identity_devices(id,authority,account_id,installation_id,name,platform,app,app_version,ip,first_seen,last_seen) VALUES(?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(authority,account_id,installation_id) DO UPDATE SET platform=excluded.platform,app=excluded.app,app_version=excluded.app_version,ip=excluded.ip,last_seen=excluded.last_seen`,
		Token(), c.authority, c.account, q.InstallationID, name, platform, app, version, clientIP(peer), now, now); e != nil {
		return Device{}, e
	}
	// A real authenticated enrollment is the one place an installation lifetime
	// is created (P2-62). It is a watermark for the registration handshake, not
	// a grant, and it never rotates an existing lifetime.
	if e := s.enrollInstallationTx(ctx, tx, q.InstallationID); e != nil {
		return Device{}, e
	}
	device, e := s.deviceTx(ctx, tx, c.authority, c.account, q.InstallationID, "")
	if e != nil {
		return device, e
	}
	return device, nil
}

const deviceColumns = `id,authority,installation_id,name,platform,app,app_version,ip,first_seen,last_seen,last_profile_id,remember_account`

func scanDevice(row interface{ Scan(...any) error }) (Device, error) {
	var d Device
	e := row.Scan(&d.ID, &d.Authority, &d.InstallationID, &d.Name, &d.Platform, &d.App, &d.AppVersion, &d.IP, &d.FirstSeen, &d.LastSeen, &d.LastProfileID, &d.RememberAccount)
	return d, e
}

func (s *Service) deviceTx(ctx context.Context, tx *sql.Tx, authority, account, installation, id string) (Device, error) {
	device, e := scanDevice(tx.QueryRowContext(ctx, `SELECT `+deviceColumns+` FROM identity_devices WHERE authority=? AND account_id=? AND (?='' OR installation_id=?) AND (?='' OR id=?)`, authority, account, installation, installation, id, id))
	if errors.Is(e, sql.ErrNoRows) {
		return Device{}, ErrDeviceUnknown
	}
	return device, e
}

// BindDeviceFamily verifies an existing binding for older callers. Issuance
// creates the binding atomically; a bearer may never move it to another device.
func (s *Service) BindDeviceFamily(ctx context.Context, bearer, installation, family string) error {
	return s.bindDeviceFamily(ctx, bearer, installation, family, "")
}

func (s *Service) BindDeviceFamilyForDevice(ctx context.Context, bearer, deviceID, installation, family string) error {
	if !validFamilyID(deviceID) {
		return ErrDeviceInput
	}
	return s.bindDeviceFamily(ctx, bearer, installation, family, deviceID)
}

func (s *Service) bindDeviceFamily(ctx context.Context, bearer, installation, family, pathID string) error {
	if !validInstallationID(installation) || !validFamilyID(family) {
		return ErrDeviceInput
	}
	gated2, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	c, e := s.deviceCallerTx(ctx, tx, bearer, false)
	if e != nil {
		return e
	}
	device, e := s.deviceTx(ctx, tx, c.authority, c.account, installation, "")
	if e != nil {
		return e
	}
	if pathID != "" && device.ID != pathID {
		return ErrDeviceUnknown
	}
	var bound string
	if e = tx.QueryRowContext(ctx, `SELECT b.device_id FROM authorization_session_families f JOIN identity_device_families b ON b.family_id=f.id WHERE f.id=? AND f.account_id=? AND f.authority=? AND f.revoked=0`, family, c.account, c.authority).Scan(&bound); e != nil {
		return e
	}
	if bound != device.ID {
		return ErrUnauthorized
	}
	if _, e = tx.ExecContext(ctx, `UPDATE identity_devices SET last_seen=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339), device.ID); e != nil {
		return e
	}
	return gated2.Commit()
}

// Devices lists the account's devices, newest activity first. The device the
// calling session is bound to is marked current; the session says which that
// is, so the caller sends no installation id to find out (it used to travel in
// the address, where proxies and logs keep it). An installation id is what a
// device renews its sign-in with, so only the caller's own is shown: every
// other device's is left out.
func (s *Service) Devices(ctx context.Context, bearer, _ string) ([]Device, error) {
	gated3, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return nil, e
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	c, e := s.deviceCallerTx(ctx, tx, bearer, true)
	if e != nil {
		return nil, e
	}
	var currentDevice string
	if e = tx.QueryRowContext(ctx, `SELECT b.device_id FROM authorization_family_tokens t JOIN identity_device_families b ON b.family_id=t.family_id WHERE t.token_hash=?`, Digest(bearer)).Scan(&currentDevice); e != nil && !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT `+deviceColumns+`,(SELECT count(*) FROM identity_device_families f JOIN authorization_session_families a ON a.id=f.family_id AND a.revoked=0 AND a.authorization_horizon>? WHERE f.device_id=identity_devices.id) FROM identity_devices WHERE authority=? AND account_id=? AND platform<>'api' ORDER BY last_seen DESC,id`, time.Now().UTC().Format(time.RFC3339), c.authority, c.account)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		var d Device
		if e = rows.Scan(&d.ID, &d.Authority, &d.InstallationID, &d.Name, &d.Platform, &d.App, &d.AppVersion, &d.IP, &d.FirstSeen, &d.LastSeen, &d.LastProfileID, &d.RememberAccount, &d.Sessions); e != nil {
			return nil, e
		}
		d.Current = d.ID == currentDevice && currentDevice != ""
		if !d.Current {
			d.InstallationID = ""
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeviceEdit renames a device, records the profile it last used, and sets whether
// the account may be remembered on it. The rename is the only field a person edits
// directly; the rest follow use.
type DeviceEdit struct {
	Name            *string `json:"name,omitempty"`
	LastProfileID   *string `json:"lastProfileId,omitempty"`
	RememberAccount *bool   `json:"rememberAccount,omitempty"`
}

// EditDevice applies a device edit. Changing Trusted or approval is an owner
// action and goes through ApproveDevice instead.
func (s *Service) EditDevice(ctx context.Context, bearer, id string, q DeviceEdit) (Device, error) {
	if !validFamilyID(id) || q.Name == nil && q.LastProfileID == nil && q.RememberAccount == nil {
		return Device{}, ErrDeviceInput
	}
	gated4, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return Device{}, e
	}
	tx := gated4.Tx()
	defer gated4.Rollback()
	c, e := s.deviceCallerTx(ctx, tx, bearer, true)
	if e != nil {
		return Device{}, e
	}
	if _, e = s.deviceTx(ctx, tx, c.authority, c.account, "", id); e != nil {
		return Device{}, e
	}
	if q.Name != nil {
		name, ok := deviceText(*q.Name, 120)
		if !ok || name == "" {
			return Device{}, ErrDeviceInput
		}
		if _, e = tx.ExecContext(ctx, `UPDATE identity_devices SET name=? WHERE id=? AND authority=? AND account_id=?`, name, id, c.authority, c.account); e != nil {
			return Device{}, e
		}
	}
	if q.LastProfileID != nil {
		profile := *q.LastProfileID
		if profile != "" {
			// A local profile is checked against this server's own profile table. A Portico
			// Account's profiles live at the account service, so the only thing this server
			// can say about one is that it is well formed; it is a "resume here" hint, and it
			// authorizes nothing.
			if c.local() {
				var deleted int
				if e = tx.QueryRowContext(ctx, `SELECT deleted FROM direct_profiles WHERE account_id=? AND id=?`, c.account, profile).Scan(&deleted); e != nil || deleted != 0 {
					return Device{}, ErrDeviceInput
				}
			} else if !validFamilyID(profile) {
				return Device{}, ErrDeviceInput
			}
		}
		if _, e = tx.ExecContext(ctx, `UPDATE identity_devices SET last_profile_id=? WHERE id=? AND authority=? AND account_id=?`, profile, id, c.authority, c.account); e != nil {
			return Device{}, e
		}
	}
	if q.RememberAccount != nil {
		allowed, e := rememberAccountsTx(ctx, tx)
		if e != nil {
			return Device{}, e
		}
		if *q.RememberAccount && !allowed {
			return Device{}, ErrForbidden
		}
		if _, e = tx.ExecContext(ctx, `UPDATE identity_devices SET remember_account=? WHERE id=? AND authority=? AND account_id=?`, *q.RememberAccount, id, c.authority, c.account); e != nil {
			return Device{}, e
		}
		// The remembered-account list is what a browser shows before anyone signs in, and it
		// only ever holds this server's own accounts. A Portico Account is remembered by the
		// account service, so there is nothing here to forget.
		if !*q.RememberAccount && c.local() {
			if _, e = tx.ExecContext(ctx, `DELETE FROM identity_browser_accounts WHERE account_id=? AND installation_id=(SELECT installation_id FROM identity_devices WHERE id=?)`, c.account, id); e != nil {
				return Device{}, e
			}
		}
	}
	device, e := s.deviceTx(ctx, tx, c.authority, c.account, "", id)
	if e != nil {
		return device, e
	}
	return device, gated4.Commit()
}

// ApproveDevice resolves a pending device. Denying it also signs it out, because a
// device the owner refused must not keep a session it obtained before the policy
// was turned on.
func (s *Service) revokeDeviceFamiliesTx(ctx context.Context, tx *sql.Tx, c deviceCaller, device string, reason RevocationReason) error {
	rows, e := tx.QueryContext(ctx, `SELECT f.family_id FROM identity_device_families f JOIN authorization_session_families a ON a.id=f.family_id WHERE f.device_id=? AND a.authority=? AND a.account_id=? AND a.revoked=0`, device, c.authority, c.account)
	if e != nil {
		return e
	}
	var families []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		families = append(families, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, family := range families {
		if e = s.revokeFamiliesTx(ctx, tx, c.authority, c.account, "", family, reason); e != nil {
			return e
		}
	}
	// Remembered profile trust is a local-account idea: it is the PIN-free profile list this
	// server keeps for its own accounts.
	if c.local() {
		if _, e = tx.ExecContext(ctx, `DELETE FROM direct_profile_trust WHERE account_id=? AND installation_id=(SELECT installation_id FROM identity_devices WHERE id=?)`, c.account, device); e != nil {
			return e
		}
	}
	// A device that is signed out keeps no feed token: the Top Shelf must go dark with it.
	if _, e = tx.ExecContext(ctx, `DELETE FROM topshelf_tokens WHERE device_id=?`, device); e != nil {
		return e
	}
	return nil
}

// SignOutDevice revokes every family the device holds. The device record stays so
// the person keeps seeing it in the list, and so signing back in on it does not
// look like a new device to an owner watching approvals.
func (s *Service) SignOutDevice(ctx context.Context, bearer, id string) error {
	if !validFamilyID(id) {
		return ErrDeviceInput
	}
	gated6, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return e
	}
	tx := gated6.Tx()
	defer gated6.Rollback()
	c, e := s.deviceCallerTx(ctx, tx, bearer, true)
	if e != nil {
		return e
	}
	if _, e = s.deviceTx(ctx, tx, c.authority, c.account, "", id); e != nil {
		return e
	}
	if e = s.revokeDeviceFamiliesTx(ctx, tx, c, id, RevokedExplicitSignout); e != nil {
		return e
	}
	return gated6.Commit()
}

// ForgetDevice removes the record entirely, after signing it out.
func (s *Service) ForgetDevice(ctx context.Context, bearer, id string) error {
	if !validFamilyID(id) {
		return ErrDeviceInput
	}
	gated7, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return e
	}
	tx := gated7.Tx()
	defer gated7.Rollback()
	c, e := s.deviceCallerTx(ctx, tx, bearer, true)
	if e != nil {
		return e
	}
	device, e := s.deviceTx(ctx, tx, c.authority, c.account, "", id)
	if e != nil {
		return e
	}
	if e = s.revokeDeviceFamiliesTx(ctx, tx, c, id, RevokedExplicitSignout); e != nil {
		return e
	}
	if c.local() {
		if _, e = tx.ExecContext(ctx, `DELETE FROM identity_browser_accounts WHERE installation_id=? AND account_id=?`, device.InstallationID, c.account); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM identity_devices WHERE id=? AND authority=? AND account_id=?`, id, c.authority, c.account); e != nil {
		return e
	}
	return gated7.Commit()
}

// SignOutEverywhere revokes every family on the account and bumps the account
// epoch, which the accounts trigger turns into a full sweep of account sessions
// and remembered profile trust. It is the direct-server equivalent of the hosted
// "sign out of all devices".
func (s *Service) SignOutEverywhere(ctx context.Context, bearer string) error {
	gated8, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return e
	}
	tx := gated8.Tx()
	defer gated8.Rollback()
	c, e := s.directCallerTx(ctx, tx, bearer, true)
	if e != nil {
		return e
	}
	if e = s.revokeDirectWithReasonTx(ctx, tx, c.account.ID, "", "", RevokedExplicitSignout); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM identity_browser_accounts WHERE account_id=?`, c.account.ID); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE accounts SET epoch=epoch+1 WHERE id=?`, c.account.ID); e != nil {
		return e
	}
	return gated8.Commit()
}

// SwitchProfileFence ends the outgoing profile's playback on this device before a
// new profile takes it over.
//
// The v2 controller model offers two outcomes for an occurrence whose owner goes
// away: end it, or hand it to the incoming owner. Portico ends it. A handover
// would carry one profile's position, restrictions and personal state into
// another's session, which is exactly what profile separation exists to prevent —
// and a child profile inheriting a parent's occurrence would inherit content its
// own restrictions forbid. Ending goes through revokeDirectTx, so occurrences,
// queues and readers retire through OnFamilyRevokedTx rather than being orphaned.
func (s *Service) SwitchProfileFence(ctx context.Context, bearer, installation, outgoingProfile string) error {
	if !validInstallationID(installation) || outgoingProfile != "" && !validFamilyID(outgoingProfile) {
		return ErrDeviceInput
	}
	gated9, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return e
	}
	tx := gated9.Tx()
	defer gated9.Rollback()
	c, e := s.deviceCallerTx(ctx, tx, bearer, false)
	if e != nil {
		return e
	}
	device, e := s.deviceTx(ctx, tx, c.authority, c.account, installation, "")
	if e != nil {
		return e
	}
	rows, e := tx.QueryContext(ctx, `SELECT f.family_id FROM identity_device_families f JOIN authorization_session_families a ON a.id=f.family_id WHERE f.device_id=? AND a.authority=? AND a.account_id=? AND a.revoked=0 AND (?='' OR a.profile_id=?)`, device.ID, c.authority, c.account, outgoingProfile, outgoingProfile)
	if e != nil {
		return e
	}
	var families []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		families = append(families, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, family := range families {
		if e = s.revokeFamiliesTx(ctx, tx, c.authority, c.account, "", family, RevokedExplicitSignout); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE identity_devices SET last_seen=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339), device.ID); e != nil {
		return e
	}
	return gated9.Commit()
}
