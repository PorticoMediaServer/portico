//go:build darwin || linux

package storage

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
)

func TestRootRegistrationDescriptorInheritance(t *testing.T) {
	c, root := registrationFixture(t)
	life, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, e := c.RegisterRoot(context.Background(), root, "", "", life)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	lease, e := r.Borrow("tiny")
	if e != nil {
		t.Fatal(e)
	}
	defer lease.Close()
	for _, file := range []*os.File{r.directory, lease.Directory} {
		flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, file.Fd(), syscall.F_GETFD, 0)
		if errno != 0 || flags&syscall.FD_CLOEXEC == 0 {
			t.Fatal("root descriptor missing CLOEXEC", errno)
		}
		info, e := file.Stat()
		if e != nil {
			t.Fatal(e)
		}
		st := info.Sys().(*syscall.Stat_t)
		cmd := exec.Command(c.Binary, "-test.run=^TestRootRegistrationInheritanceChild$", "-test.count=1")
		cmd.Env = append(os.Environ(), fmt.Sprintf("MP09_INHERIT_FD=%d", file.Fd()), fmt.Sprintf("MP09_INHERIT_OBJECT=%d:%d", st.Dev, st.Ino))
		if out, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("unrelated exec inherited root: %v %s", e, out)
		}
	}
}
func TestRootRegistrationInheritanceChild(t *testing.T) {
	raw := os.Getenv("MP09_INHERIT_FD")
	if raw == "" {
		return
	}
	fd, e := strconv.Atoi(raw)
	if e != nil {
		t.Fatal(e)
	}
	var st syscall.Stat_t
	if syscall.Fstat(fd, &st) == nil && fmt.Sprintf("%d:%d", st.Dev, st.Ino) == os.Getenv("MP09_INHERIT_OBJECT") {
		t.Fatal("registered source root leaked through exec")
	}
}
