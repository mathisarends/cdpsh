package cli

import (
	"bytes"
	"encoding/json"
	"github.com/mathisarends/cdpsh/internal/protocol"
	"strings"
	"testing"
)

func TestArgumentContracts(t *testing.T) {
	t.Setenv("CDPSH_BROWSER", "env-browser")
	t.Setenv("CDPSH_CONTEXT", "env-context")
	valid := [][]string{{"--browser", "explicit", "click", "--css", "#submit", "--timeout", "2s"}, {"wait", "--css", "#later", "--state", "visible"}, {"wait", "--url", "https://example.com/"}, {"click", "--x", "12.5", "--y", "2"}, {"scroll", "--dy", "-300"}, {"observe", "--scope", "page", "--limit", "100", "--max-bytes", "2048"}, {"call", "Future.unknown", "--scope", "browser"}, {"help", "tab", "new"}, {"press", "Ctrl+Shift+A"}, {"fill", "o1:e2", "--text=hello"}}
	for _, args := range valid {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			r, _, err := parse(args)
			if err != nil {
				t.Fatal(err)
			}
			if r.Context != "env-context" {
				t.Fatalf("env context ignored: %+v", r)
			}
		})
	}
	invalid := [][]string{{"click"}, {"click", "--css", "#x", "--role", "button"}, {"click", "--name", "X"}, {"click", "--x", "0"}, {"click", "--x", "NaN", "--y", "2"}, {"wait", "--state", "visible"}, {"wait", "--css", "x", "--state", "load"}, {"wait", "--url", "x", "--state", "load"}, {"observe", "--since", "o1"}, {"observe", "--limit", "0"}, {"observe", "--max-bytes", "1"}, {"type", "--css", "x"}, {"goto", "relative/path"}, {"connect", "--endpoint", "file:///tmp"}, {"status", "--timeout", "0s"}, {"status", "--timeout", "6m"}, {"status", "--format", "xml"}, {"status", "--context", "x", "--context", "y"}, {"press", "Ctrl+Unknown"}, {"bogus"}, {"tab"}}
	for _, args := range invalid {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if _, _, err := parse(args); err == nil {
				t.Fatal("expected invalid arguments")
			}
		})
	}
	r, _, err := parse([]string{"--browser", "explicit", "status"})
	if err != nil || r.Browser != "explicit" {
		t.Fatal("flag must override environment")
	}
}
func TestInputContracts(t *testing.T) {
	for _, test := range []struct {
		args  []string
		stdin string
		want  string
		bad   bool
	}{
		{[]string{"fill", "--css", "#x", "--text", ""}, "", "", false},
		{[]string{"type", "--text-file", "-"}, "Grüße\n", "Grüße\n", false},
		{[]string{"eval", "--expression", "1+1"}, "", "1+1", false},
		{[]string{"eval"}, "", "", true},
		{[]string{"type", "--text", "x", "--text-file", "-"}, "", "", true},
		{[]string{"call", "Test.anything"}, "", "{}", false},
		{[]string{"call", "Test.anything", "--params", "null"}, "", "", true},
		{[]string{"call", "Test.anything", "--params", "[]"}, "", "", true},
		{[]string{"call", "Test.anything", "--params-file", "-"}, `{"x":1}`, `{"x":1}`, false},
	} {
		r, _, err := parse(test.args)
		if err != nil {
			t.Fatal(err)
		}
		err = input(&r, strings.NewReader(test.stdin))
		if (err != nil) != test.bad {
			t.Fatalf("%v: %v", test.args, err)
		}
		if !test.bad && r.Payload != test.want {
			t.Fatalf("got %q want %q", r.Payload, test.want)
		}
	}
}
func TestOneJSONResponseIncludingLocalFailures(t *testing.T) {
	for _, args := range [][]string{{"click"}, {"eval"}, {"schema", "fill"}, {"schema", "call"}, {"help"}, {"--help"}, {"version"}} {
		var out, stderr bytes.Buffer
		exit := Run(args, strings.NewReader(""), &out, &stderr)
		var response protocol.Response
		dec := json.NewDecoder(&out)
		if err := dec.Decode(&response); err != nil {
			t.Fatal(err)
		}
		if dec.More() {
			t.Fatal("extra JSON output")
		}
		if stderr.Len() != 0 {
			t.Fatal("unexpected diagnostics")
		}
		if exit != response.ExitCode() {
			t.Fatal("wrong exit code")
		}
		if len(args) == 1 && args[0] == "--help" {
			result, ok := response.Result.(map[string]any)
			usage, _ := result["usage"].(string)
			if !ok || !strings.HasPrefix(usage, "cdp ") || result["commands"] == nil {
				t.Fatal("root --help must show the cdp command overview")
			}
		}
	}
}
