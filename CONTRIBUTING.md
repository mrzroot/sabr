# Contributing to sabr

Thanks for helping! Issues and pull requests in **English or Persian** are welcome.
Reports from real bad connections (mobile data, throttled routes, satellite) are especially useful:
say what you ran, how the link behaved and where sabr gave up.

## Development setup

```sh
git clone https://github.com/mrzroot/sabr && cd sabr
go test ./...               # unit + end-to-end tests (needs git; uses git daemon locally)
go build ./cmd/sabr
sh scripts/build.sh 0.1.0   # cross-compiled release binaries + SHA256SUMS in dist/
```

To try a change on a bad link without having one, run `sabr chaos` in one terminal and point
any tool at it with `HTTPS_PROXY=http://127.0.0.1:8899`.

## Where things live

| Path | What |
|---|---|
| `cmd/sabr/` | The CLI, plus the end-to-end tests that clone through a flaky proxy |
| `internal/gitx/` | Resumable clone: history in steps, files in batches, drives your real `git` |
| `internal/httpx/` | Resumable HTTP downloads with byte ranges, retries and sha256 checks |
| `internal/registry/` | Resumable `docker pull` into a docker-loadable archive |
| `internal/chaos/` | The deliberately flaky proxy behind `sabr chaos` |
| `internal/retry/`, `internal/ui/` | Back-off and terminal output |

## Commits

Small commits, one change each, with a title that says **why** the change was made.

## Security

Please report vulnerabilities privately via [GitHub security advisories](https://github.com/mrzroot/sabr/security/advisories/new).
