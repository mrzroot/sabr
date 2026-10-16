package httpx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/mrzroot/sabr/internal/chaos"
	"github.com/mrzroot/sabr/internal/retry"
	"github.com/mrzroot/sabr/internal/ui"
)

func quietUI() *ui.UI { u := ui.New(); u.Quiet = true; return u }

func payload(n int) ([]byte, string) {
	b := make([]byte, n)
	rand.New(rand.NewSource(7)).Read(b)
	s := sha256.Sum256(b)
	return b, hex.EncodeToString(s[:])
}

func serve(b []byte, ranges bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		if !ranges {
			r.Header.Del("Range")
		}
		http.ServeContent(w, r, "f.bin", time.Unix(1700000000, 0), bytes.NewReader(b))
	}))
}

func viaProxy(t *testing.T, cut int64) (*http.Client, *chaos.Proxy) {
	p := &chaos.Proxy{Cut: cut}
	addr, err := p.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	pu, _ := url.Parse("http://" + addr)
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu), DisableKeepAlives: true}}, p
}

func fast() retry.Policy {
	return retry.Policy{Max: 200, Base: time.Millisecond, Cap: 5 * time.Millisecond}
}

func TestResumesThroughDrops(t *testing.T) {
	b, sum := payload(3 << 20)
	srv := serve(b, true)
	defer srv.Close()
	hc, p := viaProxy(t, 256<<10)
	dest := filepath.Join(t.TempDir(), "f.bin")
	d := &Downloader{Client: hc, Retry: fast(), UI: quietUI(), SHA256: sum}
	r, err := d.Get(context.Background(), srv.URL+"/f.bin", dest)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, b) {
		t.Fatal("content differs")
	}
	if r.Resumes < 5 || p.Cuts.Load() < 5 {
		t.Fatalf("expected many resumes, got %d (cuts %d)", r.Resumes, p.Cuts.Load())
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatal(".part left behind")
	}
	// second run: already complete, nothing downloaded
	r2, err := d.Get(context.Background(), srv.URL+"/f.bin", dest)
	if err != nil || !r2.Reused {
		t.Fatalf("expected reuse: %+v %v", r2, err)
	}
}

func TestContinuesAcrossProcesses(t *testing.T) {
	b, sum := payload(1 << 20)
	srv := serve(b, true)
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "f.bin")
	// pretend an earlier run stopped after 300 KB
	os.WriteFile(dest+".part", b[:300<<10], 0o644)
	os.WriteFile(dest+".part.json", []byte(`{"url":"`+srv.URL+`/f.bin","etag":"\"v1\"","size":`+itoa(len(b))+`}`), 0o644)
	var ranges []string
	d := &Downloader{Retry: fast(), UI: quietUI(), SHA256: sum, Before: func(r *http.Request) { ranges = append(ranges, r.Header.Get("Range")) }}
	if _, err := d.Get(context.Background(), srv.URL+"/f.bin", dest); err != nil {
		t.Fatal(err)
	}
	if len(ranges) != 1 || ranges[0] != "bytes=307200-" {
		t.Fatalf("expected one ranged request, got %v", ranges)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestChangedFileStartsOver(t *testing.T) {
	b, sum := payload(200 << 10)
	srv := serve(b, true)
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "f.bin")
	os.WriteFile(dest+".part", bytes.Repeat([]byte("x"), 50<<10), 0o644)
	os.WriteFile(dest+".part.json", []byte(`{"url":"`+srv.URL+`/f.bin","etag":"\"old\"","size":204800}`), 0o644)
	d := &Downloader{Retry: fast(), UI: quietUI(), SHA256: sum}
	if _, err := d.Get(context.Background(), srv.URL+"/f.bin", dest); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, b) {
		t.Fatal("stale prefix kept after the file changed")
	}
}

func TestFatalErrors(t *testing.T) {
	b, _ := payload(1000)
	srv := serve(b, true)
	defer srv.Close()
	d := &Downloader{Retry: fast(), UI: quietUI()}
	_, err := d.Get(context.Background(), srv.URL+"/missing", filepath.Join(t.TempDir(), "x"))
	var fe *ErrFatal
	if !errors.As(err, &fe) {
		t.Fatalf("404 should be fatal, got %v", err)
	}
	d.SHA256 = "00"
	_, err = d.Get(context.Background(), srv.URL+"/f", filepath.Join(t.TempDir(), "y"))
	if !errors.As(err, &fe) {
		t.Fatalf("digest mismatch should be fatal, got %v", err)
	}
}

func TestStallIsDetected(t *testing.T) {
	b, sum := payload(1 << 20)
	srv := serve(b, true)
	defer srv.Close()
	p := &chaos.Proxy{Cut: 100 << 10, Freeze: true}
	addr, _ := p.Listen("127.0.0.1:0")
	defer p.Close()
	pu, _ := url.Parse("http://" + addr)
	hc := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu), DisableKeepAlives: true}}
	d := &Downloader{Client: hc, Retry: fast(), UI: quietUI(), SHA256: sum, Stall: 700 * time.Millisecond}
	start := time.Now()
	if _, err := d.Get(context.Background(), srv.URL+"/f", filepath.Join(t.TempDir(), "f")); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 30*time.Second {
		t.Fatal("too slow")
	}
}
