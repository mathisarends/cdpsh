package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
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
	ID   string
	Refs map[string]reference
}
type element struct {
	Ref      string `json:"ref"`
	Role     string `json:"role"`
	Name     string `json:"name"`
	Disabled bool   `json:"disabled,omitempty"`
	Frame    string `json:"frame"`
}
type observationResult struct {
	Observation string      `json:"observation"`
	URL         string      `json:"url"`
	Title       string      `json:"title"`
	Elements    []element   `json:"elements"`
	Text        string      `json:"text"`
	Frames      []frameInfo `json:"frames"`
	Truncated   bool        `json:"truncated"`
	Next        string      `json:"next,omitempty"`
}
type frameInfo struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	Parent    string `json:"parent,omitempty"`
	Supported bool   `json:"supported"`
}
type layout struct {
	Connected bool    `json:"connected"`
	Visible   bool    `json:"visible"`
	Viewport  bool    `json:"viewport"`
	Disabled  bool    `json:"disabled"`
	Editable  bool    `json:"editable"`
	Obscured  bool    `json:"obscured"`
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
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
func axString(v *accessibility.Value) string {
	if v == nil {
		return ""
	}
	var s string
	_ = json.Unmarshal(v.Value, &s)
	return s
}
func interactive(n *accessibility.Node) bool {
	switch axString(n.Role) {
	case "button", "link", "textbox", "searchbox", "checkbox", "radio", "combobox", "listbox", "option", "menuitem", "menuitemcheckbox", "menuitemradio", "switch", "slider", "spinbutton", "tab", "treeitem":
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
		return "", &protocol.Error{Code: "UNSUPPORTED", Message: "High-level commands support the main frame; use raw CDP or viewport coordinates for child frames", Effect: "none", Next: "cdp call"}
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
	return out, err
}

const layoutJS = `function(scroll,hit){
 const e=this;if(!(e instanceof Element)||!e.isConnected||e.ownerDocument!==document)return {connected:false};
 if(scroll)e.scrollIntoView({block:'center',inline:'center',behavior:'instant'});
 const r=e.getBoundingClientRect(),s=getComputedStyle(e),visible=r.width>0&&r.height>0&&s.visibility==='visible'&&s.display!=='none'&&e.getClientRects().length>0;
 const left=Math.max(0,r.left),right=Math.min(innerWidth,r.right),top=Math.max(0,r.top),bottom=Math.min(innerHeight,r.bottom);
 const viewport=right>left&&bottom>top;let x=(left+right)/2,y=(top+bottom)/2,obscured=hit;
 const owns=n=>{while(n){if(n===e||e.contains(n))return true;n=n.parentNode||n.host}return false};
 if(hit&&viewport){for(const [a,b] of [[.5,.5],[.2,.2],[.8,.2],[.2,.8],[.8,.8]]){
   const px=left+(right-left)*a,py=top+(bottom-top)*b;let n=document.elementFromPoint(px,py);
   while(n&&n.shadowRoot){const child=n.shadowRoot.elementFromPoint(px,py);if(!child||child===n)break;n=child}
   if(owns(n)){x=px;y=py;obscured=false;break}
 }}
 return {connected:true,visible,viewport,disabled:e.matches(':disabled')||!!e.closest('[inert]')||e.getAttribute('aria-disabled')==='true',editable:!e.readOnly&&!e.disabled&&((e instanceof HTMLInputElement&&['text','email','password','search','tel','url'].includes(e.type))||e instanceof HTMLTextAreaElement),x,y,obscured};
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
	info, err := t.evaluate(ctx, `({url:location.href,title:document.title,text:(document.body?.innerText||'').slice(0,8000)})`)
	if err != nil {
		return nil, err
	}
	var meta struct {
		URL   string `json:"url"`
		Title string `json:"title"`
		Text  string `json:"text"`
	}
	if err = json.Unmarshal(info, &meta); err != nil {
		return nil, err
	}
	frames, err := t.frames(ctx)
	if err != nil {
		return nil, err
	}
	nodes, err := accessibility.GetFullAXTree().Do(t.typed(ctx))
	if err != nil {
		return nil, err
	}
	o := &observation{ID: "o" + randomID(), Refs: map[string]reference{}}
	out := observationResult{Observation: o.ID, URL: clip(meta.URL, 2048), Title: clip(meta.Title, 256), Text: clip(meta.Text, 2048), Elements: []element{}, Frames: frames}
	out.Truncated = out.URL != meta.URL || out.Title != meta.Title || out.Text != meta.Text
	limit := intFlag(r, "limit", 60)
	scope := r.Flags["scope"]
	if scope == "" {
		scope = "viewport"
	}
	for _, n := range nodes {
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
		if len(out.Elements) >= limit {
			out.Truncated = true
			break
		}
		ref := fmt.Sprintf("%s:e%d", o.ID, len(out.Elements)+1)
		o.Refs[ref] = reference{Backend: n.BackendDOMNodeID, Document: doc, Frame: frame}
		out.Elements = append(out.Elements, element{Ref: ref, Role: clip(role, 80), Name: clip(name, 200), Disabled: l.Disabled, Frame: frame})
		if len(name) > 200 {
			out.Truncated = true
		}
	}
	budget := intFlag(r, "max-bytes", protocol.OutputBudget)
	for len(protocol.Encode(protocol.Success(t.id, out))) > budget {
		out.Truncated = true
		if len(out.Text) > 64 {
			out.Text = clip(out.Text, len(out.Text)/2)
		} else if len(out.Elements) > 0 {
			last := out.Elements[len(out.Elements)-1]
			delete(o.Refs, last.Ref)
			out.Elements = out.Elements[:len(out.Elements)-1]
		} else if len(out.Frames) > 1 {
			out.Frames = out.Frames[:len(out.Frames)-1]
		} else {
			out.Text = ""
			out.URL = clip(out.URL, 256)
			out.Title = clip(out.Title, 128)
			out.Frames[0].URL = clip(out.Frames[0].URL, 256)
			break
		}
	}
	if out.Truncated {
		out.Next = "Narrow with --role/--name, or increase --limit/--max-bytes"
	}
	// Account for the truncation hint itself.
	for len(protocol.Encode(protocol.Success(t.id, out))) > budget && len(out.Elements) > 0 {
		last := out.Elements[len(out.Elements)-1]
		delete(o.Refs, last.Ref)
		out.Elements = out.Elements[:len(out.Elements)-1]
	}
	current, err := t.document(ctx)
	if err != nil {
		return nil, err
	}
	if current != doc {
		return nil, &protocol.Error{Code: "STALE_REF", Message: "Document changed during observation", Effect: "none", Next: "observe"}
	}
	t.mu.Lock()
	t.observations[r.Context] = o
	t.mu.Unlock()
	return out, nil
}
func (t *tab) resolve(ctx context.Context, r protocol.Request) (runtime.RemoteObjectID, error) {
	frame, err := t.checkFrame(ctx, r)
	if err != nil {
		return "", err
	}
	if ref := r.Flags["ref"]; ref != "" {
		t.mu.Lock()
		o := t.observations[r.Context]
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
			}
		}
	} else {
		nodes, err := accessibility.GetFullAXTree().Do(t.typed(ctx))
		if err != nil {
			return "", err
		}
		for _, n := range nodes {
			if !n.Ignored && n.BackendDOMNodeID != 0 && axString(n.Role) == r.Flags["role"] {
				if name, ok := r.Flags["name"]; !ok || name == axString(n.Name) {
					backends = append(backends, n.BackendDOMNodeID)
				}
			}
		}
	}
	if len(backends) == 0 {
		return "", protocol.Fail("TARGET_NOT_FOUND", "Target did not match an element")
	}
	if len(backends) > 1 {
		return "", &protocol.Error{Code: "AMBIGUOUS_TARGET", Message: "Target matched multiple elements; narrow the selector or use a fresh reference", Effect: "none", Details: map[string]any{"count": len(backends), "next": "observe"}}
	}
	return t.object(ctx, backends[0])
}
func (t *tab) release(ctx context.Context) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	_ = runtime.ReleaseObjectGroup("cdpsh").Do(t.typed(cleanup))
}
