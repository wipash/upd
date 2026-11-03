package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wipash/upd/internal/config"
)

// Post runs the top-level post commands once all jobs have finished. They
// see the run's outcome in UPD_UPDATED, UPD_FAILED and UPD_EXIT.
func (r *Runner) Post(ctx context.Context, results []*Result, exit int) *Result {
	cmds := r.Config.Post.For(r.Facts.GOOS)
	if len(cmds) == 0 {
		return nil
	}
	var updated, failed []string
	for _, res := range results {
		switch {
		case res.Status == Updated:
			updated = append(updated, res.Name)
		case res.Status.Bad():
			failed = append(failed, res.Name)
		}
	}
	j := &config.Job{
		Name: "post",
		Env: map[string]string{
			"UPD_UPDATED": strings.Join(updated, " "),
			"UPD_FAILED":  strings.Join(failed, " "),
			"UPD_EXIT":    strconv.Itoa(exit),
		},
		Timeout:     config.DefaultTimeout,
		OkExitCodes: []int{0},
	}
	res := &Result{Name: "post", Status: Done, Log: filepath.Join(r.LogDir, "post.log"), Start: time.Now()}
	defer func() { res.End = time.Now() }()
	logf, err := os.OpenFile(res.Log, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		res.Status, res.Detail = Failed, err.Error()
		return res
	}
	defer logf.Close()
	saved := r.Obs
	r.Obs = nopObserver{}
	defer func() { r.Obs = saved }()
	deadline := time.Now().Add(j.Timeout)
	for _, cmd := range cmds {
		fmt.Fprintf(logf, "$ %s\n", cmd)
		st, detail, last := r.step(ctx, j, cmd, logf, deadline, j.Timeout, false)
		if st != Done {
			res.Status, res.Detail = st, detail
			return res
		}
		res.Detail = last
	}
	return res
}

type nopObserver struct{}

func (nopObserver) JobWaiting(string, []string)        {}
func (nopObserver) JobStarted(string)                  {}
func (nopObserver) JobOutput(string, []string, string) {}
func (nopObserver) JobFinished(*Result)                {}
