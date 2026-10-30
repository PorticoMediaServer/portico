package identity

import (
	"context"
	"database/sql"
	"time"
)

type issuingDeviceKey struct{}

// vouchedDeviceKey marks a paired Cast target. Its playback grant is narrow.
type vouchedDeviceKey struct{}

// WithVouchedDevice records that the pairing session vouched for the target.
func WithVouchedDevice(ctx context.Context) context.Context {
	return context.WithValue(ctx, vouchedDeviceKey{}, true)
}

type issuingDevice struct {
	registration DeviceRegistration
	peer         string
	deviceID     string
}

// WithIssuingDevice carries a client's installation claim to the one
// transaction that binds the device and creates its authorization family.
// The installation ID is a random client secret, never an IP fingerprint.
func WithIssuingDevice(ctx context.Context, registration DeviceRegistration, peer string) (context.Context, error) {
	if !registration.valid() {
		return ctx, ErrDeviceInput
	}
	return context.WithValue(ctx, issuingDeviceKey{}, issuingDevice{registration: registration, peer: peer}), nil
}

// withExistingDevice carries a previously proved account session's device into
// profile selection. The request cannot substitute another installation ID.
func withExistingDevice(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, issuingDeviceKey{}, issuingDevice{deviceID: id})
}

func (s *Service) issuingDeviceTx(ctx context.Context, tx *sql.Tx, authority, account string) (Device, error) {
	claim, _ := ctx.Value(issuingDeviceKey{}).(issuingDevice)
	if claim.deviceID != "" {
		device, err := s.deviceTx(ctx, tx, authority, account, "", claim.deviceID)
		if err != nil {
			return Device{}, err
		}
		return device, nil
	}
	q := claim.registration
	caller := deviceCaller{authority: authority, account: account}
	if q.InstallationID == "" {
		// A sign-in that names no installation (another client, a script; Cast
		// pairing and internal callers) still gets an individually revocable
		// device record, under an installation id the server makes and
		// returns. Each such sign-in remains independently revocable.
		q = DeviceRegistration{InstallationID: unnamedInstallationPrefix + Token(), Name: "Portico device", Platform: "unknown", App: "portico"}

	}
	if !q.valid() {
		return Device{}, ErrDeviceInput
	}
	device, err := s.registerDeviceTx(ctx, tx, caller, q, claim.peer)
	if err != nil {
		return device, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE identity_devices SET last_seen=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339), device.ID); err != nil {
		return Device{}, err
	}
	return device, nil
}

// unnamedInstallationPrefix marks an installation id the server made for a
// sign-in that did not name one.
const unnamedInstallationPrefix = "unnamed-"
