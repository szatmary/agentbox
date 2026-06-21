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
| `agentbox status` | List runs under `--runs-dir` and their STATUS. |
| `agentbox logs <run>` | Print a run's `logs/run.log`; `-f` to follow. |
| `agentbox stop <job\|run>` | Write the STOP file and signal any detached process. |

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
