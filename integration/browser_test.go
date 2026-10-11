//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"cdpsh/internal/protocol"
)

const fixture = `<!doctype html><html><head><title>cdpsh fixture</title><style>body{font:16px sans-serif;padding:24px}input,button{display:block;margin:12px}#covered{position:absolute;left:450px;top:60px;width:100px;height:40px}#overlay{position:absolute;left:440px;top:50px;width:140px;height:70px;background:#777;z-index:10}#scroller{height:80px;width:200px;overflow:auto}#long{height:900px}</style></head><body>
<label>Email<input id=email></label><label>Password<input id=password type=password></label>
<button id=submit onclick="document.querySelector('#result').textContent='Signed in: '+document.querySelector('#email').value">Sign in</button><p id=result></p>
<button id=covered>Covered</button><div id=overlay></div><button class=duplicate>Duplicate</button><button class=duplicate>Duplicate</button>
<button id=disabled disabled>Disabled</button><input id=readonly aria-label=Readonly readonly value=locked>
<div id=scroller><div id=long>Scroll region</div></div><div id=later></div><iframe src=/frame></iframe>
<script>const host=document.createElement('div');host.id='host';document.body.append(host);host.attachShadow({mode:'open'}).innerHTML='<button onclick="this.textContent=\'Shadow clicked\'">Shadow button</button>';</script>
</body></html>`

type harness struct {
	t                   *testing.T
	exe, home, endpoint string
}

func (h *harness) call(args ...string) protocol.Response { h.t.Helper(); return h.run("", args...) }
func (h *harness) run(stdin string, args ...string) protocol.Response {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, h.exe, args...)
	hide(cmd)
	cmd.Env = append(os.Environ(), "CDPSH_HOME="+h.home)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	var r protocol.Response
	if e := json.Unmarshal(stdout.Bytes(), &r); e != nil {
		h.t.Fatalf("%v: invalid output %q; stderr=%s err=%v", args, stdout.String(), stderr.String(), err)
	}
	actual := 0
	if err != nil {
		var e *exec.ExitError
		if errors.As(err, &e) {
			actual = e.ExitCode()
		} else {
			h.t.Fatalf("%v: %v", args, err)
		}
	}
	if actual != r.ExitCode() {
		h.t.Fatalf("%v: exit=%d response=%+v stderr=%s", args, actual, r, stderr.String())
	}
	if stderr.Len() > 0 {
		h.t.Fatalf("%v: unexpected stderr: %s", args, stderr.String())
	}
	return r
}
func ok(t *testing.T, r protocol.Response) map[string]any {
	t.Helper()
	if !r.OK {
		t.Fatalf("expected success: %+v", r.Error)
	}
	v, _ := r.Result.(map[string]any)
	return v
}
func code(t *testing.T, r protocol.Response, want string) {
	t.Helper()
	if r.OK || r.Error == nil || r.Error.Code != want {
		t.Fatalf("want %s, got %+v", want, r)
	}
}
func ref(t *testing.T, r protocol.Response, name string) string {
	t.Helper()
	for _, item := range ok(t, r)["elements"].([]any) {
		v := item.(map[string]any)
		if v["name"] == name {
			return v["ref"].(string)
		}
	}
	t.Fatalf("missing observed element %q: %+v", name, r)
	return ""
}

func TestBlankBrowserAcceptance(t *testing.T) {
	path := os.Getenv("CDPSH_TEST_BROWSER")
	if path == "" {
		t.Skip("set CDPSH_TEST_BROWSER to a Chromium executable")
	}
	dir := t.TempDir()
	profile := filepath.Join(dir, "blank-profile")
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(path, "--headless=new", "--remote-debugging-port=0", "--remote-debugging-address=127.0.0.1", "--user-data-dir="+profile, "--no-first-run", "--no-default-browser-check", "about:blank")
	hide(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	var endpoint string
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(filepath.Join(profile, "DevToolsActivePort"))
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) > 1 {
				endpoint = "http://127.0.0.1:" + strings.TrimSpace(lines[0])
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	if endpoint == "" {
		t.Fatal("isolated Chrome did not expose remote debugging")
	}
	exe := filepath.Join(dir, "cdpsh")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	build := exec.Command("go", "build", "-o", exe, "../cmd/cdpsh")
	hide(build)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	h := &harness{t: t, exe: exe, home: filepath.Join(dir, "controller"), endpoint: endpoint}
	t.Cleanup(func() { h.call("controller", "stop") })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/slow" {
			time.Sleep(time.Second)
		}
		if r.URL.Path == "/frame" {
			_, _ = io.WriteString(w, "<button>Frame button</button>")
			return
		}
		_, _ = io.WriteString(w, fixture)
	}))
	defer srv.Close()
	code(t, h.call("info"), "NO_BROWSER")
	ok(t, h.call("connect", "--endpoint", endpoint))
	code(t, h.call("info"), "NO_TAB")
	status := ok(t, h.call("status"))
	epoch := status["epoch"]
	if ok(t, h.call("status"))["epoch"] != epoch {
		t.Fatal("controller was not reused")
	}
	initial := ok(t, h.call("tabs"))["tabs"].([]any)
	if len(initial) != 1 {
		t.Fatalf("connect changed blank browser tabs: %v", initial)
	}
	original := initial[0].(map[string]any)["id"].(string)
	a := ok(t, h.call("--context", "alpha", "tab", "new"))["id"].(string)
	b := ok(t, h.call("--context", "beta", "tab", "new"))["id"].(string)
	ok(t, h.call("--context", "alpha", "goto", srv.URL))
	ok(t, h.call("--context", "beta", "goto", srv.URL+"/other"))
	alpha := func(args ...string) protocol.Response {
		return h.call(append([]string{"--context", "alpha"}, args...)...)
	}
	obs := alpha("observe")
	email := ref(t, obs, "Email")
	password := ref(t, obs, "Password")
	submit := ref(t, obs, "Sign in")
	ok(t, alpha("fill", "--ref", email, "--text", "agent@example.test"))
	secret := "secret-123"
	response := h.run(secret, "--context", "alpha", "fill", "--ref", password, "--text-file", "-")
	ok(t, response)
	if strings.Contains(string(protocol.Encode(response)), secret) {
		t.Fatal("fill echoed password")
	}
	ok(t, alpha("click", "--ref", submit))
	ok(t, alpha("eval", "--expression", `document.querySelector('#result').textContent === 'Signed in: agent@example.test'`))
	if alpha("eval", "--expression", `document.querySelector('#result').textContent`).Result != "Signed in: agent@example.test" {
		t.Fatal("native click did not run the handler")
	}
	old := email
	obs = alpha("observe")
	code(t, alpha("click", "--ref", old), "STALE_REF")
	// A new observation in another context does not expire alpha's references.
	ok(t, h.call("--context", "gamma", "tab", "use", a))
	ok(t, h.call("--context", "gamma", "observe"))
	ok(t, alpha("fill", "--ref", ref(t, obs, "Email"), "--text", ""))
	code(t, alpha("click", "--role", "button", "--name", "Duplicate"), "AMBIGUOUS_TARGET")
	code(t, alpha("click", "--css", ".duplicate"), "AMBIGUOUS_TARGET")
	code(t, alpha("click", "--css", "#covered"), "OBSCURED")
	code(t, alpha("click", "--css", "#disabled"), "DISABLED")
	code(t, alpha("fill", "--css", "#readonly", "--text", "x"), "NOT_EDITABLE")
	code(t, alpha("click", "--x", "-1", "--y", "0"), "INVALID_ARGUMENT")
	shadow := ref(t, alpha("observe", "--scope", "page"), "Shadow button")
	ok(t, alpha("click", "--ref", shadow))
	_ = ref(t, alpha("observe"), "Shadow clicked")
	removed := ref(t, alpha("observe"), "Sign in")
	ok(t, alpha("eval", "--expression", `document.querySelector('#submit').remove()`))
	code(t, alpha("click", "--ref", removed), "STALE_REF")
	frames := ok(t, alpha("observe"))["frames"].([]any)
	if len(frames) < 2 {
		t.Fatal("child frame not discoverable")
	}
	child := frames[1].(map[string]any)["id"].(string)
	code(t, alpha("click", "--frame", child, "--css", "button"), "UNSUPPORTED")
	code(t, alpha("eval", "--expression", "throw new Error('fixture failure')"), "EXECUTION_ERROR")
	code(t, alpha("cdp", "call", "Future.nonexistent"), "UNSUPPORTED")
	ok(t, alpha("cdp", "call", "Browser.getVersion", "--scope", "browser"))
	ok(t, alpha("cdp", "call", "Runtime.evaluate", "--params", `{"expression":"1+2","returnByValue":true}`))
	large := ok(t, alpha("eval", "--expression", `'abc'.repeat(20000)`))["file"].(map[string]any)
	data, err := os.ReadFile(large["path"].(string))
	if err != nil {
		t.Fatal(err)
	}
	var full string
	if json.Unmarshal(data, &full) != nil || len(full) != 60000 {
		t.Fatal("large result lost data")
	}
	shot := filepath.Join(dir, "viewport.png")
	ok(t, alpha("screenshot", "--out", shot))
	code(t, alpha("screenshot", "--out", shot), "EXECUTION_ERROR")
	ok(t, alpha("screenshot", "--out", shot, "--overwrite"))
	// Explicit waits must not hold the tab's action queue.
	var wg sync.WaitGroup
	wg.Add(1)
	waited := make(chan protocol.Response, 1)
	go func() {
		defer wg.Done()
		waited <- alpha("wait", "--css", "#later button", "--state", "visible", "--timeout", "4s")
	}()
	time.Sleep(150 * time.Millisecond)
	ok(t, alpha("eval", "--expression", `document.querySelector('#later').innerHTML='<button>Ready</button>'`))
	wg.Wait()
	ok(t, <-waited)
	code(t, alpha("wait", "--css", "#missing", "--state", "visible", "--timeout", "100ms"), "TIMEOUT")
	ok(t, alpha("fill", "--css", "#email", "--text", "typed"))
	ok(t, alpha("press", "Ctrl+A"))
	ok(t, alpha("type", "--text", "replacement"))
	if alpha("eval", "--expression", `document.querySelector('#email').value`).Result != "replacement" {
		t.Fatal("focus/type/press failed")
	}
	ok(t, alpha("scroll", "--dy", "50", "--css", "#scroller"))
	if alpha("eval", "--expression", `document.querySelector('#scroller').scrollTop > 0`).Result != true {
		t.Fatal("target scrolling had no effect")
	}
	ok(t, alpha("wait", "--state", "load"))
	ok(t, alpha("wait", "--url", srv.URL+"/"))
	// Concurrent selections in beta cannot redirect accepted alpha requests.
	var requests sync.WaitGroup
	for i := 0; i < 12; i++ {
		requests.Add(1)
		go func(i int) {
			defer requests.Done()
			if i%2 == 0 {
				ok(t, h.call("--context", "beta", "tab", "use", b))
			} else {
				r := alpha("info")
				if r.Tab != a {
					t.Errorf("context redirected request: %s", r.Tab)
				}
			}
		}(i)
	}
	requests.Wait()
	ok(t, h.call("--context", "alpha", "--tab", b, "info"))
	if ok(t, alpha("status"))["selectedTab"] != a {
		t.Fatal("explicit tab override persisted")
	}
	navRef := ref(t, alpha("observe"), "Email")
	ok(t, alpha("goto", srv.URL+"/second"))
	code(t, alpha("click", "--ref", navRef), "STALE_REF")
	ok(t, alpha("back"))
	ok(t, alpha("forward"))
	ok(t, alpha("reload"))
	timed := alpha("goto", srv.URL+"/slow", "--timeout", "150ms")
	code(t, timed, "TIMEOUT")
	if timed.Error.Effect != "possible" {
		t.Fatal("navigation timeout lost possible effect")
	}
	ok(t, alpha("wait", "--state", "load", "--timeout", "3s"))
	ok(t, alpha("eval", "--expression", `document.body.innerHTML=Array.from({length:300},(_,i)=>'<button>'+('Long name '+i+' ').repeat(25)+'</button>').join('')`))
	bounded := alpha("observe", "--scope", "page", "--limit", "300", "--max-bytes", "2048")
	if len(protocol.Encode(bounded)) > 2048 || ok(t, bounded)["truncated"] != true {
		t.Fatalf("output budget violated: %d", len(protocol.Encode(bounded)))
	}
	reconnectRef := ref(t, alpha("observe", "--scope", "page", "--limit", "1"), strings.Repeat("Long name 0 ", 25)[:197]+"…")
	ok(t, h.call("connect", "--endpoint", endpoint))
	code(t, alpha("click", "--ref", reconnectRef), "STALE_REF")
	ok(t, h.call("tab", "close", b))
	code(t, h.call("--context", "beta", "info"), "TAB_CLOSED")
	ok(t, h.call("disconnect"))
	ok(t, h.call("connect", "--endpoint", endpoint))
	tabs := ok(t, h.call("tabs"))["tabs"].([]any)
	found := false
	for _, v := range tabs {
		if v.(map[string]any)["id"] == original {
			found = true
		}
	}
	if !found {
		t.Fatal("disconnect closed an unrelated original tab")
	}
	// Closing the transport after native input must never replay that input.
	ok(t, alpha("goto", srv.URL))
	ok(t, alpha("fill", "--css", "#email", "--text", "one"))
	ok(t, h.call("disconnect"))
	code(t, alpha("type", "--text", "two"), "NO_BROWSER")
	ok(t, h.call("connect", "--endpoint", endpoint))
	if alpha("eval", "--expression", `document.querySelector('#email').value`).Result != "one" {
		t.Fatal("input replayed after disconnect")
	}
	ok(t, h.call("doctor"))
	t.Logf("Blank-profile Chrome acceptance passed; controller epoch %s; screenshot %s", fmt.Sprint(epoch), shot)
}
