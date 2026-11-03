package run

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wipash/upd/internal/config"
	"github.com/wipash/upd/internal/plan"
	"github.com/wipash/upd/internal/platform"
)

func TestRender(t *testing.T) {
	tests := map[string]string{
		"plain":                      "plain",
		"progress 40%\rprogress 60%": "progress 60%",
		"done\r":                     "done",
		"\x1b[32mgreen\x1b[0m text":  "green text",
		"abc\bd":                     "abd",
		"\x1b]0;title\x07shown":      "shown",
		"tab\there":                  "tab here",
	}
	for in, want := range tests {
		if got := render([]byte(in)); got != want {
			t.Errorf("render(%q) = %q, want %q", in, got, want)
		}
	}
	var lb lineBuffer
	lines := lb.feed([]byte("one\r\ntwo\nthr"))
	if strings.Join(lines, "|") != "one|two" || lb.current() != "thr" {
		t.Errorf("feed: %q, current %q", lines, lb.current())
	}
}

func TestDescribeChange(t *testing.T) {
	tests := []struct{ old, cur, want string }{
		{"2.1.317 (Claude Code)", "2.1.320 (Claude Code)", "2.1.317 → 2.1.320"},
		{"codex-cli 0.166.0", "codex-cli 0.167.0", "0.166.0 → 0.167.0"},
		{"same", "same", ""},
		{"deno 2.1.0\nv8 13\ntypescript 5.6", "deno 2.1.1\nv8 13\ntypescript 5.6", "2.1.0 → 2.1.1"},
		{"a 1\nb 1\nc 1\nd 1", "a 1\nb 2\nc 1\nd 2", "2 changed"},
		{"a 1\nb 1\nc 1\nd 1", "d 1\nc 1\nb 1\na 1", ""},
		{"a 1\nb 1\nc 1\nd 1", "a 1\nb 1\nc 1\nd 1\ne 1", "1 added"},
		{"a 1\nb 1\nc 1\nd 1", "a 1\nb 1\nc 1", "1 removed"},
	}
	for _, tt := range tests {
		if got := describeChange(tt.old, tt.cur); got != tt.want {
			t.Errorf("describeChange(%q, %q) = %q, want %q", tt.old, tt.cur, got, tt.want)
		}
	}
}

type recorder struct {
	mu     sync.Mutex
	starts map[string]time.Time
	ends   map[string]time.Time
}

func (r *recorder) JobWaiting(string, []string)        {}
func (r *recorder) JobOutput(string, []string, string) {}
func (r *recorder) JobStarted(n string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts[n] = time.Now()
}
func (r *recorder) JobFinished(res *Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ends[res.Name] = time.Now()
}

// runConfig runs every job in a user config that replaces the built-ins.
func runConfig(t *testing.T, ctx context.Context, doc string) (map[string]*Result, *recorder) {
	t.Helper()
	f, err := config.Parse("config.toml", []byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Merge(&config.File{Name: "builtin.toml", Jobs: map[string]*config.Spec{}}, f)
	if err != nil {
		t.Fatal(err)
	}
	facts := platform.Facts{GOOS: "linux", Home: t.TempDir()}
	p, err := plan.Build(cfg, facts, plan.Options{All: true})
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{starts: map[string]time.Time{}, ends: map[string]time.Time{}}
	r := &Runner{Config: cfg, Plan: p, Facts: facts, LogDir: t.TempDir(), Obs: rec}
	out := map[string]*Result{}
	for _, res := range r.Run(ctx) {
		out[res.Name] = res
	}
	return out, rec
}

func TestRunOrderingAndStatuses(t *testing.T) {
	dir := t.TempDir()
	ver := filepath.Join(dir, "ver")
	os.WriteFile(ver, []byte("tool 1.0\n"), 0o644)
	res, rec := runConfig(t, context.Background(), `
[commands.first]
run = "sleep 0.3; echo 'tool 2.0' > `+ver+`"
[commands.second]
after = ["first"]
run = "echo second ran"
[commands.parallel]
run = "sleep 0.1; printf 'progress 50%%\rdone here'"
[commands.watch]
after = ["first"]
version = "cat `+ver+`"
on_change = "echo restarted"
[commands.fails]
run = ["false", "echo never"]
[commands.okcode]
run = "exit 3"
ok_exit_codes = [0, 3]
[commands.skips]
skip_if = "echo already current"
run = "false"
[commands.slow]
run = "sleep 30"
timeout = "300ms"
[commands.needsroot]
sudo = true
run = "true"
`)
	want := map[string]Status{
		"first": Done, "second": Done, "parallel": Done, "watch": Updated, "fails": Failed,
		"okcode": Done, "skips": Skipped, "slow": TimedOut, "needsroot": Skipped,
	}
	for name, st := range want {
		if res[name].Status != st {
			t.Errorf("%s: status %v (%s), want %v", name, res[name].Status, res[name].Detail, st)
		}
	}
	if !rec.starts["second"].After(rec.ends["first"]) {
		t.Error("second started before first finished")
	}
	if rec.starts["parallel"].After(rec.ends["first"]) {
		t.Error("parallel waited for first")
	}
	if res["watch"].Detail != "1.0 → 2.0" {
		t.Errorf("watch detail = %q", res["watch"].Detail)
	}
	if res["parallel"].Detail != "done here" {
		t.Errorf("last line = %q", res["parallel"].Detail)
	}
	if res["skips"].Detail != "already current" {
		t.Errorf("skip reason = %q", res["skips"].Detail)
	}
	if log, _ := os.ReadFile(res["fails"].Log); strings.Contains(string(log), "never") {
		t.Error("run list continued after a failure")
	}
}

// A job that leaves a background process holding its output must still
// finish when its own process exits.
func TestBackgroundProcessDoesNotHoldJob(t *testing.T) {
	start := time.Now()
	res, _ := runConfig(t, context.Background(), `
[commands.daemon]
run = "sleep 5 & echo started"
`)
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("job took %v", d)
	}
	if res["daemon"].Status != Done {
		t.Errorf("status %v: %s", res["daemon"].Status, res["daemon"].Detail)
	}
}

func TestTimeoutKillsGrandchildren(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "survived")
	runConfig(t, context.Background(), `
[commands.slow]
run = "(sleep 1; touch `+marker+`) & sleep 30"
timeout = "200ms"
`)
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Error("a grandchild survived the timeout")
	}
}

func TestInterrupt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	start := time.Now()
	res, _ := runConfig(t, ctx, `
[commands.long]
run = "sleep 30"
[commands.later]
after = ["long"]
run = "true"
`)
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("interrupt took %v", d)
	}
	if res["long"].Status != NotRun || res["later"].Status != NotRun {
		t.Errorf("long=%v later=%v", res["long"].Status, res["later"].Status)
	}
}

// An interrupt while skip_if runs must not start the job.
func TestInterruptDuringSkipIf(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	res, _ := runConfig(t, ctx, `
[commands.job]
skip_if = "sleep 5"
run = "touch `+marker+`"
`)
	if _, err := os.Stat(marker); err == nil {
		t.Error("the job ran after the interrupt")
	}
	if res["job"].Status != NotRun {
		t.Errorf("status %v", res["job"].Status)
	}
}

// A job started in its own session does not see the terminal's Ctrl-C, so
// upd must forward it, including to post commands.
func TestInterruptReachesPost(t *testing.T) {
	f, err := config.Parse("config.toml", []byte(`post = ["sleep 10"]`))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Merge(&config.File{Name: "builtin.toml", Jobs: map[string]*config.Spec{}}, f)
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{Config: cfg, Facts: platform.Facts{GOOS: "linux", Home: t.TempDir()}, LogDir: t.TempDir(), Obs: nopObserver{}}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	start := time.Now()
	res := r.Post(ctx, nil, 0)
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("post ignored the interrupt for %v", d)
	}
	if res.Status != NotRun {
		t.Errorf("status %v", res.Status)
	}
}

func TestProgressBufferStaysSmall(t *testing.T) {
	var lb lineBuffer
	for i := range 10000 {
		lb.feed([]byte("progress " + strings.Repeat("#", i%50) + "\r"))
	}
	if len(lb.partial) > 200 {
		t.Errorf("partial line grew to %d bytes", len(lb.partial))
	}
	if !strings.HasPrefix(lb.current(), "progress") {
		t.Errorf("current = %q", lb.current())
	}
}
