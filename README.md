# wpcloud-site-git-deploy

`wpcloud-site-git-deploy` is a clean-room, statically compiled Go deploy tool
for WP Cloud-style sites.

The previous Bash implementation is preserved under `spec/` as the behavioral
oracle. The Go implementation is intentionally not a line-by-line port: command
parsing, config, state, claim reconciliation, symlink validation, and atomic path
exchange are split into reviewable Go packages.

## Current Shape

- Go module: `github.com/aipokalyptik/wpcloud-site-git-deploy`
- Binary entrypoint: `cmd/wpcloud-site-git-deploy`
- Bash reference implementation and tests: `spec/`
- Correctness contract: `spec/invariants.md`

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

Run the preserved Bash oracle on Linux:

```bash
spec/tests/run.sh
```

`spec/tests/run.sh` requires Linux/GNU tooling. macOS developers should run it
in Linux CI, a Linux VM/container, or on a disposable WP Cloud/Pressable site.

Run the Go live E2E matrix against the disposable WP Cloud/Pressable site
configured in `.env.local`:

```bash
scripts/live-e2e.sh
```

The live matrix installs the static Go binary, exercises deploy, no-op, force,
deploy roots, post-deploy maintenance, shared WordPress path safety, tag/commit
deploys, submodules, deploy-key auth, doctor checks, rollback, inspection
commands, and the current WP Cloud missing-`git-lfs` failure mode.

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

The Bash-era user documentation remains in `spec/docs/` so the oracle remains
understandable while the Go implementation reaches parity.
