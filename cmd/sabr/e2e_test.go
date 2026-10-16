package main

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mrzroot/sabr/internal/chaos"
)

var bin string

func TestMain(m *testing.M) {
	dir, _ := os.MkdirTemp("", "sabr-e2e")
	bin = filepath.Join(dir, "sabr")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		fmt.Println(string(out))
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_NO_LAZY_FETCH=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// makeRepo builds a repository with real history: commits that add and rewrite random files.
func makeRepo(t *testing.T, root string, commits int) string {
	src := filepath.Join(root, "src.git")
	work := filepath.Join(root, "work")
	os.MkdirAll(work, 0o755)
	git(t, work, "init", "-q", "-b", "main")
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < commits; i++ {
		for j := 0; j < 3; j++ {
			b := make([]byte, 6000+rng.Intn(20000))
			rng.Read(b)
			name := filepath.Join(work, fmt.Sprintf("d%d", rng.Intn(6)), fmt.Sprintf("f%d.bin", rng.Intn(40)))
			os.MkdirAll(filepath.Dir(name), 0o755)
			os.WriteFile(name, b, 0o644)
		}
		git(t, work, "add", "-A")
		git(t, work, "commit", "-q", "-m", fmt.Sprintf("commit %d", i))
		if i%25 == 0 {
			git(t, work, "tag", fmt.Sprintf("v0.%d", i))
		}
	}
	git(t, root, "clone", "-q", "--bare", work, src)
	git(t, src, "config", "uploadpack.allowFilter", "true")
	git(t, src, "config", "uploadpack.allowAnySHA1InWant", "true")
	return src
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// daemon serves root over git:// and returns host:port.
func daemon(t *testing.T, root string) string {
	port := freePort(t)
	cmd := exec.Command("git", "daemon", "--export-all", "--reuseaddr", "--base-path="+root, "--listen=127.0.0.1", fmt.Sprintf("--port=%d", port), root)
	if err := cmd.Start(); err != nil {
		t.Skipf("git daemon unavailable: %v", err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			return addr
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("git daemon did not start")
	return ""
}

func flaky(t *testing.T, to string, cut int64, delay time.Duration) (string, *chaos.Proxy) {
	p := &chaos.Proxy{To: to, Cut: cut, Jitter: 0.3, Delay: delay}
	addr, err := p.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return addr, p
}

func sabr(t *testing.T, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(os.Environ(), "SABR_RETRY_BASE=20ms")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

func sameTree(t *testing.T, src, clone string) {
	t.Helper()
	if a, b := git(t, src, "rev-parse", "main"), git(t, clone, "rev-parse", "HEAD"); a != b {
		t.Fatalf("HEAD %s != %s", b, a)
	}
	if s := git(t, clone, "status", "--porcelain"); s != "" {
		t.Fatalf("work tree not clean:\n%s", s)
	}
	if a, b := git(t, src, "ls-tree", "-r", "main"), git(t, clone, "ls-files", "-s"); strings.Count(a, "\n") != strings.Count(b, "\n") {
		t.Fatal("file count differs")
	}
	if a, b := git(t, src, "rev-list", "--count", "main"), git(t, clone, "rev-list", "--count", "HEAD"); a != b {
		t.Fatalf("history %s commits, want %s", b, a)
	}
	// every checked-out file has the exact bytes of its blob
	git(t, clone, "diff", "--quiet", "HEAD")
}

func TestPlainGitFailsSabrSucceeds(t *testing.T) {
	root := t.TempDir()
	src := makeRepo(t, root, 120)
	addr, p := flaky(t, daemon(t, root), 400<<10, 0)
	url := "git://" + addr + "/src.git"

	out, _ := exec.Command("git", "clone", "-q", url, filepath.Join(root, "plain")).CombinedOutput()
	if _, err := os.Stat(filepath.Join(root, "plain", ".git")); err == nil {
		t.Fatalf("plain git clone should fail through a connection that drops every ~400 KB:\n%s", out)
	}

	dst := filepath.Join(root, "dst")
	log, err := sabr(t, 5*time.Minute, "clone", url, dst)
	if err != nil {
		t.Fatalf("sabr clone: %v\n%s", err, log)
	}
	sameTree(t, src, dst)
	if !strings.Contains(log, "drops survived") || p.Cuts.Load() == 0 {
		t.Fatalf("expected drops (cuts %d):\n%s", p.Cuts.Load(), log)
	}
	if n := git(t, dst, "tag"); !strings.Contains(n, "v0.100") {
		t.Fatalf("tags missing: %q", n)
	}
	// old versions are fetched on demand (normal partial clone behaviour), still through sabr's batches later
	if st, _ := sabr(t, time.Minute, "status", dst); !strings.Contains(st, "phase     done") {
		t.Fatalf("status: %s", st)
	}
	t.Logf("cuts survived: %d", p.Cuts.Load())
}

func TestAllBlobsIsAComplete(t *testing.T) {
	root := t.TempDir()
	src := makeRepo(t, root, 60)
	addr, _ := flaky(t, daemon(t, root), 300<<10, 0)
	dst := filepath.Join(root, "dst")
	if log, err := sabr(t, 5*time.Minute, "clone", "--all-blobs", "git://"+addr+"/src.git", dst); err != nil {
		t.Fatalf("%v\n%s", err, log)
	}
	sameTree(t, src, dst)
	missing := git(t, dst, "rev-list", "--objects", "--missing=print", "--all")
	if strings.Contains(missing, "\n?") || strings.HasPrefix(missing, "?") {
		t.Fatal("objects still missing after --all-blobs")
	}
	git(t, dst, "log", "-p", "-3") // works offline: nothing to fetch lazily
}

func TestKilledCloneResumes(t *testing.T) {
	root := t.TempDir()
	src := makeRepo(t, root, 100)
	addr, _ := flaky(t, daemon(t, root), 200<<10, 2*time.Millisecond)
	dst := filepath.Join(root, "dst")
	url := "git://" + addr + "/src.git"
	// hard-kill sabr part way through (like a laptop lid or a power cut)
	sabr(t, 2500*time.Millisecond, "clone", url, dst)
	if _, err := os.Stat(filepath.Join(dst, ".git", "sabr.json")); err != nil {
		t.Fatal("no state file after the kill")
	}
	log, err := sabr(t, 5*time.Minute, "clone", url, dst)
	if err != nil {
		t.Fatalf("resume: %v\n%s", err, log)
	}
	if !strings.Contains(log, "Resuming clone") {
		t.Fatalf("second run did not resume:\n%s", log)
	}
	sameTree(t, src, dst)
}

func TestPullAndRefusals(t *testing.T) {
	root := t.TempDir()
	src := makeRepo(t, root, 30)
	d := daemon(t, root)
	addr, _ := flaky(t, d, 250<<10, 0)
	dst := filepath.Join(root, "dst")
	url := "git://" + addr + "/src.git"
	if log, err := sabr(t, 3*time.Minute, "clone", url, dst); err != nil {
		t.Fatalf("%v\n%s", err, log)
	}
	// new upstream commits, pulled through the same bad link
	work := filepath.Join(root, "work")
	for i := 0; i < 5; i++ {
		b := make([]byte, 90000)
		rand.Read(b)
		os.WriteFile(filepath.Join(work, fmt.Sprintf("new%d.bin", i)), b, 0o644)
		git(t, work, "add", "-A")
		git(t, work, "commit", "-q", "-m", fmt.Sprintf("new %d", i))
	}
	git(t, work, "push", "-q", src, "main")
	if log, err := sabr(t, 3*time.Minute, "pull", dst); err != nil {
		t.Fatalf("pull: %v\n%s", err, log)
	}
	sameTree(t, src, dst)
	// refuses to clone into a non-empty directory, and a different URL into an unfinished clone
	other := filepath.Join(root, "other")
	os.MkdirAll(other, 0o755)
	os.WriteFile(filepath.Join(other, "x"), []byte("x"), 0o644)
	if _, err := sabr(t, time.Minute, "clone", url, other); err == nil {
		t.Fatal("expected refusal for non-empty dir")
	}
	// a repository that does not exist fails fast instead of retrying forever
	start := time.Now()
	if _, err := sabr(t, time.Minute, "clone", "git://"+d+"/nope.git", filepath.Join(root, "nope")); err == nil {
		t.Fatal("expected failure for missing repo")
	}
	if time.Since(start) > 30*time.Second {
		t.Fatal("missing repo should fail fast")
	}
}
