# upd

[![ci](https://github.com/wipash/upd/actions/workflows/ci.yml/badge.svg)](https://github.com/wipash/upd/actions/workflows/ci.yml)

upd updates the software on a machine, running the updates in parallel. It covers OS packages, Homebrew, Flatpak, language toolchains, and command-line tools that update themselves, such as Claude Code and Codex. It is a single binary with a built-in list of update jobs, and you can add jobs or change the built-in ones in a TOML file.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/wipash/upd/main/install.sh | sh
```

This installs `~/.local/bin/upd`. Set `UPD_INSTALL_DIR` to install it somewhere else. From then on, upd updates itself along with everything else.

## Use

```
upd                 run every job that applies to this machine
upd -a              also run system jobs (apt, dnf, rpm-ostree, ...), asking for the sudo password once
upd brew claude     run only these jobs, including system and disabled ones
upd --dry-run       list every job, whether it would run, and why
upd -v              print each job's output as it runs
upd --plain         print one line per event instead of the live status board
upd --min-release-age 7d
                    change the minimum release age for this run (0d turns it off)
upd init            write a starter config file
upd config          print the config, including the built-in jobs
```

## Built-in jobs

A job runs only when its tool is installed. If a package manager such as Homebrew or apt installed a tool that can update itself, upd leaves that tool to the package manager.

- System package managers, which run only with `-a`: apt, dnf, apk, pacman, zypper, snap, rpm-ostree (Fedora Atomic desktops such as Bazzite), and the AWS CLI when it is installed for all users.
- Package managers: brew, flatpak, uv, pipx, rustup, mise, gh extensions, volta, and upd itself.
- Tools that update themselves: claude, codex, copilot, omp, pi, herdr, agy, opencode, cursor-agent, amp, goose, deno, bun, pnpm, chezmoi, rclone, yt-dlp, gcloud, az, flyctl, ghcup, juliaup, sdkman and aws.

`upd --dry-run` shows which jobs apply to this machine and why the others do not. `upd config` shows each job's exact name and commands.

## Release age

By default, upd asks tools to skip releases published less than 2 days ago, so a broken or malicious release has time to be noticed and pulled first. Only some tools support this: uv, pipx, mise, pnpm, packages installed with volta, and upd itself. The others, including brew, flatpak, the OS package managers and claude, always install the latest release.

Set `min_release_age` in the config to change the default, or `"0d"` to turn it off. If you already set a tool's own variable in your environment, such as `UV_EXCLUDE_NEWER`, upd leaves it alone.

## Configuration

upd reads `~/.config/upd/config.toml`, or `$XDG_CONFIG_HOME/upd/config.toml` when that variable is set. The file is optional. `upd init` writes a starter file with comments.

```toml
disable = ["snap"]                             # turn off built-in jobs
min_release_age = "7d"                         # skip releases younger than 7 days
post = ["notify-send upd \"exit $UPD_EXIT\""]  # run after every job has finished

[commands]
hermes = "hermes update --yes"                 # add a job

[commands.apt]                                 # change one field of a built-in job
system = false                                 # run apt without -a

[commands.herdr-restart]                       # reload herdr when brew upgrades it
after = ["brew"]
version = "herdr --version"
on_change = "herdr server reload-config"
```

When you use a built-in job's name, upd changes only the fields you write. Set a field to `""` or `[]` to clear it. `upd config` prints the merged result.

### Job fields

| field | meaning |
|---|---|
| `run` | The command, or a list of commands to run in order. upd stops at the first one that fails. |
| `after` | Jobs to wait for. This job runs after they finish, whether they succeed or fail. |
| `system` | Run the job only with `-a`, or when it is named on the command line. |
| `sudo` | Run the job with sudo. upd asks for the password once, before any job starts. |
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
