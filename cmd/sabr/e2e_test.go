// End-to-end tests: build the real sabr binary and clone/pull a real git repository served by `git daemon`
// through a chaos proxy that cuts the connection every few hundred KB. Each test asserts the end state
// (same commits, clean tree) rather than any internal step, so they hold across refactors.
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

// bin is the sabr binary built once in TestMain.
var bin string

// retryEnv shortens sabr's retry back-off so dropped connections do not slow the tests down.
const retryEnv = "SABR_RETRY_BASE=20ms"

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "sabr-e2e")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
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

// makeRepo builds a repository with real history: commits that add and rewrite random files, a tag every 25
// commits. It returns the bare copy (root/src.git) that is served to clients; root/work stays as the working
// copy so tests can add upstream commits later. The fixed seed keeps the blobs identical between runs.
func makeRepo(t *testing.T, root string, commits int) string {
	t.Helper()
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
	// Partial clone (--filter=blob:none) needs both options on the server side.
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
	t.Helper()
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

// flaky starts a proxy in front of the git daemon at `to` that cuts every connection after about `cut` bytes
// (with jitter) and optionally delays each chunk. It returns the proxy's host:port.
func flaky(t *testing.T, to string, cut int64, delay time.Duration) (string, *chaos.Proxy) {
	t.Helper()
	p := &chaos.Proxy{To: to, Cut: cut, Jitter: 0.3, Delay: delay}
	addr, err := p.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return addr, p
}

// sabr runs the binary and returns its combined output.
func sabr(timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(os.Environ(), retryEnv)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// sameTree fails unless clone has the same HEAD, history length and file count as src and a clean work tree.
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
	// every checked-out file has the exact bytes of its blob (lazily fetches blobs a partial clone lacks)
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
	log, err := sabr(5*time.Minute, "clone", url, dst)
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
	if st, _ := sabr(time.Minute, "status", dst); !strings.Contains(st, "phase     done") {
		t.Fatalf("status: %s", st)
	}
	t.Logf("cuts survived: %d", p.Cuts.Load())
}

func TestAllBlobsClonesEveryObject(t *testing.T) {
	root := t.TempDir()
	src := makeRepo(t, root, 60)
	addr, _ := flaky(t, daemon(t, root), 300<<10, 0)
	dst := filepath.Join(root, "dst")
	if log, err := sabr(5*time.Minute, "clone", "--all-blobs", "git://"+addr+"/src.git", dst); err != nil {
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
	addr, _ := flaky(t, daemon(t, root), 200<<10, 15*time.Millisecond)
	dst := filepath.Join(root, "dst")
	url := "git://" + addr + "/src.git"
	// hard-kill sabr part way through (like a laptop lid or a power cut), once it has saved progress
	cmd := exec.Command(bin, "clone", url, dst)
	cmd.Env = append(os.Environ(), retryEnv)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Minute)
	for {
		b, _ := os.ReadFile(filepath.Join(dst, ".git", "sabr.json"))
		// sabr.json is the resume file; two completed steps means there is progress worth resuming from.
		if strings.Contains(string(b), `"steps": 2`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("clone made no progress")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cmd.Process.Kill()
	cmd.Wait()
	if b, _ := os.ReadFile(filepath.Join(dst, ".git", "sabr.json")); strings.Contains(string(b), `"phase": "done"`) {
		t.Fatal("finished before the kill; nothing to resume")
	}
	log, err := sabr(5*time.Minute, "clone", url, dst)
	if err != nil {
		t.Fatalf("resume: %v\n%s", err, log)
	}
	if !strings.Contains(log, "Resuming clone") {
		t.Fatalf("second run did not resume:\n%s", log)
	}
	sameTree(t, src, dst)
}

// pushUpstreamCommits adds n commits with random 90 KB files to the working copy and pushes them to src.
func pushUpstreamCommits(t *testing.T, root, src string, n int) {
	t.Helper()
	work := filepath.Join(root, "work")
	for i := 0; i < n; i++ {
		b := make([]byte, 90000)
		rand.Read(b)
		if err := os.WriteFile(filepath.Join(work, fmt.Sprintf("new%d.bin", i)), b, 0o644); err != nil {
			t.Fatal(err)
		}
		git(t, work, "add", "-A")
		git(t, work, "commit", "-q", "-m", fmt.Sprintf("new %d", i))
	}
	git(t, work, "push", "-q", src, "main")
}

func TestPullFetchesNewCommitsThroughFlakyLink(t *testing.T) {
	root := t.TempDir()
	src := makeRepo(t, root, 30)
	addr, _ := flaky(t, daemon(t, root), 250<<10, 0)
	dst := filepath.Join(root, "dst")
	url := "git://" + addr + "/src.git"
	if log, err := sabr(3*time.Minute, "clone", url, dst); err != nil {
		t.Fatalf("%v\n%s", err, log)
	}

	pushUpstreamCommits(t, root, src, 5)

	if log, err := sabr(3*time.Minute, "pull", dst); err != nil {
		t.Fatalf("pull: %v\n%s", err, log)
	}
	sameTree(t, src, dst)
}

func TestCloneRefusesNonEmptyDirectory(t *testing.T) {
	root := t.TempDir()
	makeRepo(t, root, 3)
	addr := daemon(t, root)
	other := filepath.Join(root, "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := sabr(time.Minute, "clone", "git://"+addr+"/src.git", other); err == nil {
		t.Fatal("expected refusal for non-empty dir")
	}
}

// A repository that does not exist is a permanent error: sabr must give up at once instead of retrying forever.
func TestMissingRepositoryFailsFast(t *testing.T) {
	root := t.TempDir()
	addr := daemon(t, root)
	start := time.Now()
	if _, err := sabr(time.Minute, "clone", "git://"+addr+"/nope.git", filepath.Join(root, "nope")); err == nil {
		t.Fatal("expected failure for missing repo")
	}
	if time.Since(start) > 30*time.Second {
		t.Fatal("missing repo should fail fast")
	}
}
