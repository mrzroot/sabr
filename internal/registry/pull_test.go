package registry

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrzroot/sabr/internal/chaos"
	"github.com/mrzroot/sabr/internal/retry"
	"github.com/mrzroot/sabr/internal/ui"
)

func digest(b []byte) string { s := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(s[:]) }

func layer(t *testing.T, name string, size int) []byte {
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	data := make([]byte, size)
	rand.New(rand.NewSource(int64(size))).Read(data)
	tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(size), Typeflag: tar.TypeReg})
	tw.Write(data)
	tw.Close()
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(raw.Bytes())
	zw.Close()
	return gz.Bytes()
}

// fakeRegistry speaks enough of the distribution API: Bearer auth, an index, a manifest, blobs with ranges.
func fakeRegistry(t *testing.T) (*httptest.Server, map[string][]byte, string) {
	blobs := map[string][]byte{}
	l1, l2 := layer(t, "a.bin", 900<<10), layer(t, "b.bin", 1200<<10)
	cfg := []byte(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]}}`)
	for _, b := range [][]byte{l1, l2, cfg} {
		blobs[digest(b)] = b
	}
	man, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": mtOCI,
		"config": map[string]any{"mediaType": "application/vnd.oci.image.config.v1+json", "digest": digest(cfg), "size": len(cfg)},
		"layers": []map[string]any{
			{"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip", "digest": digest(l1), "size": len(l1)},
			{"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip", "digest": digest(l2), "size": len(l2)},
		}})
	idx, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": mtIndex, "manifests": []map[string]any{
		{"mediaType": mtOCI, "digest": "sha256:" + strings.Repeat("0", 64), "size": 10, "platform": map[string]string{"os": "linux", "architecture": "arm64"}},
		{"mediaType": mtOCI, "digest": digest(man), "size": len(man), "platform": map[string]string{"os": "linux", "architecture": "amd64"}},
	}})
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			if r.URL.Query().Get("scope") != "repository:team/app:pull" {
				http.Error(w, "bad scope", 400)
				return
			}
			w.Write([]byte(`{"token":"T"}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer T" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+srv.URL+`/token",service="fake"`)
			w.WriteHeader(401)
			return
		}
		switch {
		case r.URL.Path == "/v2/team/app/manifests/1.0":
			w.Header().Set("Content-Type", mtIndex)
			w.Write(idx)
		case r.URL.Path == "/v2/team/app/manifests/"+digest(man):
			w.Header().Set("Content-Type", mtOCI)
			w.Write(man)
		case strings.HasPrefix(r.URL.Path, "/v2/team/app/blobs/"):
			b, ok := blobs[strings.TrimPrefix(r.URL.Path, "/v2/team/app/blobs/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			http.ServeContent(w, r, "", time.Unix(1, 0), bytes.NewReader(b))
		default:
			http.NotFound(w, r)
		}
	}))
	return srv, blobs, digest(man)
}

func TestPullThroughDrops(t *testing.T) {
	srv, blobs, mdig := fakeRegistry(t)
	defer srv.Close()
	p := &chaos.Proxy{Cut: 200 << 10}
	addr, _ := p.Listen("127.0.0.1:0")
	defer p.Close()
	pu, _ := url.Parse("http://" + addr)
	hc := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu), DisableKeepAlives: true}}
	host := strings.TrimPrefix(srv.URL, "http://")
	dir := t.TempDir()
	u := ui.New()
	u.Quiet = true
	o := Options{Platform: "linux/amd64", Output: filepath.Join(dir, "app.tar"), CacheDir: filepath.Join(dir, "cache"),
		Retry: retry.Policy{Max: 500, Base: time.Millisecond, Cap: 5 * time.Millisecond}, UI: u, Insecure: true, Client: hc}
	r, err := Pull(context.Background(), host+"/team/app:1.0", o)
	if err != nil {
		t.Fatal(err)
	}
	if r.Digest != mdig || r.Layers != 2 || r.Resumes < 3 {
		t.Fatalf("unexpected result %+v (cuts %d)", r, p.Cuts.Load())
	}
	// the archive: every blob present and intact, both manifest formats
	f, _ := os.Open(o.Output)
	tr := tar.NewReader(f)
	files := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		files[h.Name] = b
	}
	f.Close() // Windows cannot replace an open file on the second pull
	for d, b := range blobs {
		got := files["blobs/sha256/"+strings.TrimPrefix(d, "sha256:")]
		if !bytes.Equal(got, b) {
			t.Fatalf("blob %s missing or corrupt in archive", d[:19])
		}
	}
	var dm []struct {
		Config   string
		RepoTags []string
		Layers   []string
	}
	if err := json.Unmarshal(files["manifest.json"], &dm); err != nil || len(dm) != 1 || len(dm[0].Layers) != 2 || dm[0].RepoTags[0] != host+"/team/app:1.0" {
		t.Fatalf("manifest.json: %s", files["manifest.json"])
	}
	if !strings.Contains(string(files["index.json"]), mdig) || files["oci-layout"] == nil {
		t.Fatalf("index.json: %s", files["index.json"])
	}
	// second pull reuses the cache
	cuts := p.Cuts.Load()
	r2, err := Pull(context.Background(), host+"/team/app:1.0", o)
	if err != nil || r2.Reused != 3 || p.Cuts.Load() != cuts {
		t.Fatalf("expected full cache reuse: %+v %v", r2, err)
	}
}

func TestPullWrongPlatform(t *testing.T) {
	srv, _, _ := fakeRegistry(t)
	defer srv.Close()
	u := ui.New()
	u.Quiet = true
	_, err := Pull(context.Background(), strings.TrimPrefix(srv.URL, "http://")+"/team/app:1.0",
		Options{Platform: "windows/amd64", Output: filepath.Join(t.TempDir(), "x.tar"), CacheDir: t.TempDir(), UI: u, Insecure: true, Retry: retry.Policy{Max: 2}})
	if err == nil || !strings.Contains(err.Error(), "linux/amd64") {
		t.Fatalf("expected a list of available platforms, got %v", err)
	}
}
