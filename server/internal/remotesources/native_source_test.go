package remotesources

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Protocol/address/digest matching only. This is not a saved-owner or live NAS
// proof; the real save must produce the retained approval after its test.
func TestNativeSourceMaterialRequiresExactSavedProtocolAndBytes(t *testing.T) {
	ctx := context.Background()
	for _, scheme := range []string{"sftp", "smb"} {
		t.Run(scheme, func(t *testing.T) {
			configuration := []byte("[fixture]\ntype=" + scheme + "\nhost=192.168.42.7\nport=2222\n")
			endpoint := remotemedia.NativeEndpoint{Scheme: scheme, Host: "192.168.42.7", Port: 2222}
			approval, err := (remotemedia.Policy{}).PrepareNativeApproval(ctx, endpoint)
			if err != nil {
				t.Fatal(err)
			}
			authority := &nativeSourceAuthority{endpoint: endpoint, config: nativeSourceConfiguration{
				Host: endpoint.Host, Port: endpoint.Port, Approval: approval, ConfigurationDigest: hex.EncodeToString(sha256Sum(configuration)),
			}}
			service := &Service{}
			if err = service.checkNativeMaterial(ctx, authority, scheme, endpoint.Host, "2222", configuration); err != nil {
				t.Fatal("saved native endpoint facts were refused", err)
			}
			for _, host := range []string{"192.168.42.8", "127.0.0.1", "169.254.169.254", "169.254.1.1"} {
				if err = service.checkNativeMaterial(ctx, authority, scheme, host, "2222", configuration); !errors.Is(err, storage.ErrRemoteChanged) {
					t.Fatal("edited endpoint reused its old approval", host, err)
				}
			}
			if err = service.checkNativeMaterial(ctx, authority, scheme, endpoint.Host, "2223", configuration); !errors.Is(err, storage.ErrRemoteChanged) {
				t.Fatal("edited port reused its old approval", err)
			}
			if err = service.checkNativeMaterial(ctx, authority, scheme, endpoint.Host, "2222", append(append([]byte(nil), configuration...), '\n')); !errors.Is(err, storage.ErrRemoteConfig) {
				t.Fatal("different backend config reused its old tested facts", err)
			}
			if err = service.checkNativeMaterial(ctx, nil, scheme, endpoint.Host, "2222", configuration); !errors.Is(err, storage.ErrRemoteConfig) {
				t.Fatal("missing native source approval was inferred", err)
			}
			authority.config.Approval.Root = "https://192.168.42.7:2222/"
			if err = service.checkNativeMaterial(ctx, authority, scheme, endpoint.Host, "2222", configuration); !errors.Is(err, remotemedia.ErrPolicy) {
				t.Fatal("HTTPS approval admitted native material", err)
			}
		})
	}
}
