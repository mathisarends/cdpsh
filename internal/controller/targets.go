package controller

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/runtime"
	"github.com/mathisarends/cdpsh/internal/protocol"
)

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
		if p.Name == "focusable" && p.Value != nil && string(p.Value.Value) == "true" {
			return true
		}
	}
	return false
}
func (t *tab) frames(ctx context.Context) ([]frameInfo, bool, error) {
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
		return nil, false, err
	}
	frames := []frameInfo{{ID: tree.FrameTree.Frame.ID, URL: clip(tree.FrameTree.Frame.URL, 1024), Supported: true}}
	truncated := frames[0].URL != tree.FrameTree.Frame.URL
	var add func(json.RawMessage, string)
	add = func(raw json.RawMessage, parent string) {
		var child struct {
			Frame struct {
				ID  string `json:"id"`
				URL string `json:"url"`
			} `json:"frame"`
			ChildFrames []json.RawMessage `json:"childFrames"`
		}
		if json.Unmarshal(raw, &child) != nil {
			return
		}
		if len(frames) >= 32 {
			truncated = true
			return
		}
		frames = append(frames, frameInfo{ID: child.Frame.ID, URL: clip(child.Frame.URL, 512), Parent: parent})
		truncated = truncated || frames[len(frames)-1].URL != child.Frame.URL
		for _, v := range child.ChildFrames {
			add(v, child.Frame.ID)
		}
	}
	for _, v := range tree.FrameTree.ChildFrames {
		add(v, tree.FrameTree.Frame.ID)
	}
	return frames, truncated, nil
}
func (t *tab) checkFrame(ctx context.Context, r protocol.Request) (string, error) {
	frames, _, err := t.frames(ctx)
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
