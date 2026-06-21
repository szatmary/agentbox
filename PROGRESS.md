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

## Status: COMPLETE
All 8 roadmap items implemented. `go build ./... && go vet ./... && go test ./...` is
green on linux/arm64 (this VM) and cross-compiles for darwin & linux on amd64+arm64.
Every subcommand exists; the README is complete.

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
