package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"unicode/utf8"
)

const Version = "1.0.0"
const MaxInput = 8 << 20
const OutputBudget = 12 << 10

func MayMutate(command string) bool {
	switch command {
	case "goto", "back", "forward", "reload", "click", "fill", "type", "press", "scroll", "focus", "check", "select", "eval", "call", "tab new", "tab close", "tab activate":
		return true
	default:
		return false
	}
}

type Request struct {
	Command  string            `json:"command"`
	Args     []string          `json:"args,omitempty"`
	Flags    map[string]string `json:"flags,omitempty"`
	Browser  string            `json:"browser"`
	Context  string            `json:"context"`
	Tab      string            `json:"tab,omitempty"`
	Deadline int64             `json:"deadline"`
	Payload  string            `json:"payload,omitempty"`
}

type Error struct {
	Code     string      `json:"code"`
	Message  string      `json:"message"`
	Effect   string      `json:"effect"`
	Next     string      `json:"next,omitempty"`
	Details  any         `json:"details,omitempty"`
	Recovery *Invocation `json:"recovery,omitempty"`
}

// Structured invocations preserve aliases and contexts without shell quoting or
// interpolating page-controlled strings into executable command hints.
type Invocation struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Flags   map[string]string `json:"flags,omitempty"`
}

func (e *Error) Error() string { return e.Message }
func Fail(code, message string) *Error {
	e := &Error{Code: code, Message: message, Effect: "none"}
	if len(message) > 1024 {
		end := 1021
		for end > 0 && !utf8.RuneStart(message[end]) {
			end--
		}
		e.Message = message[:end] + "…"
		e.Details = map[string]any{"messageTruncated": true}
	}
	return e
}
func Effect(err error, effect string) error {
	var e *Error
	if errors.As(err, &e) {
		copy := *e
		copy.Effect = effect
		return &copy
	}
	e = Failure("", err).Error
	e.Effect = effect
	return e
}

type Response struct {
	OK     bool   `json:"ok"`
	Tab    string `json:"tab,omitempty"`
	Result any    `json:"result,omitempty"`
	Error  *Error `json:"error,omitempty"`
}

func Success(tab string, result any) Response { return Response{OK: true, Tab: tab, Result: result} }
func Failure(tab string, err error) Response {
	var e *Error
	if !errors.As(err, &e) {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			e = Fail("TIMEOUT", "Request deadline exceeded")
		} else {
			e = Fail("INTERNAL_ERROR", err.Error())
		}
	}
	return Response{Tab: tab, Error: e}
}
func (r Response) ExitCode() int {
	if r.OK {
		return 0
	}
	if r.Error == nil {
		return 1
	}
	switch r.Error.Code {
	case "INVALID_ARGUMENT":
		return 2
	case "TIMEOUT":
		return 4
	case "NO_BROWSER", "CONNECTION_LOST", "CONTROLLER_ERROR":
		return 5
	case "NO_TAB", "TAB_CLOSED", "STALE_REF", "TARGET_NOT_FOUND", "AMBIGUOUS_TARGET", "OBSCURED", "NOT_EDITABLE", "NOT_VISIBLE", "DISABLED", "UNSUPPORTED":
		return 3
	default:
		return 1
	}
}
func Encode(r Response) []byte { b, _ := json.Marshal(r); return b }
