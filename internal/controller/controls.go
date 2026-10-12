package controller

import (
	"context"
	"encoding/json"
	"time"

	"cdpsh/internal/protocol"
)

// Browser input remains native. select changes a native <select> and emits the
// standard input/change events; custom comboboxes use click/press instead.
func (c *Controller) control(ctx context.Context, t *tab, r protocol.Request) (any, error) {
	id, l, err := t.actionable(ctx, r)
	if err != nil {
		return nil, err
	}
	switch r.Command {
	case "focus":
		v, err := t.function(ctx, id, `function(){this.focus();return this.getRootNode().activeElement===this}`)
		if err != nil {
			return nil, protocol.Effect(err, "possible")
		}
		if string(v) != "true" {
			return nil, &protocol.Error{Code: "NOT_EDITABLE", Message: "Element could not receive focus", Effect: "possible", Next: "observe"}
		}
		return map[string]any{"effect": "confirmed", "verified": true}, nil
	case "check":
		// Only native checkboxes/radios and ARIA checkbox/switch/radio roles have a
		// meaningful state. A requested false state cannot directly unset a radio.
		v, err := t.function(ctx, id, `function(){const role=this.getAttribute('role'),native=this instanceof HTMLInputElement&&['checkbox','radio'].includes(this.type);return {supported:native||['checkbox','switch','radio'].includes(role),radio:this.type==='radio'||role==='radio',checked:native?this.checked:this.getAttribute('aria-checked')==='true',mixed:this.indeterminate||this.getAttribute('aria-checked')==='mixed'}}`)
		if err != nil {
			return nil, err
		}
		var state struct{ Supported, Radio, Checked, Mixed bool }
		if err = json.Unmarshal(v, &state); err != nil {
			return nil, err
		}
		if !state.Supported {
			return nil, protocol.Fail("UNSUPPORTED", "check requires a checkbox, switch or radio")
		}
		want := r.Flags["checked"] == "true"
		if state.Radio && !want {
			return nil, protocol.Fail("UNSUPPORTED", "Select another radio to clear this one")
		}
		if state.Checked == want && !state.Mixed {
			return map[string]any{"effect": "none", "verified": true, "checked": want}, nil
		}
		// Reuse the exact resolved object and hit-tested point, avoiding re-resolution
		// after the state read. Verification detects replacement or rejected input.
		if err = t.dispatchClick(ctx, l.X, l.Y); err != nil {
			return nil, err
		}
		if state.Mixed && !want {
			v, err = t.function(ctx, id, `function(){return this.isConnected&&!this.indeterminate&&this.getAttribute('aria-checked')!=='mixed'&&(('checked' in this)?this.checked:this.getAttribute('aria-checked')==='true')}`)
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
		v, err = t.function(ctx, id, `function(want){return this.isConnected&&!(this.indeterminate||this.getAttribute('aria-checked')==='mixed')&&(('checked' in this)?this.checked:this.getAttribute('aria-checked')==='true')===want}`, want)
		if err != nil {
			return nil, protocol.Effect(err, "possible")
		}
		if string(v) != "true" {
			return nil, &protocol.Error{Code: "EXECUTION_ERROR", Message: "Checked state did not match; observe before retrying", Effect: "possible", Next: "observe"}
		}
		return map[string]any{"effect": "confirmed", "verified": true, "checked": want}, nil
	case "select":
		v, err := t.function(ctx, id, `function(value,label,index){if(!(this instanceof HTMLSelectElement))return {error:'UNSUPPORTED'};const options=Array.from(this.options).filter(o=>index!==null?o.index===Number(index):(value!==null?o.value===value:o.label===label));if(options.length!==1)return {error:options.length?'AMBIGUOUS_TARGET':'TARGET_NOT_FOUND'};const o=options[0];if(o.disabled||o.parentElement?.disabled)return {error:'DISABLED'};if(this.multiple)return {error:'UNSUPPORTED'};return {index:o.index,same:this.selectedIndex===o.index,value:o.value,label:o.label}}`, optional(r.Flags, "value"), optional(r.Flags, "label"), optional(r.Flags, "index"))
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
		v, err = t.function(ctx, id, `function(index,value,label){const o=this.options[index];if(!this.isConnected||!o||o.disabled||o.parentElement?.disabled||o.value!==value||o.label!==label)return false;this.selectedIndex=index;this.dispatchEvent(new Event('input',{bubbles:true,composed:true}));this.dispatchEvent(new Event('change',{bubbles:true}));return this.isConnected&&this.selectedIndex===index&&this.options[index]?.value===value&&this.options[index]?.label===label}`, state.Index, state.Value, state.Label)
		if err != nil {
			return nil, protocol.Effect(err, "possible")
		}
		if string(v) != "true" {
			return nil, &protocol.Error{Code: "EXECUTION_ERROR", Message: "Selection did not match; observe before retrying", Effect: "possible", Next: "observe"}
		}
		return map[string]any{"effect": "confirmed", "verified": true}, nil
	}
	return nil, protocol.Fail("INVALID_ARGUMENT", "Unknown control action")
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
