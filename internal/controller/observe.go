package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"unicode/utf8"

	"github.com/chromedp/cdproto/cdp"
	"github.com/mathisarends/cdpsh/internal/protocol"
)

type reference struct {
	Backend  cdp.BackendNodeID
	Document cdp.BackendNodeID
	Frame    string
}
type observation struct {
	ID       string
	Refs     map[string]reference
	Text     string
	Document cdp.BackendNodeID
}
type element struct {
	Ref      string           `json:"ref"`
	Role     string           `json:"role"`
	Name     string           `json:"name"`
	Disabled bool             `json:"disabled,omitempty"`
	Frame    string           `json:"frame"`
	Tag      string           `json:"tag,omitempty"`
	ID       string           `json:"id,omitempty"`
	Context  []group          `json:"context,omitempty"`
	Shadow   []shadowBoundary `json:"shadow,omitempty"`
	New      bool             `json:"new,omitempty"`
	Inferred bool             `json:"inferred,omitempty"`
	elementState
}
type observationResult struct {
	Observation     string                         `json:"observation"`
	URL             string                         `json:"url"`
	Title           string                         `json:"title"`
	Elements        []element                      `json:"elements"`
	Text            string                         `json:"text"`
	Frames          []frameInfo                    `json:"frames"`
	FramesTruncated bool                           `json:"framesTruncated,omitempty"`
	Truncated       bool                           `json:"truncated"`
	Next            string                         `json:"next,omitempty"`
	Viewport        viewportInfo                   `json:"viewport"`
	TextBytes       int                            `json:"textBytes"`
	TextNext        *int                           `json:"textNext,omitempty"`
	ElementOffset   int                            `json:"elementOffset"`
	ElementNext     *int                           `json:"elementNext,omitempty"`
	Continuation    map[string]protocol.Invocation `json:"continuation,omitempty"`
}
type frameInfo struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	Parent    string `json:"parent,omitempty"`
	Supported bool   `json:"supported"`
}
type layout struct {
	Connected bool         `json:"connected"`
	Visible   bool         `json:"visible"`
	Viewport  bool         `json:"viewport"`
	Disabled  bool         `json:"disabled"`
	Editable  bool         `json:"editable"`
	Obscured  bool         `json:"obscured"`
	X         float64      `json:"x"`
	Y         float64      `json:"y"`
	Left      float64      `json:"left"`
	Right     float64      `json:"right"`
	Top       float64      `json:"top"`
	Bottom    float64      `json:"bottom"`
	State     elementState `json:"state"`
}

func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max < 3 {
		return ""
	}
	end := max - 3
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + "…"
}
func intFlag(r protocol.Request, name string, def int) int {
	if s := r.Flags[name]; s != "" {
		n, _ := strconv.Atoi(s)
		return n
	}
	return def
}

func (c *Controller) observe(ctx context.Context, t *tab, r protocol.Request) (any, error) {
	frame, err := t.checkFrame(ctx, r)
	if err != nil {
		return nil, err
	}
	doc, err := t.document(ctx)
	if err != nil {
		return nil, err
	}
	scope := r.Flags["scope"]
	if scope == "" {
		scope = "viewport"
	}
	p, err := t.perceive(ctx, scope)
	if err != nil {
		return nil, err
	}
	info, err := t.evaluate(ctx, `({url:location.href,title:document.title})`)
	if err != nil {
		return nil, err
	}
	var meta struct {
		URL   string `json:"url"`
		Title string `json:"title"`
	}
	if err = json.Unmarshal(info, &meta); err != nil {
		return nil, err
	}
	o := &observation{ID: "o" + randomID(), Refs: map[string]reference{}, Text: p.text, Document: doc}
	out := observationResult{Observation: o.ID, URL: clip(meta.URL, 2048), Title: clip(meta.Title, 256), Elements: []element{}, Text: p.text, Viewport: p.viewport, TextBytes: len(p.text), ElementOffset: intFlag(r, "offset", 0)}
	out.Frames, out.FramesTruncated, err = t.frames(ctx)
	if err != nil {
		return nil, err
	}
	out.Truncated = out.URL != meta.URL || out.Title != meta.Title || out.FramesTruncated
	t.mu.Lock()
	previous := map[cdp.BackendNodeID]bool{}
	history := t.observations[r.Context]
	if len(history) > 0 {
		for _, v := range history[len(history)-1].Refs {
			previous[v.Backend] = true
		}
	}
	t.mu.Unlock()
	limit, index := intFlag(r, "limit", 60), 0
	for _, n := range p.order {
		if n.Ignored || n.BackendDOMNodeID == 0 || !interactive(n) {
			continue
		}
		role, name := axString(n.Role), axString(n.Name)
		if filter := r.Flags["role"]; filter != "" && filter != role {
			continue
		}
		if filter, ok := r.Flags["name"]; ok && filter != name {
			continue
		}
		id, err := t.object(ctx, n.BackendDOMNodeID)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		l, err := t.layout(ctx, id, false, false)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		if !l.Connected || !l.Visible || (scope == "viewport" && !l.Viewport) {
			continue
		}
		index++
		if index <= out.ElementOffset {
			continue
		}
		if len(out.Elements) >= limit {
			next := out.ElementOffset + len(out.Elements)
			out.ElementNext = &next
			break
		}
		ref := fmt.Sprintf("%s:e%d", o.ID, len(out.Elements)+1)
		o.Refs[ref] = reference{Backend: n.BackendDOMNodeID, Document: doc, Frame: frame}
		e := element{Ref: ref, Role: clip(role, 80), Name: clip(name, 200), Disabled: l.Disabled, Frame: frame, New: !previous[n.BackendDOMNodeID], Inferred: n.Inferred, elementState: l.State}
		e.Placeholder = clip(e.Placeholder, 128)
		if e.Value != nil {
			value := clip(*e.Value, 512)
			if value != *e.Value {
				e.ValueTruncated = true
			}
			e.Value = &value
		}
		for i := range e.Options {
			value, label := clip(e.Options[i].Value, 128), clip(e.Options[i].Label, 128)
			e.Options[i].Truncated = e.Options[i].Truncated || value != e.Options[i].Value || label != e.Options[i].Label
			e.Options[i].Value, e.Options[i].Label = value, label
		}
		if node := p.nodes[n.BackendDOMNodeID]; node != nil {
			e.Tag = node.Tag
			e.ID = clip(node.attr("id"), 80)
		}
		e.Context, e.Shadow = p.ancestry(n.BackendDOMNodeID)
		out.Elements = append(out.Elements, e)
		if len(name) > 200 || e.ValueTruncated {
			out.Truncated = true
		}
	}
	budget := intFlag(r, "max-bytes", protocol.OutputBudget)
	// Bound the full envelope, including continuation instructions. Text comes from
	// an immutable snapshot and can always be read back without another observe.
	update := func() {
		out.Continuation = map[string]protocol.Invocation{}
		if len(out.Text) < out.TextBytes {
			next := len(out.Text)
			out.TextNext = &next
			out.Continuation["text"] = protocol.Invocation{Command: "read", Flags: map[string]string{"browser": r.Browser, "context": r.Context, "tab": t.id, "observation": o.ID, "offset": strconv.Itoa(next), "max-bytes": strconv.Itoa(budget)}}
		} else {
			out.TextNext = nil
		}
		if out.ElementNext != nil {
			f := map[string]string{"browser": r.Browser, "context": r.Context, "tab": t.id, "scope": scope, "offset": strconv.Itoa(*out.ElementNext), "limit": strconv.Itoa(limit), "max-bytes": strconv.Itoa(budget)}
			if *out.ElementNext == out.ElementOffset && len(out.Elements) == 0 {
				f["max-bytes"] = strconv.Itoa(min(65536, budget*2))
			}
			for _, k := range []string{"role", "name"} {
				if v, ok := r.Flags[k]; ok {
					f[k] = v
				}
			}
			out.Continuation["elements"] = protocol.Invocation{Command: "observe", Flags: f}
		}
		if out.FramesTruncated {
			next := invocation(r, "call")
			next.Args = []string{"Page.getFrameTree"}
			next.Flags["tab"] = t.id
			out.Continuation["frames"] = next
		}
		if len(out.Continuation) > 0 {
			out.Truncated = true
			out.Next = "Use structured continuation commands for remaining text, targets or frame metadata"
		}
	}
	update()
	for len(protocol.Encode(protocol.Success(t.id, out))) > budget {
		if len(out.Text) > 0 {
			n := len(out.Text) / 2
			for n > 0 && !utf8.RuneStart(out.Text[n]) {
				n--
			}
			out.Text = out.Text[:n]
		} else if len(out.Elements) > 0 {
			last := out.Elements[len(out.Elements)-1]
			delete(o.Refs, last.Ref)
			out.Elements = out.Elements[:len(out.Elements)-1]
			next := out.ElementOffset + len(out.Elements)
			out.ElementNext = &next
		} else if len(out.Frames) > 1 {
			out.Frames = out.Frames[:len(out.Frames)-1]
			out.FramesTruncated = true
			out.Truncated = true
		} else {
			out.URL = clip(out.URL, 256)
			out.Title = clip(out.Title, 128)
			out.Frames[0].URL = clip(out.Frames[0].URL, 256)
			update()
			if len(protocol.Encode(protocol.Success(t.id, out))) > budget {
				return nil, protocol.Fail("INVALID_ARGUMENT", "Response metadata exceeds budget; increase --max-bytes")
			}
			break
		}
		update()
	}
	if budget == 65536 && len(out.Elements) == 0 && out.ElementNext != nil && *out.ElementNext == out.ElementOffset {
		return nil, protocol.Fail("UNSUPPORTED", "Target metadata exceeds the maximum observation budget; narrow the role/name filter or use raw CDP")
	}
	current, err := t.document(ctx)
	if err != nil {
		return nil, err
	}
	if current != doc {
		return nil, &protocol.Error{Code: "STALE_REF", Message: "Document changed during observation", Effect: "none", Next: "observe"}
	}
	t.mu.Lock()
	if t.dead {
		t.mu.Unlock()
		return nil, protocol.Fail("TAB_CLOSED", "Selected tab was closed during observation")
	}
	t.observations[r.Context] = append(t.observations[r.Context], o)
	if len(t.observations[r.Context]) > 3 {
		t.observations[r.Context] = t.observations[r.Context][len(t.observations[r.Context])-3:]
	}
	t.mu.Unlock()
	return out, nil
}

func (t *tab) retained(context, id string) *observation {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, o := range t.observations[context] {
		if o.ID == id {
			return o
		}
	}
	return nil
}

func (c *Controller) read(ctx context.Context, t *tab, r protocol.Request) (any, error) {
	o := t.retained(r.Context, r.Flags["observation"])
	if o == nil {
		return nil, &protocol.Error{Code: "STALE_REF", Message: "Observation expired; only the latest three per context and tab are retained", Effect: "none", Next: "observe"}
	}
	doc, err := t.document(ctx)
	if err != nil {
		return nil, err
	}
	if doc != o.Document {
		return nil, &protocol.Error{Code: "STALE_REF", Message: "Document changed", Effect: "none", Next: "observe"}
	}
	offset, budget := intFlag(r, "offset", 0), intFlag(r, "max-bytes", protocol.OutputBudget)
	size := budget
	for {
		text, end, err := textPage(o.Text, offset, size)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"observation": o.ID, "text": text, "offset": offset, "textBytes": len(o.Text), "truncated": end < len(o.Text)}
		if end < len(o.Text) {
			out["next"] = protocol.Invocation{Command: "read", Flags: map[string]string{"browser": r.Browser, "context": r.Context, "tab": t.id, "observation": o.ID, "offset": strconv.Itoa(end), "max-bytes": strconv.Itoa(budget)}}
		}
		if len(protocol.Encode(protocol.Success(t.id, out))) <= budget {
			return out, nil
		}
		size /= 2
		if size < 4 {
			return nil, protocol.Fail("INVALID_ARGUMENT", "Response metadata exceeds budget; increase --max-bytes")
		}
	}
}
