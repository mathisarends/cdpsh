package controller

import (
	"context"
	"sort"

	"github.com/mathisarends/cdpsh/internal/protocol"
)

func invocation(r protocol.Request, command string) protocol.Invocation {
	f := map[string]string{"browser": r.Browser, "context": r.Context}
	if r.Tab != "" {
		f["tab"] = r.Tab
	}
	return protocol.Invocation{Command: command, Flags: f}
}

func recovery(r protocol.Request, response protocol.Response) protocol.Response {
	if response.OK || response.Error == nil {
		return response
	}
	e := *response.Error
	if response.Tab != "" {
		r.Tab = response.Tab
	}
	var next protocol.Invocation
	switch e.Code {
	case "NO_BROWSER":
		next = invocation(r, "connect")
		e.Next = "connect --endpoint URL; inspect browsers for registered aliases"
	case "CONNECTION_LOST":
		next = invocation(r, "reconnect")
		e.Next = "reconnect the same alias, then observe; never replay input blindly"
	case "NO_TAB", "TAB_CLOSED":
		r.Tab = ""
		next = invocation(r, "tabs")
		e.Next = "tabs, then explicitly tab use ID"
	case "STALE_REF", "AMBIGUOUS_TARGET", "TARGET_NOT_FOUND", "OBSCURED", "NOT_VISIBLE", "DISABLED", "NOT_EDITABLE":
		next = invocation(r, "observe")
	case "TIMEOUT", "EXECUTION_ERROR", "CONTROLLER_ERROR":
		if r.Command == "screenshot" && response.Tab != "" {
			next = invocation(r, "tab activate")
			next.Args = []string{response.Tab}
			delete(next.Flags, "tab")
		} else if r.Tab != "" {
			next = invocation(r, "observe")
		} else {
			next = invocation(r, "doctor")
		}
	case "UNSUPPORTED":
		next = invocation(r, "help")
		next.Args = []string{"call"}
	}
	if next.Command != "" {
		e.Recovery = &next
		if e.Next == "" {
			e.Next = next.Command
		}
	}
	response.Error = &e
	return response
}

func (c *Controller) aliases() ([]map[string]any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	names := make([]string, 0, len(c.browsers))
	for name := range c.browsers {
		names = append(names, name)
	}
	sort.Strings(names)
	more := len(names) > 60
	if more {
		names = names[:60]
	}
	out := []map[string]any{}
	for _, name := range names {
		b := c.browsers[name]
		connected := true
		select {
		case <-b.client.Done:
			connected = false
		default:
		}
		out = append(out, map[string]any{"name": name, "connected": connected})
	}
	return out, more
}

func (c *Controller) diagnostics(ctx context.Context, r protocol.Request) (any, error) {
	c.mu.Lock()
	b, selected := c.browsers[r.Browser], c.selections[selection(r)]
	c.mu.Unlock()
	if r.Tab != "" {
		selected = r.Tab
	}
	connected := false
	checks := map[string]any{"controller": true, "cdp": false, "selectedTab": false}
	if b != nil {
		select {
		case <-b.client.Done:
		default:
			connected = true
		}
		if r.Command == "doctor" && connected {
			var version map[string]any
			if err := b.client.Call(ctx, "", "Browser.getVersion", nil, &version); err != nil {
				connected = false
				checks["cdpError"] = protocol.Failure("", err).Error.Code
			}
		}
	}
	checks["cdp"] = connected
	if r.Command == "doctor" && connected && selected != "" {
		var targetInfo struct {
			TargetInfo struct {
				Type string `json:"type"`
			} `json:"targetInfo"`
		}
		if err := b.client.Call(ctx, "", "Target.getTargetInfo", map[string]any{"targetId": selected}, &targetInfo); err != nil {
			checks["tabError"] = protocol.Failure("", err).Error.Code
		} else {
			checks["selectedTab"] = targetInfo.TargetInfo.Type == "page"
		}
	}
	aliases, _ := c.aliases()
	out := map[string]any{"version": protocol.Version, "epoch": c.Epoch, "browser": r.Browser, "context": r.Context, "connected": connected, "selectedTab": selected, "browsers": aliases}
	if r.Command == "doctor" {
		out["checks"] = checks
		if !connected {
			command := "connect"
			if b != nil {
				command = "reconnect"
			}
			out["next"] = command
			out["nextCommand"] = invocation(r, command)
		} else if selected == "" || checks["selectedTab"] != true {
			r.Tab = ""
			out["next"] = "tabs then tab use ID"
			out["nextCommand"] = invocation(r, "tabs")
		}
	}
	return out, nil
}
