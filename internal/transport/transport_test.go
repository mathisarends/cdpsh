package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/target"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

func TestTransportSpike(t *testing.T) {
	var calls atomic.Int64
	var endpoint string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/version" {
			_ = json.NewEncoder(w).Encode(map[string]string{"webSocketDebuggerUrl": endpoint})
			return
		}
		conn, _, _, err := ws.UpgradeHTTP(r, w)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			data, _, err := wsutil.ReadClientData(conn)
			if err != nil {
				return
			}
			calls.Add(1)
			var msg struct {
				ID        int64  `json:"id"`
				SessionID string `json:"sessionId"`
				Method    string `json:"method"`
			}
			_ = json.Unmarshal(data, &msg)
			var result any = map[string]any{"method": msg.Method, "session": msg.SessionID}
			if msg.Method == "Target.getTargets" {
				result = map[string]any{"targetInfos": []any{}}
			}
			if msg.Method == "Test.drop" {
				return
			}
			if msg.Method == "Test.event" {
				event, _ := json.Marshal(map[string]any{"method": "Future.event", "sessionId": "s1", "params": map[string]any{"value": 1}})
				_ = wsutil.WriteServerMessage(conn, ws.OpText, event)
			}
			response, _ := json.Marshal(map[string]any{"id": msg.ID, "result": result})
			if wsutil.WriteServerMessage(conn, ws.OpText, response) != nil {
				return
			}
		}
	}))
	defer srv.Close()
	endpoint = "ws" + strings.TrimPrefix(srv.URL, "http") + "/devtools/browser/test"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events := make(chan Event, 1)
	c, err := Dial(ctx, srv.URL, func(e Event) { events <- e })
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := target.GetTargets().Do(cdp.WithExecutor(ctx, Session{Client: c})); err != nil {
		t.Fatalf("typed browser call: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var res struct {
				Session string `json:"session"`
				Method  string `json:"method"`
			}
			if err := c.Call(ctx, "s1", "Future.unpinned", map[string]any{"x": 1}, &res); err != nil {
				t.Error(err)
			}
			if res.Session != "s1" || res.Method != "Future.unpinned" {
				t.Errorf("misrouted response: %+v", res)
			}
		}()
	}
	wg.Wait()
	if err := c.Call(ctx, "s1", "Test.event", nil, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-events:
		if e.Session != "s1" || e.Method != "Future.event" {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("event lost")
	}
	before := calls.Load()
	if err := c.Call(ctx, "s1", "Test.drop", nil, nil); err == nil {
		t.Fatal("expected connection loss")
	}
	if calls.Load() != before+1 {
		t.Fatal("request replayed")
	}
	// Explicit reconnect uses the same discovery endpoint, with a fresh router.
	c2, err := Dial(ctx, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if err := c2.Call(ctx, "", "Browser.getVersion", nil, nil); err != nil {
		t.Fatal(err)
	}
}
func TestDiscoveryDoesNotExposeCredentials(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := Discover(ctx, "http://user:secret@127.0.0.1:1?token=private")
	if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private") {
		t.Fatalf("bad discovery error: %v", err)
	}
}
