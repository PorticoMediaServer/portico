package remotesources

import (
	"context"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type smbPin struct {
	endpoint remotemedia.NativeEndpoint
	approval remotemedia.Approval
	address  netip.Addr
}

func chooseSMBPin(checked []netip.Addr, saved remotemedia.Approval) (netip.Addr, error) {
	if len(checked) == 0 {
		return netip.Addr{}, storage.ErrRemoteConfig
	}
	approved := map[string]bool{}
	for _, address := range saved.Addresses {
		approved[address] = true
	}
	ordered := append([]netip.Addr(nil), checked...)
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if approved[a.String()] != approved[b.String()] {
			return approved[a.String()]
		}
		return a.Compare(b) < 0
	})
	// A process keeps one checked address for all its internal reconnects.
	// This deliberately loses multi-address failover; a fresh launch re-pins.
	return ordered[0], nil
}

func checkSMBPins(ctx context.Context, policy remotemedia.Policy, pins []smbPin) error {
	for _, pin := range pins {
		checked, err := policy.CheckNativeEndpoint(ctx, pin.endpoint, pin.approval)
		if err != nil {
			return err
		}
		found := false
		for _, address := range checked {
			found = found || address == pin.address
		}
		if !found {
			return storage.ErrRemoteChanged
		}
	}
	return ctx.Err()
}

// pinSMBConfiguration edits only the transient copy, after callers have checked
// the original material. INI works for every accepted leaf section name, unlike
// remote-specific environment keys. Child environments exclude ambient rclone
// options, so nothing can override these hosts at launch.
func pinSMBConfiguration(raw []byte, hosts map[string]netip.Addr) ([]byte, error) {
	sections, err := parseServeConfiguration(raw)
	if err != nil {
		return nil, err
	}
	updates := map[string]map[string]string{}
	for name, address := range hosts {
		fields := sections[name]
		if fields["type"] != "smb" || !address.IsValid() || address.Zone() != "" {
			return nil, storage.ErrRemoteConfig
		}
		host := address.String()
		if address.Is6() {
			// Both qualified rclone versions concatenate host + ":" + port.
			host = "[" + host + "]"
		}
		values := map[string]string{"host": host}
		if fields["use_kerberos"] != "" {
			kerberos, err := strconv.ParseBool(fields["use_kerberos"])
			if err != nil {
				return nil, storage.ErrRemoteConfig
			}
			if kerberos && fields["spn"] == "" {
				// backend/smb/connpool.go at v1.71.1 and v1.75.2 defaults to
				// cifs/<host>, without the port. Preserve the original identity.
				values["spn"] = "cifs/" + fields["host"]
			}
		}
		updates[name] = values
	}
	var out strings.Builder
	section := ""
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]")
			out.WriteString(line + "\n")
			// Emit replacements once and omit their original lines below.
			for _, key := range []string{"host", "spn"} {
				if value, ok := updates[section][key]; ok {
					out.WriteString(key + "=" + value + "\n")
				}
			}
			continue
		}
		key, _, assignment := strings.Cut(trimmed, "=")
		if _, replace := updates[section][strings.ToLower(strings.TrimSpace(key))]; assignment && replace {
			continue
		}
		out.WriteString(line + "\n")
	}
	if out.Len() > 64<<10 {
		return nil, storage.ErrRemoteConfig
	}
	return []byte(out.String()), nil
}
