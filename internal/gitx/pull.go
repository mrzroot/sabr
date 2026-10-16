package gitx

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mrzroot/sabr/internal/ui"
)

// Pull updates an existing clone: fetch with retries, missing files in batches, then fast-forward.
func Pull(ctx context.Context, o Options) error {
	if o.UI == nil {
		o.UI = ui.New()
	}
	if o.Batch <= 0 {
		o.Batch = 256
	}
	if o.Stall == 0 {
		o.Stall = 45 * time.Second
	}
	dir, err := filepath.Abs(o.Dir)
	if err != nil {
		return err
	}
	g := &Git{Bin: o.GitBin, Dir: dir, Stall: o.Stall, Env: []string{"GIT_NO_LAZY_FETCH=1"}}
	c := &cloner{o: o, u: o.UI, g: g, path: filepath.Join(dir, ".git", "sabr-pull.json"), start: time.Now()}
	g.OnProgress = func(l string) { c.u.Live("    %s", l) }
	branch := o.Branch
	if branch == "" {
		if branch, err = g.Out(ctx, "symbolic-ref", "--short", "HEAD"); err != nil {
			return fmt.Errorf("not on a branch: %w", err)
		}
	}
	partial, _ := g.Out(ctx, "config", "--get", "extensions.partialClone")
	c.st = &State{Branch: branch, Filter: strings.TrimSpace(partial) != "", Batch: o.Batch, Step: 64}
	c.u.Step("Fetching %s", branch)
	err = c.retry(ctx, "fetch", func() error {
		r, err := g.Run(ctx, nil, c.fetchArgs("--no-tags", "origin", c.refspec())...)
		c.noteFetch(r)
		return err
	})
	if err != nil {
		return err
	}
	c.u.Done()
	if c.st.Filter {
		if err := c.blobs(ctx); err != nil {
			return err
		}
	}
	c.u.Step("Fast-forwarding %s", branch)
	if _, err := g.Run(ctx, nil, "merge", "--ff-only", "-q", "refs/remotes/origin/"+branch); err != nil {
		return err
	}
	c.u.OK("%s is at %s", branch, c.short(ctx))
	return nil
}
