// Package selfupdate replaces the running upd binary with the latest
// GitHub release.
package selfupdate

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Repo is the GitHub repository releases come from.
const Repo = "wipash/upd"

type release struct {
	Tag        string    `json:"tag_name"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Published  time.Time `json:"published_at"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// AssetName is the release asset for this platform.
func AssetName(goos, goarch string) string {
	name := fmt.Sprintf("upd_%s_%s", goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// Run updates the binary at exe from version current to the newest
// release published at least minDays days ago. It writes progress to w.
func Run(ctx context.Context, w io.Writer, current, exe string, minDays int) error {
	if current == "dev" || current == "" {
		fmt.Fprintln(w, "development build; not updating")
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	var releases []release
	if err := getJSON(ctx, "https://api.github.com/repos/"+Repo+"/releases?per_page=30", &releases); err != nil {
		return fmt.Errorf("checking for a new release: %w", err)
	}
	rel, younger := pick(releases, strings.TrimPrefix(current, "v"), time.Now().AddDate(0, 0, -minDays))
	if rel == nil {
		if younger != nil {
			unit := "days"
			if minDays == 1 {
				unit = "day"
			}
			fmt.Fprintf(w, "upd %s is up to date (%s was released less than %d %s ago)\n", current, younger.Tag, minDays, unit)
		} else {
			fmt.Fprintf(w, "upd %s is up to date\n", current)
		}
		return nil
	}
	latest := strings.TrimPrefix(rel.Tag, "v")
	want := AssetName(runtime.GOOS, runtime.GOARCH)
	var binURL, sumURL string
	for _, a := range rel.Assets {
		switch a.Name {
		case want:
			binURL = a.URL
		case "checksums.txt":
			sumURL = a.URL
		}
	}
	if binURL == "" || sumURL == "" {
		return fmt.Errorf("release %s has no %s or checksums.txt", rel.Tag, want)
	}
	sums, err := get(ctx, sumURL)
	if err != nil {
		return err
	}
	wantSum, err := checksumFor(sums, want)
	if err != nil {
		return err
	}
	bin, err := get(ctx, binURL)
	if err != nil {
		return err
	}
	got := sha256.Sum256(bin)
	if hex.EncodeToString(got[:]) != wantSum {
		return fmt.Errorf("checksum mismatch for %s", want)
	}
	if err := replace(exe, bin); err != nil {
		return err
	}
	fmt.Fprintf(w, "updated upd %s → %s\n", current, latest)
	return nil
}

// pick returns the newest release that is newer than current and was
// published before cutoff, or nil. younger is the newest release that is
// newer than current but too recent, when it is newer than the one picked.
func pick(releases []release, current string, cutoff time.Time) (rel, younger *release) {
	for i := range releases {
		r := &releases[i]
		v := strings.TrimPrefix(r.Tag, "v")
		if r.Draft || r.Prerelease || !newer(v, current) {
			continue
		}
		if r.Published.After(cutoff) {
			if younger == nil || newer(v, strings.TrimPrefix(younger.Tag, "v")) {
				younger = r
			}
			continue
		}
		if rel == nil || newer(v, strings.TrimPrefix(rel.Tag, "v")) {
			rel = r
		}
	}
	if younger != nil && rel != nil && !newer(strings.TrimPrefix(younger.Tag, "v"), strings.TrimPrefix(rel.Tag, "v")) {
		younger = nil
	}
	return rel, younger
}

// replace writes the new binary next to exe and renames it into place, so
// exe is never partially written. Windows cannot overwrite a running
// executable but can rename it, so the old file is moved aside first.
func replace(exe string, bin []byte) error {
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".upd-new-*")
	if err != nil {
		return fmt.Errorf("cannot write next to %s: %w", exe, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		old := exe + ".old"
		_ = os.Remove(old)
		if err := os.Rename(exe, old); err != nil {
			return err
		}
		if err := os.Rename(tmp.Name(), exe); err != nil {
			_ = os.Rename(old, exe)
			return err
		}
		return nil
	}
	return os.Rename(tmp.Name(), exe)
}

// CleanupOld removes the previous binary left behind by a Windows update.
func CleanupOld(exe string) {
	if runtime.GOOS == "windows" {
		_ = os.Remove(exe + ".old")
	}
}

func checksumFor(sums []byte, name string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && f[1] == name {
			return f[0], nil
		}
	}
	return "", fmt.Errorf("checksums.txt has no entry for %s", name)
}

// newer reports whether version a is newer than b, comparing dotted
// numeric parts.
func newer(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(strings.SplitN(pa[i], "-", 2)[0])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(strings.SplitN(pb[i], "-", 2)[0])
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "upd-selfupdate")
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" && strings.HasPrefix(url, "https://api.github.com/") {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(url + ": " + resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func getJSON(ctx context.Context, url string, v any) error {
	data, err := get(ctx, url)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
