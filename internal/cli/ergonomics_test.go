package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"cdpsh/internal/protocol"
)

func TestErgonomicArguments(t *testing.T) {
	for _, args := range [][]string{{"focus", "o1:e1"}, {"check", "--role", "checkbox", "--checked", "false"}, {"select", "o1:e1", "--value", ""}, {"select", "--css", "select", "--label", "Germany"}, {"reconnect"}, {"browsers"}, {"observe", "--offset", "500"}, {"read", "--observation", "o1", "--offset", "20"}} {
		if _, _, err := parse(args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	for _, args := range [][]string{{"focus"}, {"check", "o1:e1"}, {"check", "o1:e1", "--checked", "maybe"}, {"select", "o1:e1"}, {"select", "o1:e1", "--value", "DE", "--label", "Germany"}, {"read"}, {"read", "--observation", "o1", "--offset", "-1"}, {"reconnect", "--endpoint", "http://localhost:9222"}} {
		if _, _, err := parse(args); err == nil {
			t.Fatalf("%v accepted invalid arguments", args)
		}
	}
}

func TestFocusedSchemasAndExamples(t *testing.T) {
	for _, args := range [][]string{{"options", "--ref", "o1:e1", "--offset", "60"}, {"select", "--css", "select", "--index", "2"}} {
		if _, _, err := parse(args); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"options"}, {"select", "o1:e1", "--index", "NaN"}, {"select", "o1:e1", "--index", "1", "--value", "x"}} {
		if _, _, err := parse(args); err == nil {
			t.Fatalf("invalid option args accepted: %v", args)
		}
	}
	for command, d := range Definitions() {
		r := help(command, true)
		result := r.Result.(map[string]any)
		if len(commandExamples(command)) == 0 || result["inputSchema"] == nil || result["resultSchema"] == nil {
			t.Fatalf("missing discovery for %s", command)
		}
		if _, err := json.Marshal(result); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"limit", "offset", "max-bytes"} {
			if f, ok := d.Flags[key]; ok && f.Type != "integer" {
				t.Fatalf("%s --%s type=%s", command, key, f.Type)
			}
		}
	}
	for _, constraint := range commandConstraints("fill") {
		if strings.Contains(constraint, "Coordinates") {
			t.Fatal("fill exposes irrelevant coordinate constraints")
		}
	}
	if !strings.Contains(strings.Join(commandConstraints("press"), " "), "ArrowLeft") {
		t.Fatal("supported keys undiscoverable")
	}
}

func TestDoctorWithoutController(t *testing.T) {
	t.Setenv("CDPSH_HOME", t.TempDir())
	var out, stderr bytes.Buffer
	if exit := Run([]string{"doctor"}, strings.NewReader(""), &out, &stderr); exit != 0 {
		t.Fatalf("doctor failed: %s", out.String())
	}
	var r protocol.Response
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if !r.OK || !strings.Contains(out.String(), `"controller":false`) || !strings.Contains(out.String(), "nextCommand") {
		t.Fatalf("cold doctor lost diagnostics: %s", out.String())
	}
}

func TestTextTreePreservesStateBoundariesAndRecovery(t *testing.T) {
	var result any
	err := json.Unmarshal([]byte(`{"observation":"o1","url":"https://example.com","title":"Form","elements":[{"ref":"o1:e1","role":"checkbox","name":"Agree","checked":false,"expanded":false,"context":[{"role":"region","name":"Product A"}],"shadow":[{"host":"x-form","mode":"open"},{"host":"x-inner","mode":"closed"}]}],"text":"Body","frames":[{"id":"child","url":"https://example.com/frame","supported":false}],"truncated":true,"continuation":{"text":{"command":"read","flags":{"observation":"o1","offset":"4"}}}}`), &result)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = renderText(&out, protocol.Success("tab", result)); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"Product A", "SHADOW(open)", "SHADOW(closed)", "checked=false", "expanded=false", "IFRAME unsupported", "continuation=", "[o1:e1]"} {
		if !strings.Contains(out.String(), part) {
			t.Fatalf("text lost %q: %s", part, out.String())
		}
	}
	out.Reset()
	e := protocol.Fail("STALE_REF", "Expired")
	e.Recovery = &protocol.Invocation{Command: "observe", Flags: map[string]string{"context": "agent-a"}}
	if err = renderText(&out, protocol.Failure("tab", e)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "agent-a") {
		t.Fatal("text error dropped recovery context")
	}
}
