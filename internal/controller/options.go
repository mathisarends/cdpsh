package controller

import (
	"context"
	"encoding/json"
	"strconv"

	"cdpsh/internal/protocol"
)

func (c *Controller) options(ctx context.Context, t *tab, r protocol.Request) (any, error) {
	id, err := t.resolve(ctx, r)
	if err != nil {
		return nil, err
	}
	offset, limit, budget := intFlag(r, "offset", 0), intFlag(r, "limit", 60), intFlag(r, "max-bytes", protocol.OutputBudget)
	v, err := t.function(ctx, id, `function(offset,limit){if(!(this instanceof HTMLSelectElement))return {unsupported:true};const sensitive=/(cc-|one-time-code)/i.test(this.autocomplete||'');return {total:this.options.length,options:Array.from(this.options).slice(offset,offset+limit).map(o=>({index:o.index,value:sensitive?'':o.value.slice(0,128),valueRedacted:sensitive,label:o.label.slice(0,128),disabled:o.disabled||!!o.parentElement?.disabled,selected:o.selected,truncated:!sensitive&&o.value.length>128||o.label.length>128}))}}`, offset, limit)
	if err != nil {
		return nil, err
	}
	var out struct {
		Unsupported bool           `json:"unsupported"`
		Total       int            `json:"total"`
		Options     []selectOption `json:"options"`
	}
	if err = json.Unmarshal(v, &out); err != nil {
		return nil, err
	}
	if out.Unsupported {
		return nil, protocol.Fail("UNSUPPORTED", "options requires a native select; custom comboboxes use observe/click/press")
	}
	for i := range out.Options {
		option := &out.Options[i]
		value, label := clip(option.Value, 128), clip(option.Label, 128)
		option.Truncated = option.Truncated || value != option.Value || label != option.Label
		option.Value, option.Label = value, label
	}
	for {
		end := offset + len(out.Options)
		truncated := end < out.Total
		for _, option := range out.Options {
			truncated = truncated || option.Truncated
		}
		result := map[string]any{"options": out.Options, "total": out.Total, "truncated": truncated}
		if end < out.Total {
			next := invocation(r, "options")
			next.Flags["tab"] = t.id
			next.Flags["offset"] = strconv.Itoa(end)
			next.Flags["limit"] = strconv.Itoa(limit)
			next.Flags["max-bytes"] = strconv.Itoa(budget)
			for _, key := range []string{"ref", "role", "name", "css", "frame"} {
				if value, ok := r.Flags[key]; ok {
					next.Flags[key] = value
				}
			}
			result["next"] = next
		}
		if len(out.Options) == 0 && end < out.Total {
			return nil, protocol.Fail("INVALID_ARGUMENT", "Option metadata exceeds budget; increase --max-bytes")
		}
		if len(protocol.Encode(protocol.Success(t.id, result))) <= budget {
			return result, nil
		}
		if len(out.Options) == 0 {
			return nil, protocol.Fail("INVALID_ARGUMENT", "Option metadata exceeds budget; increase --max-bytes")
		}
		out.Options = out.Options[:len(out.Options)-1]
	}
}
