<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.svg">
  <source media="(prefers-color-scheme: light)" srcset="assets/logo-light.svg">
  <img alt="upd" src="assets/logo-light.svg" width="240">
</picture>

**`upd`** (*UP-dee*) updates all\* your stuff pretty quick.

[![ci](https://github.com/wipash/upd/actions/workflows/ci.yml/badge.svg)](https://github.com/wipash/upd/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/wipash/upd)](https://github.com/wipash/upd/releases/latest)

<sub>*As long as you're running Linux or macOS. Windows support coming soon.</sub>

</div>

---

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/wipash/upd/main/install.sh | sh
```

Installs `upd` to `~/.local/bin/upd`. Set `UPD_INSTALL_DIR` to put it somewhere else.

## Usage

```sh
upd                       # run all user-space updates
upd -a                    # also run system jobs (apt, dnf, rpm-ostree, ...)
upd brew claude           # run only these jobs
upd --dry-run             # list every job and whether it applies
upd -v                    # print each job's output
upd --plain               # print one line per event instead of the live dashboard
upd --min-release-age 7d  # override the minimum release age (0d turns it off)
upd init                  # write a starter config file
upd config                # print the active config
```

## Built-in jobs

`upd` ships with built-in jobs for common package managers and CLIs, and runs them in parallel. It checks whether each tool is installed before running it, and skips self-updaters that are managed by a package manager (like Homebrew, Nix, mise, or Volta).

| Group | Built-in jobs |
|---|---|
| **System** (`-a` only) | `apt`, `dnf`, `apk`, `pacman`, `zypper`, `snap`, `rpm-ostree` (Fedora Atomic desktops such as Bazzite) |
| **User package managers** | `brew`, `flatpak`, `uv`, `pipx`, `rustup`, `mise`, `gh-extensions`, `volta`, `upd` |
| **Self-updating CLIs** | `claude`, `codex`, `copilot`, `omp`, `pi`, `herdr`, `agy`, `opencode`, `cursor-agent`, `amp`, `goose`, `deno`, `bun`, `pnpm`, `chezmoi`, `rclone`, `yt-dlp`, `gcloud`, `az`, `flyctl`, `ghcup`, `juliaup`, `sdkman`, `aws` |

Run `upd --dry-run` to see which jobs apply to this machine and which ones are hidden.

## Release age

By default, `upd` asks tools to skip releases published less than 2 days ago (for tools and package managers that support it).

Set `min_release_age` in your config to change the default, or `"0d"` to turn it off.

## Configuration

Config lives at `~/.config/upd/config.toml` (or `$XDG_CONFIG_HOME/upd/config.toml`), and is completely optional. Run `upd init` to generate a commented starter file.

```toml
disable = ["snap"]                             # turn off some built-in jobs
min_release_age = "7d"                         # skip releases younger than 7 days
post = ["notify-send upd \"exit $UPD_EXIT\""]  # run once after all jobs have finished

[commands]
hermes = "hermes update --yes"                 # add your own tools to update

[commands.apt]                                 # override one field of a built-in job
system = false                                 # run apt without -a

[commands.herdr-restart]                       # reload herdr when brew upgrades it
after = ["brew"]
version = "herdr --version"
on_change = "herdr server reload-config"
```

### Job config fields

| Field | Meaning |
|---|---|
| `run` | Command, or list of commands to run in order. `upd` stops the job as soon as one fails. |
| `after` | Jobs to wait for. This job runs after they finish, whether they succeed or fail. |
| `system` | Run the job only when `upd` is invoked with `-a`. |
| `sudo` | Run the job with `sudo`. |
| `when` | Conditions that decide whether the job applies to this machine (see below). |
| `skip_if` | Command run when the job starts. If it exits 0, `upd` skips the job and shows its output as the reason. |
| `version` | Command that prints the installed version. `upd` compares before and after and shows `old → new`. |
| `on_change` | Command to run after the job when `version` output changed. |
| `env`, `cwd`, `shell` | Environment variables, working directory (default `~`), and shell (default `sh -c`). |
| `timeout` | Defaults to `15m`, and no timeout for system jobs. `"0"` turns it off. |
| `ok_exit_codes` | Exit codes that count as success. Defaults to `[0]`. |
| `release_age` | Env vars that pass the minimum release age, such as `{ UV_EXCLUDE_NEWER = "{days} days" }`. `{days}` and `{minutes}` are replaced with the age when `min_release_age` is greater than 0. |

Any command field can vary by OS, eg `run = { unix = "...", windows = "..." }`. Supported keys are `unix`, `linux`, `darwin`, and `windows`.

### `when` conditions

| Condition | Applies when |
|---|---|
| `has = "prog"` | `prog` is on `PATH`. Defaults to the first word of `run`. |
| `managed = false` | A package manager did not install `prog`. |
| `path`, `path_not` | Real path of `prog` matches (or does not match) one of these globs. |
| `os` | OS (`linux`, `darwin`, or `windows`) is in this list. |
| `distro` | `ID` or `ID_LIKE` in `/etc/os-release` includes a name in this list. |
| `wsl = true` | `upd` is running inside WSL (`false` means it is not). |
| `ssh = true` | `upd` is running in an SSH session (`false` means it is not). |
| `exists`, `missing` | Path exists (or does not exist). |
