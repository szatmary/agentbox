# Progress

Living log of what is done. Newest first. Read this + ROADMAP.md on each fresh run.

## Step status
- [x] 1. Scaffold + CI + `container.Runtime` + fake
- [x] 2. `internal/config` + `internal/run`
- [x] 3. `internal/auth`
- [x] 4. `internal/supervisor`
- [x] 5. `internal/autorun`
- [x] 6. `internal/container` real impl + `internal/embedfs`
- [x] 7. `cmd/agentbox` subcommands
- [x] 8. README + goreleaser + example job

## Status: COMPLETE + HARDENED
All 8 roadmap items implemented, then an independent security/correctness audit was
addressed end-to-end. See HARDENING.md for the per-item checklist (all boxes checked).
`go build ./... && go vet ./... && go test ./...` is green on linux/arm64 (this VM).

## Hardening log (newest first)
- H1+H2: removed false DECISIONS redaction claim; `init` scaffolds `.gitignore` (.agentbox/).
- O1+O2: autorun honors `--max-wall` (more-restrictive budget); `stop` does a Signal(0)
  liveness check before SIGTERM; detached child removes its pidfile; autorun clears stale
  `.stop` markers on startup.
- S1+S2+H3: secrets routed through a sourced 0600 env file (never argv); cred blob staged
  outside the control dir and removed after teardown; `executeRun` takes an injected
  Runtime+resolver (wiring test); control dir 0700.
- C2+C3+O3: supervisor inspects claude exit code (backoff + distinct StatusClaudeError);
  control reads distinguish "absent" (cat exit 1) from VM-unreachable; per-exec deadline
  from remaining wall budget interrupts a hung claude.
- C4: explicit uid/gid 0 honored (nil = unset sentinel via *int).
- S3+S4: extra_packages validated against a strict regex; repo URLs reject '-'-leading /
  ext:: and use a `--` separator on clone/ls-remote.
- C1: ImageExists uses `container image inspect`; exit 1 = absent, other non-zero surfaced.

## Log (newest first)
- Step 8: full README (install/usage/macOS-only caveat/Docker-backend note),
  `.goreleaser.yaml` (darwin+linux, amd64+arm64, Homebrew tap stanza), and the
  `examples/go2110` job (agentbox.toml + task.md).
- Step 7: cmd/agentbox Cobra wiring — init, doctor, build, run, autorun, status, logs,
  stop; --detach via self re-exec; shared executeRun; injectable doctor prober.
- Step 6: real container CLIRuntime (injectable command runner, tested) + embedfs
  templates (Dockerfile/task/config).
- Step 5: internal/autorun continuous loop (progress via remote HEAD, no-progress/
  max-runs/stop guards, interruptible cooldown).
- Step 4: internal/supervisor pure-Go resume loop + one-time Setup; thorough table tests.
- Step 3: internal/auth credential resolution; darwin keychain + stub.
- Step 2: internal/config (TOML + validation + overrides) and internal/run (layout +
  logging + STATUS/STOP protocol).
- Step 1: module init, docs, CI workflow, container.Runtime interface + fake.
