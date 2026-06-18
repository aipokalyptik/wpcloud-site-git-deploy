# wpcloud-site-git-deploy

`wpcloud-site-git-deploy` is being rewritten as a clean-room, statically
compiled Go deploy tool for WP Cloud-style sites.

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
wpcloud-site-git-deploy rollback --name site --to RELEASE_ID
wpcloud-site-git-deploy status --name site
wpcloud-site-git-deploy list
wpcloud-site-git-deploy destroy --name site --confirm-destroy=site
```

`update` is intentionally removed. `deploy --name site` with no explicit
`--branch`, `--tag`, or `--commit` deploys the configured default ref.

## Reference Docs

The Bash-era user documentation remains in `spec/docs/` so the oracle remains
understandable while the Go implementation reaches parity.
