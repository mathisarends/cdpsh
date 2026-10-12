package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"cdpsh/internal/protocol"
	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/runtime"
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
	Observation   string                         `json:"observation"`
	URL           string                         `json:"url"`
	Title         string                         `json:"title"`
	Elements      []element                      `json:"elements"`
	Text          string                         `json:"text"`
	Frames        []frameInfo                    `json:"frames"`
	Truncated     bool                           `json:"truncated"`
	Next          string                         `json:"next,omitempty"`
	Viewport      viewportInfo                   `json:"viewport"`
	TextBytes     int                            `json:"textBytes"`
	TextNext      *int                           `json:"textNext,omitempty"`
	ElementOffset int                            `json:"elementOffset"`
	ElementNext   *int                           `json:"elementNext,omitempty"`
	Continuation  map[string]protocol.Invocation `json:"continuation,omitempty"`
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

// Decode only the AX fields we use. cdproto's generated enum unmarshallers
// reject new Chrome properties, even if those properties are irrelevant here.
type axValue struct {
	Value json.RawMessage `json:"value"`
}
type axNode struct {
	Inferred         bool              `json:"-"`
	Ignored          bool              `json:"ignored"`
	BackendDOMNodeID cdp.BackendNodeID `json:"backendDOMNodeId"`
	Role             *axValue          `json:"role"`
	Name             *axValue          `json:"name"`
	Properties       []struct {
		Name  string   `json:"name"`
		Value *axValue `json:"value"`
	} `json:"properties"`
}

func (t *tab) ax(ctx context.Context) ([]axNode, error) {
	var result struct {
		Nodes []axNode `json:"nodes"`
	}
	err := t.call(ctx, accessibility.CommandGetFullAXTree, nil, &result)
	return result.Nodes, err
}
func axString(v *axValue) string {
	if v == nil {
		return ""
	}
	var s string
	_ = json.Unmarshal(v.Value, &s)
	return s
}
func interactive(n axNode) bool {
	switch axString(n.Role) {
	case "button", "link", "textbox", "searchbox", "checkbox", "radio", "combobox", "listbox", "option", "menuitem", "menuitemcheckbox", "menuitemradio", "switch", "slider", "spinbutton", "tab", "treeitem", "scrollbar":
		return true
	}
	for _, p := range n.Properties {
		if p.Name == "focusable" && string(p.Value.Value) == "true" {
			return true
		}
	}
	return false
}
func (t *tab) evaluate(ctx context.Context, expression string) (json.RawMessage, error) {
	obj, exception, err := runtime.Evaluate(expression).WithAwaitPromise(true).WithReturnByValue(true).Do(t.typed(ctx))
	if err != nil {
		return nil, err
	}
	if exception != nil {
		return nil, protocol.Fail("EXECUTION_ERROR", clip(exception.Text+exceptionDescription(exception), 512))
	}
	if obj.UnserializableValue != "" {
		b, _ := json.Marshal(map[string]any{"type": obj.Type, "unserializableValue": obj.UnserializableValue})
		return b, nil
	}
	if len(obj.Value) == 0 {
		b, _ := json.Marshal(map[string]any{"type": obj.Type})
		return b, nil
	}
	return json.RawMessage(obj.Value), nil
}
func exceptionDescription(e *runtime.ExceptionDetails) string {
	if e.Exception != nil && e.Exception.Description != "" {
		return ": " + e.Exception.Description
	}
	return ""
}
func (t *tab) function(ctx context.Context, id runtime.RemoteObjectID, fn string, args ...any) (json.RawMessage, error) {
	arguments := make([]*runtime.CallArgument, 0, len(args))
	for _, arg := range args {
		b, _ := json.Marshal(arg)
		arguments = append(arguments, &runtime.CallArgument{Value: b})
	}
	obj, exception, err := runtime.CallFunctionOn(fn).WithObjectID(id).WithArguments(arguments).WithReturnByValue(true).WithAwaitPromise(true).Do(t.typed(ctx))
	if err != nil {
		return nil, err
	}
	if exception != nil {
		return nil, protocol.Fail("EXECUTION_ERROR", clip(exception.Text+exceptionDescription(exception), 512))
	}
	if len(obj.Value) == 0 {
		return json.RawMessage(`null`), nil
	}
	return json.RawMessage(obj.Value), nil
}
func (t *tab) document(ctx context.Context) (cdp.BackendNodeID, error) {
	node, err := dom.GetDocument().Do(t.typed(ctx))
	if err != nil {
		return 0, err
	}
	return node.BackendNodeID, nil
}
func (t *tab) frames(ctx context.Context) ([]frameInfo, error) {
	var tree struct {
		FrameTree struct {
			Frame struct {
				ID  string `json:"id"`
				URL string `json:"url"`
			} `json:"frame"`
			ChildFrames []json.RawMessage `json:"childFrames"`
		} `json:"frameTree"`
	}
	if err := t.call(ctx, "Page.getFrameTree", nil, &tree); err != nil {
		return nil, err
	}
	frames := []frameInfo{{ID: tree.FrameTree.Frame.ID, URL: clip(tree.FrameTree.Frame.URL, 1024), Supported: true}}
	var add func(json.RawMessage, string)
	add = func(raw json.RawMessage, parent string) {
		var child struct {
			Frame struct {
				ID  string `json:"id"`
				URL string `json:"url"`
			} `json:"frame"`
			ChildFrames []json.RawMessage `json:"childFrames"`
		}
		if json.Unmarshal(raw, &child) != nil || len(frames) >= 32 {
			return
		}
		frames = append(frames, frameInfo{ID: child.Frame.ID, URL: clip(child.Frame.URL, 512), Parent: parent})
		for _, v := range child.ChildFrames {
			add(v, child.Frame.ID)
		}
	}
	for _, v := range tree.FrameTree.ChildFrames {
		add(v, tree.FrameTree.Frame.ID)
	}
	return frames, nil
}
func (t *tab) checkFrame(ctx context.Context, r protocol.Request) (string, error) {
	frames, err := t.frames(ctx)
	if err != nil {
		return "", err
	}
	main := frames[0].ID
	if r.Flags["frame"] != "" && r.Flags["frame"] != main {
		return "", &protocol.Error{Code: "UNSUPPORTED", Message: "High-level commands support the main frame; use raw CDP or viewport coordinates for child frames", Effect: "none", Next: "call"}
	}
	return main, nil
}
func (t *tab) object(ctx context.Context, backend cdp.BackendNodeID) (runtime.RemoteObjectID, error) {
	obj, err := dom.ResolveNode().WithBackendNodeID(backend).WithObjectGroup("cdpsh").Do(t.typed(ctx))
	if err != nil {
		return "", err
	}
	return obj.ObjectID, nil
}
func (t *tab) layout(ctx context.Context, id runtime.RemoteObjectID, scroll, hit bool) (layout, error) {
	value, err := t.function(ctx, id, layoutJS, scroll, hit)
	var out layout
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(value, &out)
	if err != nil || !hit || !out.Obscured || !out.Visible || !out.Viewport {
		return out, err
	}
	for _, point := range [][2]float64{{.5, .5}, {.2, .2}, {.8, .2}, {.2, .8}, {.8, .8}} {
		x, y := out.Left+(out.Right-out.Left)*point[0], out.Top+(out.Bottom-out.Top)*point[1]
		backend, _, _, err := dom.GetNodeForLocation(int64(math.Floor(x)), int64(math.Floor(y))).Do(t.typed(ctx))
		if err != nil {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			continue
		}
		hitID, err := t.object(ctx, backend)
		if err != nil {
			continue
		}
		result, exception, err := runtime.CallFunctionOn(`function(n){while(n){if(n===this||this.contains(n))return true;n=n.assignedSlot||n.parentNode||n.host}return false}`).WithObjectID(id).WithArguments([]*runtime.CallArgument{{ObjectID: hitID}}).WithReturnByValue(true).Do(t.typed(ctx))
		if err != nil {
			return out, err
		}
		if exception != nil {
			return out, protocol.Fail("EXECUTION_ERROR", "Hit test failed")
		}
		if string(result.Value) == "true" {
			out.X, out.Y, out.Obscured = x, y, false
			break
		}
	}
	return out, nil
}

const layoutJS = `function(scroll,hit){
 const e=this;if(!(e instanceof Element)||!e.isConnected||e.ownerDocument!==document)return {connected:false};
 if(scroll)e.scrollIntoView({block:'center',inline:'center',behavior:'instant'});
 const r=e.getBoundingClientRect(),s=getComputedStyle(e);
 let left=Math.max(0,r.left),right=Math.min(innerWidth,r.right),top=Math.max(0,r.top),bottom=Math.min(innerHeight,r.bottom);
 let visible=r.width>0&&r.height>0&&s.visibility==='visible'&&s.display!=='none'&&e.getClientRects().length>0,disabled=e.matches(':disabled')||e.getAttribute('aria-disabled')==='true';
 for(let p=e;p;p=p.assignedSlot||p.parentElement||p.getRootNode().host){
   const ps=getComputedStyle(p);if(ps.opacity==='0'||ps.display==='none'||p.hidden)visible=false;
   if(p.inert)disabled=true;
   if(p!==e){const b=p.getBoundingClientRect();if(/auto|scroll|hidden|clip/.test(ps.overflowX)){left=Math.max(left,b.left);right=Math.min(right,b.right)}if(/auto|scroll|hidden|clip/.test(ps.overflowY)){top=Math.max(top,b.top);bottom=Math.min(bottom,b.bottom)}}
 }
 const viewport=right>left&&bottom>top;let x=(left+right)/2,y=(top+bottom)/2,obscured=hit;
 const owns=n=>{while(n){if(n===e||e.contains(n))return true;n=n.assignedSlot||n.parentNode||n.host}return false};
 if(hit&&viewport){for(const [a,b] of [[.5,.5],[.2,.2],[.8,.2],[.2,.8],[.8,.8]]){
   const px=left+(right-left)*a,py=top+(bottom-top)*b;let n=document.elementFromPoint(px,py);
   while(n&&n.shadowRoot){const child=n.shadowRoot.elementFromPoint(px,py);if(!child||child===n)break;n=child}
   if(owns(n)){x=px;y=py;obscured=false;break}
 }}
 const boolAttr=n=>e.hasAttribute(n)?e.getAttribute(n)==='true':undefined;
 const tri=n=>e.hasAttribute(n)?(e.getAttribute(n)==='mixed'?'mixed':e.getAttribute(n)==='true'):undefined;
 let active=document.activeElement;while(active?.shadowRoot?.activeElement)active=active.shadowRoot.activeElement;
 const root=e.getRootNode(),focused=active===e||root.activeElement===e;
 const sensitive=['password','file','hidden'].includes(e.type)||/(cc-|one-time-code)/i.test(e.autocomplete||'');
 const raw=!sensitive&&('value' in e)&&!['checkbox','radio','submit','button','reset'].includes(e.type)?String(e.value):undefined;
 const state={checked:('checked' in e)?(e.indeterminate?'mixed':e.checked):tri('aria-checked'),selected:('selected' in e)?e.selected:boolAttr('aria-selected'),expanded:boolAttr('aria-expanded'),pressed:tri('aria-pressed'),focused,readonly:!!e.readOnly||e.getAttribute('aria-readonly')==='true',required:!!e.required||e.getAttribute('aria-required')==='true',value:raw?.slice(0,512),valueTruncated:raw!==undefined&&raw.length>512,placeholder:e.getAttribute('placeholder')||e.getAttribute('aria-placeholder')||undefined,inputType:e instanceof HTMLInputElement?e.type:undefined,scrollable:(e.scrollHeight>e.clientHeight&&/auto|scroll/.test(s.overflowY))||(e.scrollWidth>e.clientWidth&&/auto|scroll/.test(s.overflowX)),scrollAbove:e.scrollTop,scrollBelow:Math.max(0,e.scrollHeight-e.clientHeight-e.scrollTop)};
 if(e instanceof HTMLSelectElement&&!sensitive){state.options=Array.from(e.options).slice(0,5).map(o=>({index:o.index,value:o.value.slice(0,128),label:o.label.slice(0,128),disabled:o.disabled||!!o.parentElement?.disabled,selected:o.selected,truncated:o.value.length>128||o.label.length>128}));state.optionCount=e.options.length;state.optionsTruncated=e.options.length>5;}
 if(!state.scrollable){delete state.scrollAbove;delete state.scrollBelow}
 return {connected:true,visible,viewport,disabled,editable:!e.readOnly&&!disabled&&((e instanceof HTMLInputElement&&['text','email','password','search','tel','url'].includes(e.type))||e instanceof HTMLTextAreaElement||e.isContentEditable),x,y,left,right,top,bottom,obscured,state};
}`

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
	out.Frames, err = t.frames(ctx)
	if err != nil {
		return nil, err
	}
	out.Truncated = out.URL != meta.URL || out.Title != meta.Title
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
		if len(out.Continuation) > 0 {
			out.Truncated = true
			out.Next = "Use continuation.text to read the same snapshot, or continuation.elements for more targets"
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

func (t *tab) resolve(ctx context.Context, r protocol.Request) (runtime.RemoteObjectID, error) {
	frame, err := t.checkFrame(ctx, r)
	if err != nil {
		return "", err
	}
	if ref := r.Flags["ref"]; ref != "" {
		t.mu.Lock()
		var o *observation
		for _, candidate := range t.observations[r.Context] {
			if strings.HasPrefix(ref, candidate.ID+":") {
				o = candidate
				break
			}
		}
		var v reference
		var found bool
		if o != nil {
			v, found = o.Refs[ref]
		}
		t.mu.Unlock()
		stale := &protocol.Error{Code: "STALE_REF", Message: "Element reference expired or was removed", Effect: "none", Next: "observe"}
		if !found || v.Frame != frame {
			return "", stale
		}
		doc, err := t.document(ctx)
		if err != nil {
			return "", err
		}
		if doc != v.Document {
			return "", stale
		}
		id, err := t.object(ctx, v.Backend)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", stale
		}
		l, err := t.layout(ctx, id, false, false)
		if err != nil || !l.Connected {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", stale
		}
		return id, nil
	}
	var backends []cdp.BackendNodeID
	candidates := []map[string]string{}
	if css, ok := r.Flags["css"]; ok {
		quoted, _ := json.Marshal(css)
		var res struct {
			Result struct {
				ObjectID runtime.RemoteObjectID `json:"objectId"`
			} `json:"result"`
			Exception json.RawMessage `json:"exceptionDetails"`
		}
		if err = t.call(ctx, "Runtime.evaluate", map[string]any{"expression": "Array.from(document.querySelectorAll(" + string(quoted) + "))", "objectGroup": "cdpsh"}, &res); err != nil {
			return "", err
		}
		if len(res.Exception) > 0 {
			return "", protocol.Fail("INVALID_ARGUMENT", "Invalid CSS selector")
		}
		var props struct {
			Result []struct {
				Name  string `json:"name"`
				Value struct {
					ObjectID runtime.RemoteObjectID `json:"objectId"`
				} `json:"value"`
			} `json:"result"`
		}
		if err = t.call(ctx, "Runtime.getProperties", map[string]any{"objectId": res.Result.ObjectID, "ownProperties": true}, &props); err != nil {
			return "", err
		}
		for _, p := range props.Result {
			if _, err := strconv.Atoi(p.Name); err != nil {
				continue
			}
			node, err := dom.DescribeNode().WithObjectID(p.Value.ObjectID).Do(t.typed(ctx))
			if err == nil {
				backends = append(backends, node.BackendNodeID)
				if len(candidates) < 5 {
					candidate := map[string]string{"tag": clip(node.LocalName, 80)}
					for i := 0; i+1 < len(node.Attributes); i += 2 {
						if node.Attributes[i] == "id" {
							candidate["id"] = clip(node.Attributes[i+1], 100)
						}
					}
					candidates = append(candidates, candidate)
				}
			}
		}
	} else {
		nodes, err := t.ax(ctx)
		if err != nil {
			return "", err
		}
		for _, n := range nodes {
			if !n.Ignored && n.BackendDOMNodeID != 0 && axString(n.Role) == r.Flags["role"] {
				if name, ok := r.Flags["name"]; !ok || name == axString(n.Name) {
					backends = append(backends, n.BackendDOMNodeID)
					if len(candidates) < 5 {
						candidates = append(candidates, map[string]string{"role": clip(axString(n.Role), 80), "name": clip(axString(n.Name), 200)})
					}
				}
			}
		}
	}
	if len(backends) == 0 {
		return "", protocol.Fail("TARGET_NOT_FOUND", "Target did not match an element")
	}
	if len(backends) > 1 {
		return "", &protocol.Error{Code: "AMBIGUOUS_TARGET", Message: "Target matched multiple elements; narrow the selector or use a fresh reference", Effect: "none", Next: "observe", Details: map[string]any{"count": len(backends), "candidates": candidates, "truncated": len(backends) > len(candidates)}}
	}
	return t.object(ctx, backends[0])
}
func (t *tab) release(ctx context.Context) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	_ = runtime.ReleaseObjectGroup("cdpsh").Do(t.typed(cleanup))
}
