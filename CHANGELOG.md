# Changelog

## 0.1.0 (2026-10-16)

First release.

- `sabr clone`: resumable git clone in four phases (latest commit, history in adaptive `--deepen`
  steps, files of the latest commit in adaptive batches, checkout), state in `.git/sabr.json`,
  stall watchdog, raw.githubusercontent.com byte-range fallback for a single file that never makes it
  over git, `--depth`, `--all-blobs`, `--no-tags`, `--no-checkout`, `-b`.
- `sabr resume`, `sabr status`, `sabr pull`.
- `sabr docker pull`: registry API with token auth, multi-arch platform selection, ranged blob
  downloads into a shared verified cache, Docker 25+ / OCI archive, `--load` into docker or podman.
- `sabr get`: resumable HTTP(S) download with `If-Range`, change detection and `--sha256`.
- `sabr chaos`: a proxy that cuts or freezes every connection after N bytes, to reproduce bad networks.
- One-line installers for macOS/Linux/FreeBSD and Windows that resume and verify SHA-256.
