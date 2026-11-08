# upd

[![ci](https://github.com/wipash/upd/actions/workflows/ci.yml/badge.svg)](https://github.com/wipash/upd/actions/workflows/ci.yml)

upd (`UP-dee`) updates all* your stuff pretty quick.

*as long as you're running Linux or MacOS. Windows support coming soon.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/wipash/upd/main/install.sh | sh
```

This installs `upd` to `~/.local/bin/upd`. You can override that with `UPD_INSTALL_DIR`.

## Use

```
upd                 run all user-space updates
upd -a              also run system jobs (apt, dnf, rpm-ostree, ...)
upd brew claude     run only these jobs
upd --dry-run       list every job
upd -v              print each job's output
upd --plain         print one line per event instead of the pretty dashboard
upd --min-release-age 7d
                    change the minimum release age for this run (defaults to 2d, set to 0d to turn it off)
upd init            write a starter config file
upd config          print the current config
```

## Built-in jobs

`upd` includes a bunch of built-in update jobs for common package managers and tools. It tries to be smart about how/when to update tools, e.g. not running a self-updating tool if it was installed by a package manager. It also checks whether a tool or package manager is installed/relevant before trying to update it.

Current built-in jobs:

- System package managers, which run only with `-a`: apt, dnf, apk, pacman, zypper, snap, rpm-ostree (Fedora Atomic desktops such as Bazzite).
- User-space package managers: brew, flatpak, uv, pipx, rustup, mise, gh extensions, volta, and upd itself.
- Self-updating tools: claude, codex, copilot, omp, pi, herdr, agy, opencode, cursor-agent, amp, goose, deno, bun, pnpm, chezmoi, rclone, yt-dlp, gcloud, az, flyctl, ghcup, juliaup, sdkman and aws.

You can run `upd --dry-run` shows which jobs apply to this machine and which don't.

## Release age

By default, upd asks tools to skip releases published less than 2 days ago. Only some tools/package managers support this though.

Set `min_release_age` in the config to change the default, or `"0d"` to turn it off.

## Configuration

`upd` config is in `~/.config/upd/config.toml`, or `$XDG_CONFIG_HOME/upd/config.toml` if you have that set. The config file is optional. `upd init` makes you a starter file with comments.

```toml
disable = ["snap"]                             # turn off some built-in jobs
min_release_age = "7d"                         # skip releases younger than 7 days
post = ["notify-send upd \"exit $UPD_EXIT\""]  # run once after all jobs have finished

[commands]
hermes = "hermes update --yes"                 # add your own tools to update

[commands.apt]                                 # change one field of a built-in job
system = false                                 # run apt without -a

[commands.herdr-restart]                       # reload herdr when brew upgrades it
after = ["brew"]
version = "herdr --version"
on_change = "herdr server reload-config"
```

### Job config fields

| field | meaning |
|---|---|
| `run` | The command, or a list of commands to run in order. upd stops the job as soon as one fails. |
| `after` | Jobs to wait for. This job runs after they finish, whether they succeed or fail. |
| `system` | Run the job only when upd is invoked with `-a` |
| `sudo` | Run the job with sudo. |
| `when` | Conditions that decide whether the job applies to this machine (see below). |
| `skip_if` | A command that upd runs when the job starts. If it exits with 0, upd skips the job and shows the command's output as the reason. |
| `version` | A command that prints the installed version. upd runs it before and after the job and shows `old → new`. |
| `on_change` | A command to run after the job when the output of `version` changed. |
| `env`, `cwd`, `shell` | Environment variables, the working directory (default `~`), and the shell (default `sh -c`). |
| `timeout` | Defaults to `15m`, and to no timeout for system jobs. `"0"` turns it off. |
| `ok_exit_codes` | Exit codes that count as success. Defaults to `[0]`. |
| `release_age` | Environment variables that pass the minimum release age to the tool, such as `{ UV_EXCLUDE_NEWER = "{days} days" }`. `{days}` and `{minutes}` are replaced with the age. upd sets them only when `min_release_age` is more than 0. |

A command can differ by OS, as in `run = { unix = "...", windows = "..." }`. The keys are `unix`, `linux`, `darwin` and `windows`.

### `when`

| condition | the job applies when |
|---|---|
| `has = "prog"` | `prog` is on PATH. It defaults to the first word of `run`. |
| `managed = false` | A package manager did not install `prog`. |
| `path`, `path_not` | The real path of `prog` matches, or does not match, one of these globs. |
| `os` | The OS (`linux`, `darwin` or `windows`) is in this list. |
| `distro` | `ID` or `ID_LIKE` in `/etc/os-release` includes a name in this list. |
| `wsl = true` | upd is running inside WSL. `false` means it is not. |
| `ssh = true` | upd is running in an SSH session. `false` means it is not. |
| `exists`, `missing` | This path exists, or does not exist. |
