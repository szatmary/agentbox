# Hardening checklist

Independent audit (6 reviewers + live run) found defects in security, failure paths,
and CLI-vs-reality gaps. Each item below is fixed with a **biting regression test**
(fails on old behavior, passes on new). `go build ./... && go vet ./... && go test ./...`
must stay green on Linux. External tools stay behind interfaces tested with fakes; no
real container/gh/keychain/git is invoked in tests.

Read this + PROGRESS.md first on each fresh VM.

## Security — Critical
- [x] **S1** Secrets out of `container run`/`exec` argv — routed through a 0600 env file
      mounted into the VM and sourced before each command.
- [x] **S2** Keychain cred blob staged OUTSIDE the mounted control dir and removed after
      teardown (defer).
- [x] **S3** `extra_packages` validated against `^[A-Za-z0-9][A-Za-z0-9.+_-]*$`; metachars/
      newlines rejected.
- [x] **S4** Repo-URL `--` separator on clone/ls-remote + reject `-`-leading and `ext::`.

## Correctness — Critical/Major
- [x] **C1** `ImageExists` uses `container image inspect`; exit 1 = absent, other non-zero
      = surfaced error.
- [x] **C2** Supervisor inspects claude exit code: logs it, backs off, surfaces persistent
      non-zero/no-progress as a distinct error status (not a benign guard trip).
- [x] **C3** Control read distinguishes "file not found" (cat exit 1) from exec failure
      (VM gone) and aborts on the latter.
- [x] **C4** uid 0 passes through (real "unset" sentinel via `*int`); root bind-mount
      ownership preserved.

## Operational — Major
- [x] **O1** autorun honors `--max-wall` (more-restrictive of guards.max_wall and
      autorun.per_run_wall).
- [x] **O2** Detach/stop lifecycle: `Signal(0)` liveness check before signalling; pidfile
      removed on exit + stale pidfile cleaned; autorun consumes/clears `.stop` on startup.
- [x] **O3** Per-exec deadline derived from remaining wall budget, plumbed via ctx so a
      hung claude is interrupted.

## Hygiene — Minor
- [x] **H1** Removed the false DECISIONS "loggers redact known secret keys" claim
      (doc-vs-code lie).
- [x] **H2** `init` scaffolds `.gitignore` containing `.agentbox/`.
- [x] **H3** `executeRun` accepts an injected `Runtime` + resolver (wiring test); control
      dir tightened to 0700; dead branch removed from `config/validate.go`.

## Status: COMPLETE
All items fixed with biting tests; suite green on linux/arm64.
