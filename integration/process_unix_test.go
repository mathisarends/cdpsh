//go:build integration && !windows

package integration

import "os/exec"

func hide(cmd *exec.Cmd) {}
