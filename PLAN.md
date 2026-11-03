# upd: Go rewrite plan

`upd` replaces `~/bin/upd`, a 55-line bash script that runs a fixed list of update commands one after another. The Go version is a single static binary that runs update jobs in parallel, ships a catalog of built-in jobs for common package managers and self-updating CLIs, and lets the user override or extend that catalog from a TOML file in the XDG config directory.

- Module: `github.com/wipash/upd`, public repo, code in `~/dev/upd`.
- Go 1.27, `CGO_ENABLED=0`.
- Milestone 1: linux amd64/arm64, darwin arm64. Milestone 2: native Windows.

## 1. Principles

- Built-in jobs are data, not code. They live in an embedded TOML file that uses exactly the schema available to the user. The Go code contains no logic specific to any tool: it detects the machine, merges config, builds the job graph, runs processes, and renders output.
- A tool is built in when updating it takes knowledge (flags, environment variables, sudo, detection, ordering against other jobs). Self-updating CLIs are also built in, guarded so they stay hidden when a package manager installed them.
- Everything that can run in parallel does. Ordering exists only where two jobs really conflict.
- Failures are loud. A job that fails, times out, or blocks on a prompt is shown in red with its full output, and the run exits non-zero.

## 2. Config

### Location

1. `$XDG_CONFIG_HOME/upd/config.toml` if `XDG_CONFIG_HOME` is set and absolute.
2. Otherwise `~/.config/upd/config.toml` on Unix (macOS included), `%AppData%\upd\config.toml` on Windows.

upd does not probe a second location. The file is optional; with no file, upd runs every detected built-in. `upd config path` prints the path in use.

State (logs, the single-instance lock) goes in `$XDG_STATE_HOME/upd/`, default `~/.local/state/upd/` (`%LocalAppData%\upd\` on Windows).

### Format

TOML. Parsing uses go-toml v2's `unstable.Parser` to build a tree that keeps the line and column of every key and value, then decodes the schema by hand (spike: about 320 lines). A plain `toml.Unmarshal` into `any` runs first to catch syntax errors, duplicate keys and table redefinition. Every error carries a position, and unknown keys get a suggestion:

```
config.toml:6:1: commands.apt.sytem: unknown key (did you mean "system"?)
config.toml:6:9: commands.apt.after: expected a list of strings, got a string
```

Every field decodes into a pointer, so the merge can tell "the user set `system = false`" apart from "the user did not mention `system`".

### Shape

```toml
disable = ["snap"]                       # turn off built-ins by name
max_parallel = 0                         # 0 = unlimited (default)
post = ["notify-send \"upd: $UPD_EXIT\""]

[commands]
no-mistakes = "no-mistakes update --force"
hermes = { run = "hermes update --yes", env = { GIT_SSH_COMMAND = "ssh -o BatchMode=yes" } }

[commands.apt]                           # same name as a built-in: override its fields
system = false

[commands.herdr-restart]                 # watcher job: no run
after     = ["brew"]
version   = "herdr --version"
on_change = "herdr server reload-config"
```

A `[commands]` value is either a string (shorthand for `run`) or a table.

### Job fields

| field | type | meaning |
|---|---|---|
| `run` | cmd or list of cmd | Commands to run. A list runs in order and stops at the first failure. |
| `shell` | string | Default `sh -c` on Unix, pwsh (fallback powershell.exe) on Windows. |
| `cwd` | path | Working directory. Default: the user's home directory. |
| `env` | map | Extra environment variables. |
| `after` | list of names | Ordering only: this job starts after the named jobs finish, whatever their result. |
| `system` | bool | Runs only with `-a/--all` (or when named explicitly). |
| `sudo` | bool | Runs elevated (see section 4). |
| `when` | table | Static applicability. False means the job is hidden on this machine. |
| `skip_if` | cmd | Run at job start. Exit 0 means the job is shown as skipped, with the command's stdout as the reason. |
| `version` | cmd | Identifies installed state. Drives "updated" detection and `on_change`. |
| `on_change` | cmd | Runs after the job when the `version` output changed. |
| `timeout` | duration | Default 15m for non-system jobs, none for system jobs. |
| `ok_exit_codes` | list of int | Exit codes that count as success (needed for winget). Default `[0]`. |

A "cmd" is a string or a table keyed by OS: `{ unix = "...", linux = "...", darwin = "...", windows = "..." }`. The most specific key wins (`linux` over `unix`). A job with no `run` and no `version` command for the current OS is hidden.

### `when` predicates

All listed predicates must hold.

| predicate | meaning |
|---|---|
| `has = "bin"` | `bin` is on PATH. |
| `managed = bool` | Whether the real path of the `has` binary (symlinks resolved) matches one of the top-level `managed_paths` globs, the locations where package managers install programs (`*/Cellar/*`, `*/Caskroom/*`, `/usr/bin/*`, `/nix/*`, `*/.volta/*`, `*/WinGet/Packages/*`, ...). Self-updaters use `managed = false`. The list is defined once in the catalog and a user can replace it. |
| `path = [globs]` | The real path of the `has` binary matches at least one glob. |
| `path_not = [globs]` | The real path of the `has` binary matches none of the globs. |
| `os = [...]` | `linux`, `darwin`, `windows`. |
| `distro = [...]` | Matches `/etc/os-release` `ID` or any word of `ID_LIKE`. Linux only. |
| `wsl = bool` | Running inside WSL. |
| `ssh = bool` | Running inside an SSH session (`SSH_CONNECTION` set). |
| `exists = "path"` / `missing = "path"` | Path exists / does not exist. `~` and `$VAR` are expanded. |

`has` is derived when not given: from the first word of the first `run` command for the current OS, if that word is a plain command name (no `=`, `/`, shell keyword or shell metacharacter). For a watcher job it comes from `version` the same way. Built-in jobs whose command does not start with a plain name must set `has` explicitly; a catalog test enforces this.

### Merge rules (user config over built-ins)

- Merging is by job name, field by field. A field the user did not write keeps the built-in value.
- The string shorthand replaces only `run`. `apt = "apt-get ..."` keeps apt's `sudo`, `when` and `env`.
- Lists (`run`, `after`, `ok_exit_codes`, `post`) are replaced whole.
- `env` merges key by key. `when` is replaced whole.
- `""` or `[]` clears a field, so a user can drop a built-in's `after` or `skip_if`.
- `upd config` prints the merged config and marks each field as coming from the built-in or the user file.

### Validation (exit code 2)

- Unknown keys, wrong types, bad OS keys, bad durations.
- An `after` name that is not defined anywhere in the merged config (on any OS).
- A cycle in the full merged graph. Validation uses all jobs, not just those enabled on this machine, so a config cannot be valid on one machine and broken on another.

## 3. Job selection

| invocation | runs |
|---|---|
| `upd` | Every visible, non-disabled, non-system job. |
| `upd -a` / `upd --all` | The above plus `system` jobs. |
| `upd brew claude` | Only the named jobs, plus watcher jobs (no `run`) whose `after` includes a named job. Naming a job overrides `system` and `disable`. Naming a hidden job is an error that gives the `when` predicate that failed. Naming an unknown job is an error. |
| `upd --dry-run` | Lists every job with its state: will run, hidden (reason), disabled, system-only, or skipped (it runs `skip_if` to find out). |

An `after` reference to a job that is hidden, disabled or not selected is treated as satisfied.

## 4. Execution

### Scheduling

- Jobs form a graph from `after`. A job starts when everything it comes after has finished. With `max_parallel = 0` every ready job starts immediately.
- A failed job does not stop other jobs, including the ones that come after it.
- Exit codes: 0 all good, 1 one or more jobs (or `on_change`, or `post`) failed, 2 config or usage error, 3 another upd run holds the lock.

### Single instance

upd takes an exclusive `flock` on `$XDG_STATE_HOME/upd/lock` before doing anything else and exits 3 with `already running (pid N)` if another run holds it. Log pruning happens only while holding the lock.

### Version snapshots and `on_change`

1. At startup every selected job's `version` command runs in parallel (10s timeout each, run as the user, never under sudo).
2. A job may not start until its own snapshot and the snapshots of every job that comes after it (transitively) have completed. This stops brew from upgrading herdr before the `herdr-restart` watcher has recorded the old version.
3. After the job's `run` finishes successfully, its `version` runs again. Comparison uses trimmed stdout.
4. Different output: status "updated", the summary shows `old → new` (first line of each), and `on_change` runs with the job's `env`, `cwd`, `sudo` and timeout. If `on_change` fails, the status is "updated, on_change failed" and the run exits 1.
5. A failed or timed-out snapshot makes the version unknown: the summary shows `?` and `on_change` does not run.
6. A job without `version` reports "done" on success.

A watcher job (no `run`) waits for its `after` jobs, re-runs `version`, and runs `on_change` if it changed. It is listed only when it does something.

### Processes (Unix)

- stdin is `/dev/null`. stdout and stderr go to one pipe, read by the job's output writer.
- Non-sudo jobs run with `Setsid`: they have no controlling terminal, so an unexpected git, ssh or password prompt fails at once instead of drawing over the board.
- Sudo jobs run with `Setpgid` and keep the controlling terminal, because sudo looks up its cached credential by terminal and session (confirmed in the source of both sudo and sudo-rs).
- Default environment addition when the user has not set it: `GIT_TERMINAL_PROMPT=0`. `GIT_SSH_COMMAND` is left alone, because it would override the user's `core.sshCommand`, and ssh cannot prompt without a terminal anyway.
- A job's stdout and stderr go straight to its log file, not through a pipe. upd follows the file to show the last line. A job is done when its direct child exits, even when a background process it started (for example `herdr update --handoff` starting a new server) keeps writing to the log, and that process is not killed by SIGPIPE when upd exits.
- Timeout kills the process group (`kill(-pgid, SIGKILL)`); the spike confirmed grandchildren die.
- A watchdog polls `/proc/<pid>/stat` for processes in each sudo job's group. A process stopped by `SIGTTIN`/`SIGTTOU` (it tried to read the terminal from a background group) makes the job fail as "blocked on terminal input".

### Elevation (Unix)

- If any selected job has `sudo = true`, upd runs `sudo -v` in the foreground before the board starts, then refreshes with `sudo -n -v` every 60 seconds.
- Sudo jobs run as `sudo -n env K=V... <shell> -c '<cmd>'` (sudo strips the environment, so variables travel on the command line).
- If `sudo -v` fails or is declined, or stdin is not a terminal, sudo jobs show "skipped: no sudo" and the rest of the run continues.
- upd cannot kill root-owned processes. On Ctrl-C or timeout, sudo jobs get SIGTERM through sudo and are never escalated to SIGKILL.

Elevation sits behind an interface so Windows can plug in its own implementation (section 9).

### Ctrl-C

First Ctrl-C: jobs that were running or had not started are reported as "not run", and no new process starts. Each running step forwards SIGINT to every job's process group, wait up to 5 seconds, SIGKILL the non-sudo groups that remain, and wait for sudo jobs. Second Ctrl-C: stop waiting, warn about any sudo job still running, and exit 130. `post` does not run after Ctrl-C.

### `post`

Runs once after every job has finished, unless the run was interrupted or is a dry run. Environment: `UPD_UPDATED` and `UPD_FAILED` (space-separated job names) and `UPD_EXIT` (the exit code so far). A failing `post` command makes the run exit 1.

### Logs

Every job's raw output goes to `$XDG_STATE_HOME/upd/logs/<YYYYmmdd-HHMMSS>-<pid>/jobs/<job>.log`, and the `post` commands' output to `post.log` in the run directory. The last 10 run directories are kept. The summary prints the directory path.

## 5. Output

### TTY: status board

Hand-written with `golang.org/x/term`. No TUI framework, because Bubble Tea takes over stdin and fights with the sudo prompt.

- One row per job: spinner or result mark, name, elapsed time, last output line.
- "Last output line": output split on both `\r` and `\n` (apt, npm and uv redraw progress with `\r`), ANSI and control characters stripped, truncated to terminal width minus one. A trailing partial line counts.
- Redraw about 10 times a second, and on SIGWINCH (clear and redraw).
- The board never exceeds terminal height minus one. Running and failed jobs are shown first. Finished successful jobs fold into one line (`12 done`) when space runs out.
- At the end: the last 40 lines of each failed job's log (with the log path), then a summary table with status, duration, and `old → new` or the last output line, then "N hidden" and the log directory.

### Non-TTY, `--plain`, `-v`

Plain timestamped lines: `start brew`, `ok brew 12.3s`, `updated claude 2.1.317 → 2.1.320`, `FAIL npm` followed by its log. `-v` additionally streams every job's output with a `name | ` prefix.

## 6. Built-in catalog (milestone 1)

Every command and flag below gets checked against the tool's own docs during implementation; a tool that cannot run without prompting is dropped.

### System jobs (`system = true`)

| job | run | notes |
|---|---|---|
| apt | `apt-get -o DPkg::Lock::Timeout=600 update`, then `apt-get -y -o DPkg::Lock::Timeout=600 -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold upgrade --with-new-pkgs` | sudo; `DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=l`; distro debian |
| dnf | `dnf upgrade -y --refresh` | sudo; distro fedora or mageia; `missing = "/run/ostree-booted"` (Bazzite ships `/usr/bin/dnf`) |
| apk | `apk -U upgrade --wait 600` | sudo; distro alpine |
| pacman | `pacman -Syu --noconfirm` | sudo; distro arch |
| zypper | `zypper -n dup --auto-agree-with-licenses` (Tumbleweed, Slowroll) or `zypper -n update ...` (Leap, SLES) | sudo; `ZYPP_LOCK_TIMEOUT=600`; missing `/usr/sbin/transactional-update`. Two jobs split by distro. |
| snap | `snap refresh` | sudo; exists `/run/snapd.socket` |
| rpm-ostree | `rpm-ostree upgrade` | exists `/run/ostree-booted`, `ssh = false`; `skip_if` uupd.service or uupd-manual.service active |
| rpm-ostree-ssh | same | as above but `ssh = true` and `sudo = true` |

### User jobs

| job | run | notes |
|---|---|---|
| flatpak | `flatpak update -y --noninteractive` | |
| brew | `brew update`, then `brew upgrade --greedy` | `HOMEBREW_NO_AUTO_UPDATE=1 NONINTERACTIVE=1 HOMEBREW_NO_ENV_HINTS=1`; `skip_if` uupd active (Bazzite's uupd also updates brew, and brew's lock fails instead of waiting) |
| uv | `uv self update` only when uv's install receipt points at the `uv` on PATH, then `uv tool upgrade --all` | |
| pipx | `pipx upgrade-all` | |
| rustup | `rustup update` | |
| mise | `MISE_YES=1 mise upgrade`; `mise self-update` only when not installed by a package manager | `cwd` home; after brew |
| gh-extensions | `gh extension upgrade --all` | `GH_PROMPT_DISABLED=1 GH_NO_UPDATE_NOTIFIER=1`; after brew |
| volta | `volta install node@<current default major>`, `volta install npm@latest` if npm is pinned, `volta install <pkg>@latest` for each package in `volta list --format plain` | after brew; shell snippet in the catalog |
| upd | self-update from GitHub releases | see section 8 |

### Self-updating CLIs (each with `path_not`)

claude (`claude update`), codex (`codex update`), omp (`omp update`, after brew), pi (`pi update`, after brew), herdr (`herdr update --handoff`), herdr-core (`herdr-core update`), deno (`deno upgrade`), bun (`bun upgrade`), pnpm (`pnpm self-update`), chezmoi (`chezmoi upgrade`), rclone (`rclone selfupdate`), yt-dlp (`yt-dlp -U`), opencode (`opencode upgrade`), cursor-agent (`cursor-agent update`), amp (`amp update`), goose (`goose update`), gcloud (`gcloud components update --quiet`), az (`az upgrade --yes`), flyctl (`fly version upgrade`), sdkman (`sdk selfupdate`, `sdk update`), ghcup (`ghcup upgrade`), juliaup (`juliaup self update`, `juliaup update`), copilot (`copilot update`; GitHub Copilot CLI), agy (`agy update`; Google Antigravity CLI. Its own updater only runs while `agy` is running, so idle installs fall behind), aws (`aws update`, available since AWS CLI 2.36; two jobs: `aws` for user installs, and `aws-system` with `sudo = true` and `path = ["/usr/local/aws-cli/*"]`. Skipped unless `aws --version` reports an official installer build (`exe/`, `script-exe/`, `update-exe/`). Installs older than 2.36 need one manual reinstall before `aws update` works).

The Antigravity desktop IDE is not a job: it updates itself, and brew or winget installs are covered by those jobs.

### Not in the catalog

AUR helpers (they prompt and call sudo themselves), npm/pnpm/bun global packages (every node manager keeps its own prefix), nix, plugin managers (fisher, lazy.nvim, tpm), Windows Update, Microsoft Store.

## 7. CLI

```
upd [flags] [job...]
  -a, --all        include system jobs
      --dry-run    show what would run and why, run nothing
  -v, --verbose    stream every job's output with a prefix
      --plain      plain line output even on a TTY
upd init           write a commented starter config (refuses to overwrite)
upd config         print the merged config with the source of each field
upd config path    print the config file path in use
upd version
```

## 8. Distribution and self-update

- goreleaser on tag: static binaries for linux amd64/arm64 and darwin arm64 (windows amd64/arm64 in milestone 2), `checksums.txt`, GitHub release.
- `install.sh`: downloads the right binary, verifies the checksum, installs to `~/.local/bin/upd`.
- The built-in `upd` job checks the latest GitHub release, verifies the checksum, writes the new binary next to the old one and renames it into place (atomic on Unix, rename-and-hide on Windows). It refuses to self-update when the binary lives under a package manager's prefix.
- The new binary takes effect on the next run.

## 9. Milestone 2: Windows

The milestone 1 code must already contain these seams: per-OS commands, the elevation interface, process control behind build tags (`proc_unix.go`, `proc_windows.go`), and the plain output mode.

- Shell: `pwsh -NoLogo -NoProfile -NonInteractive -EncodedCommand <base64 UTF-16LE>`, prefixed with `$ErrorActionPreference='Stop'; $PSNativeCommandUseErrorActionPreference=$true`; fallback `powershell.exe` with `-ExecutionPolicy Bypass`.
- Elevation: upd relaunches itself once via ShellExecuteEx `runas` as `upd __worker` (one UAC prompt per run). The worker receives job names over an ACL'd named pipe, resolves the commands from the config itself, runs them, and streams status back.
- Processes: `CREATE_NEW_PROCESS_GROUP`; each job in a Job Object with KILL_ON_JOB_CLOSE, created suspended and resumed after assignment.
- Console: enable `ENABLE_VIRTUAL_TERMINAL_PROCESSING`, restore on exit; poll terminal size each redraw (no SIGWINCH). Child output sanitised for `\b`, UTF-16 and OEM code pages (`WSL_UTF8=1` for wsl.exe).
- Catalog: scoop (user), choco and winget (`system = true`, elevated, winget after choco because Windows Installer allows one MSI install at a time; winget excludes Windows Terminal and PowerShell by default), `wsl --update`, a `wsl-distros` job that runs `upd --plain` inside each distro (non-system jobs only, since sudo cannot prompt there), and the Windows commands for the cross-platform self-updaters.
- Deferred: Windows Update, Microsoft Store, Windows `sudo`.

## 10. Code layout

```
cmd/upd/              main, flag parsing
internal/config/      TOML tree builder, schema decode, merge, validation, builtin.toml (embedded)
internal/platform/    os-release, WSL, SSH, ostree detection; PATH lookup and real-path resolution
internal/plan/        when evaluation, selection, graph, cycle check
internal/run/         scheduler, snapshots, job execution, elevation interface, sudo keepalive
internal/proc/        start / interrupt / kill-tree / stopped-process watchdog (build tags)
internal/ui/          last-line writer, status board, plain output, summary
internal/state/       state dir, instance lock, log dirs, pruning
internal/selfupdate/  GitHub release check, checksum, replace
testdata/script/      testscript end-to-end tests
```

## 11. Testing

- End-to-end with `rogpeppe/go-internal/testscript`: the real `upd` binary, a temporary HOME and XDG dirs, and fake `apt-get`, `brew`, `sudo`, `claude` etc. on PATH. The fakes record argv, env and start/end times, and can sleep, fail, print `\r` progress, leave a background process behind, or try to read the terminal.
- Cases: every built-in's detection and command line, `after` ordering, snapshot ordering for watchers, skip reasons, timeouts, failure output, merge rules, selection rules, exit codes, the instance lock, `post` environment.
- Unit tests: config decoding and error messages (golden files), merge, `has` derivation, last-line extraction, board layout at small heights and widths.
- A catalog test: every built-in either has a derivable `has` or sets one, and every `after` resolves.
- Detection tests use real `/etc/os-release` files from each distro family as fixtures (`internal/plan/testdata/os-release`).
- CI (GitHub Actions): gofmt, `go vet` (also for windows), `staticcheck`, `go test -race` on Ubuntu and macOS. Windows runner from milestone 2.

## 12. Implementation order

Status: steps 1-8 are implemented and tested, except publishing the first release. Step 9 (cutover) and milestone 2 remain.


1. Config: tree builder, schema, merge, validation, `upd config`, `upd config path`, `upd init`. Golden-file tests for errors.
2. Platform detection, `when` evaluation, selection, graph, `--dry-run`.
3. Runner with plain output: processes, timeouts, Ctrl-C, logs, instance lock, exit codes.
4. Snapshots, `on_change`, watchers, `post`.
5. sudo elevation, keepalive, blocked-on-terminal watchdog.
6. Status board.
7. Built-in catalog, each entry with a testscript case and a manual check on a real machine where possible.
8. Self-update, goreleaser, `install.sh`, first release.
9. Cutover: install the release to `~/.local/bin/upd`, write the personal config (below), remove the bash script and its chezmoi source.
10. Milestone 2: Windows.

## 13. Personal config at cutover

```toml
[commands]
no-mistakes = "no-mistakes update --force"
hermes = { run = "hermes update --yes", env = { GIT_SSH_COMMAND = "ssh -o BatchMode=yes" } }
```

Everything else from the current script is covered by built-ins (claude, codex, omp, uv, brew, volta) or dropped: `npm update -g` updates nothing on this machine, because the `npm` on PATH is Hermes's and its global prefix `~/.local` has no packages, and `volta install npm@latest` is covered by the volta built-in. The codex version pre-check is dropped; `codex update` with nothing to do takes about as long as the check.

Separate cleanup on this machine: remove the standalone `~/.local/bin/uv` (0.12.18) so brew's uv (0.13.7) is the one on PATH, and find out why no-mistakes is stuck at v1.84.0 while v1.106.1 is available.
