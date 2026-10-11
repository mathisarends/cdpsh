//go:build !windows

package ipc

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func socketHome(t *testing.T) string {
	t.Helper()
	// Keep the socket path below Unix's short path limit, including on macOS.
	home, err := os.MkdirTemp("", "cdp-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(home); err != nil {
			t.Error(err)
		}
	})
	return home
}

func assertNoStartupLock(t *testing.T, home string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(home, "startup.lock")); !os.IsNotExist(err) {
		t.Fatalf("startup lock was not removed: %v", err)
	}
}

func TestRecoverStaleSocket(t *testing.T) {
	home := socketHome(t)
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: address(home), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	if err := stale.Close(); err != nil {
		t.Fatal(err)
	}
	l, err := Listen(home)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	st, err := os.Stat(address(home))
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("socket must be owner-only: %v, %v", st, err)
	}
	assertNoStartupLock(t, home)
}

func TestPreserveLiveSocket(t *testing.T) {
	home := socketHome(t)
	l, err := Listen(home)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if other, err := Listen(home); err == nil || other != nil {
		t.Fatalf("second controller should fail: listener=%v, err=%v", other, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, err := dial(ctx, home)
	if err != nil {
		t.Fatalf("live socket was disrupted: %v", err)
	}
	c.Close()
	assertNoStartupLock(t, home)
}

func TestPreserveNonSocketFile(t *testing.T) {
	home := socketHome(t)
	if err := os.WriteFile(address(home), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if l, err := Listen(home); err == nil || l != nil {
		t.Fatalf("existing regular file should fail: listener=%v, err=%v", l, err)
	}
	data, err := os.ReadFile(address(home))
	if err != nil || string(data) != "keep" {
		t.Fatalf("non-socket file was changed: %q, %v", data, err)
	}
	assertNoStartupLock(t, home)
}
