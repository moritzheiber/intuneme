# intuneme

Go CLI tool that provisions and manages a systemd-nspawn container running Microsoft Intune on an immutable Linux host. Supports two authentication stacks: **Intune** (Microsoft Identity Broker + Intune Portal) and **Himmelblau** (open-source Entra ID auth via [himmelblau-idm/himmelblau](https://github.com/himmelblau-idm/himmelblau)).

## Architecture

The repository has two main components:

- **Go CLI** (`cmd/`, `internal/`) — Responsible for container lifecycle: init, start, stop, destroy, shell. Handles host-specific setup (user creation, hostname, polkit rules) that varies per machine.
- **Container images** (`ubuntu-intune/`, `ubuntu-himmelblau/`) — Containerfile + build scripts + system files that define the container contents. Packages, systemd unit overrides, PAM config, static config files, and the Edge wrapper all live here. The auth stack determines which image is used.

**Rule of thumb:** The Go CLI starts and stops things. The container image defines what's inside the container. If something is static and doesn't depend on the host (packages, service overrides, config files), it belongs in the container image directory. If it depends on the host user/UID/hostname, it stays in `internal/provision/`.

## Auth Stack Selection

The `--auth-stack` flag on `intuneme init` selects the authentication stack:

- `intune` (default) — Uses `ubuntu-intune` image with Microsoft Identity Broker, Intune Portal, and Edge.
- `himmelblau` — Uses `ubuntu-himmelblau` image with Himmelblau daemons (`himmelblaud`, `himmelblaud-tasks`) and `himmelblau-broker` for D-Bus SSO. Edge is still available. Requires `--himmelblau-email` for user mapping.

The auth stack is stored in `config.toml` as `auth_stack` and drives image selection, profile script, TPM bind-mounts, and enrollment state backup/restore paths.

## Executing commands inside the container

`nspawn.Exec()` uses `sudo nsenter` to enter the container's namespaces and run a command as the user via `su`. This is the only reliable approach for launching GUI apps (Edge, Portal) non-interactively:

- **nsenter + nohup &** — enters namespaces, runs in a login bash (gets correct PATH for wrappers at `/usr/local/bin/`), backgrounds the process. The orphaned process is reparented to PID 1 inside the container. Proven and reliable.
- **machinectl shell** — uses polkit (good), but puts processes in a session scope that systemd cleans up when the shell exits (kills GUI apps).
- **systemd-run --machine** — requires root (direct bus transport), bypasses polkit. Permission denied as a normal user.
- **machinectl shell + systemd-run --user** — the transient user service has a minimal PATH that skips `/usr/local/bin/` wrappers, and the sanitized environment breaks X11 auth.

A sudoers rule at `/etc/sudoers.d/intuneme-exec` (installed by `intuneme init`, persists across start/stop, removed by `intuneme destroy`) makes the nsenter command passwordless, so the GNOME extension can launch apps without a terminal for sudo prompts. The `start` command reinstalls it idempotently if missing (handles upgrades from older versions).

## Before committing

Always run `make fmt` and `make lint` before committing. Fix any lint errors before creating commits.

## Documentation

Update `README.md` when adding new commands, flags, or changing existing functionality. The README contains a commands table and feature sections that must stay in sync with the code.

**update documentation** After any change to source code, update relevant documentation in CLAUDE.md, README.md and the yeti/ folder. A task is not complete without reviewing and updating relevant documentation.

**yeti/ directory** The `yeti/` directory contains documentation written for AI consumption and context enhancement, not primarily for humans. Jobs like `doc-maintainer` and `issue-worker` instruct the AI to read `yeti/OVERVIEW.md` and related files for codebase context before performing tasks. Write content in this directory to be maximally useful to an AI agent understanding the codebase — detailed architecture, patterns, and decision rationale rather than user-facing guides.
