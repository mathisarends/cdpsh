package protocol

import (
	"context"
	"testing"
)

func TestEffectsAndExitCodes(t *testing.T) {
	for _, test := range []struct {
		code string
		exit int
	}{{"INVALID_ARGUMENT", 2}, {"STALE_REF", 3}, {"TIMEOUT", 4}, {"CONNECTION_LOST", 5}, {"EXECUTION_ERROR", 1}} {
		r := Failure("t1", Fail(test.code, "test"))
		if r.ExitCode() != test.exit {
			t.Fatalf("%s: %d", test.code, r.ExitCode())
		}
	}
	r := Failure("t1", Effect(context.DeadlineExceeded, "possible"))
	if r.Error.Code != "TIMEOUT" || r.Error.Effect != "possible" || r.ExitCode() != 4 {
		t.Fatalf("%+v", r)
	}
	e := Fail("STALE_REF", "expired")
	_ = Effect(e, "possible")
	if e.Effect != "none" {
		t.Fatal("Effect mutated a shared error")
	}
}
