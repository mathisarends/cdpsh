package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mathisarends/cdpsh/internal/protocol"
	"github.com/mathisarends/cdpsh/internal/transport"
)

func waitForWorkers(t *testing.T, b *browser) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		remaining := len(b.tabs)
		b.mu.Unlock()
		if remaining == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	t.Fatalf("%d tab workers were retained", len(b.tabs))
}

func TestClosedTabsReleaseWorkers(t *testing.T) {
	for _, event := range []string{"Target.targetDestroyed", "Inspector.detached"} {
		t.Run(event, func(t *testing.T) {
			b := &browser{client: &transport.Client{Done: make(chan struct{})}, tabs: map[string]*tab{}}
			defer close(b.client.Done)
			for i := 0; i < 30; i++ {
				id := fmt.Sprintf("tab-%d", i)
				tab := b.getTab(id)
				tab.mu.Lock()
				tab.session = id
				tab.mu.Unlock()
				params, _ := json.Marshal(map[string]string{"targetId": id})
				b.event(transport.Event{Method: event, Session: id, Params: params})
			}
			waitForWorkers(t, b)
		})
	}
}

func TestDestroyedTabFailsQueuedInputWithoutReplaying(t *testing.T) {
	b := &browser{client: &transport.Client{Done: make(chan struct{})}, tabs: map[string]*tab{}}
	defer close(b.client.Done)
	tab := b.getTab("tab")
	tab.mu.Lock()
	tab.session = "session"
	tab.mu.Unlock()
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	go func() {
		defer close(finished)
		_, _ = tab.do(context.Background(), func(context.Context) (any, error) {
			close(started)
			<-release
			return nil, nil
		}, false)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	var dispatched atomic.Bool
	queued := &job{ctx: context.Background(), done: make(chan outcome, 1), mayMutate: true, run: func(context.Context) (any, error) {
		dispatched.Store(true)
		return nil, nil
	}}
	tab.enqueue(queued)
	b.event(transport.Event{Method: "Target.targetDestroyed", Params: json.RawMessage(`{"targetId":"tab"}`)})
	select {
	case out := <-queued.done:
		e := protocol.Failure("", out.err).Error
		if e.Code != "TAB_CLOSED" || e.Effect != "none" {
			t.Fatalf("queued input lost its closure/effect: %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("queued input was left waiting on a dead worker")
	}
	late := &job{ctx: context.Background(), done: make(chan outcome, 1)}
	tab.enqueue(late)
	select {
	case out := <-late.done:
		if protocol.Failure("", out.err).Error.Code != "TAB_CLOSED" {
			t.Fatal("late request did not report a closed tab")
		}
	case <-time.After(time.Second):
		t.Fatal("late request was accepted by a dead worker")
	}
	unblock()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("running request did not finish")
	}
	if dispatched.Load() {
		t.Fatal("queued input ran after the tab was destroyed")
	}
	waitForWorkers(t, b)
}

func TestConnectionLossReleasesIdleWorkers(t *testing.T) {
	b := &browser{client: &transport.Client{Done: make(chan struct{})}, tabs: map[string]*tab{}}
	for i := 0; i < 30; i++ {
		b.getTab(fmt.Sprintf("tab-%d", i))
	}
	close(b.client.Done)
	waitForWorkers(t, b)
}
