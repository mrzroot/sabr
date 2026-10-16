// Package ui prints progress and status lines for sabr.
package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// UI writes human output to stderr. Quiet suppresses everything but errors.
type UI struct {
	Out     io.Writer
	Quiet   bool
	Verbose bool
	color   bool
	mu      sync.Mutex
	live    bool
	lastLen int
}

// New returns a UI on stderr; colour only when stderr looks like a terminal.
func New() *UI {
	u := &UI{Out: os.Stderr}
	if fi, err := os.Stderr.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb" {
		u.color = true
	}
	return u
}

func (u *UI) paint(code, s string) string {
	if !u.color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (u *UI) clearLive() {
	if u.live {
		fmt.Fprint(u.Out, "\r"+strings.Repeat(" ", u.lastLen)+"\r")
		u.live = false
	}
}

func (u *UI) line(prefix, msg string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.clearLive()
	fmt.Fprintln(u.Out, prefix+msg)
}

// Step announces a phase.
func (u *UI) Step(format string, a ...any) {
	if u.Quiet {
		return
	}
	u.line(u.paint("1;33", "==> "), fmt.Sprintf(format, a...))
}

// Info prints an indented detail line.
func (u *UI) Info(format string, a ...any) {
	if u.Quiet {
		return
	}
	u.line("    ", fmt.Sprintf(format, a...))
}

// Debug prints only in verbose mode.
func (u *UI) Debug(format string, a ...any) {
	if !u.Verbose {
		return
	}
	u.line(u.paint("2", "    · "), fmt.Sprintf(format, a...))
}

// OK prints a success line.
func (u *UI) OK(format string, a ...any) {
	if u.Quiet {
		return
	}
	u.line(u.paint("1;32", "  ✓ "), fmt.Sprintf(format, a...))
}

// Warn prints a warning line (shown even when quiet).
func (u *UI) Warn(format string, a ...any) {
	u.line(u.paint("1;35", "  ! "), fmt.Sprintf(format, a...))
}

// Live replaces the current status line (only on terminals).
func (u *UI) Live(format string, a ...any) {
	if u.Quiet || !u.color {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	s := fmt.Sprintf(format, a...)
	if len(s) > 110 {
		s = s[:110]
	}
	fmt.Fprint(u.Out, "\r"+s+strings.Repeat(" ", max(0, u.lastLen-len(s))))
	u.lastLen = len(s)
	u.live = true
}

// Done ends any live line.
func (u *UI) Done() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.clearLive()
}

// Bytes formats a byte count.
func Bytes(n int64) string {
	const k = 1024
	switch {
	case n < k:
		return fmt.Sprintf("%d B", n)
	case n < k*k:
		return fmt.Sprintf("%.1f KiB", float64(n)/k)
	case n < k*k*k:
		return fmt.Sprintf("%.1f MiB", float64(n)/(k*k))
	default:
		return fmt.Sprintf("%.2f GiB", float64(n)/(k*k*k))
	}
}

// Dur formats a duration compactly.
func Dur(d time.Duration) string {
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(100 * time.Millisecond).String()
}
