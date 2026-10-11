# cdpsh — Specification

Status: v0.1 implemented; see README.md for verification and supported limits.
Updated: October 11, 2026.

cdpsh is a lightweight browser CLI written in Go for agents. It connects to an
existing Chromium browser through the Chrome DevTools Protocol (CDP) and provides
small, composable operations for observing and acting. Agents can use it to run
workflows, diagnose failures, and build reusable helpers through the shell, with
or without relevant skills.

Examples describe the implemented interface; example target IDs are placeholders.
“Must” denotes a v0.1 contract; “Proposal” denotes a detail still open for discussion.

## 1. Agreed design decisions

| Topic | Decision |
| --- | --- |
| Language | Go; one distributable binary |
| Browser lifecycle | Connect to existing browsers only; no browser launch |
| Self-healing | The agent decides; the CLI provides diagnostics and fresh observations |
| Element targets | Short-lived references, supplemented by role/name, CSS, and coordinates |
| Output | Compact JSON by default; optional text view |
| Scripting | One command per invocation; shell scripts and optional skills |
| Batch | A later extension; no embedded interpreter in v0.1 |
| Skill document | An optional skill document will be created after v0.1 |
| Protocol | Raw CDP remains available as an escape hatch |

## 2. Design philosophy

### 2.1 The agent learns; the CLI stays understandable

Self-healing means observing, acting, checking the effect, observing again when
expectations are not met, and adjusting the workflow. cdpsh provides the mechanics.
The agent interprets the page and chooses how to repair the workflow.

The CLI may wait for technical readiness within a time limit and restore a
connection. It must not guess a new target, choose an alternative button, or
automatically repeat an action that may already have executed.
A timeout after dispatching a click can mean that the click already took effect.

### 2.2 Abstractions must reduce browser work

The core abstracts recurring browser mechanics: target resolution, readable
observations, scrolling elements into view, hit-testing, input, synchronization,
and errors. An operation belongs in the core if it is useful across many websites,
has a small, understandable contract, and requires no website-specific knowledge.

“Click this observed reference” belongs in the core. “Book the cheapest flight”
belongs in a workflow. New core commands must demonstrate their value over a
combination of existing commands.

### 2.3 A small public interface with a complete escape hatch

Common tasks should work without CDP knowledge. Less common browser features
remain accessible through `cdp call` and `eval`. Not every CDP method gets its own
top-level command. Internal Go type safety does not require the shell interface
to expose hundreds of flags or require knowledge of the protocol specification.

### 2.4 Usable with or without a skill

An agent must be able to operate a browser using only concise help, command
descriptions, and result fields. A skill adds workflows and examples; it must not
contain hidden prerequisites for using the CLI. Website knowledge and repaired
workflows are stored as reviewable shell helpers or skills.

cdpsh does not automatically modify its installed implementation or skills.
The agent can develop and version its own helpers in its workspace.
The optional cdpsh skill document will be created after v0.1.

### 2.5 Token efficiency is a product contract

Output should help the agent make its next decision. Full DOM/AX trees, Base64
images, debug logs, and long help text are excluded from default output.
Truncation must be visible. Small results retain understandable field names;
the CLI does not require an additional cryptic output format.

## 3. v0.1 scope

Included: connection and diagnostics, tabs and logical agent contexts, navigation,
compact observations, element and coordinate input, explicit waits, screenshots,
JavaScript evaluation, and raw CDP calls.

Excluded from v0.1: browser installation/launch, profiles and cookie synchronization,
cloud provisioning, a built-in LLM, autonomous task planning, website adapters,
stealth/CAPTCHA features, embedded scripting languages, an MCP server, recording,
a Vizron-specific UI, and the optional skill document. Uploads, downloads, and
dialogs initially have no guaranteed high-level contract; raw CDP remains usable
where possible.

## 4. Architecture and responsibilities

```text
Agent / Shell / optional skills
            |
     short-lived cdpsh CLI
            | local IPC
     long-lived controller
            | persistent CDP / sessions / events
     existing Chromium browser
```

The CLI and controller ship in the same binary. `connect` may start the local
controller, but must never launch Chromium. The controller retains connections,
tab mappings, references, and in-flight requests across CLI invocations.

The controller separates target resolution, observation, actions, and transport.
Generated CDP types should preferably come from `chromedp/cdproto`. The exact
version will be selected and pinned together with the transport during
implementation. A custom protocol generator is not planned for v0.1.

The transport must support browser and target sessions, request multiplexing,
event subscriptions, and unknown raw CDP methods. Before choosing an existing
transport, a small feasibility spike must verify these requirements.
A custom transport is planned only if a specific gap is demonstrated.

Proposal for local IPC: a Unix-domain socket on Linux/macOS and a named pipe on
Windows, accessible to the user running the process. The controller is not a
public HTTP service. Remote browsers are connected through their CDP endpoints;
cdpsh does not provide browser hosting.

## 5. Browsers, contexts, and tabs

- A browser alias identifies a specific connection to a CDP endpoint.
- A logical context stores an agent's selected tab. It is not a Chromium
  BrowserContext and does not isolate cookies or permissions.
- A tab has an opaque ID; its order or URL is not a stable identifier.
- An explicit `--tab` overrides the context selection for the current invocation only.
- Browser and context selection use the flag first, then the environment variable,
  then the default. Proposal: `CDPSH_BROWSER`, `CDPSH_CONTEXT`; both default to `default`.
- Without a selected tab, tab-specific commands return `NO_TAB`. If a tab is lost,
  cdpsh does not automatically select an unrelated tab.

The controller resolves the target tab when accepting a request and fixes it for
that request. A concurrent context change must not redirect the action.
Mutating high-level actions are serialized per tab. Other tabs can operate in
parallel. Reads are ordered after previously accepted actions on the same tab;
long waits must still allow subsequent actions to proceed.

This coordination covers only requests handled by the same controller. Humans and
other CDP clients can still change the browser. Selecting a tab in a context and
visibly activating it in the browser are separate operations.

## 6. CLI interface

General form:

```text
cdpsh [--browser NAME] [--context NAME] [--tab ID] COMMAND [ARGUMENTS]
      [--timeout DURATION] [--format json|text]
```

| Command | Contract |
| --- | --- |
| `connect --endpoint URL [--name NAME]` | Connect to an existing browser using an HTTP discovery endpoint or WebSocket |
| `disconnect` | Disconnect CDP; leave the browser open |
| `status` / `doctor` | Compact status or focused connection diagnostics |
| `tabs` | List tabs with ID, title, and URL |
| `tab new [URL]` | Create a tab in the connected browser and select it in the context |
| `tab use ID` | Change the context's selected tab |
| `tab activate ID` | Explicitly activate the visible browser tab |
| `tab close ID` | Close the explicitly specified tab |
| `goto URL` / `back` / `forward` / `reload` | Navigate the selected tab |
| `info` | Return URL, title, and compact page status |
| `observe` | Produce a compact page observation with element references |
| `click TARGET` | Click a resolved element |
| `click --x X --y Y` | Click in CSS viewport pixels |
| `fill TARGET --text TEXT` | Replace an editable field's contents |
| `type --text TEXT` | Insert text at the current input focus |
| `press KEY` | Send a key or supported key combination |
| `scroll --dy PIXELS [--dx PIXELS] [TARGET]` | Scroll within the target area or viewport |
| `wait CONDITION` | Wait for an explicit condition until the timeout |
| `screenshot [--out PATH]` | Save the viewport to a file; return its path and metadata |
| `eval --expression JS` / `eval --file PATH` | Execute JavaScript in the selected frame |
| `cdp call METHOD --params-file PATH` | Execute a raw CDP method |
| `help [COMMAND]` | Concise overview or focused command help |
| `schema COMMAND` | Describe arguments, results, and errors in machine-readable form |

For large text inputs, `fill` and `type` also support `--text-file PATH`.
`--params JSON` is a shorthand for `cdp call`; files avoid shell quoting problems.
A file argument of `-` means stdin. Exactly one input source is allowed.

Raw CDP needs an explicit scope rule: `--scope tab` is the default;
`--scope browser` uses the browser session. Unsupported methods return errors;
a pinned schema must not block unknown methods.
A complete generated flag interface for raw CDP is not a v0.1 goal.

## 7. Observations and element targets

`observe` returns the URL, title, observation ID, relevant interactive elements,
and a bounded text excerpt. It uses accessibility data and supplementary DOM/layout
data. It does not include an LLM-generated summary.

Example of an abbreviated text view:

```text
tab=t1 observation=o7 title="Sign in"
e1 textbox "Email"
e2 textbox "Password"
e3 button "Sign in"
```

References include the observation in the target: `o7:e3`. They are bound to the
browser, controller epoch, tab, frame, and document; they are never global IDs.
A new `observe` replaces that context's references for the relevant tab.
Navigation, frame loss, and reconnects also invalidate affected references.
A referenced target is checked for existence and usability before each action.
DOM changes can also cause `STALE_REF` or an actionability error; there is no
absolute atomic guarantee between validation and the browser action.

A target must use exactly one of these forms:

```text
--ref o7:e3
--role button --name "Sign in"
--css '#submit'
--x 420 --y 310                 # coordinate actions only
```

Role/name and CSS must resolve to exactly one matching element. Multiple matches
return `AMBIGUOUS_TARGET` with a bounded list of candidates. Name matching is
exact by default; fuzzy matching and automatic alternative selection are excluded.
Frames are explicitly selected using `--frame ID`; the main frame is the default.
Frame IDs are discoverable through observations. Frame targets outside high-level
support return `UNSUPPORTED`, with guidance toward CDP or coordinates.

Proposed observation filters: `--scope viewport|page`, `--role`, `--name`,
`--limit`, `--max-bytes`, `--since OBSERVATION`. The default scope is `viewport`.
Diff output is a later optimization; v0.1 must only provide bounded full observations.
Canvas and semantically inaccessible widgets remain operable through screenshots
and coordinates. The CLI does not interpret images itself.

## 8. Actions and synchronization

Element clicks resolve the target, check visibility and whether it can be activated,
scroll if needed, and use hit-testing to verify a reachable click point. The click
is then sent through CDP input. An obscured element returns `OBSCURED`; the CLI does
not automatically fall back to JavaScript `click()`. Coordinate clicks provide no
semantic target guarantee and require coordinates within the viewport.

`fill` replaces the contents of a supported editable element and checks the
resulting value. Unsupported editors return an explicit error.
`type` inserts text at the current focus; individual keyboard events are handled
by `press`. These distinctions are explained in concise command help.

Success for `click` means the input was dispatched; it does not confirm task success.
Success for `fill` additionally confirms the checked field value.
The agent then verifies the workflow using `info`, `observe`, `wait`, or `eval`.

Navigation waits for `domcontentloaded` by default. Supported explicit waits:
`--state domcontentloaded|load`, `--url`, or an element target with
`--state visible|hidden|enabled`. Target waits may re-resolve role/name or CSS while
waiting for a new element; reference waits never switch identity.
`networkidle` is not a universal default. A page load does not confirm SPA readiness.

Proposal: a 10-second default for actions/target waits and 30 seconds for navigation.
Every request has a finite deadline that includes queue time. If navigation is
dispatched but its loading condition is not reached, the error reports the possible
effect. Multi-part input makes a best effort to release pressed keys after
cancellation; partial effects must still be reported.

## 9. Output, errors, and token budget

A normal invocation writes exactly one compact JSON object to stdout and exits.
Logs belong on stderr. Diagnostic information is not emitted alongside the result
by default. `--format text` provides a readable view of the same data;
JSON remains the stable automation contract.

```json
{"ok":true,"tab":"t1","result":{"observation":"o7","elements":[{"ref":"o7:e3","role":"button","name":"Sign in"}],"truncated":false}}
```

```json
{"ok":false,"tab":"t1","error":{"code":"STALE_REF","message":"Element reference expired","effect":"none","next":"observe"}}
```

`effect` has the values `none`, `possible`, and `confirmed`: no action dispatched,
a possible partial/full effect, or a technically confirmed effect. This
classification concerns the browser operation, not task success. A `next` hint
is a diagnostic recommendation and does not execute an action.

Proposed exit codes: `0` success, `2` invalid arguments, `3` target/page state,
`4` timeout, `5` connection/controller, `1` other internal errors. Local argument
errors must also use the same error format when JSON output is selected.

Stable error codes include `NO_BROWSER`, `NO_TAB`, `TAB_CLOSED`, `STALE_REF`,
`TARGET_NOT_FOUND`, `AMBIGUOUS_TARGET`, `OBSCURED`, `NOT_EDITABLE`, `TIMEOUT`,
`CONNECTION_LOST`, `EXECUTION_ERROR`, `UNSUPPORTED`, and `PROTOCOL_ERROR`.
JavaScript exceptions must produce `ok:false`, even when CDP transport succeeds.

Proposed budgets: observations contain at most 60 elements and 12 KiB of total
serialized output; simple action responses are typically below 1 KiB. These are
byte/element limits, not model-specific token counts. Content is truncated before
serialization; the result remains valid JSON and reports `truncated` along with
available continuation/filter options. Long names, URLs, and text must also be
bounded. Essential status and error fields are preserved.

Large `eval`/CDP results are returned as files with an absolute path, size, and type.
Screenshots are always returned as files. No silent data loss; no Base64 in default
output. Success responses do not echo entered passwords or unnecessarily repeat
complete action parameters.

## 10. Recovery and reusable helpers

A typical agent workflow:

```sh
cdpsh connect --endpoint http://127.0.0.1:9222
cdpsh tab new https://example.com/login
cdpsh observe
cdpsh fill --ref o7:e1 --text-file email.txt
cdpsh click --ref o7:e3
cdpsh observe
```

The references in this example are placeholders from the preceding observation.
An agent reads the actual IDs from JSON; a saved helper must not hard-code those
IDs across runs. It uses fresh observations or explicit semantic targets and checks
preconditions and postconditions.

On `STALE_REF`, the agent observes again and chooses based on current data.
On `effect:possible`, it first checks the effect before repeating the same action.
Bounded reconnects to the same endpoint are allowed; references become invalid
afterward. cdpsh does not open a replacement browser or replay mutating requests.
After a controller restart, no action is guaranteed to be pending or completed;
`status` reports a new epoch.

The optional skill document, created after v0.1, should explain this workflow,
reference lifetimes, file inputs, tab selection, and error interpretation.
The CLI remains fully usable without a skill. A later batch mode executes a finite
command list, stops on errors by default, and reports completed steps.
It is not a transaction and has no rollback.

## 11. Operation and extensibility

cdpsh should work on Linux, macOS, and Windows. Browser endpoints are configured
explicitly; unavailable remote debugging produces a concrete diagnostic.
An open CDP connection provides broad browser access. Logical contexts are not a
security boundary; local IPC and configuration files belong to the user.
Connection secrets are not echoed in regular output or logs.

Unrelated tabs are not closed automatically. `disconnect` closes only the
connection. Closing a tab requires an explicit ID. Screenshot/result files do not
overwrite existing files without an explicit option. By default, there is no
telemetry, recording, or persistent logging of page content.

Controller-internal action events should be able to represent context, tab,
operation, status, and technical effect. This leaves room for later integration
with workers or Vizron; an external event API is not a v0.1 contract. Raw CDP and
arbitrary JavaScript allow only limited semantic attribution.

Performance is measured separately for process startup/IPC, target resolution,
observation, and browser work. Go and persistent connections are architectural
choices; a speedup over Python is not a product promise without measurements.

## 12. Implementation and acceptance criteria

1. Transport feasibility spike: connect, target session, typed call, raw call, and reconnect.
2. CLI/controller contract: JSON, errors, deadlines, status, and context/tab selection.
3. Observation and targets: references, role/name, CSS, limits, and invalidation.
4. Actions and waits: navigation, input, screenshots, and effect classification.
5. Concise help, schemas, and a documented agent workflow without a skill.

v0.1 is accepted only when the following scenarios work against local test pages:

- An existing browser can be connected; no execution path launches a browser.
- Separate CLI invocations reuse the same connection and context selection.
- Two contexts retain their tab targets during concurrent selection changes.
- Observe → fill → click → verify works without raw CDP.
- Navigation, reconnects, and removed elements produce clear reference errors.
- Ambiguous and obscured targets are not silently clicked.
- A connection loss after input does not trigger automatic replay.
- Large pages and large raw results honor the documented output contract.
- `eval` exceptions, timeouts, and unsupported features produce structured errors.
- An agent can perform the core workflow using `help` and `schema` without a skill.

## 13. Implementation details

The design philosophy in Section 1 is agreed. v0.1 makes these implementation
choices without changing those principles:

The initial implementation pins Go 1.24+ and dependency versions in go.mod.
README.md records final budgets, deadlines, directories, editor/frame limits,
and Chrome 155 acceptance on Windows. Linux/macOS are cross-build verified;
Linux browser acceptance is included in the supplied CI workflow. Hidden-tab
screenshots may require an explicit `tab activate ID`, as reported by a timeout hint.

- Transport: chromedp v0.14.2, cdproto v0.0.0-20250724212937-08a3db8b4327,
  and a flattened-session router; Go 1.24 minimum.
- Frame actions: main frame only; unsupported child frames are discoverable.
  Open shadow DOM is supported by AX references/role targets. Custom editors
  and non-text input types have no high-level fill contract.
- Defaults: 60 observation elements, 12 KiB output, 10-second action/wait deadlines,
  30-second navigation/connect deadlines; absolute artifact paths under CDPSH_HOME.
- Native macOS browser acceptance and a broader Chromium-version matrix remain
  unverified. CI configuration is supplied; its hosted runs have not been executed here.

## 14. References and context

- [Browser Harness](https://github.com/browser-use/browser-harness): inspiration for
  a thin harness and reusable helpers that agents extend themselves.
- [Browser Harness workflow](https://github.com/browser-use/browser-harness/blob/main/SKILL.md):
  a reference for accessibility-based observation, coordinate actions, and raw CDP.
  cdpsh defines its CLI and context management independently.
- [chromedp/cdproto](https://github.com/chromedp/cdproto): generated Go commands,
  types, and events; the preferred foundation for typed internal CDP calls.
- [Chrome DevTools Protocol](https://chromedevtools.github.io/devtools-protocol/):
  the underlying protocol specification.

These references do not imply 1:1 compatibility. This document does not adopt
unverified API or performance claims from the earlier conversation as contracts.
