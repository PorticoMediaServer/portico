package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// NewRcloneRemote writes a new rclone remote, from its type and its options as rclone names
// them, the way a remote pasted from rclone's configuration is kept. Portico has no form of
// its own per kind of storage: the type must be one the rclone that comes with Portico knows,
// and every option rclone marks as a password is obscured as rclone needs it, typed as it is.
// The text then passes the same checks as a pasted remote (ParseRcloneConfig).
//
// OneDrive and Dropbox take the token `rclone authorize` prints as the option "token"; the
// text around it that rclone prints may be pasted with it. A OneDrive remote also needs its
// drive, which is asked of Microsoft with the token when no drive_id is given.
func NewRcloneRemote(ctx context.Context, kind string, options map[string]string) (string, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if !rcloneKey.MatchString(kind) {
		return "", errors.New("give the kind of storage as rclone names it, such as s3 or sftp")
	}
	if kind == "drive" {
		return "", errors.New("Google Drive is its own kind of source, signed in with your own Google credentials")
	}
	passwords, err := rclonePasswordOptions(ctx, kind)
	if err != nil {
		return "", err
	}
	clean := map[string]string{}
	for key, value := range options {
		key = strings.ToLower(strings.TrimSpace(key))
		if !passwords[key] {
			// A password is kept as typed, spaces and all.
			value = strings.TrimSpace(value)
		}
		if key == "token" {
			value = pastedToken(value)
		}
		switch {
		case !rcloneKey.MatchString(key):
			return "", errors.New("an option's name is not one rclone uses")
		case key == "type":
			return "", errors.New("give the kind of storage as the type, not as an option")
		case strings.ContainsAny(value, "\r\n"):
			// The value is not repeated: it may be a secret.
			return "", fmt.Errorf("the option %q is on more than one line", key)
		case value == "":
			continue
		case passwords[key]:
			if value, err = rcloneObscure(ctx, value); err != nil {
				return "", err
			}
		}
		clean[key] = value
	}
	if kind == "onedrive" && clean["drive_id"] == "" {
		if clean["token"] == "" {
			return "", errors.New(`paste the token that rclone authorize "onedrive" prints`)
		}
		if clean["drive_id"], clean["drive_type"], err = oneDriveDrive(ctx, clean["token"]); err != nil {
			return "", err
		}
	}
	var text strings.Builder
	text.WriteString("[portico]\ntype = " + kind + "\n")
	keys := make([]string, 0, len(clean))
	for key := range clean {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		text.WriteString(key + " = " + clean[key] + "\n")
	}
	if _, err := ParseRcloneConfig(text.String()); err != nil {
		return "", err
	}
	return text.String(), nil
}

// pastedToken is the token in what rclone authorize printed: the JSON object, without the
// lines rclone prints around it.
func pastedToken(pasted string) string {
	start, end := strings.Index(pasted, "{"), strings.LastIndex(pasted, "}")
	if start < 0 || end < start {
		return pasted
	}
	return pasted[start : end+1]
}

// rcloneTool runs the rclone that comes with Portico for a short task, with its own home
// folder and no configuration, giving it input on stdin so no secret is on its command line.
func rcloneTool(ctx context.Context, input string, args ...string) ([]byte, error) {
	home, err := rcloneHome()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, RcloneBinary(), args...)
	cmd.Env = []string{"RCLONE_CONFIG=" + filepath.Join(home, "none"), "HOME=" + home, "PATH=" + os.Getenv("PATH")}
	cmd.Stdin = strings.NewReader(input)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		var exited *exec.ExitError
		if !errors.As(err, &exited) {
			return nil, fmt.Errorf("%w: %v", ErrRcloneMissing, err)
		}
		return nil, fmt.Errorf("rclone could not be run: %w", err)
	}
	return out.Bytes(), nil
}

// rclonePasswordOptions are the options rclone keeps obscured for a kind of storage, from
// rclone's own list of the kinds it knows. A kind not on that list is refused.
func rclonePasswordOptions(ctx context.Context, kind string) (map[string]bool, error) {
	out, err := rcloneTool(ctx, "", "config", "providers")
	if err != nil {
		return nil, err
	}
	var providers []struct {
		Name    string
		Options []struct {
			Name       string
			IsPassword bool
		}
	}
	if err := json.Unmarshal(out, &providers); err != nil {
		return nil, errors.New("rclone did not list the kinds of storage it knows")
	}
	for _, p := range providers {
		if p.Name == kind {
			passwords := map[string]bool{}
			for _, o := range p.Options {
				passwords[o.Name] = passwords[o.Name] || o.IsPassword
			}
			return passwords, nil
		}
	}
	return nil, fmt.Errorf("rclone has no kind of storage called %q", kind)
}

// rcloneObscure obscures a password the way rclone keeps it.
func rcloneObscure(ctx context.Context, password string) (string, error) {
	out, err := rcloneTool(ctx, password, "obscure", "-")
	obscured := strings.TrimSpace(string(out))
	if err != nil || obscured == "" {
		return "", errors.New("rclone could not obscure a password")
	}
	return obscured, nil
}

// MicrosoftDriveURL is where Microsoft says which OneDrive a token reaches. Tests point it at a
// stand-in.
var MicrosoftDriveURL = "https://graph.microsoft.com/v1.0/me/drive"

// oneDriveDrive asks Microsoft which drive a OneDrive token reaches: its id and type
// (personal, business or documentLibrary), which rclone needs and does not look up itself.
func oneDriveDrive(ctx context.Context, token string) (id, kind string, err error) {
	var t struct {
		AccessToken string `json:"access_token"`
	}
	if json.Unmarshal([]byte(token), &t) != nil || t.AccessToken == "" {
		return "", "", errors.New(`the token is not what rclone authorize "onedrive" prints`)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, MicrosoftDriveURL, nil)
	req.Header.Set("Authorization", "Bearer "+t.AccessToken)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", errors.New("Microsoft could not be reached to find the OneDrive")
	}
	defer resp.Body.Close()
	var drive struct {
		ID        string `json:"id"`
		DriveType string `json:"driveType"`
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return "", "", errors.New(`Microsoft refused the token: run rclone authorize "onedrive" again and paste what it prints`)
	case resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&drive) != nil ||
		!rcloneKey.MatchString(strings.ToLower(drive.DriveType)) || drive.ID == "" || strings.ContainsAny(drive.ID, "\r\n"):
		return "", "", errors.New("Microsoft did not say which OneDrive the token reaches")
	}
	return drive.ID, drive.DriveType, nil
}
