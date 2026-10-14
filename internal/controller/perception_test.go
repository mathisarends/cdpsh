package controller

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mathisarends/cdpsh/internal/protocol"
)

func TestTextContinuationPreservesEveryUTF8Byte(t *testing.T) {
	want := strings.Repeat("Grüße🙂\n\"", 100)
	var got strings.Builder
	for offset := 0; offset < len(want); {
		part, end, err := textPage(want, offset, 7)
		if err != nil || !utf8.ValidString(part) || end <= offset {
			t.Fatalf("offset %d: %q %d %v", offset, part, end, err)
		}
		got.WriteString(part)
		offset = end
	}
	if got.String() != want {
		t.Fatal("text pages lost content")
	}
	for _, offset := range []int{-1, 3, len(want) + 1} {
		if _, _, err := textPage(want, offset, 20); err == nil {
			t.Fatalf("invalid offset %d accepted", offset)
		}
	}
}

func TestRecoveryPreservesBrowserContextAndTab(t *testing.T) {
	r := protocol.Request{Command: "click", Browser: "work", Context: "agent-b"}
	response := recovery(r, protocol.Failure("tab-b", protocol.Fail("STALE_REF", "expired")))
	next := response.Error.Recovery
	if next == nil || next.Command != "observe" || next.Flags["browser"] != "work" || next.Flags["context"] != "agent-b" || next.Flags["tab"] != "tab-b" {
		t.Fatalf("invalid recovery %v", next)
	}
	response = recovery(r, protocol.Failure("tab-b", protocol.Fail("TAB_CLOSED", "closed")))
	if response.Error.Recovery.Flags["tab"] != "" || response.Error.Recovery.Command != "tabs" {
		t.Fatal("closed-tab recovery retained unavailable override")
	}
}

func TestTextClippingDistinguishesScrollableAndHiddenContent(t *testing.T) {
	for _, overflow := range []string{"auto", "scroll", "hidden", "clip", "visible", ""} {
		if clipsText(overflow, "page") != (overflow == "hidden" || overflow == "clip") {
			t.Errorf("page scope clips %q incorrectly", overflow)
		}
		if clipsText(overflow, "viewport") != (overflow == "auto" || overflow == "scroll" || overflow == "hidden" || overflow == "clip") {
			t.Errorf("viewport scope clips %q incorrectly", overflow)
		}
	}
}
