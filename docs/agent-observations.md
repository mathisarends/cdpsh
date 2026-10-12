# Observations for agents

`cdp observe` combines accessibility semantics, a pierced DOM tree and CDP layout
snapshots. It returns a bounded decision surface rather than a full DOM dump.
JSON is the automation contract; `--format text` presents a compact tree for an LLM.

```text
tab=TARGET_ID observation=OBSERVATION_ID title="Checkout" url="https://example.com/"
viewport=1280x720 scroll=0,0 above=0px below=840px
main ""
  region "Product A"
    *[OBSERVATION_ID:e1] button "Buy"
  region "Product B"
    *[OBSERVATION_ID:e2] button "Buy"
  |SHADOW(open)| checkout-form
    |SHADOW(closed)| payment-field
      *[OBSERVATION_ID:e3] textbox "Email" inputType="email" value="user@example.com"
      *[OBSERVATION_ID:e4] checkbox "Accept terms" checked=false
```

References in this example are placeholders. Read actual references from your
result. `*` marks a node absent from the preceding retained observation in this
context and tab; it is an observation change marker, not proof of a DOM insertion.
The text view groups elements by semantic context and shadow path, followed by
rendered page text. It does not reproduce every DOM ancestor.

## States and context

Elements expose live values and applicable states: checked (including `mixed`),
selected, expanded, pressed, focused, readonly, required, disabled, input type,
placeholder and scroll position. Applicable false values remain explicit. Values
are bounded and report `valueTruncated`; accessible names remain bounded, so use
the reference instead of an exact name selector when a displayed name ends in an
ellipsis. Password, file, hidden, payment-autocomplete and one-time-code values
are excluded. The CLI does not redact arbitrary website prose.

`context` identifies useful ancestors such as named regions, forms, dialogs,
articles, rows and list items. This distinguishes equal labels without exposing
every anonymous layout wrapper. `shadow` lists the containing author shadow
boundaries from outermost to innermost, including closed roots and assigned slots.

AX controls remain the primary targets. CDP-reported click listeners and scroll
containers can also produce references when no interactive AX role exists. These
entries have `inferred:true`; their displayed role is a hint. Use their reference,
not a role/name selector, to address them.

## Text and element continuation

`--scope viewport` applies to both targets and rendered text. Layout text boxes
restrict a long text node to its visible lines. `--scope page` includes offscreen
page content. Child-frame contents are not mixed into main-frame observations.

The default response budget is 12 KiB, including metadata and continuation hints.
Text has no independent fixed 2 KiB cutoff. A larger `--max-bytes` can return more
text. When the budget is reached, `textBytes` describes the complete captured text
and `textNext` gives a UTF-8 byte offset into that immutable observation.

```sh
cdp observe --scope page --max-bytes 2048
cdp read --observation OBSERVATION_ID --offset BYTE_OFFSET --max-bytes 12288
```

Use the exact offset in `continuation.text.flags.offset` or the next read result.
`read` returns the captured text even if the live DOM changes. It neither observes
again nor evicts element references. Navigation/reconnect still invalidate the
snapshot. Page-scoped text is rendered text, not HTML source or hidden data.

`--offset` on `observe` counts matching visible element candidates; it is unrelated
to text byte offsets. `elementNext` and `continuation.elements` expose more targets,
including beyond the 500-element limit per response. Element continuation makes a
fresh observation; changing pages can change its ordering. Role/name filters apply
to element candidates, not to the captured page text.

Continuation and recovery instructions are objects with `command`, `args` and
`flags`. Invoke the executable with separate argument values; do not turn website
text into a shell program. They preserve browser alias, context and tab selection.

## Reference lifetime and actions

The latest **three** observations per context and tab retain their references and
text. Additional observations in other contexts do not evict them. References
still check document identity and node existence; navigation, removal, frame loss,
reconnect and controller restart invalidate affected references. Long-running
helpers should reacquire references after `STALE_REF`.

```sh
cdp check --ref OBSERVATION_ID:e4 --checked true
cdp select --role combobox --name Country --value DE
cdp focus --ref OBSERVATION_ID:e3
cdp type --text-file message.txt
```

`check` and `select` verify the result and return `effect:none` when the requested
state already matches. Radios can only be set to true. `select` supports native
single selects with exactly one enabled option matched by value, label or its
zero-based index; custom
comboboxes use click/press. Selection emits input/change events. `fill` supports
text inputs, textareas and contenteditable surfaces using browser input, and
verifies the resulting text. Custom editors can reject or transform that input;
inspect a possible effect before retrying.

Native selects expose their first five options and `optionCount` in observations.
Use `options TARGET` and its structured `next` command to discover the remaining
options without changing selection. Values and labels are bounded and mark
`truncated`; `select TARGET --index N` addresses duplicate or truncated options.
Payment/one-time-code option values are empty with `valueRedacted:true`.

Open and closed author shadow roots use backend node identity for targeting and
CDP hit testing for covered elements. CSS selectors keep normal main-document
`querySelectorAll` semantics. Use references for shadow descendants. Browser-owned
shadow internals are excluded. Child frames are listed with `supported:false` and
marked in the text view; use raw CDP or viewport coordinates for those frames.

## Discovery and recovery

`help COMMAND` includes concrete examples. `schema COMMAND` adds JSON Schemas for
parsed arguments and results, typed flag ranges/defaults and command-specific
constraints/errors. Normalize positional TARGET to `flags.ref` before validating
the input schema; convert numeric CLI strings to their declared types. Schemas
describe invocation values, not shell syntax.

`connect --name work` registers an alias; subsequent commands use `--browser work`.
Its `nextCommand` preserves that alias. `browsers` lists registered aliases without
revealing endpoints. `reconnect` uses the endpoint remembered by the running
controller and preserves tab selection, invalidating references without replaying
actions. After a controller restart, supply the endpoint to `connect` again.

`doctor` works before a controller exists and reports that missing prerequisite.
For a running connection it probes `Browser.getVersion` and the selected target.
Errors expose structured `recovery` objects; they recommend a next step but never
execute it automatically.

## Relationship to browser-use

The representation follows the useful principles of browser-use: combining DOM,
AX and layout, compressing structural wrappers, assigning actionable references,
presenting live state, identifying shadow boundaries, reporting scroll context and
recognizing listener-backed controls. The Go implementation is independent; no
Python dependency or browser-use source is bundled.

The inspected reference is commit
[`c75e8476e26d18b7617643bc2ae082fae8eae431`](https://github.com/browser-use/browser-use/tree/c75e8476e26d18b7617643bc2ae082fae8eae431).
Relevant sources are its
[DOM service](https://github.com/browser-use/browser-use/blob/c75e8476e26d18b7617643bc2ae082fae8eae431/browser_use/dom/service.py),
[serializer](https://github.com/browser-use/browser-use/blob/c75e8476e26d18b7617643bc2ae082fae8eae431/browser_use/dom/serializer/serializer.py)
and [snapshot processing](https://github.com/browser-use/browser-use/blob/c75e8476e26d18b7617643bc2ae082fae8eae431/browser_use/dom/enhanced_snapshot.py).

CLI-specific extensions are bounded JSON envelopes, immutable text continuation,
three-observation retention and explicit alias/context-preserving recovery.
This is not full browser-use feature parity: cross-frame high-level actions,
paint-order pruning, browser hosting and the built-in agent are not implemented.
