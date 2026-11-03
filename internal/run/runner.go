// Package run executes the jobs of a plan.
package run

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wipash/upd/internal/config"
	"github.com/wipash/upd/internal/plan"
	"github.com/wipash/upd/internal/platform"
	"github.com/wipash/upd/internal/proc"
)

// Status is the outcome of a job.
type Status int

const (
	Pending      Status = iota
	Waiting             // waiting for jobs it comes after
	Running             //
	Done                // succeeded; it has no version command
	Updated             // succeeded and its version changed
	UpToDate            // succeeded and its version did not change
	Skipped             // skip_if matched, or it needs sudo and sudo is unavailable
	Failed              // non-success exit code or could not start
	TimedOut            //
	Blocked             // a process stopped waiting for terminal input
	ChangeFailed        // updated, but on_change failed
	NotRun              // the run was interrupted before the job started
)

var statusNames = [...]string{"pending", "waiting", "running", "done", "updated", "up to date", "skipped", "failed", "timed out", "blocked", "on_change failed", "not run"}

func (s Status) String() string { return statusNames[s] }

// Bad reports whether the status makes the run fail.
func (s Status) Bad() bool {
	return s == Failed || s == TimedOut || s == Blocked || s == ChangeFailed
}

// Result is what happened to one job.
type Result struct {
	Name   string
	Status Status
	Detail string // skip reason, version change, failure summary or last output line
	Watch  bool   // a watcher job
	Start  time.Time
	End    time.Time
	Log    string // path of the job's log file
}

// Observer receives progress. Calls may come from many goroutines.
type Observer interface {
	JobWaiting(name string, on []string)
	JobStarted(name string)
	JobOutput(name string, lines []string, partial string)
	JobFinished(r *Result)
}

const (
	snapshotTimeout = 10 * time.Second
	skipIfTimeout   = time.Minute
	interruptGrace  = 5 * time.Second
	tailInterval    = 100 * time.Millisecond
)

// Runner runs the selected jobs of a plan.
type Runner struct {
	Config *config.Config
	Plan   *plan.Plan
	Facts  platform.Facts
	LogDir string
	Obs    Observer

	// Elevator runs sudo jobs. When it is nil, sudo jobs are skipped with
	// ElevationErr as the reason.
	Elevator     Elevator
	ElevationErr error
}

type jobState struct {
	entry    *plan.Entry
	job      *config.Job
	deps     []string // selected jobs it comes after
	gate     []string // jobs whose version snapshot must finish before it starts
	done     chan struct{}
	snapDone chan struct{}
	before   string
	snapOK   bool
	result   *Result
}

// Run runs every selected job and returns the results in name order. When
// ctx is cancelled, running jobs are interrupted and jobs that have not
// started are reported as not run.
func (r *Runner) Run(ctx context.Context) []*Result {
	selected := r.Plan.Selected()
	states := map[string]*jobState{}
	for _, e := range selected {
		states[e.Job.Name] = &jobState{entry: e, job: e.Job, done: make(chan struct{}), snapDone: make(chan struct{})}
	}
	for _, s := range states {
		for _, a := range s.job.After {
			if states[a] != nil {
				s.deps = append(s.deps, a)
			}
		}
	}
	// A job may change the version of any job that comes after it, so it
	// waits for those jobs' "before" snapshots.
	for name, s := range states {
		s.gate = descendants(states, name)
	}

	_ = os.MkdirAll(filepath.Join(r.LogDir, "jobs"), 0o755)

	for _, s := range states {
		go r.snapshot(ctx, s)
	}

	var sem chan struct{}
	if r.Config.MaxParallel > 0 {
		sem = make(chan struct{}, r.Config.MaxParallel)
	}
	var wg sync.WaitGroup
	for _, s := range states {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer close(s.done)
			s.result = r.runJob(ctx, s, states, sem)
			r.Obs.JobFinished(s.result)
		}()
	}
	wg.Wait()

	var out []*Result
	for _, e := range selected {
		out = append(out, states[e.Job.Name].result)
	}
	return out
}

// descendants returns name and every job that comes after it, directly or
// through other jobs.
func descendants(states map[string]*jobState, name string) []string {
	seen := map[string]bool{name: true}
	queue := []string{name}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for m, s := range states {
			if !seen[m] && slices.Contains(s.deps, n) {
				seen[m] = true
				queue = append(queue, m)
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

func (r *Runner) snapshot(ctx context.Context, s *jobState) {
	defer close(s.snapDone)
	cmd := s.job.Version.For(r.Facts.GOOS)
	if cmd == "" {
		return
	}
	s.before, s.snapOK = r.capture(ctx, s.job, cmd, snapshotTimeout)
}

// capture runs a short command as the user and returns its trimmed stdout.
func (r *Runner) capture(ctx context.Context, j *config.Job, cmd string, timeout time.Duration) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, code, err := proc.Output(ctx, shellArgv(j.Shell, cmd, r.Facts.GOOS), r.env(j), r.dir(j))
	return strings.TrimSpace(out), err == nil && code == 0
}

// SkipCheck runs a skip_if command. The job is skipped when it exits 0,
// and its first line of output is the reason.
func (r *Runner) SkipCheck(ctx context.Context, j *config.Job, cmd string) (reason string, skip bool) {
	out, ok := r.capture(ctx, j, cmd, skipIfTimeout)
	if !ok {
		return "", false
	}
	if reason = firstLine(out); reason == "" {
		reason = "skip_if matched"
	}
	return reason, true
}

func (r *Runner) runJob(ctx context.Context, s *jobState, states map[string]*jobState, sem chan struct{}) *Result {
	j := s.job
	res := &Result{Name: j.Name, Watch: s.entry.Watch, Log: filepath.Join(r.LogDir, "jobs", logName(j.Name))}
	notRun := func() *Result {
		res.Status, res.Detail = NotRun, "interrupted"
		return res
	}

	if len(s.deps) > 0 {
		r.Obs.JobWaiting(j.Name, s.deps)
		for _, d := range s.deps {
			select {
			case <-states[d].done:
			case <-ctx.Done():
				return notRun()
			}
		}
	}
	for _, g := range s.gate {
		select {
		case <-states[g].snapDone:
		case <-ctx.Done():
			return notRun()
		}
	}
	if sem != nil {
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
		case <-ctx.Done():
			return notRun()
		}
	}
	if ctx.Err() != nil {
		return notRun()
	}

	res.Start = time.Now()
	defer func() { res.End = time.Now() }()
	r.Obs.JobStarted(j.Name)

	logf, err := os.OpenFile(res.Log, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		res.Status, res.Detail = Failed, err.Error()
		return res
	}
	defer logf.Close()

	if cmd := j.SkipIf.For(r.Facts.GOOS); cmd != "" {
		if reason, skip := r.SkipCheck(ctx, j, cmd); skip {
			res.Status, res.Detail = Skipped, reason
			fmt.Fprintf(logf, "skipped: %s\n", reason)
			return res
		}
		if ctx.Err() != nil {
			return notRun()
		}
	}
	if j.Sudo && r.Elevator == nil {
		res.Status, res.Detail = Skipped, "no sudo"
		if r.ElevationErr != nil {
			res.Detail = "no sudo: " + r.ElevationErr.Error()
		}
		return res
	}

	var deadline time.Time
	if j.Timeout > 0 {
		deadline = time.Now().Add(j.Timeout)
	}
	var last string
	for _, cmd := range j.Run.For(r.Facts.GOOS) {
		fmt.Fprintf(logf, "$ %s\n", cmd)
		st, detail, lastLine := r.step(ctx, j, cmd, logf, deadline, j.Timeout, j.Sudo)
		last = lastLine
		if st != Done {
			res.Status, res.Detail = st, detail
			return res
		}
	}
	if ctx.Err() != nil {
		res.Status, res.Detail = NotRun, "interrupted while running"
		return res
	}

	cmd := j.Version.For(r.Facts.GOOS)
	if cmd == "" {
		res.Status, res.Detail = Done, last
		return res
	}
	<-s.snapDone
	after, ok := r.capture(ctx, j, cmd, snapshotTimeout)
	if !s.snapOK || !ok {
		res.Status, res.Detail = Done, "version unknown (?)"
		if len(j.Run) > 0 && last != "" {
			res.Detail = last
		}
		return res
	}
	change := describeChange(s.before, after)
	if change == "" {
		res.Status = UpToDate
		if lines := splitLines(after); len(lines) == 1 {
			res.Detail = lines[0]
		}
		return res
	}
	res.Status, res.Detail = Updated, change
	if oc := j.OnChange.For(r.Facts.GOOS); oc != "" {
		fmt.Fprintf(logf, "$ %s  (on_change)\n", oc)
		var ocDeadline time.Time
		if j.Timeout > 0 {
			ocDeadline = time.Now().Add(j.Timeout)
		}
		st, detail, _ := r.step(ctx, j, oc, logf, ocDeadline, j.Timeout, j.Sudo)
		if st != Done {
			res.Status, res.Detail = ChangeFailed, change+"; on_change "+detail
		}
	}
	return res
}

// step runs one command of a job with its output appended to logf.
// On interrupt (ctx cancelled) it forwards SIGINT, then after a grace
// period kills the process group. Processes owned by root under sudo
// cannot be killed by upd, so those are only asked to terminate.
func (r *Runner) step(ctx context.Context, j *config.Job, cmd string, logf *os.File, deadline time.Time, limit time.Duration, elevated bool) (st Status, detail, last string) {
	if ctx.Err() != nil {
		return NotRun, "interrupted", ""
	}
	argv := shellArgv(j.Shell, cmd, r.Facts.GOOS)
	env := r.env(j)
	if elevated {
		argv = r.Elevator.Wrap(argv, r.extraEnv(j))
	}
	var offset int64
	if fi, err := logf.Stat(); err == nil {
		offset = fi.Size()
	}
	p, err := proc.Start(proc.Spec{Argv: argv, Env: env, Dir: r.dir(j), Out: logf, Terminal: elevated})
	if err != nil {
		fmt.Fprintf(logf, "upd: %v\n", err)
		return Failed, err.Error(), ""
	}

	tail := newTailer(logf.Name(), offset, func(lines []string, partial string) { r.Obs.JobOutput(j.Name, lines, partial) })
	defer tail.close()
	type exit struct {
		code int
		err  error
	}
	exited := make(chan exit, 1)
	go func() {
		code, err := p.Wait()
		exited <- exit{code, err}
	}()

	var timer <-chan time.Time
	if !deadline.IsZero() {
		t := time.NewTimer(time.Until(deadline))
		defer t.Stop()
		timer = t.C
	}
	ticker := time.NewTicker(tailInterval)
	defer ticker.Stop()
	var timedOut, blocked bool
	checks := 0
	interrupted := ctx.Done()
	var grace <-chan time.Time
	stop := func() {
		if elevated {
			p.Terminate()
		} else {
			p.Kill()
		}
	}
	for {
		select {
		case e := <-exited:
			tail.poll()
			last = tail.last()
			switch {
			case ctx.Err() != nil:
				return NotRun, "interrupted while running", last
			case timedOut:
				return TimedOut, "timed out after " + limit.String(), last
			case blocked:
				return Blocked, "stopped waiting for terminal input", last
			case e.err != nil:
				return Failed, e.err.Error(), last
			case !slices.Contains(j.OkExitCodes, e.code):
				d := "exit " + strconv.Itoa(e.code)
				if last != "" {
					d += ": " + last
				}
				return Failed, d, last
			}
			return Done, "", last
		case <-ticker.C:
			tail.poll()
			// A process that keeps the terminal but runs in a background
			// process group is stopped by the kernel if it reads from the
			// terminal. Nothing will resume it, so end the job.
			if checks++; elevated && !blocked && checks%10 == 0 && p.Stopped() {
				blocked = true
				note(logf, "upd: stopped waiting for terminal input")
				p.Terminate()
			}
		case <-timer:
			timedOut = true
			note(logf, "upd: timed out after "+limit.String())
			stop()
		case <-interrupted:
			interrupted = nil
			note(logf, "upd: interrupted")
			p.Interrupt()
			grace = time.After(interruptGrace)
		case <-grace:
			grace = nil
			stop()
		}
	}
}

// defaultEnv is added to every job unless the user's environment already
// sets the variable. Jobs have no terminal, so ssh cannot prompt anyway;
// this stops git from trying.
var defaultEnv = map[string]string{
	"GIT_TERMINAL_PROMPT": "0",
}

// extraEnv returns the variables upd adds to the inherited environment.
func (r *Runner) extraEnv(j *config.Job) map[string]string {
	out := map[string]string{}
	for k, v := range defaultEnv {
		if _, set := os.LookupEnv(k); !set {
			out[k] = v
		}
	}
	for k, v := range j.Env {
		out[k] = v
	}
	return out
}

func (r *Runner) env(j *config.Job) []string {
	env := os.Environ()
	for k, v := range r.extraEnv(j) {
		env = append(env, k+"="+v)
	}
	return env
}

func (r *Runner) dir(j *config.Job) string {
	if j.Cwd != "" {
		return r.Facts.Expand(j.Cwd)
	}
	return r.Facts.Home
}

func logName(job string) string {
	var b strings.Builder
	for _, c := range job {
		if c == '/' || c == '\\' || c == ':' || (runtime.GOOS == "windows" && strings.ContainsRune(`<>"|?*`, c)) {
			c = '_'
		}
		b.WriteRune(c)
	}
	return b.String() + ".log"
}

// tailer follows a log file and reports new output.
type tailer struct {
	f      *os.File
	lb     lineBuffer
	lastLn string
	report func(lines []string, partial string)
	buf    []byte
}

func newTailer(path string, offset int64, report func([]string, string)) *tailer {
	t := &tailer{report: report, buf: make([]byte, 64*1024)}
	if f, err := os.Open(path); err == nil {
		t.f = f
		_, _ = f.Seek(offset, io.SeekStart)
	}
	return t
}

func (t *tailer) poll() {
	if t.f == nil {
		return
	}
	var lines []string
	read := false
	for {
		n, err := t.f.Read(t.buf)
		if n > 0 {
			read = true
			lines = append(lines, t.lb.feed(t.buf[:n])...)
		}
		if err != nil || n < len(t.buf) {
			break
		}
	}
	if !read {
		return
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i] != "" {
			t.lastLn = lines[i]
			break
		}
	}
	t.report(lines, t.lb.current())
}

// last returns the most recent non-empty line, preferring an unfinished one.
func (t *tailer) last() string {
	defer t.close()
	if c := t.lb.current(); c != "" {
		return c
	}
	return t.lastLn
}

func (t *tailer) close() {
	if t.f != nil {
		t.f.Close()
		t.f = nil
	}
}

// note appends a line from upd to a job log, starting a new line if the
// job's output did not end with one.
func note(logf *os.File, msg string) {
	if fi, err := logf.Stat(); err == nil && fi.Size() > 0 {
		b := make([]byte, 1)
		if _, err := logf.ReadAt(b, fi.Size()-1); err == nil && b[0] != '\n' {
			msg = "\n" + msg
		}
	}
	fmt.Fprintln(logf, msg)
}
