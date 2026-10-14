//go:build integration

package integration

import (
	"strings"
	"testing"

	"github.com/mathisarends/cdpsh/internal/protocol"
)

func testReviewRegressions(t *testing.T, h *harness) {
	t.Helper()
	call := func(args ...string) protocol.Response {
		return h.call(append([]string{"--context", "review"}, args...)...)
	}
	tab := ok(t, call("tab", "new"))["id"].(string)
	defer h.call("tab", "close", tab)

	t.Run("empty contenteditable verifies browser placeholder", func(t *testing.T) {
		ok(t, call("eval", "--expression", `document.body.innerHTML='<div id="editor" contenteditable="true" role="textbox">Old text</div>';true`))
		for _, text := range []string{"", "First\nSecond", "", "Third\nFourth", ""} {
			response := h.run(text, "--context", "review", "fill", "--css", "#editor", "--text-file", "-")
			if !response.OK {
				t.Fatalf("fill %q failed: %+v; editor=%v", text, response.Error, call("eval", "--expression", `({text:document.querySelector('#editor').textContent,innerText:document.querySelector('#editor').innerText,html:document.querySelector('#editor').innerHTML})`).Result)
			}
			result := ok(t, response)
			if result["verified"] != true || result["effect"] != "confirmed" {
				t.Fatalf("fill %q did not verify: %v", text, result)
			}
		}
		// Empty normalization must not hide a deletion rejected by the editor.
		ok(t, call("eval", "--expression", `document.querySelector('#editor').textContent='Kept text';document.querySelector('#editor').addEventListener('keydown',e=>e.preventDefault());true`))
		rejected := h.run("", "--context", "review", "fill", "--css", "#editor", "--text-file", "-")
		code(t, rejected, "EXECUTION_ERROR")
		if rejected.Error.Effect != "possible" {
			t.Fatal("rejected deletion lost its possible effect")
		}
		if call("eval", "--expression", `document.querySelector('#editor').textContent`).Result != "Kept text" {
			t.Fatal("rejected deletion modified the editor")
		}
	})

	t.Run("disabled ancestors block input through shadow and slots", func(t *testing.T) {
		ok(t, call("eval", "--expression", `
document.body.innerHTML='<div aria-disabled="true"><button id="disabled" onclick="window.disabledClicked=true">Disabled action</button><input id="disabledInput" value="old"></div><div id="host" aria-disabled="true"></div><div id="slots" aria-disabled="true"><button slot="action" id="slotted">Slotted disabled</button></div>';
document.querySelector('#host').attachShadow({mode:'closed'}).innerHTML='<button>Shadow disabled</button>';
document.querySelector('#slots').attachShadow({mode:'open'}).innerHTML='<slot name="action"></slot>';
true`))
		observation := call("observe")
		for _, name := range []string{"Disabled action", "Shadow disabled", "Slotted disabled"} {
			if observed(t, observation, name)["disabled"] != true {
				t.Fatalf("%q did not inherit disabled state", name)
			}
			blocked := call("click", "--ref", ref(t, observation, name))
			code(t, blocked, "DISABLED")
			if blocked.Error.Effect != "none" {
				t.Fatal("blocked click reported an input effect")
			}
		}
		code(t, call("fill", "--css", "#disabledInput", "--text", "changed"), "DISABLED")
		code(t, call("wait", "--css", "#disabled", "--state", "enabled", "--timeout", "100ms"), "TIMEOUT")
		if call("eval", "--expression", `window.disabledClicked===true || document.querySelector('#disabledInput').value!=='old'`).Result != false {
			t.Fatal("disabled descendants received input")
		}
		ok(t, call("eval", "--expression", `document.querySelector('[aria-disabled]').removeAttribute('aria-disabled');true`))
		ok(t, call("click", "--css", "#disabled"))
		if call("eval", "--expression", `window.disabledClicked===true`).Result != true {
			t.Fatal("reenabled descendant could not be clicked")
		}
	})

	t.Run("shifted digits dispatch the correct character and key", func(t *testing.T) {
		ok(t, call("eval", "--expression", `document.body.innerHTML='<input id="keys">';window.keyEvents=[];document.querySelector('#keys').addEventListener('keydown',e=>keyEvents.push({key:e.key,code:e.code,shift:e.shiftKey}));true`))
		ok(t, call("focus", "--css", "#keys"))
		for _, chord := range []string{"Shift+1", "Shift+0", "1", "Shift+A"} {
			ok(t, call("press", chord))
		}
		if call("eval", "--expression", `document.querySelector('#keys').value`).Result != "!)1A" {
			t.Fatal("shifted digits produced the wrong text")
		}
		if call("eval", "--expression", `keyEvents[0].key==='!' && keyEvents[0].code==='Digit1' && keyEvents[0].shift`).Result != true {
			t.Fatal("shifted key event did not match the physical digit key")
		}
	})

	t.Run("scroll container text respects scope", func(t *testing.T) {
		ok(t, call("eval", "--expression", `
document.body.innerHTML = '<div style="height:60px;overflow:auto"><p>Visible scroll text</p><p style="margin-top:200px">Offscreen scroll text</p></div><div style="height:60px;overflow:hidden"><p>Visible clipped text</p><p style="margin-top:200px">Hidden clipped text</p></div>';
true`))
		viewport := ok(t, call("observe"))["text"].(string)
		page := ok(t, call("observe", "--scope", "page"))["text"].(string)
		if strings.Contains(viewport, "Offscreen scroll text") || strings.Contains(viewport, "Hidden clipped text") {
			t.Fatalf("viewport leaked clipped text: %q", viewport)
		}
		if !strings.Contains(page, "Offscreen scroll text") || strings.Contains(page, "Hidden clipped text") {
			t.Fatalf("page scope omitted reachable text or exposed hidden text: %q", page)
		}
	})

	t.Run("doctor honors explicit target", func(t *testing.T) {
		defer call("tab", "use", tab)
		other := ok(t, call("tab", "new"))["id"].(string)
		ok(t, call("tab", "close", other))
		result := ok(t, call("--tab", tab, "doctor"))
		if result["selectedTab"] != tab || result["checks"].(map[string]any)["selectedTab"] != true {
			t.Fatalf("doctor inspected the stored closed selection instead of --tab: %v", result)
		}
	})

	t.Run("readonly contenteditable remains unchanged", func(t *testing.T) {
		ok(t, call("eval", "--expression", `document.body.innerHTML = '<div id="editor" contenteditable="true" role="textbox" aria-readonly="true">Read only</div>'; true`))
		code(t, call("fill", "--css", "#editor", "--text", "Changed"), "NOT_EDITABLE")
		if call("eval", "--expression", `document.querySelector('#editor').innerText`).Result != "Read only" {
			t.Fatal("readonly editor was modified")
		}
	})

	t.Run("matching controls do not scroll", func(t *testing.T) {
		ok(t, call("eval", "--expression", `
document.body.innerHTML = '<div style="height:2000px"></div><input id="checked" type="checkbox" checked><select id="selected"><option value="a">A</option><option value="b" selected>B</option></select>';
window.scrollTo(0, 0);
true`))
		if ok(t, call("check", "--css", "#checked", "--checked", "true"))["effect"] != "none" {
			t.Fatal("matching checkbox did not report a no-op")
		}
		if call("eval", "--expression", "scrollY").Result != float64(0) {
			t.Fatal("matching checkbox moved the viewport")
		}
		if ok(t, call("select", "--css", "#selected", "--value", "b"))["effect"] != "none" {
			t.Fatal("matching selection did not report a no-op")
		}
		if call("eval", "--expression", "scrollY").Result != float64(0) {
			t.Fatal("matching select moved the viewport")
		}
	})

	t.Run("truncated frames remain discoverable", func(t *testing.T) {
		ok(t, call("eval", "--expression", `
document.body.innerHTML = Array.from({length:35}, () => '<iframe srcdoc="child"></iframe>').join('');
true`))
		result := ok(t, call("observe"))
		if result["framesTruncated"] != true {
			t.Fatal("observation silently omitted frames")
		}
		continuation, _ := result["continuation"].(map[string]any)
		frames, _ := continuation["frames"].(map[string]any)
		if frames["command"] != "call" {
			t.Fatal("missing complete frame-tree discovery hint")
		}
	})

	t.Run("fallback names exclude hidden DOM text", func(t *testing.T) {
		ok(t, call("eval", "--expression", `
document.body.innerHTML = '<div id="custom">Visible custom<span hidden>Hidden listener secret</span><span style="opacity:0">Transparent listener secret</span></div><section><h2 style="display:none">Hidden heading secret</h2><button>Visible action</button></section>';
document.querySelector('#custom').addEventListener('click', () => {});
true`))
		result := call("observe", "--scope", "page")
		ok(t, result)
		encoded := string(protocol.Encode(result))
		for _, hidden := range []string{"Hidden listener secret", "Transparent listener secret", "Hidden heading secret"} {
			if strings.Contains(encoded, hidden) {
				t.Fatalf("fallback metadata exposed hidden text %q", hidden)
			}
		}
		observed(t, result, "Visible custom")
	})
}
