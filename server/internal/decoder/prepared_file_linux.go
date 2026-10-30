//go:build linux

package decoder

import (
	"os"
	"path/filepath"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
)

func confinedPreparedFileCommand(sandbox, executable string, input, custody *os.File, args []string, libraries ...string) (*preparedFileCommand, error) {
	if !filepath.IsAbs(sandbox) || filepath.Clean(sandbox) != sandbox || input == nil || custody == nil {
		return nil, ErrInvalidConfiguration
	}
	stat, e := input.Stat()
	if e != nil || !stat.Mode().IsRegular() || stat.Size() <= 0 {
		return nil, ErrInvalidConfiguration
	}
	tools, e := pinPreparedTools(executable, libraries...)
	if e != nil {
		return nil, e
	}
	ok := false
	defer func() {
		if !ok {
			tools.close()
		}
	}()
	helper, e := tools.add(sandbox)
	if e != nil {
		return nil, e
	}
	if helper.info.Mode().Perm()&0111 == 0 || os.SameFile(helper.info, tools.seen[executable].info) {
		return nil, ErrInvalidConfiguration
	}
	argv := []string{"--unshare-user", "--unshare-pid", "--unshare-net", "--unshare-ipc", "--unshare-uts", "--unshare-cgroup-try", "--disable-userns", "--cap-drop", "ALL", "--new-session", "--die-with-parent", "--clearenv", "--setenv", "LANG", "C", "--setenv", "LC_ALL", "C", "--setenv", "HOME", "/", "--chdir", "/", "--proc", "/proc", "--dev", "/dev"}
	// FD 4 is consumed by init's sync-fd, closed before codec exec. The codec
	// cannot unlock/truncate host custody through an accidentally inherited FD.
	extra := []*os.File{input, custody}
	argv = append(argv, "--ro-bind-fd", "3", "/input.media", "--sync-fd", "4")
	helperFD := -1
	for _, tool := range tools.files {
		fd := len(extra) + 3
		extra = append(extra, tool.file)
		dest := tool.path
		if tool == helper {
			helperFD = fd
			dest = "/.portico/sandbox"
		}
		argv = append(argv, "--ro-bind-fd", strconv.Itoa(fd), dest)
	}
	argv = append(argv, "--remount-ro", "/", "--", tools.entrypoint)
	argv = append(argv, args...)
	// Bind the trusted launcher itself to the descriptor we checked. Its mount
	// consumes the descriptor too; there are no ambient host FDs in the codec.
	cmd, e := mediaexec.Command(mediaexec.Job{Executable: "/proc/self/fd/" + strconv.Itoa(helperFD), Args: argv, Files: extra, PreConfined: true, Background: true})
	if e != nil {
		return nil, e
	}
	ok = true
	return &preparedFileCommand{cmd: cmd, close: tools.close, validate: tools.validate}, nil
}
