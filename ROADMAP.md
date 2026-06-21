# agentbox roadmap

`agentbox` is a single Go CLI that runs autonomous, fully-sandboxed Claude coding
agents in Apple `container` microVMs. It generalizes a bash harness (run a bounded
agent session, resume it until done, auto-relaunch) into one tested binary.

**Build constraint:** macOS-only *runtime*, but the whole module must `go build`,
`go vet`, and `go test` on Linux. Every OS/external-tool dependency sits behind a Go
interface and is unit-tested against fakes. Keychain code is `//go:build darwin` with a
non-darwin stub.

## Steps (each step: implement, make green, commit + push)

1. **Scaffold** — `go.mod` (module `github.com/szatmary/agentbox`, go 1.26), README
   skeleton, LICENSE (MIT), `.github/workflows/ci.yml`, ROADMAP/PROGRESS/DECISIONS,
   `container.Runtime` interface + fake.
2. **`internal/config`** (TOML job file + flag overrides + validation) and
   **`internal/run`** (run-dir layout + structured logging).
3. **`internal/auth`** — interfaces, source selection, darwin keychain + stub, tests.
4. **`internal/supervisor`** — pure-Go resume loop driving one long-lived VM via
   `container exec`; sentinel + guard checks; table tests vs the fake Runtime.
5. **`internal/autorun`** — continuous relaunch loop; progress via remote git HEAD;
   stop on DONE/FAILED / N no-progress / stop signal; cooldown; tests.
6. **`internal/container`** real impl shelling to `container`; **`internal/embedfs`**
   embedded Dockerfile + task template.
7. **`cmd/agentbox`** — Cobra wiring: init, doctor, build, run, autorun, status, logs,
   stop.
8. **README** (install, usage, macOS-only caveat, Docker-backend-later note),
   `goreleaser` config, example `agentbox.toml` reproducing the go2110 job.

## Status
See PROGRESS.md for per-step status and per-commit detail.
