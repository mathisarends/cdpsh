//go:build windows

package ipc

import (
	"context"
	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
	"net"
	"os/exec"
	"syscall"
)

func address(home string) string { return `\\.\pipe\cdpsh-` + Key(home) }
func dial(ctx context.Context, home string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, address(home))
}
func listen(home string) (net.Listener, error) {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}
	sddl := "D:P(A;;GA;;;" + user.User.Sid.String() + ")"
	return winio.ListenPipe(address(home), &winio.PipeConfig{SecurityDescriptor: sddl})
}
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x00000200 | 0x00000008}
}
