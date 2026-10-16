// Package retry holds sabr's backoff policy.
package retry

import (
	"context"
	"math/rand"
	"time"
)

// Policy decides how long to wait between attempts. Max == 0 means retry forever.
type Policy struct {
	Max  int
	Base time.Duration
	Cap  time.Duration
}

// Default retries forever: 1s, 2s, 4s ... capped at 30s, with jitter.
func Default() Policy { return Policy{Max: 0, Base: time.Second, Cap: 30 * time.Second} }

// Exhausted reports whether attempt (1-based count of failures so far) used up the budget.
func (p Policy) Exhausted(failures int) bool { return p.Max > 0 && failures >= p.Max }

// Delay returns the wait after the n-th consecutive failure (n >= 1).
func (p Policy) Delay(n int) time.Duration {
	if p.Base <= 0 {
		return 0
	}
	d := p.Base
	for i := 1; i < n && d < p.Cap; i++ {
		d *= 2
	}
	if p.Cap > 0 && d > p.Cap {
		d = p.Cap
	}
	// ±20% jitter so many clients do not reconnect in lockstep
	j := time.Duration(rand.Int63n(int64(d)/5+1)) - d/10
	return d + j
}

// Sleep waits for Delay(n) or until ctx is done.
func (p Policy) Sleep(ctx context.Context, n int) error {
	t := time.NewTimer(p.Delay(n))
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
