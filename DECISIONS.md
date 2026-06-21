# Decisions

Architecture decisions and their rationale. Append-only-ish; supersede with a note.

## D1 — macOS-only runtime, Linux-buildable module
agentbox drives Apple `container` and the macOS keychain (a macOS-only stack), but CI
and the dev VM are Linux. Every OS/external-tool touchpoint (`container`, `gh`, `git`,
`security`) sits behind a Go interface with a fake for tests. Keychain access lives in a
`//go:build darwin` file with a `//go:build !darwin` stub returning a clear error, so the
whole module compiles and `go test ./...` passes on Linux. Real external tools are never
invoked during `go test`.

## D2 — Dependencies
- `github.com/spf13/cobra` — CLI framework (subcommands, flags, help). Standard, stable.
- `github.com/BurntSushi/toml` — TOML parsing for the job file. Mature, supports custom
  `encoding.TextUnmarshaler` types (used for our `Duration`).
Everything else is stdlib (`log/slog`, `os/exec`, `context`, `time`, `text/template`,
`embed`).

## D3 — Durations
TOML durations (`max_wall = "3h"`) map to a `config.Duration` wrapper over
`time.Duration` implementing `encoding.TextUnmarshaler`/`TextMarshaler`. This keeps the
TOML human-friendly and yields real `time.Duration` values after parse.

## D4 — Supervisor is pure Go (the key design choice)
The per-run resume loop lives in `internal/supervisor` as pure logic over the
`container.Runtime` interface and an injectable clock. It starts one long-lived VM, runs
`claude -p <task>` on iteration 1 and `claude --continue <resume>` afterwards, reading the
`STATUS` control file after each iteration and checking guards (wall clock, iteration
count, per-call turn cap, `STOP` file) before each. Because it depends only on interfaces,
the stop/guard/sentinel logic is table-tested against the fake Runtime with no real
container, `claude`, or VM.

## D5 — Control-file protocol
The agent inside the VM signals completion by writing `/work/control/STATUS` whose first
line is `DONE` or `FAILED: <reason>`. A `STOP` file in the same dir requests a graceful
halt. The supervisor reads these by `exec`-ing `cat` in the VM and interpreting a non-zero
exit as "file absent". This mirrors the existing bash harness contract exactly.

## D6 — Autorun progress detection
Continuous relaunch detects progress by comparing the remote git `HEAD` between runs (the
agent pushes its work). N consecutive runs with no HEAD change ⇒ stop (`no-progress`).
When no repo is configured the HEAD probe is disabled and only `DONE`/`FAILED`, a stop
signal, or the optional `max_runs` guard end the loop. Documented so the behavior is not
surprising.

## D7 — Secrets are one-way and never logged
`internal/auth` resolves a Claude credential and a GitHub token + git identity into an
`Injection` (env vars / files) pushed into the VM. Credential *values* are never written
to logs or stdout; only their source/kind is reported. Loggers redact known secret keys.

## D8 — Auth source selection
`auth.claude ∈ {keychain, api_key, token}`: keychain reads the macOS keychain item
`Claude Code-credentials` (an OAuth blob); `api_key` reads `ANTHROPIC_API_KEY`; `token`
reads `CLAUDE_CODE_OAUTH_TOKEN`. `auth.github ∈ {gh, pat, none}`: `gh` shells `gh auth
token`; `pat` reads `GITHUB_TOKEN`/`GH_TOKEN`; `none` injects no GitHub credential.
