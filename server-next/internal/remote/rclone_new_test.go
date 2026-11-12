package remote_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
)

// fakeRcloneTools stands in for "rclone config providers" (a few kinds, with the options
// rclone marks as passwords) and "rclone obscure -" (reading the password from stdin), and
// exits when it was one of them.
func fakeRcloneTools(args []string) {
	switch {
	case len(args) == 2 && args[0] == "config" && args[1] == "providers":
		fmt.Print(`[{"Name":"sftp","Options":[{"Name":"host"},{"Name":"user"},{"Name":"pass","IsPassword":true},{"Name":"key_file_pass","IsPassword":true}]},
			{"Name":"onedrive","Options":[{"Name":"token"},{"Name":"drive_id"},{"Name":"drive_type"}]},{"Name":"dropbox","Options":[{"Name":"token"}]},
			{"Name":"local","Options":[]}]`)
		os.Exit(0)
	case len(args) >= 3 && args[0] == "lsjson" && args[1] == "--stat" && strings.HasPrefix(args[len(args)-1], "portico:"):
		// As rclone answers: a configuration it cannot use, a sign-in the provider refuses, and
		// a token the provider replaces (written where rclone writes it). A folder is there when
		// a file is inside it; a file is not a folder; the top is always a folder.
		switch {
		case os.Getenv("RCLONE_CONFIG_PORTICO_TYPE") != "drive":
			fmt.Fprintln(os.Stderr, "Failed to create file system: didn't find section in config file")
			os.Exit(1)
		case !fakeSignedIn():
			fmt.Fprintln(os.Stderr, "Failed to lsjson: couldn't list files: sign in: 401 Unauthorized")
			os.Exit(1)
		case os.Getenv("RCLONE_CONFIG_PORTICO_TOKEN") == fakeToken:
			os.WriteFile(os.Getenv("RCLONE_CONFIG"), []byte("[portico]\ntype = drive\ntoken = "+refreshedToken+"\n"), 0o600)
		}
		root := strings.TrimPrefix(args[len(args)-1], "portico:")
		if root == "" {
			fmt.Print(`{"Path":"","IsDir":true}`)
			os.Exit(0)
		}
		for name := range fakeFiles {
			if name == root {
				fmt.Print(`{"Path":"","IsDir":false}`)
				os.Exit(0)
			}
			if strings.HasPrefix(name, root+"/") {
				fmt.Print(`{"Path":"","IsDir":true}`)
				os.Exit(0)
			}
		}
		fmt.Fprintln(os.Stderr, "Failed to lsjson: directory not found")
		os.Exit(3)
	case len(args) == 2 && args[0] == "obscure" && args[1] == "-":
		typed, _ := io.ReadAll(os.Stdin)
		fmt.Printf("obscured-%x\n", typed)
		os.Exit(0)
	}
}

// What `rclone authorize` prints, around the token.
func authorized(token string) string {
	return "Paste the following into your remote machine --->\n" + token + "\n<---End paste"
}

// A new rclone remote is written from its type and options as a pasted one would be: typed
// passwords obscured as rclone keeps them, exactly as typed, the token rclone authorize printed taken out of the
// text around it, and a OneDrive remote's drive asked of Microsoft with the token (only when
// no drive is given). What a pasted remote may not do, a new one may not either, and no
// refusal repeats a value.
func TestNewRcloneRemote(t *testing.T) {
	fakeRcloneBinary(t)
	password := " pw-" + "7c2e " // spaces at either end are part of it
	text, err := remote.NewRcloneRemote(ctx, "SFTP", map[string]string{"host": "nas.example", "user": "me", "pass": password, "key_file_pass": ""})
	if err != nil {
		t.Fatal(err)
	}
	options, err := remote.ParseRcloneConfig(text)
	if err != nil || options["type"] != "sftp" || options["host"] != "nas.example" || options["pass"] != fmt.Sprintf("obscured-%x", password) ||
		strings.Contains(text, password) || len(options) != 4 {
		t.Fatalf("%v %q", err, text)
	}

	token := `{"access_token":"at-` + `91d0","token_type":"Bearer","refresh_token":"rt-` + `5b3a","expiry":"2026-11-11T19:00:00Z"}`
	if text, err = remote.NewRcloneRemote(ctx, "dropbox", map[string]string{"token": authorized(token)}); err != nil || text != "[portico]\ntype = dropbox\ntoken = "+token+"\n" {
		t.Fatalf("dropbox: %v %q", err, text)
	}

	var asked atomic.Int64
	answer := `{"id":"b!drive-1","driveType":"personal"}`
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		if r.Header.Get("Authorization") != "Bearer at-91d0" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		io.WriteString(w, answer)
	}))
	defer graph.Close()
	before := remote.MicrosoftDriveURL
	remote.MicrosoftDriveURL = graph.URL
	t.Cleanup(func() { remote.MicrosoftDriveURL = before })
	if text, err = remote.NewRcloneRemote(ctx, "onedrive", map[string]string{"token": authorized(token)}); err != nil {
		t.Fatal(err)
	}
	if options, err = remote.ParseRcloneConfig(text); err != nil || options["drive_id"] != "b!drive-1" || options["drive_type"] != "personal" || options["token"] != token {
		t.Fatalf("onedrive: %v %q", err, text)
	}
	// A drive the owner names is not looked up.
	if text, err = remote.NewRcloneRemote(ctx, "onedrive", map[string]string{"token": token, "drive_id": "lib-2", "drive_type": "documentLibrary"}); err != nil ||
		asked.Load() != 1 || !strings.Contains(text, "drive_id = lib-2\n") {
		t.Fatalf("a named drive: %v, %d lookups", err, asked.Load())
	}
	// A token Microsoft refuses, or one that is not a token, says so without repeating it.
	expired := strings.Replace(token, "at-91d0", "at-0000", 1)
	for _, given := range []string{expired, "at-91d0"} {
		if _, err := remote.NewRcloneRemote(ctx, "onedrive", map[string]string{"token": given}); err == nil || strings.Contains(err.Error(), "at-") {
			t.Fatalf("%q: %v", given, err)
		}
	}
	answer = `{"id":"x\ndrive_type = y","driveType":"personal"}`
	if _, err := remote.NewRcloneRemote(ctx, "onedrive", map[string]string{"token": token}); err == nil {
		t.Fatal("a drive id on two lines was written")
	}

	for _, refused := range []struct {
		kind    string
		options map[string]string
	}{
		{"drive", map[string]string{"token": token}},
		{"nosuch", nil},
		{"local", nil},
		{"sftp", map[string]string{"host": "nas.example", "ssh": "id"}},
		{"sftp", map[string]string{"host": "nas.example", "key_file": "/etc/ssh/key"}},
		{"sftp", map[string]string{"host": "nas.example", "pass": password + "\nssh = id"}},
		{"sftp", map[string]string{"type": "local"}},
		{"sftp", map[string]string{"Host Name": "x"}},
		{"onedrive", nil},
	} {
		if _, err := remote.NewRcloneRemote(ctx, refused.kind, refused.options); err == nil || strings.Contains(err.Error(), password) || strings.Contains(err.Error(), "/etc") {
			t.Errorf("%s %v: %v", refused.kind, refused.options, err)
		}
	}
}

// A remote, pasted or new, signs in only with credentials of its own: never rclone's shared
// Google client (scanning rule 9), and never this computer's own sign-in, which Oracle Object
// Storage takes by default and HDFS takes with Kerberos.
func TestRcloneRemotesSignInWithTheirOwnCredentials(t *testing.T) {
	for _, paste := range []string{
		"[a]\ntype = drive\ntoken = " + fakeToken + "\n",
		"[a]\ntype = drive\nclient_id = \ntoken = " + fakeToken + "\n",
		"[a]\ntype = oracleobjectstorage\nnamespace = n\n",
		"[a]\ntype = oracleobjectstorage\nprovider = env_auth\n",
		"[a]\ntype = oracleobjectstorage\nprovider = instance_principal_auth\n",
		"[a]\ntype = oracleobjectstorage\nprovider = workload_identity_auth\n",
		"[a]\ntype = oracleobjectstorage\nprovider = resource_principal_auth\n",
		"[a]\ntype = hdfs\nnamenode = nn:8020\nservice_principal_name = hdfs/nn\n",
	} {
		if _, err := remote.ParseRcloneConfig(paste); err == nil || strings.Contains(err.Error(), fakeToken) {
			t.Errorf("%q: %v", paste, err)
		}
	}
	for _, paste := range []string{
		"[a]\ntype = drive\nclient_id = own-client\nclient_secret = s\ntoken = " + fakeToken + "\n",
		"[a]\ntype = drive\nservice_account_credentials = {\"type\":\"service_account\"}\n",
		"[a]\ntype = oracleobjectstorage\nprovider = no_auth\nnamespace = n\n",
		"[a]\ntype = oracleobjectstorage\nprovider = user_principal_auth\n",
		"[a]\ntype = hdfs\nnamenode = nn:8020\nusername = me\n",
	} {
		if _, err := remote.ParseRcloneConfig(paste); err != nil {
			t.Errorf("%q was refused: %v", paste, err)
		}
	}
}
