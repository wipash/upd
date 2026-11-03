# upd

Updates everything on a machine in parallel: OS packages, Homebrew, Flatpak, language toolchains, and self-updating CLIs such as Claude Code and Codex. A single static binary, with a built-in catalog of update jobs that you can override and extend from a TOML file.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/wipash/upd/main/install.sh | sh
```

This installs `~/.local/bin/upd` (set `UPD_INSTALL_DIR` to change it). After that, `upd` updates itself as one of its jobs.

## Use

```
upd                 run every job that applies to this machine
upd -a              also run system jobs (apt, dnf, rpm-ostree, ...), asking for sudo once
upd brew claude     run only these jobs (even system or disabled ones)
upd --dry-run       list every job, whether it would run, and why
upd -v              print every job's output, prefixed with the job name
upd --plain         line-by-line output instead of the live status board
upd init            write a starter config
upd config          print the merged config, marking where each field came from
upd config path     print the config file path
```

Exit codes: 0 success, 1 a job (or `on_change`, or `post`) failed, 2 config or usage error, 3 another upd run is in progress, 130 interrupted.

Logs for each job are kept in `~/.local/state/upd/logs/<run>/` for the last 10 runs.

## Configuration

`$XDG_CONFIG_HOME/upd/config.toml`, by default `~/.config/upd/config.toml` (`%AppData%\upd\config.toml` on Windows). The file is optional.

```toml
disable = ["snap"]                              # built-in jobs to turn off
post = ["notify-send upd \"exit $UPD_EXIT\""]   # run after every job has finished

[commands]
no-mistakes = "no-mistakes update --force"      # a job can be just a command
hermes = { run = "hermes update --yes", env = { GIT_SSH_COMMAND = "ssh -o BatchMode=yes" } }

[commands.apt]                                  # a built-in's name: override only these fields
system = false

[commands.herdr-restart]                        # no run: react to another job's changes
after = ["brew"]
version = "herdr --version"
on_change = "herdr server reload-config"
```

### Job fields

| field | meaning |
|---|---|
| `run` | Command, or list of commands run in order (stops at the first failure). |
| `after` | Jobs this one waits for. Ordering only: it runs whatever their result. |
| `system` | Only run with `-a`, or when named. |
| `sudo` | Run with sudo. upd asks for the password once, before anything starts. |
| `when` | Whether the job applies to this machine (see below). If not, it is hidden. |
| `skip_if` | Checked when the job starts; exit 0 skips it, and its output is the reason. |
| `version` | Prints the installed version. upd compares it before and after to report `old → new`. |
| `on_change` | Runs after the job when `version` changed. |
| `env`, `cwd`, `shell` | Environment, working directory (default `~`), shell (default `sh -c`, `pwsh` on Windows). |
| `timeout` | Default `15m`, none for system jobs. `"0"` means none. |
| `ok_exit_codes` | Exit codes that count as success. Default `[0]`. |

Any command can differ per OS: `run = { unix = "...", windows = "..." }` (keys `unix`, `linux`, `darwin`, `windows`).

### `when`

| predicate | true when |
|---|---|
| `has = "prog"` | `prog` is on PATH. Defaults to the first word of `run`. |
| `managed = false` | `prog` was not installed by a package manager: its real path (symlinks followed) does not match any of the top-level `managed_paths`. |
| `path`, `path_not` | Its real path matches / does not match one of these globs (`*` matches across `/`). |
| `os`, `distro` | The OS is one of these; `/etc/os-release` `ID` or `ID_LIKE` contains one of these. |
| `wsl`, `ssh` | Running inside WSL / over SSH. |
| `exists`, `missing` | A path exists / does not exist. |

### Overriding built-ins

Your config is merged over the built-in catalog by job name, field by field. A field you do not write keeps the built-in value; writing a job as a plain string replaces only its `run`. Lists are replaced whole, `env` merges key by key, and `""` or `[]` clears a field. See the catalog in [`internal/config/builtin.toml`](internal/config/builtin.toml) or run `upd config`.

## Development

```sh
go test ./...
go run ./cmd/upd --dry-run
```

End-to-end tests in `cmd/upd/testdata/script` run the real binary against fake package managers. Releases are built by goreleaser when a `v*` tag is pushed.
