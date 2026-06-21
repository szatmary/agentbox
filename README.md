# agentbox

Run autonomous, fully-sandboxed Claude coding agents in Apple `container` microVMs.

`agentbox` is a single Go binary that starts a disposable sandbox VM, runs a bounded
[Claude Code](https://docs.claude.com/en/docs/claude-code) session inside it, resumes
that session until the agent declares the task done, and — with `autorun` — relaunches
fresh sessions until the work converges. No human in the loop.

It generalizes a hand-rolled bash harness (run a bounded agent session → resume it until
done → auto-relaunch) into one tested, configurable tool.

```
agentbox init        # scaffold agentbox.toml + task.md
agentbox doctor      # check prerequisites
agentbox build       # build the sandbox image
agentbox run         # one bounded, self-resuming run
agentbox autorun     # relaunch until the job converges

agentbox shell <run>     # interactive shell in a live run's VM
agentbox attach <run> --vscode   # open the run's workspace in VSCode (Remote-SSH)
agentbox mcp             # serve live runs to other AI agents over MCP
```

---

## ⚠️ Runtime is macOS-only

agentbox drives Apple's [`container`](https://github.com/apple/container) CLI (microVMs
on Apple silicon, macOS 26+) and reads Claude's OAuth credentials from the **macOS
keychain**. The agent therefore *runs* only on macOS.

The Go **module**, however, builds, vets, and unit-tests on **Linux and macOS** (CI does
both). Every OS/external-tool dependency — `container`, `git`, `gh`, the keychain — sits
behind a Go interface that is faked in tests, and the keychain implementation is
`//go:build darwin` with a non-darwin stub. So `go build ./... && go vet ./... && go test
./...` is green anywhere, and the supervisor/autorun logic is fully tested without a real
VM. See [DECISIONS.md](DECISIONS.md) (D1).

A **Docker/Podman backend** can be added later behind the same
[`container.Runtime`](internal/container/runtime.go) interface without touching the
supervisor, autorun, or CLI code.

---

## Install

```sh
go install github.com/szatmary/agentbox/cmd/agentbox@latest
```

Or build from source:

```sh
git clone https://github.com/szatmary/agentbox.git
cd agentbox
go build -o agentbox ./cmd/agentbox
```

Release binaries are produced with [GoReleaser](https://goreleaser.com) (see
[`.goreleaser.yaml`](.goreleaser.yaml)).

**Homebrew** (planned): once a release tap is published,

```sh
brew install szatmary/tap/agentbox
```

### Prerequisites (macOS runtime)

- macOS 26+ on Apple silicon with Apple [`container`](https://github.com/apple/container)
  installed and its service started (`container system start`).
- A Claude credential: signed in via `claude` (keychain), or `ANTHROPIC_API_KEY`, or a
  `CLAUDE_CODE_OAUTH_TOKEN`.
- `gh` authenticated (`gh auth login`) if the agent pushes to GitHub over HTTPS — or a
  `GITHUB_TOKEN`/`GH_TOKEN`.

Run `agentbox doctor` to verify all of the above.

---

## Quick start

```sh
mkdir my-job && cd my-job
agentbox init --name my-job      # writes agentbox.toml + task.md
$EDITOR task.md                  # describe the mission
agentbox doctor                  # confirm prerequisites
agentbox build                   # build the sandbox image (once)
agentbox run                     # one supervised, self-resuming run
# ...or keep relaunching until it converges:
agentbox autorun --detach        # background; logs + pidfile written
agentbox status                  # see runs and their STATUS
agentbox logs <run> -f           # follow a run's log
agentbox stop my-job             # graceful stop
```

---

## How it works

```
agentbox run
  └─ resolve credentials (keychain | env)         internal/auth
  └─ create run dir  control/ output/ logs/ workspace/   internal/run
  └─ start ONE long-lived sandbox VM              internal/container (Runtime)
  └─ supervisor loop  ── per run ──               internal/supervisor
       setup: install creds, git config, clone repo
       iter 1 : container exec  claude -p <task>
       iter N : container exec  claude --continue <resume>
       after each: read /work/control/STATUS  (DONE / FAILED ?)
       before each: guards — max_wall, max_iters, STOP file
  └─ stop + remove the VM

agentbox autorun                                  internal/autorun
  └─ run bounded sessions back-to-back
  └─ detect progress via remote git HEAD change
  └─ stop on DONE/FAILED · N no-progress runs · max_runs · stop signal
  └─ cooldown between runs
```

### The completion contract

The agent signals the supervisor through files in `/work/control` (bind-mounted to the
run's `control/` directory):

- Write `STATUS` with first line **`DONE`** when the whole task is complete.
- Write `STATUS` with first line **`FAILED: <reason>`** to abort.
- A **`STOP`** file requests a graceful halt before the next iteration.

The supervisor resumes the agent (`claude --continue`) until `STATUS` is terminal or a
guard trips. This is exactly the protocol the original bash harness used.

---

## Attach & monitor a live run

A run is autonomous, but you don't have to fly blind. agentbox can **exec into and
observe a running VM** — for humans (an interactive shell or VSCode) and for other AI
agents (MCP). Inspection is read-mostly and built on the same `container exec` transport
the supervisor already uses.

### Why everything rides on `container exec`

On this Apple `container` setup, host→VM direct networking (`--publish`, dialing the VM
IP) is unreliable, but `container exec <vm> …` works. So **every attach path rides on
`exec`** — including SSH, which is tunneled through a `ProxyCommand` rather than by opening
a port. Nothing listens on the VM IP; no ports are published. A run's container is named
after its run directory (`<job>-<id>`), so a run name resolves deterministically to its
live container — agentbox only confirms it is actually running. (See
[DECISIONS.md](DECISIONS.md) D12–D14.)

### Quick poke: `agentbox shell`

```sh
agentbox shell my-job                 # interactive bash in the latest run's VM
agentbox shell my-job -- git log -5   # or run a one-off command
```

This is a direct `container exec -it` — no SSH, nothing to configure. Great for a quick
look. For editors and richer tooling, use SSH.

### Humans: VSCode over Remote-SSH

Enable SSH for the job (off by default):

```toml
[attach]
ssh = true
```

At run start agentbox then generates an **ephemeral per-run ed25519 keypair** (private key
`0600` in the run dir, never mounted into the VM), installs the public key into the VM's
`authorized_keys`, and generates host keys — all over `exec`. Now:

```sh
agentbox ssh my-job          # install an ~/.ssh/config Host block for the run
ssh agentbox-my-job          # …and connect (tunneled through container exec)

agentbox attach my-job --vscode   # or jump straight into VSCode Remote-SSH
```

`agentbox ssh` writes a `Host agentbox-<run>` block whose `ProxyCommand` is
`agentbox ssh-proxy <run>` — a hidden subcommand that resolves the live VM and pipes
`sshd -i` (inetd mode) over `container exec`. Because the tunnel decouples `~/.ssh/config`
from the per-run container name, your editor just sees a stable host alias. Use
`agentbox ssh my-job --print` to inspect the block without installing it.

> Per-run host keys are ephemeral, so the generated block sets
> `StrictHostKeyChecking no` / `UserKnownHostsFile /dev/null`. That is safe here precisely
> because the "network" is a local `exec` pipe, not an exposed socket.

### AI agents: the MCP server

```sh
agentbox mcp                 # stdio transport (for `claude mcp add`, etc.)
agentbox mcp --http          # HTTP transport on 127.0.0.1:7337 (localhost only)
```

The server exposes the observe layer as MCP tools so an external Claude can watch and
steer live runs:

| Tool | What it does |
| --- | --- |
| `list_runs` | All runs with STATUS + live state. |
| `get_status` | One run's liveness + STATUS sentinel. |
| `tail_log` | Tail of `logs/run.log`. |
| `list_files` / `read_file` | `ls`/`cat` inside the live VM. |
| `git_status` / `git_diff` | Git state of the working tree. |
| `exec` | Run a command inside the live VM. |
| `stop` | Request a graceful stop (writes `STOP`). |

Register it with another Claude, for example:

```sh
claude mcp add agentbox -- agentbox mcp
```

### Trust model

- **The `exec` and `shell` paths run commands inside *your own* sandbox VM** — the same
  blast radius as the agent itself, which is already running `--dangerously-skip-permissions`
  in a disposable microVM. Attaching grants no privilege the run doesn't already have.
- **The SSH private key is per-run, `0600`, and lives only on the host** in the run dir
  (never bind-mounted, never logged, removed when you delete the run). Only the public key
  is installed into the VM. This reuses agentbox's existing
  [secret-handling discipline](HARDENING.md) (no secrets in argv or logs).
- **`agentbox mcp --http` binds to `127.0.0.1` by default.** The `exec` tool can run
  arbitrary commands in the sandbox, so do **not** bind it to a public interface without
  understanding that you are handing shell access to anyone who can reach the port.

---

## Configuration (`agentbox.toml`)

```toml
name = "go2110"
repo = "https://github.com/szatmary/go2110.git"   # optional; omit for no git
task = "task.md"

[guards]
max_wall  = "3h"     # wall-clock budget for one run
max_iters = 500      # max resume iterations per run
max_turns = 200      # max agent turns per individual claude call

[model]
name = ""            # account default (e.g. "claude-opus-4-8")

[auth]
claude = "keychain"  # keychain | api_key | token
github = "gh"        # gh | pat | none

[image]
extra_packages = ["golang", "poppler-utils"]   # baked into the sandbox image

[autorun]
per_run_wall   = "3h"
max_noprogress = 3   # stop after this many runs with no remote-HEAD change
cooldown       = "30s"
# max_runs     = 0   # optional hard cap on total runs (0 = unlimited)

[attach]
ssh = false          # true => install a per-run SSH key for `agentbox ssh`/`attach`
```

Durations are Go duration strings (`"3h"`, `"30s"`, `"90m"`). Validation is strict and
errors are actionable. Any flag (`--task`, `--repo`, `--max-wall`, `--model`,
`--max-iters`, `--max-turns`, `--per-run-wall`, `--cooldown`, `--max-noprogress`,
`--max-runs`, `--name`, `--claude`, `--github`) overrides the corresponding config field.

### Credential sources

| `auth.claude` | Source |
| --- | --- |
| `keychain` | macOS keychain item `Claude Code-credentials` (OAuth blob) |
| `api_key`  | `ANTHROPIC_API_KEY` environment variable |
| `token`    | `CLAUDE_CODE_OAUTH_TOKEN` environment variable |

| `auth.github` | Source |
| --- | --- |
| `gh`   | `gh auth token` |
| `pat`  | `GITHUB_TOKEN` / `GH_TOKEN` |
| `none` | no GitHub credential injected |

Credentials are injected **one-way** into the VM and are **never logged**; only their
*source* is ever reported.

---

## Commands

| Command | Description |
| --- | --- |
| `agentbox init` | Scaffold `agentbox.toml` + `task.md` in the current directory. |
| `agentbox doctor [job.toml]` | Check prerequisites: `container` installed + service + ready, `gh` (if needed), an available Claude credential. |
| `agentbox build [job.toml]` | Build/rebuild the sandbox image from the embedded Dockerfile (host UID/GID build args, `extra_packages`). `--tag`, `--no-cache`, `--base-image`. |
| `agentbox run [job.toml]` | One bounded, self-resuming run. `--detach` to background. Flags override config. |
| `agentbox autorun [job.toml]` | Continuous relaunch loop. `--detach`. |
| `agentbox status` | List runs under `--runs-dir` and their STATUS. `--live` probes each VM's liveness. |
| `agentbox logs <run>` | Print a run's `logs/run.log`; `-f` to follow. |
| `agentbox stop <job\|run>` | Write the STOP file and signal any detached process. |
| `agentbox shell <run> [-- cmd…]` | Interactive shell (or one-off command) in a live run's VM via `container exec -it`. |
| `agentbox ssh <run>` | Install (or `--print`) an `~/.ssh/config` Host block reaching the run over a `container exec` ProxyCommand. |
| `agentbox attach <run> --vscode` | Configure SSH and open the run's workspace in VSCode Remote-SSH. |
| `agentbox mcp [--stdio\|--http]` | Serve the observe layer to AI agents over MCP. |

Run directories default to `.agentbox/runs/<name>-<timestamp>/`; override with
`--runs-dir`.

---

## Example: the `go2110` job

[`examples/go2110/`](examples/go2110) reproduces the kind of long-running, self-resuming
job agentbox was built for:

```sh
cd examples/go2110
agentbox build
agentbox autorun        # relaunches 3h sessions until go2110 converges or stalls
```

---

## Development

```sh
go build ./...   # builds on linux & macos
go vet ./...
go test ./...    # no real container / gh / git / keychain is invoked
```

The interesting logic — the supervisor's stop/guard/sentinel decisions and autorun's
progress/stop decisions — is table-tested against fakes. See
[ROADMAP.md](ROADMAP.md), [PROGRESS.md](PROGRESS.md), and [DECISIONS.md](DECISIONS.md).

## License

MIT © Matthew Szatmary
