package gitx

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mrzroot/sabr/internal/httpx"
	"github.com/mrzroot/sabr/internal/retry"
	"github.com/mrzroot/sabr/internal/ui"
)

// Options for a resumable clone.
type Options struct {
	URL        string
	Dir        string
	Branch     string // empty = remote default branch
	Depth      int    // 0 = full history
	AllBlobs   bool   // also download every historical file version (a complete, non-partial clone)
	NoCheckout bool
	NoTags     bool
	NoRaw      bool // disable the raw.githubusercontent.com fallback for single huge files
	Step       int  // initial history step (commits per fetch)
	Batch      int  // initial files per fetch
	Retry      retry.Policy
	Stall      time.Duration
	GitBin     string
	UI         *ui.UI
}

// State is persisted in .git/sabr.json so an interrupted clone continues where it stopped.
type State struct {
	Version   int       `json:"version"`
	URL       string    `json:"url"`
	Branch    string    `json:"branch"`
	Phase     string    `json:"phase"` // history, blobs, checkout, done
	Depth     int       `json:"depth"`
	AllBlobs  bool      `json:"allBlobs"`
	Filter    bool      `json:"filter"` // server + git support partial clone
	Step      int       `json:"step"`
	Batch     int       `json:"batch"`
	Have      int       `json:"have"` // commits of history fetched so far (when a depth limit is set)
	Steps     int       `json:"steps"`
	Failures  int       `json:"failures"`
	Received  int64     `json:"received"`
	Started   time.Time `json:"started"`
	Updated   time.Time `json:"updated"`
	Elapsed   float64   `json:"elapsedSeconds"`
	NoTags    bool      `json:"noTags"`
	TagsDone  bool      `json:"tagsDone"`
	BlobTotal int       `json:"blobTotal"`
}

type cloner struct {
	o     Options
	g     *Git
	st    *State
	path  string
	start time.Time
	u     *ui.UI
}

// StatePath returns the state file for a work tree.
func StatePath(dir string) string { return filepath.Join(dir, ".git", "sabr.json") }

// LoadState reads .git/sabr.json from dir.
func LoadState(dir string) (*State, error) {
	b, err := os.ReadFile(StatePath(dir))
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (c *cloner) save() {
	c.st.Updated = time.Now()
	c.st.Elapsed += time.Since(c.start).Seconds()
	c.start = time.Now()
	b, _ := json.MarshalIndent(c.st, "", "  ")
	tmp := c.path + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, c.path)
	}
}

// fatal git errors that retrying cannot fix
var fatalRe = regexp.MustCompile(`(?i)repository not found|repository not exported|access denied|requested URL returned error: 40[134]|authentication failed|could not read username|permission denied \(publickey|does not appear to be a git repository|not found: did you run git update-server-info|couldn't find remote ref|invalid refspec|unknown option|is not a valid|ambiguous argument`)

// Fatal reports whether err can never succeed on retry.
func Fatal(err error) bool {
	var ge *Error
	if errors.As(err, &ge) {
		return fatalRe.MatchString(ge.Stderr)
	}
	return false
}

// Clone runs or resumes a clone. Calling it again on the same directory continues the work.
func Clone(ctx context.Context, o Options) (*State, error) {
	if o.UI == nil {
		o.UI = ui.New()
	}
	if o.Step <= 0 {
		o.Step = 64
	}
	if o.Batch <= 0 {
		o.Batch = 256
	}
	if o.Stall == 0 {
		o.Stall = 45 * time.Second
	}
	dir, err := filepath.Abs(o.Dir)
	if err != nil {
		return nil, err
	}
	c := &cloner{o: o, u: o.UI, path: StatePath(dir), start: time.Now()}
	c.g = &Git{Bin: o.GitBin, Dir: dir, Stall: o.Stall, Env: []string{"GIT_NO_LAZY_FETCH=1"}}
	c.g.OnProgress = func(l string) { c.u.Live("    %s", l) }
	if o.UI.Verbose {
		c.g.Trace = func(a []string) { c.u.Debug("git %s", strings.Join(a, " ")) }
	}

	if st, err := LoadState(dir); err == nil {
		if o.URL != "" && st.URL != o.URL {
			return nil, fmt.Errorf("%s holds an unfinished clone of %s, not %s", dir, st.URL, o.URL)
		}
		c.st = st
		if st.Phase == "done" {
			c.u.OK("%s is already complete", dir)
			return st, nil
		}
		c.u.Step("Resuming clone of %s (phase: %s, %d steps done)", st.URL, st.Phase, st.Steps)
	} else {
		if o.URL == "" {
			return nil, fmt.Errorf("%s has no unfinished sabr clone", dir)
		}
		if ents, err := os.ReadDir(dir); err == nil && len(ents) > 0 {
			return nil, fmt.Errorf("destination %s exists and is not empty", dir)
		}
		if err := c.init(ctx, dir); err != nil {
			return nil, err
		}
	}

	for c.st.Phase != "done" {
		if err := ctx.Err(); err != nil {
			c.save()
			return c.st, err
		}
		var err error
		switch c.st.Phase {
		case "history":
			err = c.history(ctx)
		case "blobs":
			err = c.blobs(ctx)
		case "checkout":
			err = c.checkout(ctx)
		default:
			return c.st, fmt.Errorf("unknown phase %q in %s", c.st.Phase, c.path)
		}
		c.save()
		if err != nil {
			return c.st, err
		}
	}
	c.u.Done()
	return c.st, nil
}

func (c *cloner) init(ctx context.Context, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	ver, _, err := Version(ctx, c.o.GitBin)
	if err != nil {
		return fmt.Errorf("git not found: %w", err)
	}
	filter := ver[0] > 2 || ver[0] == 2 && ver[1] >= 29
	c.u.Step("Cloning %s into %s", c.o.URL, dir)
	if !filter {
		c.u.Warn("git %d.%d has no partial-clone batching; history still comes in resumable steps (git 2.29+ recommended)", ver[0], ver[1])
	}
	for _, a := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", c.o.URL},
		{"config", "fetch.writeCommitGraph", "false"},
	} {
		if _, err := c.g.Run(ctx, nil, a...); err != nil {
			return err
		}
	}
	if filter {
		for _, a := range [][]string{
			{"config", "core.repositoryformatversion", "1"},
			{"config", "extensions.partialClone", "origin"},
			{"config", "remote.origin.promisor", "true"},
			{"config", "remote.origin.partialclonefilter", "blob:none"},
		} {
			if _, err := c.g.Run(ctx, nil, a...); err != nil {
				return err
			}
		}
	}
	c.st = &State{Version: 1, URL: c.o.URL, Branch: c.o.Branch, Phase: "history", Depth: c.o.Depth, AllBlobs: c.o.AllBlobs,
		Filter: filter, Step: c.o.Step, Batch: c.o.Batch, Started: time.Now(), NoTags: c.o.NoTags}
	c.save()
	if c.st.Branch == "" {
		var out string
		err := c.retry(ctx, "find default branch", func() error {
			r, err := c.g.Run(ctx, nil, "ls-remote", "--symref", "origin", "HEAD")
			if err != nil {
				return err
			}
			out = string(r.Stdout)
			return nil
		})
		if err != nil {
			return err
		}
		m := regexp.MustCompile(`ref: refs/heads/(\S+)\s+HEAD`).FindStringSubmatch(out)
		if m == nil {
			return fmt.Errorf("remote did not report a default branch; pass --branch")
		}
		c.st.Branch = m[1]
	}
	c.u.Info("branch %s", c.st.Branch)
	c.save()
	return nil
}

// retry runs fn until it succeeds, a fatal error happens, or the policy gives up.
func (c *cloner) retry(ctx context.Context, what string, fn func() error) error {
	fails := 0
	for {
		err := fn()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if Fatal(err) {
			return err
		}
		fails++
		c.st.Failures++
		c.save()
		if c.o.Retry.Exhausted(fails) {
			return fmt.Errorf("%s: gave up after %d attempts: %w", what, fails, err)
		}
		d := c.o.Retry.Delay(fails)
		c.u.Warn("%s failed (%s); retrying in %s, nothing downloaded so far is lost", what, oneLine(err), ui.Dur(d))
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

func oneLine(err error) string {
	s := err.Error()
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return strings.ReplaceAll(s, "\n", " ")
}

func (c *cloner) shallow() bool {
	_, err := os.Stat(filepath.Join(c.g.Dir, ".git", "shallow"))
	return err == nil
}

func (c *cloner) refspec() string {
	return "+refs/heads/" + c.st.Branch + ":refs/remotes/origin/" + c.st.Branch
}

func (c *cloner) fetchArgs(extra ...string) []string {
	a := []string{"-c", "gc.auto=0", "-c", "maintenance.auto=false", "fetch", "--progress", "--no-write-fetch-head", "--recurse-submodules=no"}
	if c.st.Filter {
		a = append(a, "--filter=blob:none")
	}
	return append(a, extra...)
}

func (c *cloner) haveTip(ctx context.Context) bool {
	_, err := c.g.Out(ctx, "rev-parse", "-q", "--verify", "refs/remotes/origin/"+c.st.Branch)
	return err == nil
}

func (c *cloner) noteFetch(r *Result) {
	if r == nil {
		return
	}
	c.st.Received += r.Received
	if c.st.Filter && strings.Contains(r.Stderr, "filtering not recognized by server") {
		c.u.Warn("server does not support partial clone; each history step will include file contents")
		c.st.Filter = false
	}
}

// history fetches commits and trees (no file contents) newest first, in growing steps.
func (c *cloner) history(ctx context.Context) error {
	if !c.haveTip(ctx) {
		c.u.Step("Fetching the latest commit")
		err := c.retry(ctx, "fetch latest commit", func() error {
			r, err := c.g.Run(ctx, nil, c.fetchArgs("--no-tags", "--depth=1", "origin", c.refspec())...)
			c.noteFetch(r)
			return err
		})
		if err != nil {
			return err
		}
		c.st.Steps++
		c.st.Have = 1
		c.u.Done()
		c.u.OK("latest commit %s", c.short(ctx))
		c.save()
	}
	if c.st.Depth != 1 && c.shallow() {
		c.u.Step("Fetching history in resumable steps (commits and folders only)")
	}
	fails := 0
	for c.shallow() && (c.st.Depth == 0 || c.st.Have < c.st.Depth) {
		step := c.st.Step
		if c.st.Depth > 0 && c.st.Have+step > c.st.Depth {
			step = c.st.Depth - c.st.Have
		}
		before := shallowSet(c.g.Dir)
		r, err := c.g.Run(ctx, nil, c.fetchArgs("--no-tags", fmt.Sprintf("--deepen=%d", step), "origin", c.refspec())...)
		c.noteFetch(r)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if Fatal(err) {
				return err
			}
			fails++
			c.st.Failures++
			if c.st.Step > 1 {
				c.st.Step = max(1, c.st.Step/2)
			}
			c.save()
			if c.o.Retry.Exhausted(fails) {
				return fmt.Errorf("history: gave up after %d attempts: %w", fails, err)
			}
			d := c.o.Retry.Delay(fails)
			c.u.Warn("step of %d commits failed (%s); next try %d commits in %s", step, oneLine(err), c.st.Step, ui.Dur(d))
			if err := sleep(ctx, d); err != nil {
				return err
			}
			continue
		}
		fails = 0
		c.st.Steps++
		c.st.Have += step
		after := shallowSet(c.g.Dir)
		c.u.Done()
		if c.shallow() {
			c.u.Info("+%d commits (%d so far)", step, c.st.Have)
		} else {
			c.u.OK("full history fetched (%d steps)", c.st.Steps)
		}
		if c.shallow() && sameSet(before, after) {
			// the server could not deepen further; ask for the rest explicitly
			r, err := c.g.Run(ctx, nil, c.fetchArgs("--no-tags", "--unshallow", "origin", c.refspec())...)
			c.noteFetch(r)
			if err != nil && !Fatal(err) {
				continue
			}
		}
		if c.st.Step < 1<<16 {
			c.st.Step *= 2
		}
		c.save()
	}
	if !c.st.NoTags && !c.shallow() && !c.st.TagsDone {
		c.u.Step("Fetching tags")
		err := c.retry(ctx, "fetch tags", func() error {
			r, err := c.g.Run(ctx, nil, c.fetchArgs("--tags", "origin")...)
			c.noteFetch(r)
			return err
		})
		if err != nil {
			return err
		}
		c.u.Done()
		c.st.TagsDone = true
	}
	if c.st.Filter && !c.o.NoCheckout || c.st.Filter && c.st.AllBlobs {
		c.st.Phase = "blobs"
	} else {
		c.st.Phase = "checkout"
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func shallowSet(dir string) string {
	b, _ := os.ReadFile(filepath.Join(dir, ".git", "shallow"))
	return string(b)
}
func sameSet(a, b string) bool { return a == b }

func (c *cloner) short(ctx context.Context) string {
	s, _ := c.g.Out(ctx, "log", "-1", "--format=%h %s", "refs/remotes/origin/"+c.st.Branch)
	if len(s) > 72 {
		s = s[:72] + "…"
	}
	return s
}

// Missing lists blob ids that are not in the local object store yet.
func (c *cloner) missing(ctx context.Context) ([]string, error) {
	args := []string{"rev-list", "--objects", "--missing=print"}
	if c.st.AllBlobs {
		args = append(args, "--all")
	} else {
		args = append(args, "-n", "1", "refs/remotes/origin/"+c.st.Branch)
	}
	r, err := c.g.Run(ctx, nil, args...)
	if err != nil {
		return nil, err
	}
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(r.Stdout))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if l := sc.Text(); strings.HasPrefix(l, "?") {
			out = append(out, strings.TrimSpace(l[1:]))
		}
	}
	return out, sc.Err()
}

// blobs downloads file contents in adaptive batches: bigger after a success, smaller after a drop.
func (c *cloner) blobs(ctx context.Context) error {
	todo, err := c.missing(ctx)
	if err != nil {
		return err
	}
	if c.st.BlobTotal == 0 || len(todo) > c.st.BlobTotal {
		c.st.BlobTotal = len(todo)
	}
	if len(todo) > 0 {
		what := "files of the latest commit"
		if c.st.AllBlobs {
			what = "every version of every file"
		}
		c.u.Step("Fetching %s: %d to go", what, len(todo))
	}
	fails, oneFails := 0, 0
	var paths map[string]string
	for len(todo) > 0 {
		n := min(c.st.Batch, len(todo))
		batch := todo[:n]
		// noop negotiation: without it the client advertises the commits it has, and the
		// server leaves out blobs reachable from them, which are exactly the ones we want
		r, err := c.g.Run(ctx, []byte(strings.Join(batch, "\n")+"\n"),
			append([]string{"-c", "fetch.negotiationAlgorithm=noop"}, c.fetchArgs("--no-tags", "--stdin", "origin")...)...)
		c.noteFetch(r)
		if err == nil {
			todo = todo[n:]
			c.st.Steps++
			fails, oneFails = 0, 0
			if c.st.Batch < 1<<15 {
				c.st.Batch *= 2
			}
			done := c.st.BlobTotal - len(todo)
			c.u.Done()
			c.u.Info("%d/%d files (%d%%)", done, c.st.BlobTotal, done*100/max(1, c.st.BlobTotal))
			c.save()
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if Fatal(err) {
			return err
		}
		fails++
		c.st.Failures++
		if n == 1 {
			oneFails++
		}
		c.st.Batch = max(1, c.st.Batch/4)
		c.save()
		// a single file that never makes it through: fetch it over plain HTTPS with byte ranges
		if n == 1 && oneFails >= 3 && !c.o.NoRaw {
			if paths == nil {
				paths = c.paths(ctx)
			}
			if ok := c.rawFallback(ctx, batch[0], paths); ok {
				todo = todo[1:]
				oneFails = 0
				continue
			}
		}
		if c.o.Retry.Exhausted(fails) {
			return fmt.Errorf("files: gave up after %d attempts: %w", fails, err)
		}
		d := c.o.Retry.Delay(fails)
		c.u.Warn("batch of %d files failed (%s); next batch %d files in %s", n, oneLine(err), c.st.Batch, ui.Dur(d))
		if err := sleep(ctx, d); err != nil {
			return err
		}
	}
	c.u.Done()
	c.st.Phase = "checkout"
	if c.o.NoCheckout && !c.st.AllBlobs {
		c.st.Phase = "checkout"
	}
	return nil
}

func (c *cloner) paths(ctx context.Context) map[string]string {
	m := map[string]string{}
	r, err := c.g.Run(ctx, nil, "ls-tree", "-r", "-z", "refs/remotes/origin/"+c.st.Branch)
	if err != nil {
		return m
	}
	for _, rec := range bytes.Split(r.Stdout, []byte{0}) {
		meta, path, ok := bytes.Cut(rec, []byte{'\t'})
		if !ok {
			continue
		}
		f := strings.Fields(string(meta))
		if len(f) == 3 && f[1] == "blob" {
			m[f[2]] = string(path)
		}
	}
	return m
}

var ghRe = regexp.MustCompile(`^(?:https://|git@)github\.com[/:]([^/]+)/([^/]+?)(?:\.git)?/?$`)

// rawFallback downloads one file of the latest commit from raw.githubusercontent.com with HTTP
// range requests (so even a huge file survives drops), then stores it as the exact git blob.
func (c *cloner) rawFallback(ctx context.Context, oid string, paths map[string]string) bool {
	m := ghRe.FindStringSubmatch(c.st.URL)
	path, ok := paths[oid]
	if m == nil || !ok {
		return false
	}
	commit, err := c.g.Out(ctx, "rev-parse", "refs/remotes/origin/"+c.st.Branch)
	if err != nil {
		return false
	}
	segs := strings.Split(path, "/")
	for i := range segs {
		segs[i] = url.PathEscape(segs[i])
	}
	u := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", m[1], m[2], commit, strings.Join(segs, "/"))
	tmp := filepath.Join(c.g.Dir, ".git", "sabr-raw", oid)
	_ = os.MkdirAll(filepath.Dir(tmp), 0o755)
	c.u.Step("%s keeps dropping over git; downloading it in byte ranges instead", path)
	d := &httpx.Downloader{Retry: c.o.Retry, Stall: c.o.Stall, UI: c.u}
	if _, err := d.Get(ctx, u, tmp); err != nil {
		c.u.Warn("range download failed: %s", oneLine(err))
		return false
	}
	got, err := c.g.Out(ctx, "hash-object", "-w", "--no-filters", tmp)
	_ = os.Remove(tmp)
	if err != nil || got != oid {
		c.u.Warn("downloaded file does not match git object %s (got %s); falling back to git", oid[:10], got)
		return false
	}
	c.u.OK("%s stored and verified (%s)", path, oid[:10])
	return true
}

func (c *cloner) checkout(ctx context.Context) error {
	b := c.st.Branch
	if !c.o.NoCheckout {
		c.u.Step("Checking out %s", b)
		if _, err := c.g.Run(ctx, nil, "checkout", "-q", "-f", "-B", b, "refs/remotes/origin/"+b); err != nil {
			// something is still missing: go back and fetch it
			if c.st.Filter {
				c.u.Warn("checkout needs more files; fetching them")
				c.st.Phase = "blobs"
				return nil
			}
			return err
		}
	} else {
		if _, err := c.g.Run(ctx, nil, "update-ref", "refs/heads/"+b, "refs/remotes/origin/"+b); err != nil {
			return err
		}
		_, _ = c.g.Run(ctx, nil, "symbolic-ref", "HEAD", "refs/heads/"+b)
	}
	_, _ = c.g.Run(ctx, nil, "config", "branch."+b+".remote", "origin")
	_, _ = c.g.Run(ctx, nil, "config", "branch."+b+".merge", "refs/heads/"+b)
	_, _ = c.g.Run(ctx, nil, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/"+b)
	_, _ = c.g.Run(ctx, nil, "-c", "gc.autoDetach=false", "gc", "--auto", "--quiet")
	c.st.Phase = "done"
	return nil
}
