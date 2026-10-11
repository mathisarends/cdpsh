package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"cdpsh/internal/controller"
	"cdpsh/internal/ipc"
	"cdpsh/internal/protocol"
)

type Flag struct {
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Choices     []string `json:"choices,omitempty"`
}
type Definition struct {
	Summary string          `json:"summary"`
	Usage   string          `json:"usage"`
	Flags   map[string]Flag `json:"flags"`
	Min     int             `json:"minArgs"`
	Max     int             `json:"maxArgs"`
	Result  string          `json:"result"`
}

func stringFlag(desc string) Flag { return Flag{Type: "string", Description: desc} }
func choice(desc string, values ...string) Flag {
	return Flag{Type: "string", Description: desc, Choices: values}
}
func flags(pairs ...string) map[string]Flag {
	m := map[string]Flag{}
	for i := 0; i < len(pairs); i += 2 {
		m[pairs[i]] = stringFlag(pairs[i+1])
	}
	return m
}

var globals = map[string]Flag{
	"browser": stringFlag("Browser alias; default CDPSH_BROWSER or default"), "context": stringFlag("Logical selection context; default CDPSH_CONTEXT or default"), "tab": stringFlag("Override selected tab for this request"), "timeout": stringFlag("Finite duration including queue time; 10s, navigation/connect 30s; maximum 5m"), "format": choice("Output format", "json", "text"), "help": {Type: "boolean", Description: "Show focused help"},
}

func Definitions() map[string]Definition {
	m := map[string]Definition{
		"connect":         {"Connect to an existing Chromium browser; never launches a browser", "connect --endpoint URL [--name NAME]", flags("endpoint", "HTTP discovery or browser WebSocket URL", "name", "Browser alias to register"), 0, 0, "browser, connected, epoch"},
		"disconnect":      {"Disconnect CDP and leave all browser tabs open", "disconnect", flags(), 0, 0, "disconnected"},
		"status":          {"Controller epoch, connection and selected tab", "status", flags(), 0, 0, "version, epoch, browser, context, connected, selectedTab"},
		"doctor":          {"Focused controller and CDP diagnostics", "doctor", flags(), 0, 0, "status fields, checks, next"},
		"tabs":            {"List page targets with bounded output", "tabs [--limit N] [--offset N]", flags("limit", "Tab limit; default 60, maximum 500", "offset", "Skip this many page tabs; default 0"), 0, 0, "tabs: [{id,title,url}], truncated, next"},
		"tab new":         {"Create and select a tab in this context", "tab new [URL]", flags(), 0, 1, "id, selected"},
		"tab use":         {"Select a tab without visibly activating it", "tab use ID", flags(), 1, 1, "id, selected"},
		"tab activate":    {"Visibly activate an explicitly named tab", "tab activate ID", flags(), 1, 1, "id, effect"},
		"tab close":       {"Close an explicitly named tab", "tab close ID", flags(), 1, 1, "id, effect"},
		"goto":            {"Navigate and wait for DOM readiness", "goto URL [--state domcontentloaded|load]", flags(), 1, 1, "state, effect"},
		"back":            {"Navigate back in history", "back [--state domcontentloaded|load]", flags(), 0, 0, "state, effect"},
		"forward":         {"Navigate forward in history", "forward [--state domcontentloaded|load]", flags(), 0, 0, "state, effect"},
		"reload":          {"Reload and wait for the new document", "reload [--state domcontentloaded|load]", flags(), 0, 0, "state, effect"},
		"info":            {"Read URL, title, document readiness", "info", flags(), 0, 0, "url, title, state"},
		"observe":         {"Bounded accessibility/DOM observation with fresh context-local references", "observe [--scope viewport|page] [--role ROLE] [--name NAME] [--limit N] [--max-bytes N]", flags("role", "Exact accessibility role filter", "name", "Exact accessible name filter", "limit", "Element limit; default 60, maximum 500", "max-bytes", "Serialized response budget; default 12288, range 2048..65536"), 0, 0, "observation, url, title, elements, text, frames, truncated, next"},
		"click":           {"Hit-test then dispatch a click; verify task success separately", "click TARGET | click --x X --y Y", flags("x", "CSS viewport X", "y", "CSS viewport Y"), 0, 1, "effect"},
		"fill":            {"Replace an input/textarea value using native input and verify it; never echoes text", "fill TARGET (--text TEXT | --text-file PATH)", flags("text", "Replacement text", "text-file", "UTF-8 file or - for stdin"), 0, 1, "effect, verified"},
		"type":            {"Insert text at the current focus; use press for keyboard events", "type (--text TEXT | --text-file PATH)", flags("text", "Text to insert", "text-file", "UTF-8 file or - for stdin"), 0, 0, "effect"},
		"press":           {"Send one key or modifier combination; best-effort release on failure", "press KEY", flags(), 1, 1, "effect"},
		"scroll":          {"Scroll a target area or the viewport by CSS pixels", "scroll --dy PIXELS [--dx PIXELS] [TARGET]", flags("dy", "Vertical CSS pixel delta", "dx", "Horizontal CSS pixel delta"), 0, 1, "effect"},
		"wait":            {"Poll an explicit condition while allowing later tab actions", "wait --url URL | wait --state STATE [TARGET]", flags("url", "Exact URL to wait for"), 0, 1, "satisfied"},
		"screenshot":      {"Save a viewport PNG to a file; never emits Base64", "screenshot [--out PATH] [--overwrite]", map[string]Flag{"out": stringFlag("Absolute or caller-relative file path"), "overwrite": {Type: "boolean", Description: "Explicitly replace an existing output file"}}, 0, 0, "path, bytes, type, width, height"},
		"eval":            {"Evaluate JavaScript; exceptions fail, large values become JSON files", "eval (--expression JS | --file PATH)", flags("expression", "JavaScript expression, promises awaited", "file", "UTF-8 expression file or - for stdin"), 0, 0, "JSON value or {file:{path,bytes,type}}"},
		"cdp call":        {"Execute any raw CDP method without a pinned method allowlist", "cdp call METHOD [--params JSON | --params-file PATH] [--scope tab|browser]", flags("params", "JSON object parameters", "params-file", "UTF-8 JSON object file or - for stdin"), 1, 1, "Raw JSON result or {file:{path,bytes,type}}"},
		"help":            {"Concise overview or focused command help", "help [COMMAND]", flags(), 0, 2, "commands or command definition"},
		"schema":          {"Machine-readable command arguments, result and error contract", "schema COMMAND", flags(), 1, 2, "command, flags, globals, constraints, result, errors, exitCodes"},
		"version":         {"Print the CLI version", "version", flags(), 0, 0, "version"},
		"controller stop": {"Stop this user's local controller; leave browsers open", "controller stop", flags(), 0, 0, "stopped"},
	}
	for _, name := range []string{"goto", "back", "forward", "reload"} {
		d := m[name]
		d.Flags["state"] = choice("Navigation readiness; default domcontentloaded", "domcontentloaded", "load")
		m[name] = d
	}
	for _, name := range []string{"observe", "click", "fill", "type", "press", "scroll", "wait", "eval"} {
		d := m[name]
		d.Flags["frame"] = stringFlag("Frame ID; main frame supported, child frames explicitly unsupported")
		m[name] = d
	}
	for _, name := range []string{"click", "fill", "scroll", "wait"} {
		d := m[name]
		d.Flags["ref"] = stringFlag("Fresh observation reference, e.g. oID:e1")
		d.Flags["role"] = stringFlag("Exact accessibility role")
		d.Flags["name"] = stringFlag("Exact accessible name; requires --role")
		d.Flags["css"] = stringFlag("CSS selector that must match exactly one element")
		m[name] = d
	}
	d := m["observe"]
	d.Flags["scope"] = choice("Elements in viewport (default) or entire page", "viewport", "page")
	m["observe"] = d
	d = m["cdp call"]
	d.Flags["scope"] = choice("CDP session; default tab", "tab", "browser")
	m["cdp call"] = d
	d = m["wait"]
	d.Flags["state"] = choice("Explicit readiness or target condition", "domcontentloaded", "load", "visible", "hidden", "enabled")
	m["wait"] = d
	return m
}
func parse(args []string) (protocol.Request, string, error) {
	r := protocol.Request{Flags: map[string]string{}, Browser: os.Getenv("CDPSH_BROWSER"), Context: os.Getenv("CDPSH_CONTEXT")}
	format := "json"
	defs := Definitions()
	position := []string{}
	all := map[string]Flag{}
	for k, v := range globals {
		all[k] = v
	}
	for _, d := range defs {
		for k, v := range d.Flags {
			all[k] = v
		}
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			position = append(position, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "--") {
			position = append(position, arg)
			continue
		}
		name, value, has := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		spec, ok := all[name]
		if !ok {
			return r, format, protocol.Fail("INVALID_ARGUMENT", "Unknown flag --"+name)
		}
		if _, ok := r.Flags[name]; ok {
			return r, format, protocol.Fail("INVALID_ARGUMENT", "Duplicate flag --"+name)
		}
		if spec.Type == "boolean" {
			if !has {
				value = "true"
			}
			if value != "true" && value != "false" {
				return r, format, protocol.Fail("INVALID_ARGUMENT", "Boolean flag requires true or false")
			}
		} else if !has {
			i++
			if i >= len(args) || strings.HasPrefix(args[i], "--") {
				return r, format, protocol.Fail("INVALID_ARGUMENT", "Missing value for --"+name)
			}
			value = args[i]
		}
		r.Flags[name] = value
		if name == "format" {
			format = value
		}
	}
	if len(position) == 0 {
		r.Command = "help"
	} else {
		r.Command = position[0]
		r.Args = position[1:]
		if r.Command == "tab" || r.Command == "cdp" || r.Command == "controller" {
			if len(r.Args) == 0 {
				return r, format, protocol.Fail("INVALID_ARGUMENT", "Missing subcommand")
			}
			r.Command += " " + r.Args[0]
			r.Args = r.Args[1:]
		}
	}
	if v, ok := r.Flags["browser"]; ok {
		r.Browser = v
	}
	if r.Browser == "" {
		r.Browser = "default"
	}
	if v, ok := r.Flags["context"]; ok {
		r.Context = v
	}
	if r.Context == "" {
		r.Context = "default"
	}
	r.Tab = r.Flags["tab"]
	if format != "json" && format != "text" {
		return r, "json", protocol.Fail("INVALID_ARGUMENT", "--format must be json or text")
	}
	d, ok := defs[r.Command]
	if !ok {
		return r, format, protocol.Fail("INVALID_ARGUMENT", "Unknown command; use help")
	}
	if r.Flags["help"] == "true" {
		r.Args = strings.Fields(r.Command)
		r.Command = "help"
		return r, format, nil
	}
	for name, value := range r.Flags {
		spec, global := globals[name]
		if !global {
			spec, ok = d.Flags[name]
			if !ok {
				return r, format, protocol.Fail("INVALID_ARGUMENT", "Flag --"+name+" is not valid for "+r.Command)
			}
		}
		if len(spec.Choices) > 0 {
			found := false
			for _, v := range spec.Choices {
				found = found || v == value
			}
			if !found {
				return r, format, protocol.Fail("INVALID_ARGUMENT", "Invalid value for --"+name)
			}
		}
	}
	if len(r.Args) < d.Min || len(r.Args) > d.Max {
		return r, format, protocol.Fail("INVALID_ARGUMENT", "Usage: "+d.Usage)
	}
	if (r.Command == "click" || r.Command == "fill" || r.Command == "scroll" || r.Command == "wait") && len(r.Args) == 1 {
		if r.Flags["ref"] != "" {
			return r, format, protocol.Fail("INVALID_ARGUMENT", "Provide only one reference")
		}
		r.Flags["ref"] = r.Args[0]
		r.Args = nil
	}
	has := func(k string) bool { _, ok := r.Flags[k]; return ok }
	if r.Command == "click" || r.Command == "fill" || r.Command == "scroll" || r.Command == "wait" {
		targets := 0
		for _, k := range []string{"ref", "role", "css"} {
			if has(k) {
				targets++
				if r.Flags[k] == "" {
					return r, format, protocol.Fail("INVALID_ARGUMENT", "Target selector cannot be empty")
				}
			}
		}
		if has("name") && !has("role") {
			return r, format, protocol.Fail("INVALID_ARGUMENT", "--name requires --role")
		}
		if has("x") || has("y") {
			if !has("x") || !has("y") {
				return r, format, protocol.Fail("INVALID_ARGUMENT", "Coordinates require both --x and --y")
			}
			targets++
		}
		if targets > 1 || ((r.Command == "click" || r.Command == "fill") && targets != 1) {
			return r, format, protocol.Fail("INVALID_ARGUMENT", "Use exactly one target: --ref, --role/--name, --css, or click coordinates")
		}
		if r.Command == "wait" {
			state := r.Flags["state"]
			url := has("url")
			if url && (state != "" || targets != 0) {
				return r, format, protocol.Fail("INVALID_ARGUMENT", "URL waits cannot combine state or target")
			}
			if !url && ((state == "visible" || state == "hidden" || state == "enabled") != (targets == 1) || state == "") {
				return r, format, protocol.Fail("INVALID_ARGUMENT", "wait requires --url, page --state, or a target with --state visible|hidden|enabled")
			}
		}
	}
	for _, k := range []string{"x", "y", "dx", "dy"} {
		if has(k) {
			f, e := strconv.ParseFloat(r.Flags[k], 64)
			if e != nil || math.IsNaN(f) || math.IsInf(f, 0) {
				return r, format, protocol.Fail("INVALID_ARGUMENT", "--"+k+" must be a finite number")
			}
		}
	}
	if r.Command == "scroll" && !has("dy") {
		return r, format, protocol.Fail("INVALID_ARGUMENT", "scroll requires --dy")
	}
	if r.Command == "connect" {
		if !has("endpoint") {
			return r, format, protocol.Fail("INVALID_ARGUMENT", "connect requires --endpoint")
		}
		u, e := url.Parse(r.Flags["endpoint"])
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "ws" && u.Scheme != "wss") {
			return r, format, protocol.Fail("INVALID_ARGUMENT", "Endpoint must be an absolute HTTP(S) or WS(S) URL")
		}
	}
	if (r.Command == "goto" || r.Command == "tab new") && len(r.Args) > 0 {
		u, e := url.Parse(r.Args[0])
		if e != nil || u.Scheme == "" {
			return r, format, protocol.Fail("INVALID_ARGUMENT", "Navigation requires an absolute URL")
		}
	}
	if r.Command == "press" {
		if _, _, err := controller.KeySpec(r.Args[0]); err != nil {
			return r, format, err
		}
	}
	for _, k := range []string{"limit", "max-bytes"} {
		if has(k) {
			n, e := strconv.Atoi(r.Flags[k])
			min, max := 1, 500
			if k == "max-bytes" {
				min, max = 2048, 65536
			}
			if e != nil || n < min || n > max {
				return r, format, protocol.Fail("INVALID_ARGUMENT", fmt.Sprintf("--%s must be in %d..%d", k, min, max))
			}
		}
	}
	if has("offset") {
		n, e := strconv.Atoi(r.Flags["offset"])
		if e != nil || n < 0 || n > 1000000 {
			return r, format, protocol.Fail("INVALID_ARGUMENT", "--offset must be an integer in 0..1000000")
		}
	}
	timeout := 10 * time.Second
	switch r.Command {
	case "connect", "goto", "back", "forward", "reload":
		timeout = 30 * time.Second
	}
	if s := r.Flags["timeout"]; s != "" {
		v, e := time.ParseDuration(s)
		if e != nil || v <= 0 || v > 5*time.Minute {
			return r, format, protocol.Fail("INVALID_ARGUMENT", "--timeout must be a positive duration up to 5m")
		}
		timeout = v
	}
	r.Deadline = time.Now().Add(timeout).UnixMilli()
	return r, format, nil
}
func input(r *protocol.Request, in io.Reader) error {
	var names []string
	required := true
	switch r.Command {
	case "fill", "type":
		names = []string{"text", "text-file"}
	case "eval":
		names = []string{"expression", "file"}
	case "cdp call":
		names = []string{"params", "params-file"}
		required = false
	default:
		return nil
	}
	count := 0
	for _, name := range names {
		if _, ok := r.Flags[name]; ok {
			count++
		}
	}
	if count > 1 || (required && count != 1) {
		return protocol.Fail("INVALID_ARGUMENT", "Provide exactly one input source: --"+strings.Join(names, " or --"))
	}
	if count == 0 {
		r.Payload = "{}"
		return nil
	}
	if s, ok := r.Flags[names[0]]; ok {
		r.Payload = s
	} else {
		path := r.Flags[names[1]]
		reader := in
		if path != "-" {
			f, err := os.Open(path)
			if err != nil {
				return protocol.Fail("INVALID_ARGUMENT", "Cannot read input file")
			}
			defer f.Close()
			reader = f
		}
		data, err := io.ReadAll(io.LimitReader(reader, protocol.MaxInput+1))
		if err != nil {
			return protocol.Fail("INVALID_ARGUMENT", "Cannot read input")
		}
		r.Payload = string(data)
	}
	if len(r.Payload) > protocol.MaxInput/2 {
		return protocol.Fail("INVALID_ARGUMENT", "Input exceeds 4 MiB")
	}
	if !utf8.ValidString(r.Payload) {
		return protocol.Fail("INVALID_ARGUMENT", "Input must be UTF-8")
	}
	if r.Command == "cdp call" {
		var obj map[string]json.RawMessage
		if json.Unmarshal([]byte(r.Payload), &obj) != nil || obj == nil {
			return protocol.Fail("INVALID_ARGUMENT", "CDP parameters must be a JSON object")
		}
	}
	for _, name := range names {
		delete(r.Flags, name)
	}
	return nil
}
func help(command string, schema bool) protocol.Response {
	defs := Definitions()
	if command == "" {
		names := make([]string, 0, len(defs))
		for name := range defs {
			names = append(names, name)
		}
		sort.Strings(names)
		commands := []map[string]string{}
		for _, name := range names {
			commands = append(commands, map[string]string{"command": name, "summary": defs[name].Summary})
		}
		return protocol.Success("", map[string]any{"version": protocol.Version, "usage": "cdpsh [--browser NAME] [--context NAME] [--tab ID] COMMAND [FLAGS]", "commands": commands, "next": "help COMMAND or schema COMMAND"})
	}
	d, ok := defs[command]
	if !ok {
		return protocol.Failure("", protocol.Fail("INVALID_ARGUMENT", "Unknown help/schema command"))
	}
	result := map[string]any{"command": command, "definition": d, "globals": globals}
	if schema {
		result["constraints"] = []string{"One command and one JSON response per invocation", "A target is one of ref, role/name, CSS, or click coordinates", "Role/name and CSS require exactly one match; names are exact", "Text/eval input requires exactly one source; - means stdin", "References expire on new observations in their context, navigation or reconnect", "Child frame high-level operations are unsupported; use CDP/coordinates", "A confirmed input effect does not establish task success", "CDPSH_HOME selects controller and artifact directory"}
		result["response"] = map[string]any{"ok": "boolean", "tab": "optional string", "result": "command result on success", "error": "{code,message,effect:none|possible|confirmed,next?,details?}"}
		result["errors"] = []string{"INVALID_ARGUMENT", "NO_BROWSER", "NO_TAB", "TAB_CLOSED", "STALE_REF", "TARGET_NOT_FOUND", "AMBIGUOUS_TARGET", "OBSCURED", "NOT_EDITABLE", "NOT_VISIBLE", "DISABLED", "TIMEOUT", "CONNECTION_LOST", "EXECUTION_ERROR", "UNSUPPORTED", "PROTOCOL_ERROR", "CONTROLLER_ERROR"}
		result["exitCodes"] = map[string]int{"success": 0, "internal/execution/protocol": 1, "arguments": 2, "target/page state": 3, "timeout": 4, "connection/controller": 5}
	}
	return protocol.Success("", result)
}
func Run(args []string, in io.Reader, out, stderr io.Writer) int {
	if len(args) == 2 && args[0] == "_serve" {
		if err := controller.Serve(args[1]); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	r, format, err := parse(args)
	var response protocol.Response
	if err == nil {
		err = input(&r, in)
	}
	if err != nil {
		response = protocol.Failure("", err)
	} else {
		switch r.Command {
		case "help", "schema":
			response = help(strings.Join(r.Args, " "), r.Command == "schema")
		case "version":
			response = protocol.Success("", map[string]any{"version": protocol.Version})
		default:
			if path := r.Flags["out"]; path != "" {
				abs, e := filepath.Abs(path)
				if e != nil {
					response = protocol.Failure("", e)
					break
				}
				r.Flags["out"] = abs
			}
			ctx, cancel := context.WithDeadline(context.Background(), time.UnixMilli(r.Deadline))
			encoded, e := json.Marshal(r)
			if e != nil || len(encoded) > protocol.MaxInput {
				response = protocol.Failure("", protocol.Fail("INVALID_ARGUMENT", "Encoded request exceeds 8 MiB"))
				cancel()
				break
			}
			response = ipc.Call(ctx, ipc.Home(), r, r.Command == "connect")
			cancel()
		}
	}
	if format == "text" {
		err = renderText(out, response)
	} else {
		_, err = fmt.Fprintln(out, string(protocol.Encode(response)))
	}
	if err != nil {
		return 1
	}
	return response.ExitCode()
}
func renderText(out io.Writer, r protocol.Response) error {
	if !r.OK {
		_, err := fmt.Fprintf(out, "%s: %s (effect=%s)\n", r.Error.Code, r.Error.Message, r.Error.Effect)
		return err
	}
	b, _ := json.Marshal(r.Result)
	var obs struct {
		Observation string        `json:"observation"`
		Title       string        `json:"title"`
		Elements    []elementText `json:"elements"`
		Text        string        `json:"text"`
		Truncated   bool          `json:"truncated"`
	}
	_ = json.Unmarshal(b, &obs)
	if obs.Observation != "" {
		var text strings.Builder
		fmt.Fprintf(&text, "tab=%s observation=%s title=%q\n", r.Tab, obs.Observation, obs.Title)
		for _, e := range obs.Elements {
			fmt.Fprintf(&text, "%s %s %q\n", e.Ref, e.Role, e.Name)
		}
		if obs.Text != "" {
			fmt.Fprintln(&text, obs.Text)
		}
		if obs.Truncated {
			fmt.Fprintln(&text, "[truncated; narrow filters or increase limits]")
		}
		_, err := io.WriteString(out, text.String())
		return err
	}
	_, err := fmt.Fprintln(out, string(b))
	return err
}

type elementText struct{ Ref, Role, Name string }
