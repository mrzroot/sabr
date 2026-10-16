// Package gitx drives the real git binary to clone and fetch in small, retryable steps.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Git runs git commands in Dir with a stall watchdog.
type Git struct {
	Bin   string
	Dir   string
	Env   []string
	Stall time.Duration // kill a command that prints nothing for this long (0 = never)
	// OnProgress receives git's progress lines (e.g. "Receiving objects: 45% (..)").
	OnProgress func(line string)
	// Trace receives every command line run.
	Trace func(args []string)
}

// ErrStalled means the watchdog killed git because it made no progress.
var ErrStalled = errors.New("no progress (connection stalled)")

// Result of one git command.
type Result struct {
	Stdout   []byte
	Stderr   string
	Received int64 // bytes git reported receiving, when it printed progress
}

// Error wraps a failed git command with its stderr.
type Error struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(lastLines(e.Stderr, 3))
	if msg == "" {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("git %s: %s", strings.Join(short(e.Args), " "), msg)
}
func (e *Error) Unwrap() error { return e.Err }

func short(a []string) []string {
	out := []string{}
	for i := 0; i < len(a); i++ {
		s := a[i]
		if s == "-c" {
			i++
			continue
		}
		out = append(out, s)
		if len(out) == 4 {
			break
		}
	}
	return out
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(s, "\r", "\n")), "\n")
	keep := []string{}
	for i := len(lines) - 1; i >= 0 && len(keep) < n; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" || progressRe.MatchString(l) {
			continue
		}
		keep = append([]string{l}, keep...)
	}
	return strings.Join(keep, " | ")
}

var progressRe = regexp.MustCompile(`^(remote: )?(Enumerating|Counting|Compressing|Receiving|Resolving|Unpacking|Updating|Checking|Total|Filtering)`)
var receivedRe = regexp.MustCompile(`Receiving objects:.*?, ([0-9.]+) (bytes|KiB|MiB|GiB)`)

// Run executes git with args. stdin may be nil.
func (g *Git) Run(ctx context.Context, stdin []byte, args ...string) (*Result, error) {
	bin := g.Bin
	if bin == "" {
		bin = "git"
	}
	if g.Trace != nil {
		g.Trace(args)
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, args...)
	cmd.Dir = g.Dir
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "LANG=C"), g.Env...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out bytes.Buffer
	act := &activity{last: time.Now()}
	cmd.Stdout = io.MultiWriter(&out, act)
	pr, pw := io.Pipe()
	cmd.Stderr = io.MultiWriter(pw, act)
	var errBuf bytes.Buffer
	var received int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		var line []byte
		for {
			n, err := pr.Read(buf)
			for _, b := range buf[:n] {
				errBuf.WriteByte(b)
				if b == '\r' || b == '\n' {
					if len(line) > 0 {
						l := string(line)
						if m := receivedRe.FindStringSubmatch(l); m != nil {
							received = parseSize(m[1], m[2])
						}
						if g.OnProgress != nil && progressRe.MatchString(strings.TrimSpace(l)) {
							g.OnProgress(strings.TrimSpace(l))
						}
					}
					line = line[:0]
				} else {
					line = append(line, b)
				}
			}
			if err != nil {
				return
			}
		}
	}()
	if err := cmd.Start(); err != nil {
		pw.Close()
		<-done
		return nil, &Error{Args: args, Err: err}
	}
	stalled := false
	if g.Stall > 0 {
		go func() {
			t := time.NewTicker(time.Second)
			defer t.Stop()
			for {
				select {
				case <-cctx.Done():
					return
				case <-t.C:
					if act.since() > g.Stall {
						stalled = true
						cancel()
						return
					}
				}
			}
		}()
	}
	err := cmd.Wait()
	pw.Close()
	<-done
	res := &Result{Stdout: out.Bytes(), Stderr: errBuf.String(), Received: received}
	if err != nil {
		if stalled {
			err = ErrStalled
		}
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		return res, &Error{Args: args, Stderr: res.Stderr, Err: err}
	}
	return res, nil
}

// Out runs git and returns trimmed stdout.
func (g *Git) Out(ctx context.Context, args ...string) (string, error) {
	r, err := g.Run(ctx, nil, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(r.Stdout)), nil
}

type activity struct {
	mu   sync.Mutex
	last time.Time
}

func (a *activity) Write(p []byte) (int, error) {
	a.mu.Lock()
	a.last = time.Now()
	a.mu.Unlock()
	return len(p), nil
}
func (a *activity) since() time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	return time.Since(a.last)
}

func parseSize(num, unit string) int64 {
	f, _ := strconv.ParseFloat(num, 64)
	switch unit {
	case "KiB":
		f *= 1024
	case "MiB":
		f *= 1024 * 1024
	case "GiB":
		f *= 1024 * 1024 * 1024
	}
	return int64(f)
}

// Version returns git's version as [major, minor].
func Version(ctx context.Context, bin string) ([2]int, string, error) {
	g := &Git{Bin: bin}
	s, err := g.Out(ctx, "version")
	if err != nil {
		return [2]int{}, "", err
	}
	m := regexp.MustCompile(`(\d+)\.(\d+)`).FindStringSubmatch(s)
	if m == nil {
		return [2]int{}, s, fmt.Errorf("cannot parse %q", s)
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	return [2]int{a, b}, s, nil
}
