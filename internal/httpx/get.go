// Package httpx downloads files over HTTP(S) with byte-range resume, retries and a stall watchdog.
package httpx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mrzroot/sabr/internal/retry"
	"github.com/mrzroot/sabr/internal/ui"
)

// Version is sent in the User-Agent header.
var Version = "dev"

// Downloader fetches one URL into a file, resuming partial downloads.
type Downloader struct {
	Client *http.Client
	Retry  retry.Policy
	Stall  time.Duration // abort a connection that delivers no byte for this long
	UI     *ui.UI
	// Before is called on every request (e.g. to add an Authorization header).
	Before func(*http.Request)
	// Unauthorized is called on HTTP 401; return nil to retry (e.g. after refreshing a token).
	Unauthorized func() error
	SHA256       string // expected hex digest of the whole file, verified at the end
	Label        string
}

// Result describes a finished download.
type Result struct {
	Size     int64
	Resumes  int // times the download continued from a partial file
	Attempts int
	Reused   bool // the file was already complete
	Elapsed  time.Duration
}

type meta struct {
	URL          string `json:"url"`
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"lastModified,omitempty"`
	Size         int64  `json:"size"`
}

// ErrFatal wraps errors that retrying cannot fix (404, 403, digest mismatch ...).
type ErrFatal struct{ Err error }

func (e *ErrFatal) Error() string { return e.Err.Error() }
func (e *ErrFatal) Unwrap() error { return e.Err }

func (d *Downloader) client() *http.Client {
	if d.Client != nil {
		return d.Client
	}
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: true,
		TLSHandshakeTimeout: 30 * time.Second, ResponseHeaderTimeout: 60 * time.Second, IdleConnTimeout: 30 * time.Second}}
}

// Get downloads url to dest. A dest.part file holds progress between runs.
func (d *Downloader) Get(ctx context.Context, url, dest string) (*Result, error) {
	if d.UI == nil {
		d.UI = ui.New()
	}
	if d.Stall == 0 {
		d.Stall = 45 * time.Second
	}
	start := time.Now()
	res := &Result{}
	if d.SHA256 != "" {
		if fi, err := os.Stat(dest); err == nil && fi.Mode().IsRegular() {
			if sum, err := fileSHA(dest); err == nil && sum == strings.ToLower(d.SHA256) {
				res.Size, res.Reused = fi.Size(), true
				return res, nil
			}
		}
	}
	part, metaPath := dest+".part", dest+".part.json"
	var m meta
	if b, err := os.ReadFile(metaPath); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	if m.URL != "" && m.URL != url && d.SHA256 == "" {
		_ = os.Remove(part)
		m = meta{}
	}
	m.URL = url
	fails := 0
	for {
		res.Attempts++
		have := fileSize(part)
		if have > 0 {
			res.Resumes++
		}
		done, err := d.once(ctx, url, part, &m, have)
		_ = writeMeta(metaPath, m)
		if err == nil && done {
			break
		}
		if err == nil {
			err = errors.New("connection closed early")
		}
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		var fe *ErrFatal
		if errors.As(err, &fe) {
			return res, err
		}
		fails++
		if d.Retry.Exhausted(fails) {
			return res, fmt.Errorf("gave up after %d attempts: %w", fails, err)
		}
		// a failure that still moved bytes forward resets the backoff
		if fileSize(part) > have {
			fails = 1
		}
		wait := d.Retry.Delay(fails)
		d.UI.Done()
		d.UI.Warn("%s: %s at %s; resuming in %s", d.label(url), short(err), ui.Bytes(fileSize(part)), ui.Dur(wait))
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return res, ctx.Err()
		case <-t.C:
		}
	}
	d.UI.Done()
	if d.SHA256 != "" {
		sum, err := fileSHA(part)
		if err != nil {
			return res, err
		}
		if sum != strings.ToLower(d.SHA256) {
			_ = os.Remove(part)
			_ = os.Remove(metaPath)
			return res, &ErrFatal{fmt.Errorf("sha256 mismatch: expected %s, got %s (partial file removed)", d.SHA256, sum)}
		}
	}
	if err := os.Rename(part, dest); err != nil {
		return res, err
	}
	_ = os.Remove(metaPath)
	res.Size = fileSize(dest)
	res.Elapsed = time.Since(start)
	return res, nil
}

func (d *Downloader) label(url string) string {
	if d.Label != "" {
		return d.Label
	}
	if i := strings.LastIndex(url, "/"); i >= 0 && i < len(url)-1 {
		s := url[i+1:]
		if j := strings.IndexAny(s, "?#"); j >= 0 {
			s = s[:j]
		}
		if len(s) > 40 {
			s = s[:40] + "…"
		}
		return s
	}
	return url
}

func short(err error) string {
	s := err.Error()
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

// once performs a single request. done reports the file is complete.
func (d *Downloader) once(ctx context.Context, url, part string, m *meta, have int64) (bool, error) {
	if m.Size > 0 && have == m.Size {
		return true, nil
	}
	if m.Size > 0 && have > m.Size {
		_ = os.Truncate(part, 0)
		have = 0
	}
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, url, nil)
	if err != nil {
		return false, &ErrFatal{err}
	}
	req.Header.Set("User-Agent", "sabr/"+Version+" (+https://github.com/mrzroot/sabr)")
	req.Header.Set("Accept-Encoding", "identity")
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
		if m.ETag != "" && !strings.HasPrefix(m.ETag, "W/") {
			req.Header.Set("If-Range", m.ETag)
		} else if m.LastModified != "" {
			req.Header.Set("If-Range", m.LastModified)
		}
	}
	if d.Before != nil {
		d.Before(req)
	}
	resp, err := d.client().Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized && d.Unauthorized != nil:
		if err := d.Unauthorized(); err != nil {
			return false, &ErrFatal{err}
		}
		return false, errors.New("token refreshed")
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		if total := totalFromContentRange(resp.Header.Get("Content-Range")); total >= 0 && total == have {
			m.Size = total
			return true, nil
		}
		_ = os.Truncate(part, 0)
		return false, errors.New("server rejected the range; starting over")
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode >= 500:
		return false, fmt.Errorf("HTTP %d", resp.StatusCode)
	case resp.StatusCode >= 400:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return false, &ErrFatal{fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, req.URL.Host, strings.TrimSpace(string(body)))}
	}
	flags := os.O_CREATE | os.O_WRONLY
	offset := int64(0)
	if resp.StatusCode == http.StatusPartialContent {
		start := startFromContentRange(resp.Header.Get("Content-Range"))
		if start != have {
			_ = os.Truncate(part, 0)
			return false, fmt.Errorf("server resumed at byte %d instead of %d", start, have)
		}
		offset = have
		if t := totalFromContentRange(resp.Header.Get("Content-Range")); t > 0 {
			m.Size = t
		}
	} else {
		// 200: full body (fresh download, or the file changed / server ignores ranges)
		flags |= os.O_TRUNC
		m.Size = resp.ContentLength
		if m.Size < 0 {
			m.Size = 0
		}
	}
	if et := resp.Header.Get("ETag"); et != "" {
		m.ETag = et
	}
	if lm := resp.Header.Get("Last-Modified"); lm != "" {
		m.LastModified = lm
	}
	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return false, &ErrFatal{err}
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return false, &ErrFatal{err}
	}
	var got atomic.Int64
	got.Store(offset)
	lastByte := atomic.Int64{}
	lastByte.Store(time.Now().UnixNano())
	stalled := atomic.Bool{}
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		prev, prevT := got.Load(), time.Now()
		for {
			select {
			case <-stop:
				return
			case now := <-t.C:
				if time.Duration(now.UnixNano()-lastByte.Load()) > d.Stall {
					stalled.Store(true)
					cancel()
					return
				}
				cur := got.Load()
				if now.Sub(prevT) >= time.Second {
					speed := float64(cur-prev) / now.Sub(prevT).Seconds()
					prev, prevT = cur, now
					if m.Size > 0 {
						d.UI.Live("    %s  %s / %s  %d%%  %s/s", d.label(url), ui.Bytes(cur), ui.Bytes(m.Size), cur*100/m.Size, ui.Bytes(int64(speed)))
					} else {
						d.UI.Live("    %s  %s  %s/s", d.label(url), ui.Bytes(cur), ui.Bytes(int64(speed)))
					}
				}
			}
		}
	}()
	buf := make([]byte, 64*1024)
	var rerr error
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				rerr = &ErrFatal{werr}
				break
			}
			got.Add(int64(n))
			lastByte.Store(time.Now().UnixNano())
		}
		if err != nil {
			if err != io.EOF {
				rerr = err
			}
			break
		}
	}
	close(stop)
	_ = f.Sync()
	if stalled.Load() {
		return false, errors.New("stalled")
	}
	if rerr != nil {
		return false, rerr
	}
	size := got.Load()
	if m.Size > 0 && size < m.Size {
		return false, errors.New("connection closed early")
	}
	if m.Size == 0 {
		m.Size = size
	}
	return true, nil
}

func startFromContentRange(s string) int64 {
	// bytes 100-199/1000
	s = strings.TrimPrefix(strings.TrimSpace(s), "bytes ")
	a, _, ok := strings.Cut(s, "-")
	if !ok {
		return -1
	}
	n, err := strconv.ParseInt(a, 10, 64)
	if err != nil {
		return -1
	}
	return n
}

func totalFromContentRange(s string) int64 {
	_, t, ok := strings.Cut(s, "/")
	if !ok || t == "*" {
		return -1
	}
	n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
	if err != nil {
		return -1
	}
	return n
}

func fileSize(p string) int64 {
	fi, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return fi.Size()
}

func writeMeta(p string, m meta) error {
	b, _ := json.Marshal(m)
	return os.WriteFile(p, b, 0o644)
}

func fileSHA(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// FileSHA256 returns the hex sha256 of a file.
func FileSHA256(p string) (string, error) { return fileSHA(p) }
