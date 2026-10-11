<div align="center">

# cdp

### Observe. Act. Verify.

A small Go browser CLI for agents, with persistent sessions and compact JSON.

[![CI](https://github.com/mathisarends/cdpsh/actions/workflows/ci.yml/badge.svg)](https://github.com/mathisarends/cdpsh/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.24%2B-00ADD8?logo=go&logoColor=white)](go.mod)
![Platforms](https://img.shields.io/badge/platforms-Linux%20%C2%B7%20macOS%20%C2%B7%20Windows-353a46)

[Quickstart](#quickstart) · [Usage guide](docs/usage.md) · [Specification](SPEC.md) · [Transport design](docs/transport.md)

</div>

Connect an existing Chromium browser. Read a bounded observation, act on a precise
target, then check the result. A local controller keeps the CDP connection alive
between shell commands, so your agent can work one step at a time.

```sh
cdp observe
cdp fill --role textbox --name Email --text-file email.txt
cdp click --role button --name "Sign in"
cdp observe
```

## Why cdp?

| Capability | What you get |
| --- | --- |
| **Small observations** | Accessible roles, names and fresh element references; a 12 KiB default response budget with explicit truncation. |
| **Precise actions** | References, role/name, CSS or click coordinates. Ambiguous and covered targets return useful errors. |
| **Persistent sessions** | One binary with a local controller; independent tab selections for each agent context. |
| **Clear recovery** | Structured errors and `none` / `possible` / `confirmed` effects. The agent chooses the next step. |
| **Shell friendly** | One JSON response per invocation, meaningful exit codes, UTF-8 file/stdin inputs and file-based screenshots. |
| **Room to go deeper** | JavaScript evaluation and arbitrary raw CDP calls, plus machine-readable command schemas. |

## Quickstart

Build with **Go 1.24+**:

```sh
git clone https://github.com/mathisarends/cdpsh.git
cd cdpsh
go build -o bin/cdp ./cmd/cdp
```

On Windows, build with `go build -o bin/cdp.exe ./cmd/cdp`. Add `bin` to your
`PATH`, or invoke the executable from there directly. The repository is named
`cdpsh`; the command is **`cdp`**.

Enable remote debugging on a Chromium browser you manage separately, using a
dedicated profile. `cdp` connects to that browser; it never starts or installs one.
The endpoint grants access to the connected browser.

```sh
cdp connect --endpoint http://127.0.0.1:9222
cdp tabs
cdp tab use TARGET_ID
cdp observe
cdp fill --role textbox --name Email --text-file email.txt
cdp click --role button --name "Sign in"
cdp observe
```

Replace `TARGET_ID` with an ID from `tabs`. Create `email.txt` as a UTF-8 file with
the exact text to enter. You can also open a tab with `cdp tab new URL`.

## A contract an agent can work with

Observations provide short-lived references. Use an actual reference from the
preceding result to target that element:

```sh
cdp click --ref OBSERVATION_ID:e1
cdp wait --css '#result' --state visible
cdp observe
```

Navigation, reconnects, removed elements and a new observation in the same context
invalidate references. A dispatched click confirms the input operation; observe
again to determine whether the task succeeded. If an error reports
`effect: "possible"`, inspect the page before retrying.

```json
{"ok":true,"tab":"TARGET_ID","result":{"effect":"confirmed"}}
```

```json
{"ok":false,"tab":"TARGET_ID","error":{"code":"STALE_REF","message":"Element reference expired or was removed","effect":"none","next":"observe"}}
```

## Discover the commands

```sh
cdp help
cdp help click
cdp schema fill
cdp observe --format text
cdp --context agent-a tab use TARGET_A
cdp --context agent-b tab use TARGET_B
cdp screenshot --out viewport.png
cdp eval --file inspect.js
cdp call Runtime.evaluate --params-file request.json
cdp call Browser.getVersion --scope browser
```

| Area | Commands |
| --- | --- |
| Connection | `connect`, `disconnect`, `status`, `doctor` |
| Tabs | `tabs`, `tab new`, `tab use`, `tab activate`, `tab close` |
| Navigation | `goto`, `back`, `forward`, `reload`, `info` |
| Observe and act | `observe`, `screenshot`, `click`, `fill`, `type`, `press`, `scroll`, `wait` |
| Advanced | `eval`, `call`, `help`, `schema`, `version`, `controller stop` |

Contexts share browser cookies and permissions, while keeping their tab selections
separate. High-level targeting supports the main frame and open shadow DOM through
accessibility references. Child-frame actions use raw CDP or coordinates. `fill`
supports text inputs and textareas; custom editors need an explicit workflow.

See the [usage guide](docs/usage.md) for deadlines, output limits, exit codes,
environment variables, supported inputs and screenshot behavior.

## Development

```sh
go test ./...
go vet ./...
go test -race ./...
golangci-lint run --build-tags integration ./...
```

CI tests and builds on **Linux, macOS and Windows**. The Linux job also runs the
race detector and an acceptance suite that launches a separate browser with a
blank profile, builds the real CLI and exercises it through separate processes.

```sh
CDPSH_TEST_BROWSER=/path/to/chromium CDPSH_TEST_RACE=1 go test -tags integration -v ./integration
```

Windows setup and acceptance-test details are in the [usage guide](docs/usage.md#development-and-verification).
The existing `CDPSH_*` environment variables remain supported.

**v0.1 scope:** browser connection and control. Browser hosting, batch execution,
MCP and the optional agent skill are deferred; see the [specification](SPEC.md).
