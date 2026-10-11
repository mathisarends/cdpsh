package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cdpsh/internal/protocol"
	"github.com/chromedp/cdproto/runtime"
)

func (c *Controller) execute(ctx context.Context, t *tab, r protocol.Request) (any, error) {
	defer t.release(ctx)
	switch r.Command {
	case "info":
		v, err := t.evaluate(ctx, `({url:location.href,title:document.title,state:document.readyState})`)
		if err != nil {
			return nil, err
		}
		var out map[string]any
		_ = json.Unmarshal(v, &out)
		out["url"] = clip(out["url"].(string), 2048)
		out["title"] = clip(out["title"].(string), 256)
		return out, nil
	case "observe":
		return c.observe(ctx, t, r)
	case "goto", "back", "forward", "reload":
		return c.navigate(ctx, t, r)
	case "click":
		return c.click(ctx, t, r)
	case "fill":
		return c.fill(ctx, t, r)
	case "type":
		if _, err := t.checkFrame(ctx, r); err != nil {
			return nil, err
		}
		if err := t.call(ctx, "Input.insertText", map[string]any{"text": r.Payload}, nil); err != nil {
			return nil, protocol.Effect(err, "possible")
		}
		return map[string]any{"effect": "confirmed"}, nil
	case "press":
		if _, err := t.checkFrame(ctx, r); err != nil {
			return nil, err
		}
		if err := t.press(ctx, r.Args[0]); err != nil {
			return nil, err
		}
		return map[string]any{"effect": "confirmed"}, nil
	case "scroll":
		return c.scroll(ctx, t, r)
	case "screenshot":
		return c.screenshot(ctx, t, r)
	case "eval":
		if _, err := t.checkFrame(ctx, r); err != nil {
			return nil, err
		}
		v, err := t.evaluate(ctx, r.Payload)
		if err != nil {
			if ctx.Err() != nil {
				cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = t.call(cleanup, "Runtime.terminateExecution", nil, nil)
			}
			return nil, protocol.Effect(err, "possible")
		}
		return c.externalize(v, "application/json")
	case "cdp call":
		var v json.RawMessage
		if err := t.call(ctx, r.Args[0], json.RawMessage(r.Payload), &v); err != nil {
			return nil, protocol.Effect(err, "possible")
		}
		return c.externalize(v, "application/json")
	default:
		return nil, protocol.Fail("INVALID_ARGUMENT", "Unknown command")
	}
}
func (t *tab) actionable(ctx context.Context, r protocol.Request) (runtime.RemoteObjectID, layout, error) {
	id, err := t.resolve(ctx, r)
	if err != nil {
		return "", layout{}, err
	}
	l, err := t.layout(ctx, id, true, true)
	if err != nil {
		return "", l, protocol.Effect(err, "possible")
	}
	if !l.Connected {
		return "", l, &protocol.Error{Code: "STALE_REF", Message: "Target was removed", Effect: "possible", Next: "observe"}
	}
	if !l.Visible || !l.Viewport {
		return "", l, protocol.Fail("NOT_VISIBLE", "Target is not visible")
	}
	if l.Disabled {
		return "", l, protocol.Fail("DISABLED", "Target is disabled")
	}
	if l.Obscured {
		return "", l, protocol.Fail("OBSCURED", "Target is covered by another element")
	}
	return id, l, nil
}
func (c *Controller) click(ctx context.Context, t *tab, r protocol.Request) (any, error) {
	var x, y float64
	if s, ok := r.Flags["x"]; ok {
		x, _ = strconv.ParseFloat(s, 64)
		y, _ = strconv.ParseFloat(r.Flags["y"], 64)
		v, err := t.evaluate(ctx, `({width:innerWidth,height:innerHeight})`)
		if err != nil {
			return nil, err
		}
		var size struct{ Width, Height float64 }
		_ = json.Unmarshal(v, &size)
		if x < 0 || y < 0 || x >= size.Width || y >= size.Height {
			return nil, protocol.Fail("INVALID_ARGUMENT", "Coordinates must be inside the CSS viewport")
		}
	} else {
		_, l, err := t.actionable(ctx, r)
		if err != nil {
			return nil, err
		}
		x, y = l.X, l.Y
	}
	for _, kind := range []string{"mousePressed", "mouseReleased"} {
		if err := t.call(ctx, "Input.dispatchMouseEvent", map[string]any{"type": kind, "x": x, "y": y, "button": "left", "clickCount": 1}, nil); err != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = t.call(cleanup, "Input.dispatchMouseEvent", map[string]any{"type": "mouseReleased", "x": x, "y": y, "button": "left", "clickCount": 1}, nil)
			return nil, protocol.Effect(err, "possible")
		}
	}
	return map[string]any{"effect": "confirmed"}, nil
}
func (c *Controller) fill(ctx context.Context, t *tab, r protocol.Request) (any, error) {
	id, l, err := t.actionable(ctx, r)
	if err != nil {
		return nil, err
	}
	if !l.Editable {
		return nil, protocol.Fail("NOT_EDITABLE", "Target is not a supported editable input or textarea")
	}
	if _, err = t.function(ctx, id, `function(){this.focus();this.select();return true}`); err != nil {
		return nil, protocol.Effect(err, "possible")
	}
	if r.Payload == "" {
		err = t.press(ctx, "Backspace")
	} else {
		err = t.call(ctx, "Input.insertText", map[string]any{"text": r.Payload}, nil)
	}
	if err != nil {
		return nil, protocol.Effect(err, "possible")
	}
	value, err := t.function(ctx, id, `function(){return this.value}`)
	if err != nil {
		return nil, protocol.Effect(err, "possible")
	}
	var got string
	_ = json.Unmarshal(value, &got)
	if got != r.Payload {
		return nil, &protocol.Error{Code: "EXECUTION_ERROR", Message: "Field value did not match after input; inspect the field before retrying", Effect: "possible", Next: "observe"}
	}
	return map[string]any{"effect": "confirmed", "verified": true}, nil
}

type key struct {
	Key, Code, Text string
	VK              int
}

func KeySpec(chord string) (key, int, error) {
	parts := strings.Split(chord, "+")
	mod := 0
	for _, p := range parts[:len(parts)-1] {
		switch strings.ToLower(p) {
		case "ctrl", "control":
			mod |= 2
		case "alt":
			mod |= 1
		case "shift":
			mod |= 8
		case "meta", "cmd", "command":
			mod |= 4
		default:
			return key{}, 0, protocol.Fail("INVALID_ARGUMENT", "Unsupported key modifier")
		}
	}
	name := parts[len(parts)-1]
	keys := map[string]key{"Enter": {"Enter", "Enter", "\r", 13}, "Tab": {"Tab", "Tab", "", 9}, "Escape": {"Escape", "Escape", "", 27}, "Backspace": {"Backspace", "Backspace", "", 8}, "Delete": {"Delete", "Delete", "", 46}, "Space": {" ", "Space", " ", 32}, "ArrowLeft": {"ArrowLeft", "ArrowLeft", "", 37}, "ArrowUp": {"ArrowUp", "ArrowUp", "", 38}, "ArrowRight": {"ArrowRight", "ArrowRight", "", 39}, "ArrowDown": {"ArrowDown", "ArrowDown", "", 40}, "Home": {"Home", "Home", "", 36}, "End": {"End", "End", "", 35}, "PageUp": {"PageUp", "PageUp", "", 33}, "PageDown": {"PageDown", "PageDown", "", 34}}
	k, ok := keys[name]
	if !ok && len(name) == 1 {
		ch := strings.ToUpper(name)[0]
		if ch >= 'A' && ch <= 'Z' {
			text := strings.ToLower(name)
			if mod&8 != 0 {
				text = strings.ToUpper(name)
			}
			k = key{text, "Key" + string(ch), text, int(ch)}
			ok = true
		} else if ch >= '0' && ch <= '9' {
			k = key{name, "Digit" + name, name, int(ch)}
			ok = true
		}
	}
	if !ok {
		return key{}, 0, protocol.Fail("INVALID_ARGUMENT", "Unsupported key; use Enter, Tab, Escape, navigation keys, letters/digits, or modifier combinations")
	}
	if mod&(1|2|4) != 0 {
		k.Text = ""
	}
	return k, mod, nil
}
func (t *tab) press(ctx context.Context, chord string) error {
	k, mod, err := KeySpec(chord)
	if err != nil {
		return err
	}
	params := map[string]any{"type": "keyDown", "key": k.Key, "code": k.Code, "windowsVirtualKeyCode": k.VK, "modifiers": mod}
	if k.Text != "" {
		params["text"] = k.Text
		params["unmodifiedText"] = k.Text
	}
	if err = t.call(ctx, "Input.dispatchKeyEvent", params, nil); err == nil {
		params["type"] = "keyUp"
		delete(params, "text")
		delete(params, "unmodifiedText")
		err = t.call(ctx, "Input.dispatchKeyEvent", params, nil)
	}
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		params["type"] = "keyUp"
		delete(params, "text")
		delete(params, "unmodifiedText")
		_ = t.call(cleanup, "Input.dispatchKeyEvent", params, nil)
		return protocol.Effect(err, "possible")
	}
	return nil
}
func (c *Controller) scroll(ctx context.Context, t *tab, r protocol.Request) (any, error) {
	if _, err := t.checkFrame(ctx, r); err != nil {
		return nil, err
	}
	dx, _ := strconv.ParseFloat(r.Flags["dx"], 64)
	dy, _ := strconv.ParseFloat(r.Flags["dy"], 64)
	if r.Flags["ref"] != "" || r.Flags["css"] != "" || r.Flags["role"] != "" {
		id, _, err := t.actionable(ctx, r)
		if err != nil {
			return nil, err
		}
		_, err = t.function(ctx, id, `function(dx,dy){
 let e=this;
 while(e && e!==document.documentElement){
   const s=getComputedStyle(e);
   if((dy && e.scrollHeight>e.clientHeight && /auto|scroll/.test(s.overflowY)) || (dx && e.scrollWidth>e.clientWidth && /auto|scroll/.test(s.overflowX)))break;
   e=e.parentElement||e.getRootNode().host;
 }
 if(!e||e===document.documentElement)window.scrollBy({left:dx,top:dy,behavior:'instant'});
 else e.scrollBy({left:dx,top:dy,behavior:'instant'});
 return true;
}`, dx, dy)
		if err != nil {
			return nil, protocol.Effect(err, "possible")
		}
	} else {
		params, _ := json.Marshal(map[string]any{"left": dx, "top": dy, "behavior": "instant"})
		if _, err := t.evaluate(ctx, "window.scrollBy("+string(params)+");true"); err != nil {
			return nil, protocol.Effect(err, "possible")
		}
	}
	return map[string]any{"effect": "confirmed"}, nil
}
func (c *Controller) writeFile(path string, data []byte, overwrite bool) (map[string]any, error) {
	if path == "" {
		if err := os.MkdirAll(filepath.Join(c.Home, "artifacts"), 0700); err != nil {
			return nil, err
		}
		path = filepath.Join(c.Home, "artifacts", randomID()+".json")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if overwrite {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(abs, flags, 0600)
	if err != nil {
		return nil, protocol.Fail("EXECUTION_ERROR", "Cannot create output file; existing files require --overwrite")
	}
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	return map[string]any{"path": abs, "bytes": len(data)}, nil
}
func (c *Controller) externalize(v json.RawMessage, mime string) (any, error) {
	if len(v) == 0 {
		v = json.RawMessage(`null`)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, v); err != nil {
		return nil, err
	}
	v = compact.Bytes()
	if len(v) <= protocol.OutputBudget-512 {
		return v, nil
	}
	file, err := c.writeFile("", v, false)
	if err != nil {
		return nil, err
	}
	file["type"] = mime
	return map[string]any{"file": file}, nil
}
func (c *Controller) screenshot(ctx context.Context, t *tab, r protocol.Request) (any, error) {
	var res struct {
		Data string `json:"data"`
	}
	if err := t.call(ctx, "Page.captureScreenshot", map[string]any{"format": "png", "captureBeyondViewport": false, "fromSurface": true}, &res); err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(res.Data)
	if err != nil {
		return nil, err
	}
	path := r.Flags["out"]
	if path == "" {
		if err = os.MkdirAll(filepath.Join(c.Home, "artifacts"), 0700); err != nil {
			return nil, err
		}
		path = filepath.Join(c.Home, "artifacts", randomID()+".png")
	}
	file, err := c.writeFile(path, data, r.Flags["overwrite"] == "true")
	if err != nil {
		return nil, err
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	file["type"] = "image/png"
	file["width"] = cfg.Width
	file["height"] = cfg.Height
	return file, nil
}
func (t *tab) pageState(ctx context.Context) (string, string, error) {
	var res struct {
		FrameTree struct {
			Frame struct {
				LoaderID string `json:"loaderId"`
			} `json:"frame"`
		} `json:"frameTree"`
	}
	if err := t.call(ctx, "Page.getFrameTree", nil, &res); err != nil {
		return "", "", err
	}
	value, err := t.evaluate(ctx, `document.readyState`)
	var state string
	_ = json.Unmarshal(value, &state)
	return res.FrameTree.Frame.LoaderID, state, err
}
func (c *Controller) navigate(ctx context.Context, t *tab, r protocol.Request) (any, error) {
	before, _, err := t.pageState(ctx)
	if err != nil {
		return nil, err
	}
	expected := ""
	expectedURL := ""
	switch r.Command {
	case "goto":
		var res struct {
			LoaderID  string `json:"loaderId"`
			ErrorText string `json:"errorText"`
		}
		err = t.call(ctx, "Page.navigate", map[string]any{"url": r.Args[0]}, &res)
		expected = res.LoaderID
		if res.ErrorText != "" {
			err = protocol.Fail("EXECUTION_ERROR", clip(res.ErrorText, 512))
		}
	case "reload":
		err = t.call(ctx, "Page.reload", nil, nil)
	case "back", "forward":
		var history struct {
			CurrentIndex int `json:"currentIndex"`
			Entries      []struct {
				ID  int    `json:"id"`
				URL string `json:"url"`
			} `json:"entries"`
		}
		if err = t.call(ctx, "Page.getNavigationHistory", nil, &history); err != nil {
			return nil, err
		}
		index := history.CurrentIndex - 1
		if r.Command == "forward" {
			index = history.CurrentIndex + 1
		}
		if index < 0 || index >= len(history.Entries) {
			return nil, protocol.Fail("TARGET_NOT_FOUND", "No navigation history entry in that direction")
		}
		expectedURL = history.Entries[index].URL
		err = t.call(ctx, "Page.navigateToHistoryEntry", map[string]any{"entryId": history.Entries[index].ID}, nil)
	}
	if err != nil {
		return nil, protocol.Effect(err, "possible")
	}
	t.mu.Lock()
	t.observations = map[string]*observation{}
	t.mu.Unlock()
	state := r.Flags["state"]
	if state == "" {
		state = "domcontentloaded"
	}
	for {
		loader, ready, err := t.pageState(ctx)
		if err == nil {
			loaded := ready == "complete" || (state == "domcontentloaded" && ready == "interactive")
			correct := expected == "" || loader == expected
			if r.Command == "reload" {
				correct = loader != before
			}
			if expectedURL != "" {
				v, e := t.evaluate(ctx, `location.href`)
				var url string
				_ = json.Unmarshal(v, &url)
				correct = e == nil && url == expectedURL
			}
			if correct && loaded {
				return map[string]any{"state": state, "effect": "confirmed"}, nil
			}
		} else if ctx.Err() != nil {
			return nil, protocol.Effect(ctx.Err(), "possible")
		} else {
			var pe *protocol.Error
			if errors.As(err, &pe) && (pe.Code == "CONNECTION_LOST" || pe.Code == "TAB_CLOSED") {
				return nil, protocol.Effect(err, "possible")
			}
		}
		select {
		case <-ctx.Done():
			return nil, protocol.Effect(ctx.Err(), "possible")
		case <-time.After(40 * time.Millisecond):
		}
	}
}
func (c *Controller) wait(ctx context.Context, t *tab, r protocol.Request) (any, error) {
	for {
		v, err := t.do(ctx, func(ctx context.Context) (any, error) { defer t.release(ctx); return t.condition(ctx, r) })
		if err != nil {
			return nil, err
		}
		if v == true {
			return map[string]any{"satisfied": true}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
func (t *tab) condition(ctx context.Context, r protocol.Request) (any, error) {
	if url, ok := r.Flags["url"]; ok {
		v, err := t.evaluate(ctx, `location.href`)
		if err != nil {
			return nil, err
		}
		var got string
		_ = json.Unmarshal(v, &got)
		return got == url, nil
	}
	state := r.Flags["state"]
	if state == "domcontentloaded" || state == "load" {
		_, ready, err := t.pageState(ctx)
		return ready == "complete" || (state == "domcontentloaded" && ready == "interactive"), err
	}
	id, err := t.resolve(ctx, r)
	if err != nil {
		var e *protocol.Error
		if errors.As(err, &e) && e.Code == "TARGET_NOT_FOUND" {
			return state == "hidden", nil
		}
		return nil, err
	}
	l, err := t.layout(ctx, id, false, false)
	if err != nil {
		return nil, err
	}
	switch state {
	case "visible":
		return l.Connected && l.Visible, nil
	case "hidden":
		return !l.Connected || !l.Visible, nil
	case "enabled":
		return l.Connected && !l.Disabled, nil
	}
	return nil, protocol.Fail("INVALID_ARGUMENT", "Unsupported wait state")
}
