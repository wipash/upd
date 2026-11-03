// Command upd updates everything on this machine in parallel.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/wipash/upd/internal/config"
	"github.com/wipash/upd/internal/plan"
	"github.com/wipash/upd/internal/platform"
	"github.com/wipash/upd/internal/run"
	"github.com/wipash/upd/internal/selfupdate"
	"github.com/wipash/upd/internal/state"
	"github.com/wipash/upd/internal/ui"
)

// version is set at build time.
var version = "dev"

const (
	exitOK      = 0
	exitFailed  = 1
	exitUsage   = 2
	exitLocked  = 3
	exitSignal  = 130
	keepLogRuns = 10
)

const usage = `usage: upd [flags] [job...]
       upd init | config [path] | version | self-update

Runs every update job that applies to this machine, in parallel.
Naming jobs runs only those (even system or disabled ones).

flags:
  -a, --all       include system jobs (OS package managers; may ask for sudo)
      --dry-run   show what would run and why, without running anything
  -v, --verbose   print every job's output, prefixed with its name
      --plain     plain line output even on a terminal
  -h, --help      show this help
`

type options struct {
	all, dryRun, verbose, plain bool
	names                       []string
}

func main() {
	os.Exit(mainErr(os.Args[1:]))
}

func mainErr(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "init":
			return cmdInit()
		case "config":
			return cmdConfig(args[1:])
		case "version":
			fmt.Println(version)
			return exitOK
		case "self-update":
			exe, err := os.Executable()
			if err == nil {
				exe, err = filepath.EvalSymlinks(exe)
			}
			if err == nil {
				err = selfupdate.Run(context.Background(), os.Stdout, version, exe)
			}
			if err != nil {
				fmt.Fprintln(os.Stderr, "upd:", err)
				return exitFailed
			}
			return exitOK
		case "help":
			fmt.Print(usage)
			return exitOK
		}
	}
	var o options
	for _, a := range args {
		switch a {
		case "-a", "--all":
			o.all = true
		case "--dry-run":
			o.dryRun = true
		case "-v", "--verbose":
			o.verbose = true
		case "--plain":
			o.plain = true
		case "-h", "--help":
			fmt.Print(usage)
			return exitOK
		default:
			if strings.HasPrefix(a, "-") {
				fmt.Fprintf(os.Stderr, "upd: unknown flag %s\n\n%s", a, usage)
				return exitUsage
			}
			o.names = append(o.names, a)
		}
	}
	return cmdRun(o)
}

func loadConfig() (*config.Config, string, int) {
	path, err := config.UserPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "upd:", err)
		return nil, "", exitUsage
	}
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "upd:", err)
		return nil, path, exitUsage
	}
	return cfg, path, exitOK
}

func cmdConfig(args []string) int {
	if len(args) == 1 && args[0] == "path" {
		path, err := config.UserPath()
		if err != nil {
			fmt.Fprintln(os.Stderr, "upd:", err)
			return exitUsage
		}
		fmt.Println(path)
		if _, err := os.Stat(path); err != nil {
			fmt.Fprintln(os.Stderr, "(the file does not exist; upd init creates it)")
		}
		return exitOK
	}
	if len(args) > 0 {
		fmt.Fprintf(os.Stderr, "upd: unknown config command %q\n", strings.Join(args, " "))
		return exitUsage
	}
	cfg, _, code := loadConfig()
	if cfg == nil {
		return code
	}
	fmt.Print(config.Format(cfg))
	return exitOK
}

func cmdInit() int {
	path, err := config.UserPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "upd:", err)
		return exitUsage
	}
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintf(os.Stderr, "upd: %s already exists\n", path)
		return exitUsage
	}
	cfg, _, code := loadConfig()
	if cfg == nil {
		return code
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "upd:", err)
		return exitFailed
	}
	if err := os.WriteFile(path, []byte(starterConfig(cfg)), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "upd:", err)
		return exitFailed
	}
	fmt.Println("wrote", path)
	return exitOK
}

func starterConfig(cfg *config.Config) string {
	var b strings.Builder
	b.WriteString(`# upd configuration. Built-in jobs run automatically when they apply to
# this machine; see them with "upd config" and "upd --dry-run".
#
# Built-in jobs: ` + wrapList(cfg.Names(), "#   ") + `

# Turn built-in jobs off by name.
# disable = ["snap"]

# Commands run once after every job has finished. They see UPD_UPDATED,
# UPD_FAILED (job names) and UPD_EXIT.
# post = ["notify-send upd \"exit $UPD_EXIT\""]

[commands]
# A job is a command string, or a table:
# no-mistakes = "no-mistakes update --force"
# hermes = { run = "hermes update --yes", after = ["brew"] }
#
# Fields: run, shell, cwd, env, after, system, sudo, when, skip_if,
# version, on_change, timeout, ok_exit_codes. Using a built-in job's name
# overrides only the fields you write:
# [commands.apt]
# system = false
#
# A job without run watches another job's work:
# [commands.herdr-restart]
# after = ["brew"]
# version = "herdr --version"
# on_change = "herdr server reload-config"
`)
	return b.String()
}

func wrapList(names []string, indent string) string {
	var b strings.Builder
	col := 80
	for i, n := range names {
		if col+len(n)+2 > 76 {
			b.WriteString("\n" + indent)
			col = len(indent)
		}
		b.WriteString(n)
		col += len(n)
		if i < len(names)-1 {
			b.WriteString(", ")
			col += 2
		}
	}
	return b.String()
}

func cmdRun(o options) int {
	cfg, _, code := loadConfig()
	if cfg == nil {
		return code
	}
	facts := platform.Detect()
	p, err := plan.Build(cfg, facts, plan.Options{All: o.all, Names: o.names})
	if err != nil {
		fmt.Fprintln(os.Stderr, "upd:", err)
		return exitUsage
	}
	if o.dryRun {
		return dryRun(cfg, p, facts)
	}
	selected := p.Selected()
	if len(selected) == 0 {
		fmt.Println("nothing to do: no jobs apply to this machine (see upd --dry-run)")
		return exitOK
	}

	dir, err := state.Dir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "upd:", err)
		return exitFailed
	}
	lock, err := state.Acquire(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "upd:", err)
		var locked *state.ErrLocked
		if errors.As(err, &locked) {
			return exitLocked
		}
		return exitFailed
	}
	defer lock.Release()
	logDir, err := state.NewRunDir(dir, time.Now(), keepLogRuns)
	if logDir == "" {
		fmt.Fprintln(os.Stderr, "upd:", err)
		return exitFailed
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stdoutTTY := term.IsTerminal(int(os.Stdout.Fd()))
	restoreVT, vtOK := ui.EnableVT(os.Stdout)
	defer restoreVT()
	style := ui.Style{Color: stdoutTTY && vtOK && os.Getenv("NO_COLOR") == ""}

	r := &run.Runner{Config: cfg, Plan: p, Facts: facts, LogDir: logDir}
	if slices.ContainsFunc(selected, func(e *plan.Entry) bool { return e.Job.Sudo }) {
		r.Elevator, r.ElevationErr = run.PrepareElevation(ctx)
		if r.ElevationErr != nil {
			fmt.Fprintf(os.Stderr, "upd: %v; jobs that need sudo will be skipped\n", r.ElevationErr)
		} else {
			defer r.Elevator.Close()
		}
	}

	var names, watchers []string
	for _, e := range selected {
		names = append(names, e.Job.Name)
		if e.Watch {
			watchers = append(watchers, e.Job.Name)
		}
	}
	size := func() (int, int) {
		w, h, err := term.GetSize(int(os.Stdout.Fd()))
		if err != nil || w <= 0 {
			return 80, 24
		}
		return w, h
	}
	var board *ui.Board
	if stdoutTTY && vtOK && !o.plain && !o.verbose {
		board = ui.NewBoard(os.Stdout, style, size, names, watchers)
		r.Obs = board
	} else {
		pl := &ui.Plain{W: os.Stdout, Style: style, Verbose: o.verbose}
		pl.SetWatchers(watchers)
		r.Obs = pl
	}

	interrupted := handleSignals(cancel, func() {
		if board != nil {
			board.Close()
		}
	})

	results := r.Run(ctx)
	if board != nil {
		board.Close()
	}

	exit := exitOK
	for _, res := range results {
		if res.Status.Bad() {
			exit = exitFailed
		}
	}
	var post *run.Result
	if !interrupted() {
		post = r.Post(ctx, results, exit)
		if post != nil && post.Status.Bad() {
			exit = exitFailed
		}
	}
	hidden := 0
	for _, e := range p.Entries {
		if e.State == plan.Hidden {
			hidden++
		}
	}
	width, _ := size()
	ui.Summary(os.Stdout, style, results, post, hidden, logDir, width)
	if interrupted() {
		return exitSignal
	}
	return exit
}

// handleSignals cancels the run on the first interrupt and exits on the
// second. cleanup restores the terminal before exiting.
func handleSignals(cancel func(), cleanup func()) (interrupted func() bool) {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	got := make(chan struct{})
	go func() {
		<-ch
		close(got)
		cancel()
		<-ch
		cleanup()
		fmt.Fprintln(os.Stderr, "\nupd: interrupted again; exiting without waiting for running jobs (sudo jobs may still be running)")
		os.Exit(exitSignal)
	}()
	return func() bool {
		select {
		case <-got:
			return true
		default:
			return false
		}
	}
}

func dryRun(cfg *config.Config, p *plan.Plan, facts platform.Facts) int {
	type rowT struct{ name, state, detail string }
	var groups [4][]rowT
	for _, n := range p.Names {
		e := p.Entries[n]
		r := rowT{name: n, state: e.State.String()}
		g := 0
		switch e.State {
		case plan.Run:
			if cmd := e.Job.SkipIf.For(facts.GOOS); cmd != "" {
				if reason, skip := skipCheck(e.Job, cmd, facts); skip {
					r.state, r.detail = "will skip", reason
				}
			}
			if e.Job.Sudo {
				r.detail = strings.TrimSpace(r.detail + " (sudo)")
			}
			if e.Watch {
				r.detail = strings.TrimSpace(r.detail + " (watcher)")
			}
		case plan.Hidden:
			g, r.detail = 3, e.Reason
		default:
			g = 1
		}
		groups[g] = append(groups[g], r)
	}
	w := io.Writer(os.Stdout)
	nameW := 0
	for _, n := range p.Names {
		nameW = max(nameW, len(n))
	}
	for _, g := range groups {
		for _, r := range g {
			fmt.Fprintln(w, strings.TrimRight(fmt.Sprintf("%-*s  %-21s %s", nameW, r.name, r.state, r.detail), " "))
		}
	}
	return exitOK
}

// skipCheck runs a job's skip_if command for --dry-run.
func skipCheck(j *config.Job, cmd string, facts platform.Facts) (string, bool) {
	r := &run.Runner{Facts: facts}
	return r.SkipCheck(context.Background(), j, cmd)
}
