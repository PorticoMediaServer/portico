//go:build !release && !devtrust

package trust

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestUntaggedTrustNoRoot(t *testing.T) {
	if _, e := DefaultRoot(); e == nil {
		t.Fatal("untagged verifier trusted a root")
	}
	if Development || Release || DevelopmentRoot != "" {
		t.Fatal("untagged verifier contains development trust")
	}
}

// A26: a release build refuses to start with the development root, and starts
// with any other valid root. Each case compiles this package with -tags release
// and the root linked in, and runs no tests: only the package's init.
func TestReleaseBuildRejectsDevelopmentRoot(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the package twice")
	}
	run := func(root string) (string, error) {
		// The same file lives in the Hosted and server copies of this package.
		symbol := reflect.TypeFor[Certificate]().PkgPath() + ".OfficialRoot"
		cmd := exec.Command("go", "test", "-count=1", "-tags", "release", "-ldflags", "-X "+symbol+"="+root, "-run", "^$", ".")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	const developmentRoot = "Uj7jC87DWZ0XbmIlQudnqU2O3N148wQ9eX4YgmU_Ppo"
	if out, err := run(developmentRoot); err == nil || !strings.Contains(out, "release requires the official Hosted root") {
		t.Fatalf("release build accepted the development root: %v\n%s", err, out)
	}
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := run(base64.RawURLEncoding.EncodeToString(public)); err != nil {
		t.Fatalf("release build refused a valid root: %v\n%s", err, out)
	}
}
