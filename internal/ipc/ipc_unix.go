//go:build !windows

package ipc

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

func address(home string) string { return filepath.Join(home, "controller.sock") }
func dial(ctx context.Context, home string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", address(home))
}
func listen(home string) (net.Listener, error) {
	path := address(home)
	l, err := net.Listen("unix", path)
	if err != nil {
		// Serialize stale socket cleanup through a separate exclusive lock file.
		lock, e := os.OpenFile(filepath.Join(home, "startup.lock"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return nil, err
		}
		defer os.Remove(lock.Name())
		defer lock.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		if c, e := dial(ctx, home); e == nil {
			c.Close()
			return nil, err
		}
		if st, e := os.Lstat(path); e != nil || st.Mode()&os.ModeSocket == 0 {
			return nil, err
		}
		if e = os.Remove(path); e != nil {
			return nil, e
		}
		l, err = net.Listen("unix", path)
	}
	if err == nil {
		err = os.Chmod(path, 0600)
		if err != nil {
			l.Close()
		}
	}
	return l, err
}
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
