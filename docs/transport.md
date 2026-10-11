# Transport feasibility decision

The spike is executable as `go test ./internal/transport -run TestTransportSpike`.
It verifies HTTP discovery, a typed browser command, flattened target-session
routing, concurrent request multiplexing, an unknown raw method, raw event
delivery, connection loss without replay, and explicit reconnect.

The pinned chromedp high-level context lifecycle detaches **and closes** a borrowed
target on cancellation ([upstream implementation](https://github.com/chromedp/chromedp/blob/v0.14.2/chromedp.go)).
Its Browser API also does not publicly expose construction of an executor for an
arbitrary attached session. Those behaviors do not fit cdp's contract: disconnect
must leave targets open, and the controller must own persistent session routing
independently of short-lived CLI requests.

cdp therefore reuses chromedp's exported `DialContext` and `Transport` for
WebSocket framing and transport, with a small request/session router implementing
`cdp.Executor`. Generated cdproto commands remain usable internally. Raw methods
and events bypass enum decoding and are not restricted by a method allowlist.
No protocol generator or custom WebSocket implementation was added.

Chrome 155 includes an AX property named `uninteresting` absent from the pinned
generated enums. Accessibility results deliberately decode only the fields used
by cdp and tolerate unknown fields/properties. This is covered by a regression
test. AX roles/names come from Chromium, not a locally invented accessibility model.

The browser acceptance suite separately verifies actual target attachment,
native input, event-driven reference invalidation plus document/node validation,
reconnect, and disconnect without closing the original blank browser tab.
