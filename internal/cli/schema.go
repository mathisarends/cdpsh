package cli

import "strings"

func targetCommand(command string) bool {
	switch command {
	case "click", "fill", "focus", "check", "select", "options", "scroll", "wait":
		return true
	}
	return false
}

func bound(n float64) *float64 { return &n }

func enrichDefinitions(defs map[string]Definition) map[string]Definition {
	for command, d := range defs {
		for key, f := range d.Flags {
			switch key {
			case "limit":
				f.Type = "integer"
				f.Default = 60
				f.Minimum = bound(1)
				f.Maximum = bound(500)
			case "offset":
				f.Type = "integer"
				f.Default = 0
				f.Minimum = bound(0)
				f.Maximum = bound(1000000)
				if command == "read" {
					f.Maximum = bound(2147483647)
				}
			case "max-bytes":
				f.Type = "integer"
				f.Default = 12288
				f.Minimum = bound(2048)
				f.Maximum = bound(65536)
			case "index":
				f.Type = "integer"
				f.Minimum = bound(0)
				f.Maximum = bound(2147483647)
			case "x", "y", "dx", "dy":
				f.Type = "number"
			}
			if key == "state" && (command == "goto" || command == "back" || command == "forward" || command == "reload") {
				f.Default = "domcontentloaded"
			}
			if key == "scope" {
				f.Default = "viewport"
				if command == "call" {
					f.Default = "tab"
				}
			}
			d.Flags[key] = f
		}
		defs[command] = d
	}
	return defs
}

func commandExamples(command string) []string {
	m := map[string][]string{
		"connect":   {"cdp connect --endpoint http://127.0.0.1:9222", "cdp connect --endpoint http://127.0.0.1:9222 --name work", "cdp --browser work tabs"},
		"reconnect": {"cdp --browser work reconnect", "cdp --browser work --context agent-a observe"},
		"browsers":  {"cdp browsers"}, "disconnect": {"cdp --browser work disconnect"}, "status": {"cdp --browser work --context agent-a status"}, "doctor": {"cdp --browser work doctor"},
		"tabs": {"cdp tabs", "cdp tabs --offset 60"}, "tab use": {"cdp --context agent-a tab use TARGET_ID"}, "tab new": {"cdp tab new https://example.com"}, "tab activate": {"cdp tab activate TARGET_ID"}, "tab close": {"cdp tab close TARGET_ID"},
		"observe": {"cdp observe --format text", "cdp observe --scope page --role button --offset 60 --limit 100 --max-bytes 24000"},
		"read":    {"cdp read --observation OBSERVATION_ID --offset 2048"},
		"click":   {"cdp click --ref OBSERVATION_ID:e1", "cdp click --role button --name \"Sign in\"", "cdp click --x 420 --y 310"},
		"fill":    {"cdp fill --ref OBSERVATION_ID:e1 --text-file email.txt", "cdp fill --role textbox --name Email --text user@example.com"},
		"focus":   {"cdp focus --ref OBSERVATION_ID:e1", "cdp type --text-file message.txt"},
		"check":   {"cdp check --role checkbox --name \"Accept terms\" --checked true"},
		"options": {"cdp options --role combobox --name Country", "cdp options --ref OBSERVATION_ID:e1 --offset 60"},
		"select":  {"cdp select --css '#country' --value DE", "cdp select --ref OBSERVATION_ID:e1 --label Germany", "cdp select --ref OBSERVATION_ID:e1 --index 3"},
		"type":    {"cdp type --text-file message.txt", "cdp type --text-file -"}, "press": {"cdp press Ctrl+A", "cdp press Shift+Tab"}, "scroll": {"cdp scroll --dy 600", "cdp scroll --ref OBSERVATION_ID:e1 --dy 300"},
		"wait":       {"cdp wait --css '#result' --state visible --timeout 5s", "cdp wait --url https://example.com/done", "cdp wait --state load"},
		"screenshot": {"cdp screenshot --out viewport.png"}, "eval": {"cdp eval --file inspect.js", "cdp eval --expression 'document.title'"}, "call": {"cdp call Browser.getVersion --scope browser", "cdp call Runtime.evaluate --params-file request.json"},
		"goto": {"cdp goto https://example.com --state domcontentloaded"}, "back": {"cdp back"}, "forward": {"cdp forward"}, "reload": {"cdp reload --state load"}, "info": {"cdp info"}, "help": {"cdp help click"}, "schema": {"cdp schema fill"}, "version": {"cdp version"}, "controller stop": {"cdp controller stop"},
	}
	return m[command]
}

func commandConstraints(command string) []string {
	c := []string{"One command and one response per invocation; global flags may appear before or after commands"}
	if targetCommand(command) {
		c = append(c, "TARGET is an observation reference; alternatively use --ref, --role with optional --name, or --css", "Role/name and CSS require exactly one match; names are exact", "The latest three observations per context and tab retain references; navigation, removal and reconnect invalidate them", "High-level actions support the main frame and nested open/closed author shadow roots; child frames require CDP/coordinates")
	}
	switch command {
	case "click":
		c = append(c, "Coordinates require both --x and --y and cannot combine with another target", "Confirmed means input dispatched; observe to verify task success")
	case "fill", "type", "eval":
		c = append(c, "Exactly one inline or UTF-8 file input source is required; - means stdin; maximum 4 MiB")
	case "check":
		c = append(c, "--checked true|false is required; an already matching state returns effect:none", "A radio can only be set to true")
	case "options":
		c = append(c, "TARGET must be a native select; use next.flags.offset for continuation", "Option values and labels are bounded; --index selects truncated or duplicate options precisely")
	case "select":
		c = append(c, "Exactly one of --value, --label or --index is required; matching is exact", "Only native single select elements are supported; custom comboboxes use click/press")
	case "read":
		c = append(c, "--observation is required; text comes from that immutable snapshot", "Offsets are UTF-8 bytes; use next.flags.offset; read does not evict references")
	case "observe":
		c = append(c, "Viewport scope applies to elements and rendered text; page scope includes offscreen content", "Use structured continuation fields; text can be paged with read without observing again", "Inferred listener/scroll targets must use their reference; their display role is not an AX role selector")
	case "connect":
		c = append(c, "--endpoint is required and must be a browser HTTP(S) discovery or WS(S) endpoint", "--name registers an alias; subsequent commands use --browser ALIAS", "No tab is selected implicitly; use tabs and tab use ID")
	case "reconnect":
		c = append(c, "Uses the endpoint remembered in this controller; after a controller restart use connect --endpoint", "References are invalidated; mutating requests are never replayed")
	case "wait":
		c = append(c, "Choose --url, a page --state domcontentloaded|load, or a target with --state visible|hidden|enabled")
	case "press":
		c = append(c, "Keys: Enter, Tab, Escape, Backspace, Delete, Space, ArrowLeft/Up/Right/Down, Home, End, PageUp/Down, letters, digits", "Modifiers: Ctrl/Control, Alt, Shift, Meta/Cmd/Command separated by +")
	case "call":
		c = append(c, "Parameters must be a JSON object; exactly one optional params source; - means stdin", "--scope tab is default; use --scope browser for browser methods")
	}
	return c
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}
func primitive(typ string) map[string]any    { return map[string]any{"type": typ} }
func requiredFlag(key string) map[string]any { return map[string]any{"required": []string{key}} }

// Schemas describe parsed CLI values, not an executable shell command string.
func inputSchema(command string, d Definition) map[string]any {
	properties := map[string]any{}
	for k, f := range globals {
		properties[k] = flagSchema(f)
	}
	for k, f := range d.Flags {
		properties[k] = flagSchema(f)
		if k == "ref" || k == "role" || k == "css" || k == "observation" {
			properties[k].(map[string]any)["minLength"] = 1
		}
	}
	f := objectSchema(properties)
	var all []any
	if targetCommand(command) {
		alternatives := []any{}
		for _, key := range []string{"ref", "role", "css"} {
			alternatives = append(alternatives, map[string]any{"required": []string{key}, "not": map[string]any{"anyOf": otherTargets(key, command)}})
		}
		if command == "click" {
			alternatives = append(alternatives, map[string]any{"required": []string{"x", "y"}, "not": map[string]any{"anyOf": []any{requiredFlag("ref"), requiredFlag("role"), requiredFlag("css")}}})
		}
		if command == "scroll" || command == "wait" {
			alternatives = append(alternatives, map[string]any{"not": map[string]any{"anyOf": []any{requiredFlag("ref"), requiredFlag("role"), requiredFlag("css")}}})
		}
		// Positional TARGET is normalized to --ref before validating this schema.
		all = append(all, map[string]any{"oneOf": alternatives})
		all = append(all, map[string]any{"dependentRequired": map[string]any{"name": []string{"role"}}})
	}
	if sources := inputSources(command); len(sources) > 0 {
		one := []any{}
		for _, key := range sources {
			one = append(one, requiredFlag(key))
		}
		if command == "call" {
			one = append(one, map[string]any{"not": map[string]any{"anyOf": one}})
		}
		all = append(all, map[string]any{"oneOf": one})
	}
	switch command {
	case "connect":
		f["required"] = []string{"endpoint"}
	case "read":
		f["required"] = []string{"observation"}
	case "check":
		f["required"] = []string{"checked"}
	case "scroll":
		f["required"] = []string{"dy"}
	case "wait":
		all = append(all, map[string]any{"oneOf": []any{
			map[string]any{"required": []string{"url"}, "not": map[string]any{"anyOf": []any{requiredFlag("state"), requiredFlag("ref"), requiredFlag("role"), requiredFlag("css")}}},
			map[string]any{"required": []string{"state"}, "properties": map[string]any{"state": map[string]any{"enum": []string{"domcontentloaded", "load"}}}, "not": map[string]any{"anyOf": []any{requiredFlag("url"), requiredFlag("ref"), requiredFlag("role"), requiredFlag("css")}}},
			map[string]any{"required": []string{"state"}, "properties": map[string]any{"state": map[string]any{"enum": []string{"visible", "hidden", "enabled"}}}, "anyOf": []any{requiredFlag("ref"), requiredFlag("role"), requiredFlag("css")}, "not": requiredFlag("url")},
		}})
	}
	if len(all) > 0 {
		f["allOf"] = all
	}
	args := map[string]any{"type": "array", "items": primitive("string"), "minItems": d.Min, "maxItems": d.Max}
	if targetCommand(command) {
		args["maxItems"] = 0
	}
	s := objectSchema(map[string]any{"args": args, "flags": f}, "args", "flags")
	s["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	s["description"] = "Parsed command arguments; normalize positional TARGET to flags.ref before validating. Global defaults are applied by the CLI."
	return s
}

func flagSchema(f Flag) map[string]any {
	s := primitive(f.Type)
	s["description"] = f.Description
	if len(f.Choices) > 0 {
		s["enum"] = f.Choices
	}
	if f.Default != nil {
		s["default"] = f.Default
	}
	if f.Minimum != nil {
		s["minimum"] = *f.Minimum
	}
	if f.Maximum != nil {
		s["maximum"] = *f.Maximum
	}
	return s
}
func otherTargets(key, command string) []any {
	a := []any{}
	for _, k := range []string{"ref", "role", "css"} {
		if k != key {
			a = append(a, requiredFlag(k))
		}
	}
	if command == "click" {
		a = append(a, requiredFlag("x"), requiredFlag("y"))
	}
	return a
}
func inputSources(command string) []string {
	switch command {
	case "fill", "type":
		return []string{"text", "text-file"}
	case "eval":
		return []string{"expression", "file"}
	case "call":
		return []string{"params", "params-file"}
	case "select":
		return []string{"value", "label", "index"}
	}
	return nil
}

func invocationSchema() map[string]any {
	return objectSchema(map[string]any{
		"command": primitive("string"),
		"args":    map[string]any{"type": "array", "items": primitive("string")},
		"flags":   map[string]any{"type": "object", "additionalProperties": primitive("string")},
	}, "command")
}

func optionSchema() map[string]any {
	return objectSchema(map[string]any{
		"index": map[string]any{"type": "integer", "minimum": 0},
		"value": primitive("string"), "label": primitive("string"),
		"disabled": primitive("boolean"), "selected": primitive("boolean"),
		"truncated": primitive("boolean"), "valueRedacted": primitive("boolean"),
	}, "index", "value", "label", "selected")
}

func arraySchema(items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}

func resultSchema(command string) map[string]any {
	props := map[string]any{}
	put := func(typ, keys string) {
		for _, k := range strings.Fields(keys) {
			props[k] = primitive(typ)
		}
	}
	switch command {
	case "observe":
		put("string", "observation url title text next")
		put("integer", "textBytes textNext elementOffset elementNext")
		put("boolean", "truncated framesTruncated")
		viewport := map[string]any{}
		for _, k := range []string{"width", "height", "x", "y", "above", "below"} {
			viewport[k] = primitive("number")
		}
		props["viewport"] = objectSchema(viewport, "width", "height", "x", "y", "above", "below")
		props["continuation"] = objectSchema(map[string]any{"text": invocationSchema(), "elements": invocationSchema(), "frames": invocationSchema()})
		e := map[string]any{}
		for _, k := range []string{"ref", "role", "name", "frame", "tag", "id", "value", "placeholder", "inputType"} {
			e[k] = primitive("string")
		}
		for _, k := range []string{"inferred", "disabled", "new", "selected", "expanded", "focused", "readonly", "required", "scrollable", "valueTruncated", "optionsTruncated"} {
			e[k] = primitive("boolean")
		}
		for _, k := range []string{"checked", "pressed"} {
			e[k] = map[string]any{"oneOf": []any{primitive("boolean"), map[string]any{"const": "mixed"}}}
		}
		e["context"] = arraySchema(objectSchema(map[string]any{"role": primitive("string"), "name": primitive("string"), "shadowDepth": map[string]any{"type": "integer", "minimum": 0}}, "role"))
		e["shadow"] = arraySchema(objectSchema(map[string]any{"host": primitive("string"), "mode": map[string]any{"enum": []string{"open", "closed", "user-agent"}}}, "host", "mode"))
		e["options"] = arraySchema(optionSchema())
		for _, k := range []string{"scrollAbove", "scrollBelow"} {
			e[k] = primitive("number")
		}
		e["optionCount"] = primitive("integer")
		props["elements"] = map[string]any{"type": "array", "items": objectSchema(e, "ref", "role", "name", "frame")}
		props["frames"] = arraySchema(objectSchema(map[string]any{"id": primitive("string"), "url": primitive("string"), "parent": primitive("string"), "supported": primitive("boolean")}, "id", "url", "supported"))
	case "options":
		props["options"] = arraySchema(optionSchema())
		put("integer", "total")
		put("boolean", "truncated")
		props["next"] = invocationSchema()
	case "read":
		put("string", "observation text")
		put("integer", "offset textBytes")
		put("boolean", "truncated")
		props["next"] = invocationSchema()
	case "click", "type", "press", "scroll", "focus", "check", "select", "fill", "tab close", "tab activate":
		props["effect"] = map[string]any{"enum": []string{"none", "possible", "confirmed"}}
		put("boolean", "verified checked")
		put("string", "id")
	case "goto", "back", "forward", "reload":
		put("string", "state effect")
	case "info":
		put("string", "url title state")
		put("boolean", "truncated")
	case "wait":
		put("boolean", "satisfied")
	case "tabs":
		put("array", "tabs")
		put("boolean", "truncated")
		put("string", "next")
	case "browsers":
		put("array", "browsers")
		put("boolean", "truncated")
	case "connect", "reconnect":
		put("string", "browser epoch next")
		put("boolean", "connected")
		props["nextCommand"] = invocationSchema()
	case "status", "doctor":
		put("string", "version epoch browser context selectedTab next")
		put("boolean", "connected")
		put("object", "checks")
		props["nextCommand"] = invocationSchema()
		put("array", "browsers")
	case "tab new", "tab use":
		put("string", "id")
		put("boolean", "selected")
	case "disconnect":
		put("boolean", "disconnected")
	case "controller stop":
		put("boolean", "stopped")
	case "version":
		put("string", "version")
	case "screenshot":
		put("string", "path type")
		put("integer", "bytes width height")
	default:
		return map[string]any{"description": "Arbitrary JSON result; large eval/call values use {file:{path,bytes,type}}"}
	}
	return objectSchema(props)
}

func commandErrors(command string) []string {
	e := []string{"INVALID_ARGUMENT"}
	if command == "help" || command == "schema" || command == "version" {
		return e
	}
	e = append(e, "TIMEOUT", "NO_BROWSER", "CONNECTION_LOST", "CONTROLLER_ERROR", "INTERNAL_ERROR", "PROTOCOL_ERROR")
	if targetCommand(command) {
		e = append(e, "NO_TAB", "TAB_CLOSED", "STALE_REF", "TARGET_NOT_FOUND", "AMBIGUOUS_TARGET", "NOT_VISIBLE", "OBSCURED", "DISABLED", "UNSUPPORTED", "EXECUTION_ERROR")
	}
	if command == "fill" || command == "focus" {
		e = append(e, "NOT_EDITABLE")
	}
	if command == "observe" || command == "read" {
		e = append(e, "NO_TAB", "TAB_CLOSED", "STALE_REF", "UNSUPPORTED", "EXECUTION_ERROR")
	}
	if command == "eval" || command == "call" {
		e = append(e, "NO_TAB", "TAB_CLOSED", "UNSUPPORTED", "EXECUTION_ERROR")
	}
	return e
}
