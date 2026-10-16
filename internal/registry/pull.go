// Package registry pulls container images from OCI / Docker registries with resumable layer
// downloads and writes an archive that `docker load` (and podman load) accept.
package registry

import (
	"archive/tar"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/mrzroot/sabr/internal/httpx"
	"github.com/mrzroot/sabr/internal/retry"
	"github.com/mrzroot/sabr/internal/ui"
)

// Ref is a parsed image reference.
type Ref struct {
	Registry string // registry-1.docker.io, ghcr.io ...
	Repo     string // library/alpine
	Tag      string
	Digest   string
	Original string
}

// ParseRef understands alpine, alpine:3.20, user/app@sha256:..., ghcr.io/o/r:tag, localhost:5000/x.
func ParseRef(s string) (Ref, error) {
	r := Ref{Original: s}
	if s == "" || strings.ContainsAny(s, " \t") {
		return r, fmt.Errorf("invalid image reference %q", s)
	}
	name := s
	if i := strings.Index(name, "@"); i >= 0 {
		r.Digest = name[i+1:]
		name = name[:i]
		if !strings.HasPrefix(r.Digest, "sha256:") {
			return r, fmt.Errorf("unsupported digest %q", r.Digest)
		}
	}
	if i := strings.LastIndex(name, ":"); i >= 0 && !strings.Contains(name[i:], "/") {
		r.Tag = name[i+1:]
		name = name[:i]
	}
	parts := strings.SplitN(name, "/", 2)
	if len(parts) == 2 && (strings.ContainsAny(parts[0], ".:") || parts[0] == "localhost") {
		r.Registry, r.Repo = parts[0], parts[1]
	} else {
		r.Registry, r.Repo = "registry-1.docker.io", name
	}
	if r.Registry == "docker.io" || r.Registry == "index.docker.io" {
		r.Registry = "registry-1.docker.io"
	}
	if r.Registry == "registry-1.docker.io" && !strings.Contains(r.Repo, "/") {
		r.Repo = "library/" + r.Repo
	}
	if r.Tag == "" && r.Digest == "" {
		r.Tag = "latest"
	}
	if !regexp.MustCompile(`^[a-z0-9]+(?:[._/-][a-z0-9]+)*$`).MatchString(r.Repo) {
		return r, fmt.Errorf("invalid repository name %q", r.Repo)
	}
	return r, nil
}

// Name returns the name docker shows for the image (alpine:3.20, ghcr.io/o/r:tag).
func (r Ref) Name() string {
	repo := r.Repo
	host := r.Registry
	if host == "registry-1.docker.io" {
		host = ""
		repo = strings.TrimPrefix(repo, "library/")
	}
	n := repo
	if host != "" {
		n = host + "/" + repo
	}
	if r.Tag != "" {
		return n + ":" + r.Tag
	}
	return n
}

// Options for Pull.
type Options struct {
	Platform string // os/arch[/variant]; default linux/<this arch>
	Output   string // archive path
	CacheDir string // blob cache (shared between pulls)
	Retry    retry.Policy
	Stall    time.Duration
	UI       *ui.UI
	Insecure bool // plain http (for local registries)
	Jobs     int  // parallel layer downloads
	Username string
	Password string
	Client   *http.Client // optional (tests)
}

// Result of a pull.
type Result struct {
	Ref      Ref
	Digest   string
	Layers   int
	Bytes    int64
	Resumes  int
	Reused   int
	Archive  string
	Platform string
}

type client struct {
	o     Options
	ref   Ref
	hc    *http.Client
	mu    sync.Mutex
	token string
	realm string
	svc   string
	u     *ui.UI
}

const (
	mtIndex    = "application/vnd.oci.image.index.v1+json"
	mtList     = "application/vnd.docker.distribution.manifest.list.v2+json"
	mtOCI      = "application/vnd.oci.image.manifest.v1+json"
	mtDocker   = "application/vnd.docker.distribution.manifest.v2+json"
	acceptList = mtIndex + ", " + mtList + ", " + mtOCI + ", " + mtDocker
)

type descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Platform  *struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
		Variant      string `json:"variant,omitempty"`
	} `json:"platform,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type manifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Config        descriptor   `json:"config"`
	Layers        []descriptor `json:"layers"`
	Manifests     []descriptor `json:"manifests"`
}

func (c *client) scheme() string {
	if c.o.Insecure {
		return "http"
	}
	return "https"
}

func (c *client) base() string { return c.scheme() + "://" + c.ref.Registry + "/v2/" + c.ref.Repo }

// auth handles the Bearer token dance (anonymous unless credentials are given).
func (c *client) auth(ctx context.Context, challenge string) error {
	if challenge != "" {
		c.realm, c.svc = parseChallenge(challenge)
	}
	if c.realm == "" {
		if c.o.Username != "" {
			return nil // basic auth is added per request
		}
		return errors.New("registry asked for authentication without a Bearer realm")
	}
	q := url.Values{}
	if c.svc != "" {
		q.Set("service", c.svc)
	}
	q.Set("scope", "repository:"+c.ref.Repo+":pull")
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.realm+"?"+q.Encode(), nil)
	if c.o.Username != "" {
		req.SetBasicAuth(c.o.Username, c.o.Password)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return &httpx.ErrFatal{Err: fmt.Errorf("token request: HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(b)))}
	}
	var t struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return err
	}
	c.mu.Lock()
	c.token = t.Token
	if c.token == "" {
		c.token = t.AccessToken
	}
	c.mu.Unlock()
	return nil
}

func parseChallenge(h string) (realm, service string) {
	for _, m := range regexp.MustCompile(`(\w+)="([^"]*)"`).FindAllStringSubmatch(h, -1) {
		switch m[1] {
		case "realm":
			realm = m[2]
		case "service":
			service = m[2]
		}
	}
	return
}

func (c *client) authorize(req *http.Request) {
	c.mu.Lock()
	tok := c.token
	c.mu.Unlock()
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	} else if c.o.Username != "" {
		req.SetBasicAuth(c.o.Username, c.o.Password)
	}
}

// getJSON fetches a manifest with retries; small, so it is simply re-requested on failure.
func (c *client) getManifest(ctx context.Context, ref string) ([]byte, string, error) {
	fails := 0
	for {
		b, mt, err := c.getManifestOnce(ctx, ref)
		if err == nil {
			return b, mt, nil
		}
		var fe *httpx.ErrFatal
		if errors.As(err, &fe) || ctx.Err() != nil {
			return nil, "", err
		}
		fails++
		if c.o.Retry.Exhausted(fails) {
			return nil, "", err
		}
		d := c.o.Retry.Delay(fails)
		c.u.Warn("manifest: %s; retrying in %s", err, ui.Dur(d))
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-time.After(d):
		}
	}
}

func (c *client) getManifestOnce(ctx context.Context, ref string) ([]byte, string, error) {
	for try := 0; try < 2; try++ {
		rctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		req, _ := http.NewRequestWithContext(rctx, http.MethodGet, c.base()+"/manifests/"+ref, nil)
		req.Header.Set("Accept", acceptList)
		c.authorize(req)
		resp, err := c.hc.Do(req)
		if err != nil {
			cancel()
			return nil, "", err
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		cancel()
		if err != nil {
			return nil, "", err
		}
		switch {
		case resp.StatusCode == 401 && try == 0:
			if err := c.auth(ctx, resp.Header.Get("WWW-Authenticate")); err != nil {
				return nil, "", err
			}
			continue
		case resp.StatusCode == 200:
			mt := resp.Header.Get("Content-Type")
			if i := strings.Index(mt, ";"); i >= 0 {
				mt = mt[:i]
			}
			return b, mt, nil
		case resp.StatusCode == 404 || resp.StatusCode == 401 || resp.StatusCode == 403:
			return nil, "", &httpx.ErrFatal{Err: fmt.Errorf("%s: HTTP %d %s", c.ref.Original, resp.StatusCode, strings.TrimSpace(string(b[:min(len(b), 200)])))}
		default:
			return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
		}
	}
	return nil, "", errors.New("authentication loop")
}

// DefaultPlatform is linux/<host arch>.
func DefaultPlatform() string {
	p := "linux/" + runtime.GOARCH
	if runtime.GOARCH == "arm" {
		p += "/v7"
	}
	return p
}

// Pull downloads the image and writes a docker-loadable archive.
func Pull(ctx context.Context, image string, o Options) (*Result, error) {
	ref, err := ParseRef(image)
	if err != nil {
		return nil, err
	}
	if o.UI == nil {
		o.UI = ui.New()
	}
	if o.Platform == "" {
		o.Platform = DefaultPlatform()
	}
	if o.Jobs <= 0 {
		o.Jobs = 3
	}
	if o.CacheDir == "" {
		d, _ := os.UserCacheDir()
		o.CacheDir = filepath.Join(d, "sabr", "blobs")
	}
	if o.Output == "" {
		o.Output = strings.NewReplacer("/", "_", ":", "_", "@", "_").Replace(ref.Name()) + ".tar"
	}
	if err := os.MkdirAll(o.CacheDir, 0o755); err != nil {
		return nil, err
	}
	hc := o.Client
	if hc == nil {
		hc = &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: true,
			TLSHandshakeTimeout: 30 * time.Second, ResponseHeaderTimeout: 60 * time.Second}}
	}
	c := &client{o: o, ref: ref, u: o.UI, hc: hc}
	res := &Result{Ref: ref, Archive: o.Output, Platform: o.Platform}

	o.UI.Step("Resolving %s (%s)", ref.Name(), o.Platform)
	want := ref.Tag
	if ref.Digest != "" {
		want = ref.Digest
	}
	body, mt, err := c.getManifest(ctx, want)
	if err != nil {
		return nil, err
	}
	var m manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("bad manifest: %w", err)
	}
	if mt == "" {
		mt = m.MediaType
	}
	if mt == mtIndex || mt == mtList || len(m.Manifests) > 0 {
		d, err := pickPlatform(m.Manifests, o.Platform)
		if err != nil {
			return nil, err
		}
		res.Digest = d.Digest
		body, mt, err = c.getManifest(ctx, d.Digest)
		if err != nil {
			return nil, err
		}
		m = manifest{}
		if err := json.Unmarshal(body, &m); err != nil {
			return nil, err
		}
	} else {
		res.Digest = "sha256:" + sha256hex(body)
	}
	if m.Config.Digest == "" {
		return nil, fmt.Errorf("unsupported manifest type %q (schema 1 images are not supported)", mt)
	}
	var total int64
	for _, l := range m.Layers {
		total += l.Size
	}
	res.Layers = len(m.Layers)
	o.UI.Info("%s · %d layers · %s", res.Digest[:19], len(m.Layers), ui.Bytes(total))

	// blobs: config first, then layers in parallel
	all := append([]descriptor{m.Config}, m.Layers...)
	var wg sync.WaitGroup
	sem := make(chan struct{}, o.Jobs)
	errs := make([]error, len(all))
	var mu sync.Mutex
	for i, d := range all {
		wg.Add(1)
		go func(i int, d descriptor) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r, err := c.blob(ctx, d)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs[i] = err
				return
			}
			res.Bytes += r.Size
			res.Resumes += max(0, r.Resumes)
			if r.Reused {
				res.Reused++
			}
		}(i, d)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return res, e
		}
	}
	o.UI.Step("Writing %s", o.Output)
	if err := writeArchive(o.Output, o.CacheDir, ref, m, body); err != nil {
		return res, err
	}
	return res, nil
}

func pickPlatform(ms []descriptor, want string) (descriptor, error) {
	p := strings.Split(want, "/")
	var avail []string
	for _, d := range ms {
		if d.Platform == nil || d.Annotations["vnd.docker.reference.type"] == "attestation-manifest" {
			continue
		}
		pl := d.Platform.OS + "/" + d.Platform.Architecture
		if d.Platform.Variant != "" {
			pl += "/" + d.Platform.Variant
		}
		avail = append(avail, pl)
		if d.Platform.OS == p[0] && len(p) > 1 && d.Platform.Architecture == p[1] && (len(p) < 3 || d.Platform.Variant == p[2]) {
			return d, nil
		}
	}
	return descriptor{}, &httpx.ErrFatal{Err: fmt.Errorf("no %s image; available: %s", want, strings.Join(avail, ", "))}
}

func (c *client) blobPath(digest string) string {
	return filepath.Join(c.o.CacheDir, strings.Replace(digest, ":", "-", 1))
}

func (c *client) blob(ctx context.Context, d descriptor) (*httpx.Result, error) {
	hex := strings.TrimPrefix(d.Digest, "sha256:")
	label := hex[:12]
	dl := &httpx.Downloader{Client: c.hc, Retry: c.o.Retry, Stall: c.o.Stall, UI: c.u, SHA256: hex, Label: label,
		Before: c.authorize,
		Unauthorized: func() error {
			return c.auth(ctx, "")
		}}
	if c.token == "" && c.realm == "" {
		// make sure we have a token before the first blob (registries answer 401 without one)
		_ = c.auth(ctx, "")
	}
	r, err := dl.Get(ctx, c.base()+"/blobs/"+d.Digest, c.blobPath(d.Digest))
	if err != nil {
		return nil, fmt.Errorf("layer %s: %w", label, err)
	}
	if r.Reused {
		c.u.Info("%s cached (%s)", label, ui.Bytes(r.Size))
	} else {
		note := ""
		if r.Resumes > 0 {
			note = fmt.Sprintf(", resumed %d×", r.Resumes)
		}
		c.u.OK("%s %s%s", label, ui.Bytes(r.Size), note)
	}
	return r, nil
}

// writeArchive produces the layout `docker save` uses since Docker 25: an OCI image layout
// (oci-layout, index.json, blobs/sha256/...) plus the classic manifest.json, so both the
// classic and the containerd image stores (and podman) can load it.
func writeArchive(out, cache string, ref Ref, m manifest, manifestBody []byte) error {
	tmp := out + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(f)
	now := time.Unix(0, 0)
	addBytes := func(name string, b []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(b)), ModTime: now, Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err := tw.Write(b)
		return err
	}
	addFile := func(name, path string) error {
		fh, err := os.Open(path)
		if err != nil {
			return err
		}
		defer fh.Close()
		fi, err := fh.Stat()
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: fi.Size(), ModTime: now, Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err = io.Copy(tw, fh)
		return err
	}
	fail := func(err error) error { tw.Close(); f.Close(); os.Remove(tmp); return err }
	for _, dir := range []string{"blobs/", "blobs/sha256/"} {
		if err := tw.WriteHeader(&tar.Header{Name: dir, Mode: 0o755, ModTime: now, Typeflag: tar.TypeDir}); err != nil {
			return fail(err)
		}
	}
	bp := func(d string) string { return "blobs/sha256/" + strings.TrimPrefix(d, "sha256:") }
	if err := addFile(bp(m.Config.Digest), filepath.Join(cache, strings.Replace(m.Config.Digest, ":", "-", 1))); err != nil {
		return fail(err)
	}
	var layers []string
	seen := map[string]bool{}
	for _, l := range m.Layers {
		layers = append(layers, bp(l.Digest))
		if seen[l.Digest] {
			continue
		}
		seen[l.Digest] = true
		if err := addFile(bp(l.Digest), filepath.Join(cache, strings.Replace(l.Digest, ":", "-", 1))); err != nil {
			return fail(err)
		}
	}
	mdig := "sha256:" + sha256hex(manifestBody)
	if err := addBytes(bp(mdig), manifestBody); err != nil {
		return fail(err)
	}
	mt := m.MediaType
	if mt == "" {
		mt = mtOCI
	}
	idx := map[string]any{
		"schemaVersion": 2,
		"mediaType":     mtIndex,
		"manifests": []map[string]any{{
			"mediaType": mt, "digest": mdig, "size": len(manifestBody),
			"annotations": map[string]string{
				"io.containerd.image.name":          fullName(ref),
				"org.opencontainers.image.ref.name": refName(ref),
			},
		}},
	}
	ib, _ := json.Marshal(idx)
	if err := addBytes("index.json", ib); err != nil {
		return fail(err)
	}
	if err := addBytes("oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`)); err != nil {
		return fail(err)
	}
	dm := []map[string]any{{"Config": bp(m.Config.Digest), "RepoTags": []string{ref.Name()}, "Layers": layers}}
	if ref.Tag == "" {
		dm[0]["RepoTags"] = []string{}
	}
	db, _ := json.Marshal(dm)
	if err := addBytes("manifest.json", db); err != nil {
		return fail(err)
	}
	if err := tw.Close(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, out)
}

func fullName(r Ref) string {
	host := r.Registry
	if host == "registry-1.docker.io" {
		host = "docker.io"
	}
	n := host + "/" + r.Repo
	if r.Tag != "" {
		n += ":" + r.Tag
	}
	return n
}

func refName(r Ref) string {
	if r.Tag != "" {
		return r.Tag
	}
	return r.Digest
}

// DockerAuth returns credentials for a registry from ~/.docker/config.json "auths" (base64 user:pass), if any.
func DockerAuth(registry string) (string, string) {
	home, _ := os.UserHomeDir()
	b, err := os.ReadFile(filepath.Join(home, ".docker", "config.json"))
	if err != nil {
		return "", ""
	}
	var cfg struct {
		Auths map[string]struct {
			Auth string `json:"auth"`
		} `json:"auths"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return "", ""
	}
	keys := []string{registry, "https://" + registry}
	if registry == "registry-1.docker.io" {
		keys = append(keys, "https://index.docker.io/v1/", "docker.io")
	}
	for _, k := range keys {
		if a, ok := cfg.Auths[k]; ok && a.Auth != "" {
			raw, err := base64.StdEncoding.DecodeString(a.Auth)
			if err == nil {
				u, p, _ := strings.Cut(string(raw), ":")
				return u, p
			}
		}
	}
	return "", ""
}
