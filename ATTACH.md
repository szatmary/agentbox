# Attach & Observe — design + progress

The "attach + observe" layer lets external consumers exec into and monitor a **live
run's VM**: humans via VSCode (SSH) and AI agents via MCP. Everything rides on
`container exec` — the only reliable transport on this Apple `container` setup.

Read this + PROGRESS.md on every fresh run.

## Grounding (do NOT relitigate)
- `container exec` is the ONLY transport. No `--publish`, no host→VM TCP, no listening on
  the VM IP. SSH rides on `exec` via a ProxyCommand (`container exec -i <vm> sshd -i`).
- A run's container is started with `--name <run>` where `<run>` == the run-dir base name
  (`<job>-<id>`). So **run name → live container is deterministic**: the container name *is*
  the run-dir base; we only confirm it is actually running (via `Inspect`).

## Roadmap status
- [x] 1. `internal/observe` + `Runtime.Inspect` + fakes; table tests.
- [x] 2. `agentbox shell <run>` (exec -it via `Runtime.ExecStream`) + status integration.
- [x] 3. `internal/attach`: ed25519 keygen, authorized_keys install, `ssh-proxy`, `ssh`,
       `attach --vscode`.
- [x] 4. Image: `openssh-server`; `[attach] ssh` flag; wire key install into the run path.
- [x] 5. `internal/mcp` server + `agentbox mcp` (stdio + `--http`).
- [x] 6. README "Attach & monitor" section; trust model; goreleaser/help.

## Interface additions (the single seam stays the only seam)
- `container.Container{ID,Name,Image,Running}` + `Runtime.Inspect(ctx,id)` — resolve a run
  name to a live container and report liveness. CLIRuntime shells `container inspect <id>`
  (JSON, tolerant parse — version-sensitive, isolated like the subcommand constants, D10).
- `Runtime.ExecStream(ctx,id,StreamOptions)` — interactive/attached exec with stdin + an
  optional TTY, for `shell` and the `ssh-proxy` stdio tunnel. Buffered `Exec` is unchanged.

## Packages
- `internal/observe` — read/exec layer over one live run. Holds the host run dir (for
  STATUS + logs/run.log) and the Runtime (for in-VM `ls`/`cat`/`git` via exec). All methods
  resolve run→live-container first (error if not running). Pure; table-tested vs the Fake.
- `internal/attach` — ed25519 keygen into the run dir (priv 0600), authorized_keys install
  command, `~/.ssh/config` Host-block rendering + idempotent install, ProxyCommand wiring.
- `internal/mcp` — JSON-RPC 2.0 subset (initialize / tools/list / tools/call) over stdio
  and HTTP, exposing the observe layer as tools.

## SSH-over-exec specifics
- ProxyCommand: `agentbox ssh-proxy <run>` → resolves the live VM → `ExecStream` of
  `sudo /usr/sbin/sshd -i` (inetd mode) with our stdio piped through. `sudo` because sshd
  needs root for host keys + privsep; the `agent` user has NOPASSWD sudo (see DECISIONS).
- Host block (`agentbox ssh <run>`): `Host agentbox-<run>` / `User agent` /
  `IdentityFile <run>/ssh/id_ed25519` / `IdentitiesOnly yes` / `StrictHostKeyChecking no` /
  `UserKnownHostsFile /dev/null` / `ProxyCommand <abs-agentbox> ssh-proxy <run> [--runs-dir]`.
  Ephemeral per-run host keys ⇒ host-key checking is off by design (local exec tunnel only).
- Run start (gated on `[attach] ssh = true`): generate the keypair into `<run>/ssh/`, then a
  setup command creates host keys (`ssh-keygen -A`), `/run/sshd`, and appends the pubkey to
  the agent's `authorized_keys`.
