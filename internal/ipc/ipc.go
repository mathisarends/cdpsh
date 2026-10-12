package ipc

import (
	"cdpsh/internal/protocol"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func Home() string {
	if p := os.Getenv("CDPSH_HOME"); p != "" {
		p, _ = filepath.Abs(p)
		return p
	}
	p, err := os.UserCacheDir()
	if err != nil {
		p = os.TempDir()
	}
	return filepath.Join(p, "cdpsh")
}
func Key(home string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(home)))
	return hex.EncodeToString(sum[:12])
}
func Call(ctx context.Context, home string, req protocol.Request, start bool) protocol.Response {
	conn, err := dial(ctx, home)
	if err != nil && start {
		if e := os.MkdirAll(home, 0700); e != nil {
			return protocol.Failure("", protocol.Fail("CONTROLLER_ERROR", "Cannot create controller directory"))
		}
		exe, e := os.Executable()
		if e != nil {
			return protocol.Failure("", e)
		}
		cmd := exec.Command(exe, "_serve", home)
		detach(cmd)
		if e = cmd.Start(); e != nil {
			return protocol.Failure("", protocol.Fail("CONTROLLER_ERROR", "Cannot start local controller"))
		}
		_ = cmd.Process.Release()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for err != nil {
			select {
			case <-ctx.Done():
				return protocol.Failure("", ctx.Err())
			case <-timer.C:
				return protocol.Failure("", protocol.Fail("CONTROLLER_ERROR", "Controller did not become ready"))
			case <-time.After(30 * time.Millisecond):
			}
			conn, err = dial(ctx, home)
		}
	}
	if err != nil {
		next := protocol.Invocation{Command: "connect", Flags: map[string]string{"browser": req.Browser, "context": req.Context}}
		if req.Command == "doctor" {
			return protocol.Success("", map[string]any{"version": protocol.Version, "browser": req.Browser, "context": req.Context, "connected": false, "selectedTab": "", "checks": map[string]bool{"controller": false, "cdp": false, "selectedTab": false}, "next": "connect --endpoint URL", "nextCommand": next, "browsers": []any{}})
		}
		e := protocol.Fail("NO_BROWSER", "No controller is running; use connect --endpoint URL")
		e.Recovery = &next
		return protocol.Failure("", e)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}
	if err = json.NewEncoder(conn).Encode(req); err != nil {
		return protocol.Failure("", protocol.Fail("CONTROLLER_ERROR", "Cannot send request to controller"))
	}
	var response protocol.Response
	if err = json.NewDecoder(io.LimitReader(conn, protocol.MaxInput)).Decode(&response); err != nil {
		effect := "none"
		if protocol.MayMutate(req.Command) {
			effect = "possible"
		}
		var networkError net.Error
		if ctx.Err() != nil {
			err = protocol.Effect(ctx.Err(), effect)
		} else if errors.As(err, &networkError) && networkError.Timeout() {
			err = protocol.Effect(context.DeadlineExceeded, effect)
		} else {
			err = protocol.Effect(protocol.Fail("CONTROLLER_ERROR", "Controller disconnected; inspect browser state before retrying"), effect)
		}
		return protocol.Failure(req.Tab, err)
	}
	return response
}
func Listen(home string) (net.Listener, error) {
	if err := os.MkdirAll(home, 0700); err != nil {
		return nil, err
	}
	return listen(home)
}
