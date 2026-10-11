package controller

import (
	"cdpsh/internal/protocol"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestQueuedAndDispatchedTimeoutEffects(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	queued := &job{done: make(chan outcome, 1), mayMutate: true}
	_, err := await(ctx, queued)
	if protocol.Failure("", err).Error.Effect != "none" {
		t.Fatal("queued action reported an effect")
	}
	dispatched := &job{done: make(chan outcome, 1), mayMutate: true}
	dispatched.started.Store(true)
	_, err = await(ctx, dispatched)
	if protocol.Failure("", err).Error.Effect != "possible" {
		t.Fatal("dispatched action lost uncertainty")
	}
}
func TestArtifactsDoNotOverwriteAndLargeValuesAreLossless(t *testing.T) {
	c := New(t.TempDir())
	large := `"` + strings.Repeat("abc", 10000) + `"`
	v, err := c.externalize([]byte(large), "application/json")
	if err != nil {
		t.Fatal(err)
	}
	file := v.(map[string]any)["file"].(map[string]any)
	path := file["path"].(string)
	data, err := os.ReadFile(path)
	if err != nil || string(data) != large {
		t.Fatal("artifact content changed")
	}
	if _, err = c.writeFile(path, []byte("replacement"), false); err == nil {
		t.Fatal("overwrote file")
	}
	if _, err = c.writeFile(path, []byte("replacement"), true); err != nil {
		t.Fatal(err)
	}
}

func TestAXIgnoresFutureChromeProperties(t *testing.T) {
	var node axNode
	if err := json.Unmarshal([]byte(`{"ignored":false,"backendDOMNodeId":12,"role":{"value":"button","type":"futureType"},"name":{"value":"Send"},"properties":[{"name":"uninteresting","value":{"value":true}}],"futureField":42}`), &node); err != nil {
		t.Fatal(err)
	}
	if !interactive(node) || axString(node.Name) != "Send" {
		t.Fatal("new AX property broke observation")
	}
}
func TestClippingPreservesUTF8AndBudgets(t *testing.T) {
	for n := 3; n < 40; n++ {
		s := clip(strings.Repeat("🙂ü", 20), n)
		if len(s) > n || !utf8.ValidString(s) {
			t.Fatalf("n=%d: %q", n, s)
		}
	}
}
func TestNoBrowserAndEpoch(t *testing.T) {
	c := New(t.TempDir())
	other := New(t.TempDir())
	if c.Epoch == other.Epoch {
		t.Fatal("epochs reused")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r := c.Handle(ctx, protocol.Request{Command: "info"})
	if r.Error.Code != "NO_BROWSER" {
		t.Fatal(r)
	}
	status := c.Handle(ctx, protocol.Request{Command: "status"})
	if !status.OK {
		t.Fatal(status)
	}
}
