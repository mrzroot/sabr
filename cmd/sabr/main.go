// Command sabr (صبر, "patience"): downloads that never start over.
// Resumable git clone, docker pull and file downloads for slow, throttled and unreliable networks.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mrzroot/sabr/internal/chaos"
	"github.com/mrzroot/sabr/internal/gitx"
	"github.com/mrzroot/sabr/internal/httpx"
	"github.com/mrzroot/sabr/internal/registry"
	"github.com/mrzroot/sabr/internal/retry"
	"github.com/mrzroot/sabr/internal/ui"
)

var version = "dev"

const usage = `sabr (صبر) — downloads that never start over

Usage:
  sabr clone <url> [dir]        resumable git clone (re-run the same command to continue)
  sabr resume [dir]             continue an interrupted clone
  sabr pull [dir]               resumable fetch + fast-forward of an existing clone
  sabr status [dir]             show the progress of a clone
  sabr get <url> [-o file]      resumable HTTP(S) download (byte ranges, retries, sha256)
  sabr docker pull <image>      resumable image pull -> docker-loadable archive (--load to import)
  sabr chaos [--cut 2MB]        a deliberately flaky proxy, to test any tool on a bad connection
  sabr version

Common flags:
  --retries N     give up after N failed attempts in a row (default 0 = never give up)
  --stall 45s     treat a connection with no data for this long as dropped
  -q, --quiet     only warnings and errors
  -v, --verbose   show every git command

clone flags:
  -b, --branch B  branch to clone (default: the remote's default branch)
  --depth N       only the last N commits
  --all-blobs     also download every old version of every file (full, non-partial clone)
  --no-tags       skip tags
  --no-checkout   do not check out files
  --step N        commits per history step to start with (default 64, adapts)
  --batch N       files per batch to start with (default 256, adapts)
  --no-raw        never fall back to raw.githubusercontent.com byte ranges for a huge file

Docs: https://mrzroot.github.io/sabr/
`

type common struct {
	retries int
	stall   time.Duration
	quiet   bool
	verbose bool
}

func (c *common) register(fs *flag.FlagSet) {
	fs.IntVar(&c.retries, "retries", 0, "")
	fs.DurationVar(&c.stall, "stall", 45*time.Second, "")
	fs.BoolVar(&c.quiet, "quiet", false, "")
	fs.BoolVar(&c.quiet, "q", false, "")
	fs.BoolVar(&c.verbose, "verbose", false, "")
	fs.BoolVar(&c.verbose, "v", false, "")
}

func (c *common) ui() *ui.UI {
	u := ui.New()
	u.Quiet, u.Verbose = c.quiet, c.verbose
	return u
}

func (c *common) policy() retry.Policy {
	p := retry.Default()
	p.Max = c.retries
	if s := os.Getenv("SABR_RETRY_BASE"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			p.Base = d
			p.Cap = 8 * d
		}
	}
	return p
}

// parse lets flags appear before or after positional arguments.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if strings.Contains(name, "=") {
				continue
			}
			if f := fs.Lookup(name); f != nil {
				if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
					continue
				}
				if i+1 < len(args) {
					flags = append(flags, args[i+1])
					i++
				}
			}
			continue
		}
		pos = append(pos, a)
	}
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	if err := fs.Parse(flags); err != nil {
		return nil, err
	}
	return pos, nil
}

func main() {
	httpx.Version = version
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code := run(ctx, os.Args[1:])
	os.Exit(code)
}

func run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	var err error
	switch args[0] {
	case "clone":
		err = cmdClone(ctx, args[1:])
	case "resume":
		err = cmdResume(ctx, args[1:])
	case "pull":
		err = cmdPull(ctx, args[1:])
	case "status":
		err = cmdStatus(args[1:])
	case "get":
		err = cmdGet(ctx, args[1:])
	case "docker":
		if len(args) < 2 || args[1] != "pull" {
			fmt.Fprintln(os.Stderr, "usage: sabr docker pull <image> [--platform linux/amd64] [-o file.tar] [--load]")
			return 2
		}
		err = cmdDocker(ctx, args[2:])
	case "chaos":
		err = cmdChaos(ctx, args[1:])
	case "version", "--version", "-V":
		fmt.Println(version)
		return 0
	case "help", "--help", "-h":
		fmt.Print(usage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "sabr: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	if err == nil {
		return 0
	}
	if errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "\nInterrupted. Everything downloaded so far is kept; run the same command again to continue.")
		return 130
	}
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	fmt.Fprintln(os.Stderr, "sabr: "+err.Error())
	return 1
}

func defaultDir(u string) string {
	u = strings.TrimSuffix(strings.TrimRight(u, "/"), ".git")
	if i := strings.LastIndexAny(u, "/:"); i >= 0 {
		u = u[i+1:]
	}
	if u == "" {
		u = "repo"
	}
	return u
}

func cmdClone(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("clone", flag.ContinueOnError)
	var c common
	c.register(fs)
	var o gitx.Options
	fs.StringVar(&o.Branch, "branch", "", "")
	fs.StringVar(&o.Branch, "b", "", "")
	fs.IntVar(&o.Depth, "depth", 0, "")
	fs.BoolVar(&o.AllBlobs, "all-blobs", false, "")
	fs.BoolVar(&o.NoTags, "no-tags", false, "")
	fs.BoolVar(&o.NoCheckout, "no-checkout", false, "")
	fs.BoolVar(&o.NoRaw, "no-raw", false, "")
	fs.IntVar(&o.Step, "step", 64, "")
	fs.IntVar(&o.Batch, "batch", 256, "")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 1 || len(pos) > 2 {
		return errors.New("usage: sabr clone <url> [dir]")
	}
	o.URL = pos[0]
	o.Dir = defaultDir(o.URL)
	if len(pos) == 2 {
		o.Dir = pos[1]
	}
	o.Retry, o.Stall, o.UI = c.policy(), c.stall, c.ui()
	return finishClone(ctx, o)
}

func finishClone(ctx context.Context, o gitx.Options) error {
	start := time.Now()
	st, err := gitx.Clone(ctx, o)
	if err != nil {
		return err
	}
	o.UI.OK("done: %s (%s, %d steps, %d drops survived, %s received)", o.Dir, ui.Dur(time.Since(start)), st.Steps, st.Failures, ui.Bytes(st.Received))
	if !st.AllBlobs && st.Filter {
		o.UI.Info("history is complete; old versions of files download on demand (git log -p, blame). Use --all-blobs for everything up front.")
	}
	return nil
}

func cmdResume(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	var c common
	c.register(fs)
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	dir := "."
	if len(pos) > 0 {
		dir = pos[0]
	}
	st, err := gitx.LoadState(dir)
	if err != nil {
		return fmt.Errorf("%s has no unfinished sabr clone", dir)
	}
	return finishClone(ctx, gitx.Options{URL: st.URL, Dir: dir, Retry: c.policy(), Stall: c.stall, UI: c.ui(), NoTags: st.NoTags, AllBlobs: st.AllBlobs})
}

func cmdPull(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("pull", flag.ContinueOnError)
	var c common
	c.register(fs)
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	dir := "."
	if len(pos) > 0 {
		dir = pos[0]
	}
	return gitx.Pull(ctx, gitx.Options{Dir: dir, Retry: c.policy(), Stall: c.stall, UI: c.ui()})
}

func cmdStatus(args []string) error {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	st, err := gitx.LoadState(dir)
	if err != nil {
		return fmt.Errorf("%s has no sabr clone", dir)
	}
	fmt.Printf("url       %s\nbranch    %s\nphase     %s\nsteps     %d\ndrops     %d survived\nreceived  %s\nhistory   %s\n",
		st.URL, st.Branch, st.Phase, st.Steps, st.Failures, ui.Bytes(st.Received), map[bool]string{true: "complete", false: "partial"}[st.Phase != "history"])
	if st.BlobTotal > 0 {
		fmt.Printf("files     %d to fetch at start of the files phase\n", st.BlobTotal)
	}
	return nil
}

func cmdGet(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("get", flag.ContinueOnError)
	var c common
	c.register(fs)
	var out, sum string
	fs.StringVar(&out, "o", "", "")
	fs.StringVar(&out, "output", "", "")
	fs.StringVar(&sum, "sha256", "", "")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: sabr get <url> [-o file] [--sha256 hex]")
	}
	u := pos[0]
	if out == "" {
		out = defaultDir(strings.SplitN(u, "?", 2)[0])
		if out == "" || out == "repo" {
			out = "download"
		}
	}
	ux := c.ui()
	ux.Step("Downloading %s", u)
	d := &httpx.Downloader{Retry: c.policy(), Stall: c.stall, UI: ux, SHA256: sum}
	r, err := d.Get(ctx, u, out)
	if err != nil {
		return err
	}
	note := ""
	if r.Resumes > 0 {
		note = fmt.Sprintf(", resumed %d×", r.Resumes)
	}
	if r.Reused {
		ux.OK("%s already complete (sha256 matches)", out)
	} else {
		ux.OK("%s (%s in %s%s)", out, ui.Bytes(r.Size), ui.Dur(r.Elapsed), note)
	}
	if sum != "" {
		ux.Info("sha256 verified")
	}
	return nil
}

func cmdDocker(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("docker pull", flag.ContinueOnError)
	var c common
	c.register(fs)
	var o registry.Options
	var load bool
	fs.StringVar(&o.Platform, "platform", "", "")
	fs.StringVar(&o.Output, "o", "", "")
	fs.StringVar(&o.Output, "output", "", "")
	fs.StringVar(&o.CacheDir, "cache", "", "")
	fs.BoolVar(&o.Insecure, "insecure", false, "")
	fs.IntVar(&o.Jobs, "jobs", 3, "")
	fs.BoolVar(&load, "load", false, "")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: sabr docker pull <image> [--platform os/arch] [-o file.tar] [--load]")
	}
	o.Retry, o.Stall, o.UI = c.policy(), c.stall, c.ui()
	ref, err := registry.ParseRef(pos[0])
	if err != nil {
		return err
	}
	o.Username, o.Password = os.Getenv("SABR_REGISTRY_USER"), os.Getenv("SABR_REGISTRY_PASSWORD")
	if o.Username == "" {
		o.Username, o.Password = registry.DockerAuth(ref.Registry)
	}
	start := time.Now()
	r, err := registry.Pull(ctx, pos[0], o)
	if err != nil {
		return err
	}
	o.UI.OK("%s -> %s (%d layers, %s, %d cached, %d resumes, %s)", r.Ref.Name(), r.Archive, r.Layers, ui.Bytes(r.Bytes), r.Reused, r.Resumes, ui.Dur(time.Since(start)))
	if load {
		bin := "docker"
		if _, err := exec.LookPath(bin); err != nil {
			if _, err2 := exec.LookPath("podman"); err2 == nil {
				bin = "podman"
			} else {
				return errors.New("--load: neither docker nor podman is on PATH; the archive is ready to copy and `docker load -i` elsewhere")
			}
		}
		o.UI.Step("%s load -i %s", bin, r.Archive)
		cmd := exec.CommandContext(ctx, bin, "load", "-i", r.Archive)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s load: %w", bin, err)
		}
		o.UI.OK("loaded %s", r.Ref.Name())
	} else {
		abs, _ := filepath.Abs(r.Archive)
		o.UI.Info("import it with:  docker load -i %s", abs)
	}
	return nil
}

func cmdChaos(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("chaos", flag.ContinueOnError)
	var listen, cut, to string
	var jitter float64
	var freeze bool
	fs.StringVar(&listen, "listen", "127.0.0.1:8899", "")
	fs.StringVar(&cut, "cut", "2MB", "")
	fs.StringVar(&to, "to", "", "")
	fs.Float64Var(&jitter, "jitter", 0.5, "")
	fs.BoolVar(&freeze, "freeze", false, "")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	n, err := parseSize(cut)
	if err != nil {
		return err
	}
	p := &chaos.Proxy{Cut: n, Jitter: jitter, Freeze: freeze, To: to, Log: func(f string, a ...any) { fmt.Fprintf(os.Stderr, "  ✂ "+f+"\n", a...) }}
	addr, err := p.Listen(listen)
	if err != nil {
		return err
	}
	mode := "cut"
	if freeze {
		mode = "freeze"
	}
	fmt.Fprintf(os.Stderr, "chaos proxy on %s: every connection is %s after ~%s\n", addr, mode+"d", cut)
	if to == "" {
		fmt.Fprintf(os.Stderr, "try it:\n  HTTPS_PROXY=http://%s git -c http.proxy=http://%s clone https://github.com/git/git    # starts over, fails\n  HTTPS_PROXY=http://%s sabr clone https://github.com/git/git                                   # finishes\n", addr, addr, addr)
	} else {
		fmt.Fprintf(os.Stderr, "TCP mode: connections to %s are forwarded to %s\n", addr, to)
	}
	<-ctx.Done()
	fmt.Fprintf(os.Stderr, "\n%d connections, %d cut, %s forwarded\n", p.Conns.Load(), p.Cuts.Load(), ui.Bytes(p.Bytes.Load()))
	p.Close()
	return nil
}

func parseSize(s string) (int64, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	mult := int64(1)
	for _, u := range []struct {
		suf string
		m   int64
	}{{"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}, {"G", 1 << 30}, {"M", 1 << 20}, {"K", 1 << 10}, {"B", 1}} {
		if strings.HasSuffix(s, u.suf) {
			mult = u.m
			s = strings.TrimSuffix(s, u.suf)
			break
		}
	}
	var f float64
	if _, err := fmt.Sscanf(s, "%g", &f); err != nil {
		return 0, fmt.Errorf("bad size %q", s)
	}
	return int64(f * float64(mult)), nil
}
