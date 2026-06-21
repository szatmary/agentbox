# agentbox

Run autonomous, fully-sandboxed Claude coding agents in Apple `container` microVMs.

`agentbox` is a single Go binary that starts a disposable sandbox VM, runs a bounded
Claude Code session inside it, resumes that session until the agent declares the task
done, and (optionally) relaunches fresh sessions until the work converges — all without a
human in the loop.

> **Runtime is macOS-only.** agentbox drives Apple's [`container`](https://github.com/apple/container)
> CLI and the macOS keychain, so the agent actually *runs* only on macOS 26+. The module
> still builds, vets, and unit-tests on Linux (CI does), because every OS/tool dependency
> sits behind an interface that is faked in tests. A Docker/Podman backend can be added
> later behind the same `container.Runtime` interface.

Status: under active construction — see [ROADMAP.md](ROADMAP.md) and
[PROGRESS.md](PROGRESS.md). Full usage docs land with step 8.

## Layout

| Package | Responsibility |
| --- | --- |
| `cmd/agentbox` | Cobra CLI: `init`, `doctor`, `build`, `run`, `autorun`, `status`, `logs`, `stop` |
| `internal/container` | `Runtime` interface + real (`container` CLI) and fake backends |
| `internal/config` | TOML job file, flag overrides, validation |
| `internal/run` | Run-directory layout (`control/ output/ logs/ workspace/`) + logging |
| `internal/auth` | Claude credential + GitHub token + git identity resolution |
| `internal/supervisor` | Pure-Go per-run resume loop driving one long-lived VM |
| `internal/autorun` | Continuous relaunch loop with git-HEAD progress detection |
| `internal/embedfs` | Embedded default Dockerfile + task template |

## License

MIT © Matthew Szatmary
