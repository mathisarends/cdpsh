package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"cdpsh/internal/ipc"
	"cdpsh/internal/protocol"
	"cdpsh/internal/transport"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/target"
)

type Controller struct {
	mu         sync.Mutex
	manage     sync.Mutex
	browsers   map[string]*browser
	selections map[string]string
	Epoch      string
	Home       string
	stop       chan struct{}
	once       sync.Once
}
type browser struct {
	client   *transport.Client
	endpoint string
	mu       sync.Mutex
	tabs     map[string]*tab
}
type tab struct {
	browser      *browser
	id           string
	session      string // Guarded by mu; shared with the browser event reader.
	mu           sync.Mutex
	dead         bool
	observations map[string]*observation
	queue        []*job
	wake         chan struct{}
}
type job struct {
	ctx       context.Context
	run       func(context.Context) (any, error)
	done      chan outcome
	started   atomic.Bool
	mayMutate bool
}
type outcome struct {
	result any
	err    error
}

func randomID() string { var b [8]byte; _, _ = rand.Read(b[:]); return hex.EncodeToString(b[:]) }
func New(home string) *Controller {
	return &Controller{Home: home, Epoch: randomID(), browsers: map[string]*browser{}, selections: map[string]string{}, stop: make(chan struct{})}
}
func selection(r protocol.Request) string { return r.Browser + "\x00" + r.Context }
func (c *Controller) Close() {
	c.once.Do(func() {
		close(c.stop)
		c.mu.Lock()
		defer c.mu.Unlock()
		for _, b := range c.browsers {
			b.client.Close()
		}
	})
}

func Serve(home string) error {
	l, err := ipc.Listen(home)
	if err != nil {
		return err
	}
	defer l.Close()
	c := New(home)
	defer c.Close()
	go func() { <-c.stop; l.Close() }()
	sem := make(chan struct{}, 256)
	for {
		conn, err := l.Accept()
		if err != nil {
			select {
			case <-c.stop:
				return nil
			default:
				return err
			}
		}
		select {
		case sem <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		go func(conn net.Conn) {
			defer func() { conn.Close(); <-sem }()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			var req protocol.Request
			if err := json.NewDecoder(io.LimitReader(conn, protocol.MaxInput)).Decode(&req); err != nil {
				_ = json.NewEncoder(conn).Encode(protocol.Failure("", protocol.Fail("INVALID_ARGUMENT", "Invalid controller request")))
				return
			}
			deadline := time.UnixMilli(req.Deadline)
			if req.Deadline == 0 || deadline.After(time.Now().Add(5*time.Minute)) {
				deadline = time.Now().Add(30 * time.Second)
			}
			_ = conn.SetDeadline(deadline.Add(time.Second))
			ctx, cancel := context.WithDeadline(context.Background(), deadline)
			defer cancel()
			response := c.Handle(ctx, req)
			_ = json.NewEncoder(conn).Encode(response)
			if req.Command == "controller stop" && response.OK {
				c.Close()
			}
		}(conn)
	}
}
func (b *browser) event(e transport.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if e.Method == "Target.targetDestroyed" {
		var p struct {
			TargetID string `json:"targetId"`
		}
		_ = json.Unmarshal(e.Params, &p)
		if t := b.tabs[p.TargetID]; t != nil {
			t.mu.Lock()
			t.dead = true
			t.observations = map[string]*observation{}
			t.mu.Unlock()
		}
		return
	}
	if e.Method != "Page.frameNavigated" && e.Method != "Page.frameDetached" && e.Method != "Runtime.executionContextsCleared" && e.Method != "Inspector.detached" {
		return
	}
	for _, t := range b.tabs {
		t.mu.Lock()
		if t.session == e.Session {
			t.observations = map[string]*observation{}
			if e.Method == "Inspector.detached" {
				t.dead = true
			}
		}
		t.mu.Unlock()
	}
}
func (b *browser) getTab(id string) *tab {
	b.mu.Lock()
	defer b.mu.Unlock()
	if t := b.tabs[id]; t != nil {
		return t
	}
	t := &tab{id: id, browser: b, wake: make(chan struct{}, 1), observations: map[string]*observation{}}
	b.tabs[id] = t
	go t.worker()
	return t
}
func (t *tab) enqueue(j *job) {
	select {
	case <-t.browser.client.Done:
		j.done <- outcome{err: protocol.Fail("CONNECTION_LOST", "Browser connection lost; reconnect before retrying")}
		return
	default:
	}
	t.mu.Lock()
	t.queue = append(t.queue, j)
	t.mu.Unlock()
	select {
	case t.wake <- struct{}{}:
	default:
	}
}
func (t *tab) worker() {
	for {
		t.mu.Lock()
		var j *job
		if len(t.queue) > 0 {
			j = t.queue[0]
			t.queue[0] = nil
			t.queue = t.queue[1:]
		}
		t.mu.Unlock()
		if j == nil {
			select {
			case <-t.wake:
				continue
			case <-t.browser.client.Done:
				return
			}
		}
		if j.ctx.Err() != nil {
			j.done <- outcome{err: j.ctx.Err()}
			continue
		}
		if err := t.attach(j.ctx); err != nil {
			j.done <- outcome{err: err}
			continue
		}
		j.started.Store(true)
		r, err := j.run(j.ctx)
		j.done <- outcome{r, err}
	}
}
func (t *tab) attach(ctx context.Context) error {
	t.mu.Lock()
	dead := t.dead
	session := t.session
	t.mu.Unlock()
	if dead {
		return protocol.Fail("TAB_CLOSED", "Selected tab was closed; explicitly select another tab")
	}
	select {
	case <-t.browser.client.Done:
		return protocol.Fail("CONNECTION_LOST", "Browser connection lost; use connect to reconnect")
	default:
	}
	if session != "" {
		return nil
	}
	var res struct {
		SessionID string `json:"sessionId"`
	}
	if err := t.browser.client.Call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": t.id, "flatten": true}, &res); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var pe *protocol.Error
		if errors.As(err, &pe) && pe.Code == "CONNECTION_LOST" {
			return err
		}
		return protocol.Fail("TAB_CLOSED", "Target is unavailable; list tabs and explicitly select one")
	}
	t.mu.Lock()
	t.session = res.SessionID
	t.mu.Unlock()
	for _, method := range []string{"Page.enable", "Runtime.enable", "DOM.enable", "Accessibility.enable"} {
		if err := t.call(ctx, method, nil, nil); err != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = t.browser.client.Call(cleanup, "", "Target.detachFromTarget", map[string]any{"sessionId": res.SessionID}, nil)
			cancel()
			t.mu.Lock()
			t.session = ""
			t.mu.Unlock()
			return err
		}
	}
	return nil
}
func (t *tab) call(ctx context.Context, method string, params, result any) error {
	t.mu.Lock()
	session := t.session
	t.mu.Unlock()
	return t.browser.client.Call(ctx, session, method, params, result)
}
func (t *tab) typed(ctx context.Context) context.Context {
	t.mu.Lock()
	session := t.session
	t.mu.Unlock()
	return cdp.WithExecutor(ctx, transport.Session{Client: t.browser.client, ID: session})
}
func await(ctx context.Context, j *job) (any, error) {
	select {
	case out := <-j.done:
		return out.result, out.err
	case <-ctx.Done():
		select {
		case out := <-j.done:
			return out.result, out.err
		default:
		}
		if j.mayMutate && j.started.Load() {
			return nil, protocol.Effect(ctx.Err(), "possible")
		}
		return nil, ctx.Err()
	}
}
func (t *tab) do(ctx context.Context, fn func(context.Context) (any, error), mayMutate bool) (any, error) {
	j := &job{ctx: ctx, run: fn, done: make(chan outcome, 1), mayMutate: mayMutate}
	t.enqueue(j)
	return await(ctx, j)
}

func (c *Controller) Handle(ctx context.Context, r protocol.Request) protocol.Response {
	if r.Browser == "" {
		r.Browser = "default"
	}
	if r.Context == "" {
		r.Context = "default"
	}
	if r.Flags == nil {
		r.Flags = map[string]string{}
	}
	if ctx.Err() != nil {
		return protocol.Failure("", ctx.Err())
	}
	if r.Command == "connect" || r.Command == "disconnect" {
		return c.connection(ctx, r)
	}
	if r.Command == "controller stop" {
		return protocol.Success("", map[string]any{"stopped": true})
	}
	c.mu.Lock()
	b := c.browsers[r.Browser]
	if r.Command == "status" || r.Command == "doctor" {
		selected := c.selections[selection(r)]
		c.mu.Unlock()
		connected := false
		if b != nil {
			select {
			case <-b.client.Done:
			default:
				connected = true
			}
		}
		result := map[string]any{"version": protocol.Version, "epoch": c.Epoch, "browser": r.Browser, "context": r.Context, "connected": connected, "selectedTab": selected}
		if r.Command == "doctor" {
			result["checks"] = map[string]any{"controller": true, "cdp": connected}
			if !connected {
				result["next"] = "connect --endpoint URL"
			}
		}
		return protocol.Success("", result)
	}
	if b == nil {
		c.mu.Unlock()
		return protocol.Failure("", protocol.Fail("NO_BROWSER", "Browser alias is not connected; use connect --endpoint URL"))
	}
	select {
	case <-b.client.Done:
		c.mu.Unlock()
		return protocol.Failure("", protocol.Fail("CONNECTION_LOST", "Browser connection lost; use connect to reconnect"))
	default:
	}
	if r.Command == "tabs" || r.Command == "tab new" || r.Command == "tab use" || r.Command == "tab activate" || r.Command == "tab close" || (r.Command == "cdp call" && r.Flags["scope"] == "browser") {
		c.mu.Unlock()
		result, err := c.browserCommand(ctx, b, r)
		if err != nil {
			return protocol.Failure("", err)
		}
		return protocol.Success("", result)
	}
	id := r.Tab
	if id == "" {
		id = c.selections[selection(r)]
	}
	if id == "" {
		c.mu.Unlock()
		return protocol.Failure("", protocol.Fail("NO_TAB", "No selected tab; use tabs then tab use ID, or tab new"))
	}
	t := b.getTab(id)
	// Resolve and enqueue while holding the selection lock: a concurrent tab use
	// cannot redirect the accepted request, and per-tab acceptance order is FIFO.
	j := &job{ctx: ctx, done: make(chan outcome, 1), mayMutate: protocol.MayMutate(r.Command)}
	if r.Command == "wait" {
		j.run = func(context.Context) (any, error) { return nil, nil }
	} else {
		j.run = func(ctx context.Context) (any, error) { return c.execute(ctx, t, r) }
	}
	t.enqueue(j)
	c.mu.Unlock()
	result, err := await(ctx, j)
	if err == nil && r.Command == "wait" {
		result, err = c.wait(ctx, t, r)
	}
	if err != nil {
		if r.Command == "screenshot" {
			pe := protocol.Failure(id, err).Error
			if pe.Code == "TIMEOUT" {
				copy := *pe
				copy.Next = "tab activate " + id
				copy.Message = "Screenshot timed out; Chromium may need explicit tab activation"
				err = &copy
			}
		}
		return protocol.Failure(id, err)
	}
	return protocol.Success(id, result)
}
func (c *Controller) connection(ctx context.Context, r protocol.Request) protocol.Response {
	c.manage.Lock()
	defer c.manage.Unlock()
	if ctx.Err() != nil {
		return protocol.Failure("", ctx.Err())
	}
	if r.Command == "disconnect" {
		c.mu.Lock()
		b := c.browsers[r.Browser]
		delete(c.browsers, r.Browser)
		c.mu.Unlock()
		if b != nil {
			b.client.Close()
		}
		return protocol.Success("", map[string]any{"disconnected": true})
	}
	name := r.Flags["name"]
	if name == "" {
		name = r.Browser
	}
	b := &browser{endpoint: r.Flags["endpoint"], tabs: map[string]*tab{}}
	client, err := transport.Dial(ctx, b.endpoint, b.event)
	if err != nil {
		return protocol.Failure("", err)
	}
	b.client = client
	if _, err := target.GetTargets().Do(cdp.WithExecutor(ctx, transport.Session{Client: client})); err != nil {
		client.Close()
		return protocol.Failure("", err)
	}
	if err = client.Call(ctx, "", "Target.setDiscoverTargets", map[string]any{"discover": true}, nil); err != nil {
		client.Close()
		return protocol.Failure("", err)
	}
	c.mu.Lock()
	old := c.browsers[name]
	c.browsers[name] = b
	c.mu.Unlock()
	if old != nil {
		old.client.Close()
	}
	return protocol.Success("", map[string]any{"browser": name, "connected": true, "epoch": c.Epoch})
}
func (c *Controller) browserCommand(ctx context.Context, b *browser, r protocol.Request) (any, error) {
	if r.Command == "cdp call" {
		var result json.RawMessage
		err := b.client.Call(ctx, "", r.Args[0], json.RawMessage(r.Payload), &result)
		if err != nil {
			return nil, protocol.Effect(err, "possible")
		}
		return c.externalize(result, "application/json")
	}
	if r.Command == "tabs" {
		tabs, err := target.GetTargets().Do(cdp.WithExecutor(ctx, transport.Session{Client: b.client}))
		if err != nil {
			return nil, err
		}
		sort.Slice(tabs, func(i, j int) bool { return tabs[i].TargetID < tabs[j].TargetID })
		out := []map[string]any{}
		offset := intFlag(r, "offset", 0)
		limit := intFlag(r, "limit", 60)
		index := 0
		truncated := false
		for _, v := range tabs {
			if v.Type == "page" {
				index++
				if index <= offset {
					continue
				}
				if len(out) >= limit {
					truncated = true
					break
				}
				item := map[string]any{"id": v.TargetID, "title": clip(v.Title, 256), "url": clip(v.URL, 2048)}
				candidate := append(out, item)
				if len(protocol.Encode(protocol.Success("", map[string]any{"tabs": candidate}))) > protocol.OutputBudget-256 {
					truncated = true
					break
				}
				out = candidate
			}
		}
		result := map[string]any{"tabs": out, "truncated": truncated}
		if truncated {
			result["next"] = fmt.Sprintf("tabs --offset %d", offset+len(out))
		}
		return result, nil
	}
	if r.Command == "tab new" {
		url := "about:blank"
		if len(r.Args) > 0 {
			url = r.Args[0]
		}
		var result struct {
			TargetID string `json:"targetId"`
		}
		err := b.client.Call(ctx, "", "Target.createTarget", map[string]any{"url": url}, &result)
		if err != nil {
			return nil, protocol.Effect(err, "possible")
		}
		c.mu.Lock()
		if c.browsers[r.Browser] == b {
			c.selections[selection(r)] = result.TargetID
		}
		c.mu.Unlock()
		return map[string]any{"id": result.TargetID, "selected": true}, nil
	}
	id := r.Args[0]
	if r.Command == "tab use" {
		var result struct {
			TargetInfo struct {
				Type string `json:"type"`
			} `json:"targetInfo"`
		}
		if err := b.client.Call(ctx, "", "Target.getTargetInfo", map[string]any{"targetId": id}, &result); err != nil {
			return nil, protocol.Fail("TAB_CLOSED", "Tab ID does not exist")
		}
		if result.TargetInfo.Type != "page" {
			return nil, protocol.Fail("UNSUPPORTED", "Selected target is not a page tab")
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.browsers[r.Browser] != b {
			return nil, protocol.Fail("CONNECTION_LOST", "Browser connection changed")
		}
		c.selections[selection(r)] = id
		return map[string]any{"id": id, "selected": true}, nil
	}
	t := b.getTab(id)
	return t.do(ctx, func(ctx context.Context) (any, error) {
		method := "Target.activateTarget"
		if r.Command == "tab close" {
			method = "Target.closeTarget"
		}
		var result any
		if err := b.client.Call(ctx, "", method, map[string]any{"targetId": id}, &result); err != nil {
			return nil, protocol.Effect(err, "possible")
		}
		if r.Command == "tab close" {
			t.mu.Lock()
			t.dead = true
			t.observations = map[string]*observation{}
			t.mu.Unlock()
		}
		return map[string]any{"id": id, "effect": "confirmed"}, nil
	}, true)
}
