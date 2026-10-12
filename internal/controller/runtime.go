package controller

import (
	"context"
	"encoding/json"

	"cdpsh/internal/protocol"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/runtime"
)

func (t *tab) evaluate(ctx context.Context, expression string) (json.RawMessage, error) {
	obj, exception, err := runtime.Evaluate(expression).WithAwaitPromise(true).WithReturnByValue(true).Do(t.typed(ctx))
	if err != nil {
		return nil, err
	}
	if exception != nil {
		return nil, protocol.Fail("EXECUTION_ERROR", clip(exception.Text+exceptionDescription(exception), 512))
	}
	if obj.UnserializableValue != "" {
		b, _ := json.Marshal(map[string]any{"type": obj.Type, "unserializableValue": obj.UnserializableValue})
		return b, nil
	}
	if len(obj.Value) == 0 {
		b, _ := json.Marshal(map[string]any{"type": obj.Type})
		return b, nil
	}
	return json.RawMessage(obj.Value), nil
}
func exceptionDescription(e *runtime.ExceptionDetails) string {
	if e.Exception != nil && e.Exception.Description != "" {
		return ": " + e.Exception.Description
	}
	return ""
}
func (t *tab) function(ctx context.Context, id runtime.RemoteObjectID, fn string, args ...any) (json.RawMessage, error) {
	arguments := make([]*runtime.CallArgument, 0, len(args))
	for _, arg := range args {
		b, _ := json.Marshal(arg)
		arguments = append(arguments, &runtime.CallArgument{Value: b})
	}
	obj, exception, err := runtime.CallFunctionOn(fn).WithObjectID(id).WithArguments(arguments).WithReturnByValue(true).WithAwaitPromise(true).Do(t.typed(ctx))
	if err != nil {
		return nil, err
	}
	if exception != nil {
		return nil, protocol.Fail("EXECUTION_ERROR", clip(exception.Text+exceptionDescription(exception), 512))
	}
	if len(obj.Value) == 0 {
		return json.RawMessage(`null`), nil
	}
	return json.RawMessage(obj.Value), nil
}
func (t *tab) document(ctx context.Context) (cdp.BackendNodeID, error) {
	node, err := dom.GetDocument().Do(t.typed(ctx))
	if err != nil {
		return 0, err
	}
	return node.BackendNodeID, nil
}
