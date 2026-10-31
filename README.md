<div align="center">

<img src="docs/banner.svg" alt="sabr (صبر): downloads that never start over" width="100%">

# sabr · صبر

**Downloads that never start over.**
Resumable `git clone`, `docker pull` and file downloads for slow, throttled and unreliable connections.

[![CI](https://github.com/mrzroot/sabr/actions/workflows/ci.yml/badge.svg)](https://github.com/mrzroot/sabr/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/mrzroot/sabr)](https://github.com/mrzroot/sabr/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE)
![Go 1.22+](https://img.shields.io/badge/go-1.22%2B-00ADD8)

[Website](https://mrzroot.github.io/sabr/) · [فارسی](README.fa.md) · [Releases](https://github.com/mrzroot/sabr/releases) · [Changelog](CHANGELOG.md) · [Contributing](CONTRIBUTING.md)

</div>

### ⚡ Quickstart

```sh
curl -fsSL https://mrzroot.github.io/sabr/install.sh | sh
sabr clone https://github.com/cli/cli    # if the link drops, run the same command again: it continues
```

```text
$ git -c http.proxy=http://127.0.0.1:8899 clone https://github.com/cli/cli     # a link that drops every ~2 MB
fatal: early EOF
fatal: fetch-pack: invalid index-pack output              ← back to 0%, every time

$ HTTPS_PROXY=http://127.0.0.1:8899 sabr clone https://github.com/cli/cli
==> Fetching history in resumable steps (commits and folders only)
    +64 commits (65 so far)
  ! step of 128 commits failed (early EOF); next try 64 commits in 985ms
    +64 commits (129 so far)
    …
==> Fetching files of the latest commit: 1506 to go
    256/1506 files (16%)
  ! batch of 512 files failed (early EOF); next batch 128 files in 927ms
    …
==> pkg/cmd/attestation/test/data/gh_2.60.1_windows_arm64.zip keeps dropping over git; downloading it in byte ranges instead
==> Checking out trunk
  ✓ done: cli (49s, 27 steps, 17 drops survived, 14.0 MiB received)     ← 12,357 commits, clean tree, same HEAD
```

(A real run, trimmed. `sabr chaos` is the bad link; see below.)

`git clone` is all-or-nothing: one pack, one connection, and when that connection drops at 97 %
everything is thrown away. `docker pull` on the default image store does the same to a 600 MB layer.
On a good fibre line nobody notices. On a mobile link, a throttled international route, satellite,
a train, or anywhere the internet is unreliable, it can make a repository impossible to clone at all.

**sabr** splits the work into small pieces that are each saved as soon as they arrive, so a drop
costs you seconds, not the whole download. It is a single static binary that drives your real `git`
and talks to registries directly. No server, no account, no config.

## Install

```sh
curl -fsSL https://mrzroot.github.io/sabr/install.sh | sh          # macOS, Linux, WSL, FreeBSD
```
```powershell
irm https://mrzroot.github.io/sabr/install.ps1 | iex                 # Windows
```

The installers themselves resume, verify the binary's SHA-256 against the release, and need no root
or admin rights. Or download a binary from [Releases](https://github.com/mrzroot/sabr/releases), or
`go install github.com/mrzroot/sabr/cmd/sabr@latest`.

## Use

| Instead of | Run | What happens on a drop |
|---|---|---|
| `git clone URL` | `sabr clone URL` | the current small step is retried; everything before it is kept |
| (Ctrl+C, reboot, dead battery) | the same `sabr clone URL` again, or `sabr resume DIR` | continues from `.git/sabr.json` |
| `git pull` | `sabr pull` | fetch retried, missing files in batches, then fast-forward |
| `docker pull IMG` | `sabr docker pull IMG --load` | the layer continues from the byte it stopped at |
| `curl -O URL` / `wget` | `sabr get URL --sha256 HEX` | byte-range resume, change detection, checksum |

```sh
sabr clone https://github.com/torvalds/linux              # full history, latest files, tags
sabr clone --depth 50 https://github.com/torvalds/linux   # only recent history
sabr clone --all-blobs URL                                # every old file version too (a full, non-partial clone)
sabr docker pull python:3.12-slim --load                  # straight into docker (or podman)
sabr docker pull ghcr.io/owner/app:1.2 --platform linux/arm64 -o app.tar   # carry it to an offline machine
sabr get https://nodejs.org/dist/v24.21.0/node-v24.21.0-linux-x64.tar.xz --sha256 …
```

Flags shared by every command: `--retries N` (default 0 = never give up), `--stall 45s` (a connection
that delivers nothing for this long counts as dropped), `-q`, `-v`.

### See it for yourself

`sabr chaos` is a deliberately terrible network: a local proxy that cuts every connection after a
few megabytes. Point any tool at it and compare.

```sh
sabr chaos --cut 2MB &                                     # listens on 127.0.0.1:8899
git -c http.proxy=http://127.0.0.1:8899 clone https://github.com/cli/cli    # fails, every time
HTTPS_PROXY=http://127.0.0.1:8899 sabr clone https://github.com/cli/cli     # finishes
HTTPS_PROXY=http://127.0.0.1:8899 sabr docker pull python:3.12-slim --load  # finishes
```

That exact experiment runs in CI on every push against real GitHub, Docker Hub and nodejs.org.

## How it works

**git clone** becomes four resumable phases, all done by your own `git` (2.29+ for the batching):

1. **Latest commit.** A shallow, *blobless* fetch: one commit and its folder listing, no file contents.
2. **History, newest first, in growing steps.** `git fetch --deepen=N --filter=blob:none`. Each
   finished step is saved for good. After a success the step doubles; after a drop it halves, so the
   step size settles at what your connection can actually deliver between drops (like TCP's window).
3. **Files of the latest commit, in batches.** sabr lists the missing blobs and fetches them by id
   (`git fetch --stdin` with `noop` negotiation, the same request git's own lazy fetch makes). Batches
   grow on success and shrink on failure. If even a single file cannot get through (a 2 GB asset on
   a link that drops every 50 MB), sabr downloads that one file from `raw.githubusercontent.com` with
   HTTP byte ranges and stores it with `git hash-object`, checking that the id matches exactly.
4. **Checkout,** with lazy fetching disabled, so nothing can silently fall back to a one-shot download.

The result is a normal git repository with complete history. By default it is a *partial clone*
(like `git clone --filter=blob:none`): old versions of files are fetched when a command needs them.
`--all-blobs` downloads every version up front in the same resumable batches.

Progress lives in `.git/sabr.json`, so killing sabr (or the laptop) loses at most the step in flight.
Stalls are detected by a watchdog on git's progress output, not just by TCP timeouts that can take
many minutes.

**docker pull** talks to the registry API directly: token auth (anonymous, `~/.docker/config.json`
or `SABR_REGISTRY_USER`/`SABR_REGISTRY_PASSWORD`), picks your platform from multi-arch indexes, and
downloads each blob with HTTP `Range` requests into a shared cache, verifying every SHA-256. It writes
the archive layout `docker save` produces since Docker 25 (an OCI image layout plus `manifest.json`),
so `docker load`, the containerd image store and `podman load` all accept it. Works with Docker Hub,
GHCR, Quay, private registries and mirrors such as `docker.arvancloud.ir`.

**sabr get** keeps `file.part` plus a small metadata file, resumes with `Range` + `If-Range`, starts
over when the server says the file changed, and verifies `--sha256`.

## Compared with what exists

| | survives a drop | survives a kill / reboot | automatic | git history | docker image | single file |
|---|:-:|:-:|:-:|:-:|:-:|:-:|
| `git clone` | ✗ | ✗ | – | ✓ | | |
| `git clone --depth 1` then `git fetch --deepen` by hand | per step | per step | ✗ | ✓ | | |
| `git backfill` (git 2.49+) | old blobs only | old blobs only | ✓ | needs a finished blobless clone first | | |
| [Stack Overflow sparse-checkout batch script](https://stackoverflow.com/questions/79852493) | files only | files only | partly | metadata clone is still one shot | | |
| `docker pull` (default store) | ✗ | ✗ | – | | ✓ | |
| `docker pull` with the containerd snapshotter | ✓ | ✓ | ✓ | | ✓ (daemon config change) | |
| `skopeo` / `crane` | retries restart the blob | ✗ | ✓ | | ✓ | |
| `wget -c`, `curl -C -`, aria2 | ✓ | ✓ | ✓ | | | ✓ |
| **sabr** | ✓ | ✓ | ✓ | ✓ | ✓ (no daemon changes, any machine) | ✓ |

## Verified

| What | How |
|---|---|
| Plain `git clone` fails, `sabr clone` succeeds, through a link cut every ~400 KB | e2e test with a real `git daemon` and the chaos proxy |
| Identical result: same HEAD, same commit count, same files, clean work tree, tags | e2e checks against the source repo |
| `--all-blobs` leaves no missing objects; `git log -p` works offline | e2e |
| `kill -9` mid-clone, then the same command resumes and finishes | e2e |
| `sabr pull` brings new commits through the bad link; fast-forward | e2e |
| Missing repositories fail fast instead of retrying forever | e2e |
| Real GitHub (`cli/cli`, 12 000+ commits) through a 2 MB-cut proxy: plain git fails, sabr matches `ls-remote` | CI `real-network` job |
| Real Docker Hub `python:3.12-slim` through the same proxy, `docker load`, `docker run` | CI `real-network` job |
| `--platform linux/arm64` from a multi-arch index | CI |
| `sabr get` of a Node.js tarball through the proxy, verified against nodejs.org SHASUMS | CI |
| Resume across processes, changed-file restart, stall detection, 404 / checksum are fatal | unit tests |
| Docker archive: every blob intact, both manifest formats, cache reuse | unit test with a fake registry behind the proxy |
| Installers: fresh HOME, PATH update, tampered binary refused; Windows PowerShell 5.1 and 7 | CI |

Tested on Linux, macOS and Windows in CI. Not tested yet: git servers without partial-clone support
in the wild (sabr falls back to resumable history steps that include files), submodules
(cloned normally by `git submodule update` afterwards), SSH remotes through the stall watchdog.

## Limits

- One git object can only arrive over git in one piece. For github.com sabr falls back to byte ranges
  for such a file; other hosts need the connection to stay up long enough for that single file.
- `--depth` counts commits along first parents, like git.
- sabr makes many small requests instead of one big one. On a perfect connection it is a little
  slower than `git clone`; on a bad one it is the difference between finishing and not.
- It does not bypass filtering or sanctions; it makes the connection you already have go further.

## Develop

```sh
go test ./...            # unit + end-to-end tests (needs git; uses git daemon locally)
go build ./cmd/sabr
sh scripts/build.sh 0.1.0   # cross-compiled release binaries + SHA256SUMS in dist/
```

## Contributing

Bug reports, ideas and pull requests are welcome, in English or Persian. Start with
[CONTRIBUTING.md](CONTRIBUTING.md) (setup, where the code lives, how to test) and the
[issue forms](https://github.com/mrzroot/sabr/issues/new/choose). Release history is in [CHANGELOG.md](CHANGELOG.md).

## License

MIT © [Mohammadreza Zare (M-R-Z)](https://github.com/mrzroot)
