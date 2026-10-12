package controller

import (
	"context"
	"encoding/json"
	"time"

	"cdpsh/internal/protocol"
	"github.com/chromedp/cdproto/runtime"
)

// Resolve once, read state before scrolling, and validate geometry only when
// input is necessary. Already matching controls return without side effects.
func (c *Controller) control(ctx context.Context, t *tab, r protocol.Request) (any, error) {
	id, err := t.resolve(ctx, r)
	if err != nil {
		return nil, err
	}
	switch r.Command {
	case "focus":
		return t.focus(ctx, id)
	case "check":
		return t.check(ctx, id, r.Flags["checked"] == "true")
	case "select":
		return t.selectOption(ctx, id, r)
	}
	return nil, protocol.Fail("INVALID_ARGUMENT", "Unknown control action")
}

func (t *tab) focus(ctx context.Context, id runtime.RemoteObjectID) (any, error) {
	if _, err := t.actionableObject(ctx, id); err != nil {
		return nil, err
	}
	v, err := t.function(ctx, id, focusJS)
	if err != nil {
		return nil, protocol.Effect(err, "possible")
	}
	if string(v) != "true" {
		return nil, &protocol.Error{Code: "NOT_EDITABLE", Message: "Element could not receive focus", Effect: "possible", Next: "observe"}
	}
	return map[string]any{"effect": "confirmed", "verified": true}, nil
}

func (t *tab) check(ctx context.Context, id runtime.RemoteObjectID, want bool) (any, error) {
	// Only native checkboxes/radios and ARIA checkbox/switch/radio roles have a
	// meaningful state. A requested false state cannot directly unset a radio.
	v, err := t.function(ctx, id, checkStateJS)
	if err != nil {
		return nil, err
	}
	var state struct{ Connected, Supported, Radio, Checked, Mixed bool }
	if err = json.Unmarshal(v, &state); err != nil {
		return nil, err
	}
	if !state.Connected {
		return nil, protocol.Fail("STALE_REF", "Target was removed")
	}
	if !state.Supported {
		return nil, protocol.Fail("UNSUPPORTED", "check requires a checkbox, switch or radio")
	}
	if state.Radio && !want {
		return nil, protocol.Fail("UNSUPPORTED", "Select another radio to clear this one")
	}
	if state.Checked == want && !state.Mixed {
		return map[string]any{"effect": "none", "verified": true, "checked": want}, nil
	}
	l, err := t.actionableObject(ctx, id)
	if err != nil {
		return nil, err
	}
	// Reuse the exact resolved object and hit-tested point, avoiding re-resolution
	// after the state read. Verification detects replacement or rejected input.
	if err = t.dispatchClick(ctx, l.X, l.Y); err != nil {
		return nil, err
	}
	if state.Mixed && !want {
		v, err = t.function(ctx, id, checkedJS, true)
		if err != nil {
			return nil, protocol.Effect(err, "possible")
		}
		if string(v) == "true" {
			l, err = t.layout(ctx, id, true, true)
			if err != nil {
				return nil, protocol.Effect(err, "possible")
			}
			if !l.Connected || !l.Visible || !l.Viewport || l.Disabled || l.Obscured {
				return nil, &protocol.Error{Code: "EXECUTION_ERROR", Message: "Mixed checkbox changed during input; observe before retrying", Effect: "possible", Next: "observe"}
			}
			if err = t.dispatchClick(ctx, l.X, l.Y); err != nil {
				return nil, protocol.Effect(err, "possible")
			}
		}
	}
	v, err = t.function(ctx, id, checkedJS, want)
	if err != nil {
		return nil, protocol.Effect(err, "possible")
	}
	if string(v) != "true" {
		return nil, &protocol.Error{Code: "EXECUTION_ERROR", Message: "Checked state did not match; observe before retrying", Effect: "possible", Next: "observe"}
	}
	return map[string]any{"effect": "confirmed", "verified": true, "checked": want}, nil
}

func (t *tab) selectOption(ctx context.Context, id runtime.RemoteObjectID, r protocol.Request) (any, error) {
	v, err := t.function(ctx, id, selectStateJS, optional(r.Flags, "value"), optional(r.Flags, "label"), optional(r.Flags, "index"))
	if err != nil {
		return nil, err
	}
	var state struct {
		Error string
		Index int
		Same  bool
		Value string
		Label string
	}
	if err = json.Unmarshal(v, &state); err != nil {
		return nil, err
	}
	if state.Error != "" {
		return nil, protocol.Fail(state.Error, "select requires a single native select and exactly one enabled option; use --value, --label or --index")
	}
	if state.Same {
		return map[string]any{"effect": "none", "verified": true}, nil
	}
	if _, err = t.actionableObject(ctx, id); err != nil {
		return nil, err
	}
	v, err = t.function(ctx, id, selectJS, state.Index, state.Value, state.Label)
	if err != nil {
		return nil, protocol.Effect(err, "possible")
	}
	if string(v) != "true" {
		return nil, &protocol.Error{Code: "EXECUTION_ERROR", Message: "Selection did not match; observe before retrying", Effect: "possible", Next: "observe"}
	}
	return map[string]any{"effect": "confirmed", "verified": true}, nil
}

func optional(flags map[string]string, key string) any {
	if v, ok := flags[key]; ok {
		return v
	}
	return nil
}

func (t *tab) dispatchClick(ctx context.Context, x, y float64) error {
	for _, kind := range []string{"mousePressed", "mouseReleased"} {
		if err := t.call(ctx, "Input.dispatchMouseEvent", map[string]any{"type": kind, "x": x, "y": y, "button": "left", "clickCount": 1}, nil); err != nil {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			defer cancel()
			_ = t.call(cleanup, "Input.dispatchMouseEvent", map[string]any{"type": "mouseReleased", "x": x, "y": y, "button": "left", "clickCount": 1}, nil)
			return protocol.Effect(err, "possible")
		}
	}
	return nil
}
