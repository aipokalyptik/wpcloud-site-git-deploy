# wpcloud-site-git-deploy

`wpcloud-site-git-deploy` is a clean-room, statically compiled Go deploy tool
for WP Cloud-style sites.

The implementation is intentionally split into reviewable Go packages for
command parsing, config, state, claim reconciliation, symlink validation, and
atomic path exchange.

## Current Shape

- Go module: `github.com/aipokalyptik/wpcloud-site-git-deploy`
- Binary entrypoint: `cmd/wpcloud-site-git-deploy`
- Correctness contract: `docs/invariants.md`
- Correctness coverage: Go unit/integration tests, the Go conformance harness,
  the testing matrix, and the live E2E matrix

The hard environmental invariant remains unchanged: public docroot symlinks must
be relative and must resolve under the docroot. WP Cloud mounts `$HOME` for SSH
but not for HTTP requests, so HTTP-visible links must never target `$HOME`.

## Build

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./cmd/wpcloud-site-git-deploy
```

The implementation uses the Go standard library plus `golang.org/x/sys`.
External deploy subsystems still run as external commands where that is the
contract: `git`, `git-lfs`, `ssh`, `ssh-keygen`, and `rsync`.

Common local tasks are also wrapped in `make` targets. The Makefile keeps Go
build and module caches under the ignored `.cache/` directory so checks do not
write into `$HOME`:

```bash
make build      # static Linux amd64 binary in dist/
make check      # Go tests, vet, shell syntax, shellcheck, and diff checks
make conformance # black-box Go binary conformance checks
make clean      # remove generated build/cache artifacts
make live-e2e   # run the disposable-site E2E matrix
```

## Install

Build or provide a Linux amd64 binary, then run:

```bash
scripts/install.sh
```

The installer places the binary at:

```text
$HOME/.wpcloud-site-git-deploy/bin/wpcloud-site-git-deploy
```

On a WP Cloud/Pressable site, build the static binary elsewhere and include it
as `dist/wpcloud-site-git-deploy-linux-amd64` before uploading the source bundle.
The installer uses that bundled binary first and only builds on-host when a Go
toolchain is available.

## Test

Run Go tests:

```bash
go test ./...
go vet ./...
```

Run the local Makefile check suite:

```bash
make check
```

`make check` runs Go tests, `go vet`, maintained shell syntax checks,
`shellcheck`, the Go conformance harness, static live-E2E script checks, and
diff whitespace checks.

Run the Go live E2E matrix against the disposable WP Cloud/Pressable site
configured in `.env.local`:

```bash
scripts/live-e2e.sh
```

The live matrix installs the static Go binary, exercises deploy, no-op, force,
deploy roots, post-deploy maintenance, shared WordPress path safety, tag/commit
deploys, submodules, deploy-key auth, doctor checks, rollback, inspection
commands, and Git LFS hydration. If `git-lfs` is missing on the disposable
site, the matrix downloads the official Linux amd64 Git LFS release artifact,
verifies its SHA-256 checksum, and installs it into the tool-managed bin
directory before running the LFS deploy scenario.

## Go CLI Direction

The Go CLI uses a flat verb namespace and no positional arguments. Deployment
name is always passed as `--name`.

Examples:

```bash
wpcloud-site-git-deploy init \
  --name site \
  --repo git@example.com:team/site.git \
  --docroot /srv/htdocs \
  --deployment-id site \
  --default-ref main

wpcloud-site-git-deploy deploy --name site
wpcloud-site-git-deploy deploy --name site --branch main --force
wpcloud-site-git-deploy deploy --name site --tag v1.2.3
wpcloud-site-git-deploy deploy --name site --commit SHA
wpcloud-site-git-deploy rollback --name site --to RELEASE_ID
wpcloud-site-git-deploy status --name site
wpcloud-site-git-deploy auth --name site
wpcloud-site-git-deploy auth --name site --use-key /path/to/key
wpcloud-site-git-deploy auth --name site --import-key /path/to/key
wpcloud-site-git-deploy doctor --name site
wpcloud-site-git-deploy config --name site --set deploy_root=public
wpcloud-site-git-deploy config --name site --unset deploy_root
wpcloud-site-git-deploy list
wpcloud-site-git-deploy destroy --name site --confirm-destroy=site
```

`update` is intentionally removed. `deploy --name site` with no explicit
`--branch`, `--tag`, or `--commit` deploys the configured default ref.

All command input is flag-based. There are no positional deployment names in the
Go CLI.

## Reference Docs

The supported user-facing behavior is documented by this README. The core
correctness contract lives in `docs/invariants.md`, and test coverage is mapped
in `docs/testing-matrix.md`.
