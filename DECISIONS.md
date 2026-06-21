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
`Injection` (env vars / files) pushed into the VM. Credential *values* are never passed
to a logger or written to stdout; only their source/kind is reported. Secret values are
also kept out of process argv: they are written to a 0600 env file (staged outside the
mounted control dir and removed after teardown) and sourced inside the VM, rather than
injected as `container run -e KEY=VAL` (which would be visible via `ps`/`container
inspect`). See HARDENING S1/S2.

## D8 — Auth source selection
`auth.claude ∈ {keychain, api_key, token}`: keychain reads the macOS keychain item
`Claude Code-credentials` (an OAuth blob); `api_key` reads `ANTHROPIC_API_KEY`; `token`
reads `CLAUDE_CODE_OAUTH_TOKEN`. `auth.github ∈ {gh, pat, none}`: `gh` shells `gh auth
token`; `pat` reads `GITHUB_TOKEN`/`GH_TOKEN`; `none` injects no GitHub credential.

## D9 — `--dangerously-skip-permissions` inside the sandbox
The supervised `claude` runs with `--dangerously-skip-permissions`. The agent is
fully autonomous inside a disposable microVM with no human to answer permission
prompts, so interactive approval would deadlock the loop. The blast radius is the
sandbox VM, which is stopped and removed at the end of every run. This is the same
posture as the bash harness agentbox generalizes.

## D11 — Hardening: secrets, failure paths, CLI-vs-reality (audit response)
An independent audit found defects the fake-based tests were structurally blind to. Key
decisions made while fixing them (full list in HARDENING.md):
- **Secrets never in argv (S1/S2).** All secret env vars and the keychain OAuth blob go
  into a 0600 file staged in a dir *outside* the bind-mounted control dir, mounted
  read-only, sourced (`set -a; . file`) before every setup/claude exec, and removed after
  teardown. Nothing secret reaches `container run`/`exec` argv.
- **Input validation (S3/S4).** `extra_packages` must match
  `^[A-Za-z0-9][A-Za-z0-9.+_-]*$`; repo URLs reject `-`-leading and `ext::` and pass a
  `--` separator to git, closing flag/transport injection.
- **Honest failure status (C2/C3).** A claude that exits non-zero with no STATUS is no
  longer a benign guard trip: the supervisor backs off and aborts with `claude_error`
  after `MaxClaudeErrors`. A control read that fails for any reason other than cat's
  exit-1 ("absent") aborts (dead VM), instead of looping.
- **Exec is wall-bounded (O3).** Each claude exec gets a ctx deadline derived from the
  remaining wall budget so a hung agent is interrupted within the iteration.
- **uid 0 is valid (C4).** `DockerfileData.HostUID/HostGID` are `*int`; nil means unset
  (defaults to 1000), an explicit 0 is honored so root/CI bind-mount ownership is correct.
- **Detach/stop lifecycle (O2).** `stop` does a `Signal(0)` liveness check before SIGTERM
  (never signals a reused PID) and removes stale pidfiles; the detached child removes its
  pidfile on exit; autorun consumes stale `.stop` markers at startup.
- **Testability (H3).** `executeRun` delegates to `executeRunWith(rt, resolver)` so the
  full config→auth→supervisor wiring is exercised with fakes that return non-zero exits
  and dead-VM conditions; control dir is 0700.

## D10 — `container` CLI shelling and version drift
`internal/container.CLIRuntime` shells out to Apple's `container` CLI. Subcommand
names live in named constants (`build`, `run`, `exec`, `stop`, `delete`,
`image inspect`) so they can be adjusted for a given `container` release in one
place. The command runner is an injectable `commandFunc`, so argument construction
is unit-tested without invoking the real binary; the real `execCommand` path is
covered using `/bin/sh`. `agentbox doctor` surfaces a clear message if `container`
or its service is unavailable.

## D12 — Attach + observe rides entirely on `container exec`
The "attach + observe" layer (humans via SSH/VSCode, AI agents via MCP) never opens a
port or dials the VM IP. On this Apple `container` setup, host→VM direct TCP and
`--publish` are flaky, but `container exec` is reliable (verified). So every path —
including SSH — rides on `exec`. SSH is tunneled by a `ProxyCommand`
(`agentbox ssh-proxy <run>`) that runs `sshd -i` (inetd mode) inside the VM over a piped
`exec`; nothing listens on the VM IP. This decision is grounded and not relitigated.

The single seam stays the only seam: the `container.Runtime` interface gains exactly two
methods — `Inspect` (resolve a run name → live container + liveness; CLIRuntime parses
`container inspect` JSON, isolated like the subcommand constants per D10) and `ExecStream`
(stdin + optional TTY, no buffering — for the interactive `shell` and the SSH tunnel).
Both are faked, so `shell`/`ssh-proxy` argv and run→container resolution are asserted
without a real VM.

## D13 — Run name ⇄ live container is deterministic
A supervised run starts its VM with `--name <job>-<id>`, which is exactly the run-dir base
name. So a run resolves to its live container by *name*; the attach/observe code only has
to confirm the container is running (via `Inspect`), never guess an id. `internal/observe`
holds the host run dir (for the STATUS sentinel and `logs/run.log`, read directly so
status survives VM teardown) and the Runtime (for in-VM `ls`/`cat`/`git`/arbitrary exec).
Every VM-touching method resolves first and returns `ErrNotRunning` when the VM is down.

## D14 — SSH key handling, sshd-as-root, and ephemeral host keys
- **Ephemeral per-run ed25519 keypair.** Generated at run start when `[attach] ssh = true`,
  written `0600` into `<run>/ssh/` (a sibling of the bind-mounted dirs, so the private key
  is never visible inside the VM). Only the *public* key is installed into the VM's
  `authorized_keys` (via an `exec` setup command — public keys are not secret). OpenSSH key
  marshaling uses `golang.org/x/crypto/ssh` (the one added dependency).
- **`sshd -i` runs as root via `sudo`.** The image's default user is the non-root `agent`,
  but sshd needs root for host keys and privilege separation. `agent` already has
  passwordless sudo in the image, so the ProxyCommand runs `sudo /usr/sbin/sshd -i -e`.
  Host keys + `/run/sshd` are created in the same one-time setup (`ssh-keygen -A`). No
  network sshd is ever started — inetd-over-exec only.
- **Host-key checking is off by design.** Per-run host keys are throwaway and reached only
  through the local `exec` pipe, so the generated `~/.ssh/config` block sets
  `StrictHostKeyChecking no` / `UserKnownHostsFile /dev/null`. There is no stable host
  identity to pin and nothing is network-exposed.
- **ProxyCommand carries an absolute `--runs-dir`.** ssh invokes the ProxyCommand from an
  arbitrary cwd, so the run dir is resolved to an absolute path when the Host block is
  written.

## D15 — MCP server is a hand-rolled JSON-RPC 2.0 subset
Rather than take an MCP-library dependency, `internal/mcp` implements just the methods MCP
needs — `initialize`, `tools/list`, `tools/call`, and notifications — over stdio
(newline-delimited JSON) and a minimal JSON-RPC-over-HTTP transport. This keeps the server
small, dependency-free, and fully table-testable against the fake runtime. Tools are thin
wrappers over `internal/observe`. Tool failures are returned as `isError` results (so the
model can read and react), while protocol misuse yields JSON-RPC errors. The `exec` tool
runs in the user's own sandbox, so `--http` binds to `127.0.0.1` by default (trust model in
the README).
