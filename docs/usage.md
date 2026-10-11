# Using cdp

A Go browser CLI for agents. Each invocation emits one compact JSON result and
uses a persistent local controller connected to an **existing** Chromium browser.
Recovery decisions belong to the agent; actions are never automatically replayed.

## Build

Go 1.24 or newer:

```sh
go build -o bin/cdp ./cmd/cdp
```

On Windows use `bin/cdp.exe` as the output name. There is one executable for the
CLI and its controller. It never starts or installs a browser.

## First workflow

Enable remote debugging on a browser you manage separately. Use a dedicated
profile; Chromium's remote debugging endpoint gives access to that browser.

```sh
cdp connect --endpoint http://127.0.0.1:9222
cdp tabs
cdp tab use TARGET_ID
cdp observe
cdp fill --role textbox --name Email --text-file email.txt
cdp click --role button --name "Sign in"
cdp observe
```

Alternatively `tab new URL` creates and selects a new tab. References returned by
`observe`, such as `o6b6f5d3ca457c88e2:e1`, may replace role/name targets:

```sh
cdp click --ref OBSERVATION_ID:e1
cdp wait --css '#result' --state visible
```

Read the actual reference from the preceding JSON. A new observation replaces
references for that context and tab. Navigation, removed elements, a reconnect,
or a controller restart invalidate references. CSS and role/name selectors must
match exactly one element; names use exact matching. `--frame` explicitly supports
the main frame. Observations list child frames with `supported:false`; use raw CDP
or viewport coordinates for them. Open shadow DOM elements are accessible through
AX references and role/name; CSS selectors use the main document's querySelectorAll.

`fill` replaces supported text/email/password/search/tel/url inputs or textareas,
sends native input, and verifies the value. `type` inserts text at the current
focus. `press` sends keyboard events, e.g. `Ctrl+A`, `Enter`, or `Shift+Tab`.
Number, date, file, contenteditable and custom editors have no high-level fill
contract in this release. Scrolling uses the DOM scrolling APIs; clicks always
use CDP input after visibility, enabled-state and hit-test checks.

## Commands and discovery

`help` provides the overview; `help click` and `schema fill` expose focused usage
and machine-readable contracts. Global flags can appear before or after commands.
Use `--format text` for readable observations. JSON remains the automation format.

`tabs` is bounded as well; use its `next` hint (`tabs --offset N`) to continue.

| Area | Commands |
| --- | --- |
| Connection | `connect`, `disconnect`, `status`, `doctor` |
| Tabs | `tabs`, `tab new/use/activate/close` |
| Navigation | `goto`, `back`, `forward`, `reload`, `info` |
| Observation | `observe`, `screenshot` |
| Input | `click`, `fill`, `type`, `press`, `scroll` |
| Synchronization | `wait` |
| Escape hatches | `eval`, `call` |
| Local lifecycle | `controller stop`, `version` |

```sh
cdp --context agent-a tab use TARGET_A
cdp --context agent-b tab use TARGET_B
cdp --context agent-a observe --scope page --role button
cdp --context agent-b --tab TARGET_A info
cdp screenshot --out viewport.png
cdp eval --file inspect.js
cdp call Runtime.evaluate --params-file request.json
cdp call Browser.getVersion --scope browser
```

Contexts retain independent tab selections. `--tab` overrides selection only for
one invocation. Contexts share the browser's cookies and permissions. Selection
does not activate the visible browser tab; `tab activate ID` does that explicitly.
Actions and reads are ordered per tab; explicit waits allow later actions to run.

Chrome 155 in headless mode can stall screenshot capture for a hidden tab. Use
`tab activate ID` explicitly before a screenshot in that case. A screenshot timeout
provides that diagnostic hint; the CLI never activates another tab implicitly.

`--text-file`, `--file`, and `--params-file` accept `-` for stdin. Input files must
be UTF-8. Exactly one text/expression source is required; CDP parameters default
to `{}` and must be a JSON object. Input payloads are limited to 4 MiB.

## Results, deadlines and recovery

```json
{"ok":true,"tab":"TARGET_A","result":{"effect":"confirmed"}}
{"ok":false,"tab":"TARGET_A","error":{"code":"STALE_REF","message":"Element reference expired or was removed","effect":"none","next":"observe"}}
```

`effect` reports the technical browser operation: `none`, `possible`, or
`confirmed`. A dispatched click does not confirm business success. On `possible`,
inspect the page before retrying. Reconnect explicitly with `connect`; there is
no automatic target substitution or mutation replay. `status` reports the
controller epoch. Connections and references live in memory, not in profile files.

Actions and waits default to 10 seconds; navigation and connect to 30 seconds.
`--timeout 2s` includes queue time, with a maximum of five minutes. Navigation
waits for `domcontentloaded`; use `--state load` if needed. SPA readiness needs
an explicit element/URL wait or JavaScript check. `networkidle` is unsupported.

Observations default to 60 elements and a **12 KiB total JSON response budget**.
Use `--limit 100 --max-bytes 24000`, with maximums of 500 elements and 64 KiB.
Truncation is explicit, and strings are bounded without breaking UTF-8 or JSON.
Large eval/CDP values are saved losslessly to JSON files. Screenshots are PNG files.
Results give absolute paths, byte sizes and media types; no Base64 is printed.
Existing output files require `--overwrite`. Password values are not echoed by
fill responses or read into observations.

| Exit | Meaning |
| --- | --- |
| 0 | Success |
| 1 | Execution, protocol, or internal error |
| 2 | Invalid arguments or input |
| 3 | Target/page state, stale ref, or unsupported feature |
| 4 | Timeout |
| 5 | Browser connection or controller error |

## Local controller

`connect` starts the controller if necessary. IPC uses an owner-only named pipe
on Windows and a Unix socket with mode `0600` on Linux/macOS. The controller has
no public HTTP listener. `disconnect` removes a browser connection; `controller
stop` stops the local controller. Both leave browser tabs open.

| Variable | Default / purpose |
| --- | --- |
| `CDPSH_BROWSER` | `default`; browser alias |
| `CDPSH_CONTEXT` | `default`; selection context |
| `CDPSH_HOME` | User cache directory + `cdpsh`; controller/artifact directory |

Flags override environment defaults. Browser aliases are registered by
`connect --endpoint URL --name ALIAS`. Select them with `--browser ALIAS`.
No telemetry or persistent page-content logs are enabled.

## Development and verification

```sh
gofmt -w cmd internal integration
go test ./...
go vet ./...
go test -race ./...
golangci-lint run --build-tags integration ./...
```

The tagged acceptance suite starts a separate Chromium process with a fresh
temporary profile, serves local fixture pages, builds the real CLI, and invokes
it in separate processes. Only the test harness launches a browser. The test
cleans up its controller, browser and temporary profile.

```powershell
$env:CDPSH_TEST_BROWSER = 'C:\Program Files\Google\Chrome\Application\chrome.exe'
go test -tags integration -v ./integration
```

```sh
CDPSH_TEST_BROWSER=/path/to/chromium go test -tags integration -v ./integration
```

Set `CDPSH_TEST_RACE=1` to build the tested CLI/controller with race instrumentation
as well. This checks races across the real IPC requests and CDP event reader.

Acceptance is exercised on Chrome 155 on Windows, including explicit activation
before screenshots. The fixture uses headless rendering flags and a blank profile.
CI runs unit tests, vet and builds on Linux, macOS and Windows, plus lint, race
checks and the blank-profile browser suite on Linux. Go race testing on Windows
requires a working C compiler in PATH. Dependency versions are
pinned in `go.mod` and `go.sum`. See [SPEC.md](../SPEC.md) for the design and
[transport decision](transport.md) for the transport feasibility decision.

Batch, MCP, browser lifecycle management and the optional agent skill are deferred.
