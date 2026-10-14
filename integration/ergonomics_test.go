//go:build integration

package integration

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mathisarends/cdpsh/internal/protocol"
)

func observed(t *testing.T, r protocol.Response, name string) map[string]any {
	t.Helper()
	for _, item := range ok(t, r)["elements"].([]any) {
		e := item.(map[string]any)
		if e["name"] == name {
			return e
		}
	}
	t.Fatalf("missing %q in %s", name, protocol.Encode(r))
	return nil
}

func testErgonomics(t *testing.T, h *harness) {
	t.Helper()
	call := func(args ...string) protocol.Response {
		return h.call(append([]string{"--context", "ergonomics"}, args...)...)
	}
	tab := ok(t, call("tab", "new"))["id"].(string)
	defer h.call("tab", "close", tab)
	ok(t, call("eval", "--expression", `document.body.innerHTML='<main><section aria-label="Product A"><button>Buy</button></section><section aria-label="Product B"><button>Buy</button></section><label>Agree<input id="agree" type="checkbox"></label><select id="country" aria-label="Country"><option value="">Choose</option><option value="DE">Germany</option><option disabled value="FR">France</option></select><button aria-expanded="false" aria-pressed="mixed">Menu</button><input aria-label="Name" value="Mathi" placeholder="Full name" required><input type="password" aria-label="Password" value="never-echo-this"><input autocomplete="cc-number" aria-label="Card" value="never-echo-card"><div contenteditable="true" role="textbox" aria-label="Editor">Old</div><div id="custom">Custom click</div><div id="host"></div><div id="slots"><button slot="action">Slotted button</button></div><div style="opacity:0">Invisible text</div><div style="position:absolute;top:4000px">Below viewport</div></main>';document.querySelector('#custom').addEventListener('click',()=>window.customClicked=true);const root=document.querySelector('#host').attachShadow({mode:'open'});root.innerHTML='<section aria-label="Shadow form"><div id="nested"></div></section>';const closed=root.querySelector('#nested').attachShadow({mode:'closed'});closed.innerHTML='<input aria-label="Shadow name"><button onclick="window.closedClicked=(window.closedClicked||0)+1">Closed button</button><div id="localoverlay" style="display:none;position:fixed;inset:0;background:gray;z-index:9999"></div><span>Closed shadow text</span>';window.closedTest=closed;document.querySelector('#slots').attachShadow({mode:'open'}).innerHTML='<section aria-label="Slot group"><slot name="action"></slot></section>';window.checkChanges=0;document.querySelector('#agree').addEventListener('change',()=>window.checkChanges++);window.selectChanges=0;document.querySelector('#country').addEventListener('change',()=>window.selectChanges++);true`))
	obs := call("observe", "--max-bytes", "24000")
	encoded := string(protocol.Encode(obs))
	for _, secret := range []string{"never-echo-this", "never-echo-card", "Invisible text", "Below viewport"} {
		if strings.Contains(encoded, secret) {
			t.Fatalf("unexpected content %q", secret)
		}
	}
	if !strings.Contains(ok(t, obs)["text"].(string), "Closed shadow text") {
		t.Fatal("closed shadow text missing")
	}
	name := observed(t, obs, "Name")
	if name["value"] != "Mathi" || name["placeholder"] != "Full name" || name["required"] != true {
		t.Fatalf("missing live field state: %v", name)
	}
	menu := observed(t, obs, "Menu")
	if menu["expanded"] != false || menu["pressed"] != "mixed" {
		t.Fatalf("missing explicit false/mixed states: %v", menu)
	}
	agree := observed(t, obs, "Agree")
	if agree["checked"] != false {
		t.Fatalf("missing unchecked state: %v", agree)
	}
	closed := observed(t, obs, "Closed button")
	slotted := observed(t, obs, "Slotted button")
	if slotted["shadow"] == nil {
		t.Fatal("assigned slot lost composed shadow ancestry")
	}
	contexts, _ := json.Marshal(slotted["context"])
	if !strings.Contains(string(contexts), "Slot group") {
		t.Fatalf("slotted element lost semantic grouping: %v", slotted)
	}
	boundaries := closed["shadow"].([]any)
	if len(boundaries) != 2 || boundaries[0].(map[string]any)["mode"] != "open" || boundaries[1].(map[string]any)["mode"] != "closed" {
		t.Fatalf("nested shadow boundaries missing: %v", closed)
	}
	buyGroups := []string{}
	for _, item := range ok(t, obs)["elements"].([]any) {
		e := item.(map[string]any)
		if e["name"] == "Buy" {
			b, _ := json.Marshal(e["context"])
			buyGroups = append(buyGroups, string(b))
		}
	}
	if len(buyGroups) != 2 || buyGroups[0] == buyGroups[1] || !strings.Contains(buyGroups[0], "Product A") || !strings.Contains(buyGroups[1], "Product B") {
		t.Fatalf("duplicate buttons lack context: %v", buyGroups)
	}
	ok(t, call("click", "--ref", closed["ref"].(string)))
	if call("eval", "--expression", "window.closedClicked").Result != float64(1) {
		t.Fatal("closed shadow click did not reach target")
	}
	ok(t, call("eval", "--expression", `window.closedTest.querySelector('#localoverlay').style.display='block';true`))
	code(t, call("click", "--ref", closed["ref"].(string)), "OBSCURED")
	ok(t, call("eval", "--expression", `window.closedTest.querySelector('#localoverlay').style.display='none';true`))
	ok(t, call("fill", "--ref", observed(t, obs, "Shadow name")["ref"].(string), "--text", "inside shadow"))
	ok(t, call("focus", "--ref", observed(t, obs, "Shadow name")["ref"].(string)))
	ok(t, call("type", "--text", "!"))
	if call("eval", "--expression", `window.closedTest.querySelector('input').value`).Result != "inside shadow!" {
		t.Fatal("focus/type missed closed root input")
	}
	ok(t, call("fill", "--ref", observed(t, obs, "Editor")["ref"].(string), "--text", "Native editor input"))
	if call("eval", "--expression", `document.querySelector('[contenteditable]').innerText`).Result != "Native editor input" {
		t.Fatal("contenteditable fill failed")
	}
	ok(t, call("check", "--ref", agree["ref"].(string), "--checked", "true"))
	if ok(t, call("check", "--ref", agree["ref"].(string), "--checked", "true"))["effect"] != "none" {
		t.Fatal("repeated check changed state")
	}
	if call("eval", "--expression", "window.checkChanges").Result != float64(1) {
		t.Fatal("check emitted duplicate change")
	}
	ok(t, call("check", "--ref", agree["ref"].(string), "--checked", "false"))
	ok(t, call("eval", "--expression", `document.querySelector('#agree').indeterminate=true;true`))
	ok(t, call("check", "--ref", agree["ref"].(string), "--checked", "false"))
	if call("eval", "--expression", `document.querySelector('#agree').checked||document.querySelector('#agree').indeterminate`).Result != false {
		t.Fatal("check did not clear mixed state")
	}
	country := observed(t, obs, "Country")["ref"].(string)
	ok(t, call("select", "--ref", country, "--value", "DE"))
	if ok(t, call("select", "--ref", country, "--label", "Germany"))["effect"] != "none" {
		t.Fatal("repeated select emitted change")
	}
	if call("eval", "--expression", "window.selectChanges").Result != float64(1) {
		t.Fatal("select emitted duplicate change")
	}
	code(t, call("select", "--ref", country, "--value", "FR"), "DISABLED")
	listed := ok(t, call("options", "--ref", country, "--limit", "1"))
	if listed["total"] != float64(3) || listed["next"] == nil {
		t.Fatal("options continuation missing")
	}
	ok(t, call("select", "--ref", country, "--index", "0"))
	code(t, call("select", "--ref", country, "--index", "10"), "TARGET_NOT_FOUND")
	custom := observed(t, obs, "Custom click")
	if custom["inferred"] != true {
		t.Fatal("listener-backed element was not marked inferred")
	}
	ok(t, call("click", "--ref", custom["ref"].(string)))
	if call("eval", "--expression", "window.customClicked").Result != true {
		t.Fatal("custom listener target was not operable")
	}

	// More observations retain identity for three snapshots, including filtered reads.
	checkboxRef := agree["ref"].(string)
	ok(t, call("observe", "--role", "button"))
	ok(t, call("observe", "--role", "textbox"))
	ok(t, call("check", "--ref", checkboxRef, "--checked", "true"))
	ok(t, call("observe"))
	code(t, call("check", "--ref", checkboxRef, "--checked", "true"), "STALE_REF")

	// A single large text node must obey viewport scope; immutable read pages
	// must reconstruct all UTF-8 content even after the live page changes.
	ok(t, call("eval", "--expression", `document.body.innerHTML='<pre id="long"></pre><button>After text</button>';document.querySelector('pre').textContent=('Grüße🙂 "Zitat"\n').repeat(2000);true`))
	viewport := ok(t, call("observe"))
	if viewport["textBytes"].(float64) > 3000 {
		t.Fatal("viewport text includes offscreen lines")
	}
	page := call("observe", "--scope", "page", "--max-bytes", "2048")
	if len(protocol.Encode(page)) > 2048 {
		t.Fatal("observation budget exceeded")
	}
	result := ok(t, page)
	id := result["observation"].(string)
	text := result["text"].(string)
	offset := len(text)
	ok(t, call("eval", "--expression", `document.querySelector('pre').textContent='live content changed';true`))
	for offset < int(result["textBytes"].(float64)) {
		r := call("read", "--observation", id, "--offset", strconv.Itoa(offset), "--max-bytes", "2048")
		if len(protocol.Encode(r)) > 2048 {
			t.Fatal("read budget exceeded")
		}
		part := ok(t, r)["text"].(string)
		if part == "" || !utf8.ValidString(part) {
			t.Fatal("read did not advance on a UTF-8 boundary")
		}
		text += part
		offset += len(part)
	}
	if len(text) != int(result["textBytes"].(float64)) || strings.Count(text, "Grüße🙂") != 2000 || strings.Contains(text, "live content changed") {
		t.Fatal("immutable text continuation lost content")
	}
	code(t, call("read", "--observation", id, "--offset", "3"), "INVALID_ARGUMENT")

	// Every element remains reachable beyond the 500-element per-response limit.
	ok(t, call("eval", "--expression", `document.body.innerHTML=Array.from({length:510},(_,i)=>'<button>B'+i+'</button>').join('');true`))
	paginated := ok(t, call("observe", "--scope", "page", "--limit", "500", "--max-bytes", "65536"))
	seen := map[string]bool{}
	for _, item := range paginated["elements"].([]any) {
		seen[item.(map[string]any)["name"].(string)] = true
	}
	for paginated["elementNext"] != nil {
		next := int(paginated["elementNext"].(float64))
		paginated = ok(t, call("observe", "--scope", "page", "--offset", strconv.Itoa(next), "--limit", "500", "--max-bytes", "65536"))
		for _, item := range paginated["elements"].([]any) {
			seen[item.(map[string]any)["name"].(string)] = true
		}
	}
	if len(seen) != 510 {
		t.Fatalf("pagination lost targets: %d", len(seen))
	}

	// Reconnect remembers aliases and preserves selection but expires references.
	registration := ok(t, h.call("connect", "--endpoint", h.endpoint, "--name", "work"))
	next := registration["nextCommand"].(map[string]any)
	if next["flags"].(map[string]any)["browser"] != "work" {
		t.Fatal("connect hint lost registered alias")
	}
	ok(t, h.call("--browser", "work", "--context", "ergonomics", "tab", "use", tab))
	aliasRef := ref(t, h.call("--browser", "work", "--context", "ergonomics", "observe"), "B0")
	ok(t, h.call("--browser", "work", "reconnect"))
	stale := h.call("--browser", "work", "--context", "ergonomics", "click", "--ref", aliasRef)
	code(t, stale, "STALE_REF")
	if stale.Error.Recovery == nil || stale.Error.Recovery.Flags["browser"] != "work" || stale.Error.Recovery.Flags["context"] != "ergonomics" || stale.Error.Recovery.Flags["tab"] != tab {
		t.Fatal("recovery lost request context")
	}
	doctor := ok(t, h.call("--browser", "work", "--context", "ergonomics", "doctor"))
	checks := doctor["checks"].(map[string]any)
	if checks["cdp"] != true || checks["selectedTab"] != true {
		t.Fatalf("doctor did not check live targets: %v", checks)
	}
	code(t, h.call("--browser", "unknown", "reconnect"), "NO_BROWSER")
	ok(t, h.call("--browser", "work", "disconnect"))
}
