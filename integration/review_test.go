//go:build integration

package integration

import (
	"strings"
	"testing"

	"cdpsh/internal/protocol"
)

func testReviewRegressions(t *testing.T, h *harness) {
	t.Helper()
	call := func(args ...string) protocol.Response {
		return h.call(append([]string{"--context", "review"}, args...)...)
	}
	tab := ok(t, call("tab", "new"))["id"].(string)
	defer h.call("tab", "close", tab)

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
