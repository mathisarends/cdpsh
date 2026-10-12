package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"cdpsh/internal/protocol"
)

func renderText(out io.Writer, r protocol.Response) error {
	if !r.OK {
		_, err := fmt.Fprintf(out, "%s: %s (effect=%s)\n", r.Error.Code, r.Error.Message, r.Error.Effect)
		if err != nil {
			return err
		}
		if r.Error.Recovery != nil {
			b, _ := json.Marshal(r.Error.Recovery)
			_, err = fmt.Fprintf(out, "recovery=%s\n", b)
		} else if r.Error.Next != "" {
			_, err = fmt.Fprintf(out, "next=%s\n", r.Error.Next)
		}
		return err
	}
	b, _ := json.Marshal(r.Result)
	var obs struct {
		Observation  string                         `json:"observation"`
		URL          string                         `json:"url"`
		Title        string                         `json:"title"`
		Elements     []map[string]any               `json:"elements"`
		Text         string                         `json:"text"`
		Offset       *int                           `json:"offset"`
		TextBytes    int                            `json:"textBytes"`
		Truncated    bool                           `json:"truncated"`
		Viewport     map[string]any                 `json:"viewport"`
		Frames       []map[string]any               `json:"frames"`
		Continuation map[string]protocol.Invocation `json:"continuation"`
		Next         any                            `json:"next"`
	}
	_ = json.Unmarshal(b, &obs)
	if obs.Observation == "" {
		_, err := fmt.Fprintln(out, string(b))
		return err
	}
	var text strings.Builder
	if obs.Offset != nil {
		fmt.Fprintf(&text, "tab=%s observation=%s offset=%d textBytes=%d\n", r.Tab, obs.Observation, *obs.Offset, obs.TextBytes)
	} else {
		fmt.Fprintf(&text, "tab=%s observation=%s title=%q url=%q\n", r.Tab, obs.Observation, obs.Title, obs.URL)
	}
	if obs.Viewport != nil {
		fmt.Fprintf(&text, "viewport=%vx%v scroll=%v,%v above=%vpx below=%vpx\n", obs.Viewport["width"], obs.Viewport["height"], obs.Viewport["x"], obs.Viewport["y"], obs.Viewport["above"], obs.Viewport["below"])
	}
	previous := ""
	for _, e := range obs.Elements {
		context, _ := json.Marshal(e["context"])
		shadow, _ := json.Marshal(e["shadow"])
		ancestry := string(context) + string(shadow)
		depth := 0
		if groups, ok := e["context"].([]any); ok {
			depth += len(groups)
		}
		if shadows, ok := e["shadow"].([]any); ok {
			depth += len(shadows)
		}
		if ancestry != previous {
			level := 0
			shadowLevel := 0
			shadows, _ := e["shadow"].([]any)
			printShadow := func() {
				item := shadows[shadowLevel].(map[string]any)
				fmt.Fprintf(&text, "%s|SHADOW(%s)| %q\n", strings.Repeat("  ", level), item["mode"], item["host"])
				level++
				shadowLevel++
			}
			if groups, ok := e["context"].([]any); ok {
				for _, g := range groups {
					item := g.(map[string]any)
					shadowDepth, _ := item["shadowDepth"].(float64)
					for shadowLevel < len(shadows) && shadowLevel < int(shadowDepth) {
						printShadow()
					}
					name, _ := item["name"].(string)
					fmt.Fprintf(&text, "%s%s %q\n", strings.Repeat("  ", level), item["role"], name)
					level++
				}
			}
			for shadowLevel < len(shadows) {
				printShadow()
			}
			previous = ancestry
		}
		marker := ""
		if e["new"] == true {
			marker = "*"
		}
		fmt.Fprintf(&text, "%s%s[%s] %s %q", strings.Repeat("  ", depth), marker, e["ref"], e["role"], e["name"])
		for _, key := range []string{"inferred", "disabled", "checked", "selected", "expanded", "pressed", "focused", "readonly", "required", "inputType", "placeholder", "value", "valueTruncated", "options", "optionCount", "optionsTruncated", "scrollable", "scrollAbove", "scrollBelow"} {
			if v, ok := e[key]; ok {
				value, _ := json.Marshal(v)
				fmt.Fprintf(&text, " %s=%s", key, value)
			}
		}
		text.WriteByte('\n')
	}
	for _, frame := range obs.Frames {
		if frame["supported"] == false {
			fmt.Fprintf(&text, "|IFRAME unsupported| id=%s url=%q; use raw CDP or coordinates\n", frame["id"], frame["url"])
		}
	}
	if obs.Text != "" {
		fmt.Fprintln(&text, "text:")
		fmt.Fprintln(&text, obs.Text)
	}
	if obs.Truncated {
		text.WriteString("[truncated]\n")
	}
	if len(obs.Continuation) > 0 {
		next, _ := json.Marshal(obs.Continuation)
		fmt.Fprintf(&text, "continuation=%s\n", next)
	} else if obs.Next != nil {
		next, _ := json.Marshal(obs.Next)
		fmt.Fprintf(&text, "next=%s\n", next)
	}
	_, err := io.WriteString(out, text.String())
	return err
}
