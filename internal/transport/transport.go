// Package transport routes flattened CDP sessions over chromedp's existing
// WebSocket transport. It deliberately owns no browser or target lifecycle.
package transport

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"cdpsh/internal/protocol"
	"github.com/chromedp/cdproto"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/go-json-experiment/json/jsontext"
)

type Event struct {
	Method  string
	Session string
	Params  json.RawMessage
}
type Client struct {
	conn    chromedp.Transport
	mu      sync.Mutex
	writeMu sync.Mutex
	pending map[int64]chan *cdproto.Message
	next    atomic.Int64
	Done    chan struct{}
	onEvent func(Event)
	once    sync.Once
}
type Session struct {
	Client *Client
	ID     string
}

func (s Session) Execute(ctx context.Context, method string, params, result any) error {
	return s.Client.Call(ctx, s.ID, method, params, result)
}

func Discover(ctx context.Context, endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return "", protocol.Fail("INVALID_ARGUMENT", "Endpoint must be an absolute HTTP(S) or WS(S) URL")
	}
	switch u.Scheme {
	case "ws", "wss":
		return endpoint, nil
	case "http", "https":
	default:
		return "", protocol.Fail("INVALID_ARGUMENT", "Endpoint must use http, https, ws, or wss")
	}
	if !strings.HasSuffix(u.Path, "/json/version") {
		u.Path = strings.TrimRight(u.Path, "/") + "/json/version"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", protocol.Fail("CONNECTION_LOST", "CDP discovery failed; check endpoint and remote debugging")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", protocol.Fail("CONNECTION_LOST", "CDP discovery did not return HTTP 200")
	}
	var v struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&v) != nil || v.WebSocketDebuggerURL == "" {
		return "", protocol.Fail("CONNECTION_LOST", "Discovery response has no browser WebSocket URL")
	}
	ws, err := url.Parse(v.WebSocketDebuggerURL)
	if err != nil || ws.Host == "" || (ws.Scheme != "ws" && ws.Scheme != "wss") {
		return "", protocol.Fail("CONNECTION_LOST", "Discovery returned an invalid WebSocket URL")
	}
	return ws.String(), nil
}
func Dial(ctx context.Context, endpoint string, onEvent func(Event)) (*Client, error) {
	ws, err := Discover(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	conn, err := chromedp.DialContext(ctx, ws)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, protocol.Fail("CONNECTION_LOST", "Cannot connect to CDP WebSocket; check endpoint and remote debugging")
	}
	c := &Client{conn: conn, pending: map[int64]chan *cdproto.Message{}, Done: make(chan struct{}), onEvent: onEvent}
	go c.read()
	return c, nil
}
func (c *Client) Close() { c.once.Do(func() { close(c.Done); c.conn.Close() }) }
func (c *Client) read() {
	defer c.Close()
	for {
		var msg cdproto.Message
		if c.conn.Read(context.Background(), &msg) != nil {
			return
		}
		if msg.ID != 0 {
			c.mu.Lock()
			ch := c.pending[msg.ID]
			c.mu.Unlock()
			if ch != nil {
				ch <- &msg
			}
			continue
		}
		if msg.Method != "" && c.onEvent != nil {
			c.onEvent(Event{Method: string(msg.Method), Session: string(msg.SessionID), Params: json.RawMessage(msg.Params)})
		}
	}
}
func (c *Client) Call(ctx context.Context, session, method string, params, result any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if params == nil {
		data = []byte(`{}`)
	}
	id := c.next.Add(1)
	ch := make(chan *cdproto.Message, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	msg := &cdproto.Message{ID: id, SessionID: target.SessionID(session), Method: cdproto.MethodType(method), Params: jsontext.Value(data)}
	c.writeMu.Lock()
	// chromedp's connection does not implement per-write cancellation. Close on
	// deadline so a stalled socket cannot strand this or subsequent requests.
	stop := context.AfterFunc(ctx, c.Close)
	err = c.conn.Write(ctx, msg)
	stop()
	c.writeMu.Unlock()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return protocol.Fail("CONNECTION_LOST", "CDP connection lost while sending request")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.Done:
		return protocol.Fail("CONNECTION_LOST", "CDP connection lost; reconnect and inspect the page before retrying")
	case msg := <-ch:
		if msg.Error != nil {
			code := "PROTOCOL_ERROR"
			if msg.Error.Code == -32601 {
				code = "UNSUPPORTED"
			}
			return protocol.Fail(code, msg.Error.Message)
		}
		if result != nil && len(msg.Result) > 0 {
			return json.Unmarshal(msg.Result, result)
		}
		return nil
	}
}
