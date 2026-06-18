# Code Flow

This document maps each public verb to the Go code path that handles it. The
charts are intentionally split by verb so a maintainer can audit one command
without reading the whole CLI. Line links point to the current implementation and
should be refreshed when the relevant code moves.

All public commands enter through `main`, parse into a `cli.Command`, resolve the
tool state layout, and dispatch by verb.

## Verb Index

This table is the fastest way to understand command intent before dropping into
the flowcharts.

| Verb | Primary handler | Main package boundary | Mutates state? | Intent |
| --- | --- | --- | --- | --- |
| `help`, `--help`, `-h` | `runHelp` | `internal/cli` | No | Print command syntax. |
| `version`, `--version` | `runVersion` | `internal/cli` | No | Print build version. |
| `init` | `runInit` | `internal/config` | `$HOME` config | Create a deployment config. |
| `list` | `runList` | `internal/state` | No | List configured deployment names. |
| `status` | `runStatus` | `internal/config` | No | Print one deployment config. |
| `config` | `runConfig` | `internal/config` | `$HOME` config | Set or unset supported config keys. |
| `deploy` | `runDeploy` | `internal/engine` | `$HOME` cache/worktree and docroot release namespace | Fetch, stage, promote, and record a release. |
| `rollback` | `runRollback` | `internal/engine` | docroot public links and current pointer | Make an existing release current. |
| `releases` | `runReleases` | `internal/releases` | No | List release ids and mark current. |
| `branches` | `runInspection` | `internal/engine` | repo cache only when `--fetch` is set | Inspect cached or freshly fetched branch refs. |
| `tags` | `runInspection` | `internal/engine` | repo cache only when `--fetch` is set | Inspect cached or freshly fetched tags. |
| `commits` | `runInspection` | `internal/engine` | repo cache only when `--fetch` is set | Inspect cached or freshly fetched commits. |
| `auth` | `runAuth` | `internal/auth` | config and optional managed key files | Configure or remove the deploy key. |
| `doctor` | `runDoctor` | `internal/doctor` | No | Report environment, key, Git, claims, and symlink health. |
| `destroy` | `runDestroy` | `internal/state` | `$HOME` tool-managed state | Remove config/cache/tmp/managed keys, but not the served docroot release. |

```mermaid
flowchart TD
  MAIN["main()"]
  RUN["cli.Run(ctx,args,stdout,stderr)"]
  PARSE["Parse(args)"]
  VALIDATE["validateCommand"]
  LAYOUT["defaultLayout"]
  DISPATCH{"cmd.Verb"}
  HANDLER["verb handler"]
  ERROR["return error to stderr"]

  MAIN --> RUN
  RUN --> PARSE
  PARSE --> VALIDATE
  VALIDATE --> LAYOUT
  LAYOUT --> DISPATCH
  DISPATCH --> HANDLER
  PARSE --> ERROR
  VALIDATE --> ERROR
  HANDLER --> ERROR
```

Source anchors:

- Entry point: [cmd/wpcloud-site-git-deploy/main.go](../cmd/wpcloud-site-git-deploy/main.go#L10)
- Parser: [internal/cli/parser.go](../internal/cli/parser.go#L75)
- Validation: [internal/cli/parser.go](../internal/cli/parser.go#L156)
- Dispatcher: [internal/cli/run.go](../internal/cli/run.go#L124)
- State layout root: [internal/cli/run.go](../internal/cli/run.go#L456)

## Help And Version

```mermaid
flowchart TD
  DISPATCH{"cmd.Verb"}
  HELP["runHelp"]
  VERSION["runVersion"]
  STDOUT["write stdout"]

  DISPATCH -->|"help, --help, -h"| HELP
  DISPATCH -->|"version, --version"| VERSION
  HELP --> STDOUT
  VERSION --> STDOUT
```

Source anchors:

- Dispatch cases: [internal/cli/run.go](../internal/cli/run.go#L133)
- Help output: [internal/cli/run.go](../internal/cli/run.go#L166)
- Version output: [internal/cli/run.go](../internal/cli/run.go#L195)

## `init`

```mermaid
flowchart TD
  PARSE["parse init flags"]
  VALIDATE["require repo, docroot, deployment id, default ref, keep >= 1"]
  BUILD["build config.Deployment"]
  MAINT{"maintenance override?"}
  SAVE["config.Save config.json"]
  PRINT["print initialized"]

  PARSE --> VALIDATE
  VALIDATE --> BUILD
  BUILD --> MAINT
  MAINT -->|"--maintenance-file"| BUILD_MAINT["set explicit maintenance file"]
  MAINT -->|"--no-maintenance-file"| BUILD_NO_MAINT["disable maintenance"]
  MAINT -->|"default"| SAVE
  BUILD_MAINT --> SAVE
  BUILD_NO_MAINT --> SAVE
  SAVE --> PRINT
```

Source anchors:

- Init flags: [internal/cli/parser.go](../internal/cli/parser.go#L94)
- Init validation: [internal/cli/parser.go](../internal/cli/parser.go#L169)
- Handler: [internal/cli/run.go](../internal/cli/run.go#L200)
- Config save: [internal/config/config.go](../internal/config/config.go#L80)

## `list`

```mermaid
flowchart TD
  HANDLER["runList"]
  ROOT["$HOME/.wpcloud-site-git-deploy/deployments"]
  READ["read deployment directories"]
  SORT["sort names"]
  PRINT["print one name per line"]

  HANDLER --> ROOT --> READ --> SORT --> PRINT
```

Source anchors:

- Handler: [internal/cli/run.go](../internal/cli/run.go#L226)
- Directory enumeration: [internal/cli/run.go](../internal/cli/run.go#L464)
- State deployment directory: [internal/state/state.go](../internal/state/state.go#L18)

## `status`

```mermaid
flowchart TD
  HANDLER["runStatus"]
  LOAD["config.Load"]
  PRINT["print config fields"]

  HANDLER --> LOAD --> PRINT
```

Source anchors:

- Handler: [internal/cli/run.go](../internal/cli/run.go#L237)
- Status output: [internal/cli/run.go](../internal/cli/run.go#L483)
- Config load: [internal/config/config.go](../internal/config/config.go#L113)

## `config`

```mermaid
flowchart TD
  PARSE["parse repeated --set and --unset"]
  VALIDATE["validate supported keys"]
  LOAD["load deployment config"]
  APPLY["applyConfigChanges"]
  ACTION{"set or unset"}
  SET["setConfigValue via configKeySpecs"]
  UNSET["unsetConfigValue via configKeySpecs"]
  SAVE["config.Save"]
  PRINT["print configured"]

  PARSE --> VALIDATE --> LOAD --> APPLY --> ACTION
  ACTION -->|"--set key=value"| SET --> SAVE
  ACTION -->|"--unset key"| UNSET --> SAVE
  SAVE --> PRINT
```

Source anchors:

- Config flags: [internal/cli/parser.go](../internal/cli/parser.go#L102)
- Config validation: [internal/cli/parser.go](../internal/cli/parser.go#L188)
- Key registry: [internal/cli/run.go](../internal/cli/run.go#L31)
- Handler: [internal/cli/run.go](../internal/cli/run.go#L246)
- Apply/set/unset: [internal/cli/run.go](../internal/cli/run.go#L500)

## `deploy`

```mermaid
flowchart TD
  PARSE["parse optional branch, tag, commit, force, hooks"]
  REF{"explicit ref?"}
  LOAD["load config"]
  ENGINE["engine.Deploy"]
  REQUIRE["require git and rsync"]
  REPO["ensure repo cache"]
  FETCH["fetch refs and tags"]
  RESOLVE["resolve commit"]
  NOOP{"same commit and deploy root, no force?"}
  WORKTREE["create git worktree"]
  FEATURES["prepare submodules and LFS"]
  ROOT["select deploy root"]
  RSYNC["rsync source to incoming"]
  PROMOTE["Promote incoming release"]
  META["write release metadata"]
  PRINT{"NoOp?"}

  PARSE --> REF
  REF -->|"none"| LOAD
  REF -->|"branch/tag/commit"| LOAD
  LOAD --> ENGINE --> REQUIRE --> REPO --> FETCH --> RESOLVE --> NOOP
  NOOP -->|"yes"| PRINT
  NOOP -->|"no or --force"| WORKTREE --> FEATURES --> ROOT --> RSYNC --> PROMOTE --> META --> PRINT
```

Source anchors:

- Deploy flags: [internal/cli/parser.go](../internal/cli/parser.go#L105)
- Ref validation: [internal/cli/parser.go](../internal/cli/parser.go#L206)
- CLI handler: [internal/cli/run.go](../internal/cli/run.go#L261)
- Engine entry: [internal/engine/deploy.go](../internal/engine/deploy.go#L34)
- Repo cache and fetch: [internal/engine/deploy.go](../internal/engine/deploy.go#L46)
- No-op check: [internal/engine/deploy.go](../internal/engine/deploy.go#L59)
- Worktree: [internal/engine/deploy.go](../internal/engine/deploy.go#L70)
- Git features: [internal/engine/deploy.go](../internal/engine/deploy.go#L234)
- Rsync incoming: [internal/engine/deploy.go](../internal/engine/deploy.go#L308)
- Promotion call: [internal/engine/deploy.go](../internal/engine/deploy.go#L113)
- Metadata write: [internal/engine/deploy.go](../internal/engine/deploy.go#L123)

### Deploy Decision Tree

The decision tree below shows why a deploy exits early, no-ops, or proceeds to
promotion. The table after it is usually better for auditing the safety intent of
each branch.

```mermaid
flowchart TD
  START["engine.Deploy"]
  COMMANDS{"git and rsync available?"}
  REPO{"repo cache exists?"}
  CLONE["clone bare repo"]
  SETURL["set origin URL"]
  FETCH{"fetch succeeds?"}
  RESOLVE{"ref resolves?"}
  SAME{"current metadata matches commit and deploy root?"}
  FORCE{"--force?"}
  WORKTREE{"worktree add succeeds?"}
  SUBMODULES{"submodules initialized?"}
  LFS{"LFS paths detected?"}
  LFS_TOOL{"git-lfs available and pull succeeds?"}
  POINTERS{"LFS pointers remain?"}
  DEPLOY_ROOT{"deploy root exists and is directory?"}
  RSYNC{"rsync incoming succeeds?"}
  PROMOTE["enter promotion decision tree"]
  NOOP["return no_op=true"]
  FAIL["fail"]

  START --> COMMANDS
  COMMANDS -->|"no"| FAIL
  COMMANDS -->|"yes"| REPO
  REPO -->|"no"| CLONE --> FETCH
  REPO -->|"yes"| SETURL --> FETCH
  FETCH -->|"no"| FAIL
  FETCH -->|"yes"| RESOLVE
  RESOLVE -->|"no"| FAIL
  RESOLVE -->|"yes"| SAME
  SAME -->|"yes"| FORCE
  FORCE -->|"no"| NOOP
  FORCE -->|"yes"| WORKTREE
  SAME -->|"no"| WORKTREE
  WORKTREE -->|"no"| FAIL
  WORKTREE -->|"yes"| SUBMODULES
  SUBMODULES -->|"no"| FAIL
  SUBMODULES -->|"yes"| LFS
  LFS -->|"no"| DEPLOY_ROOT
  LFS -->|"yes"| LFS_TOOL
  LFS_TOOL -->|"no"| FAIL
  LFS_TOOL -->|"yes"| POINTERS
  POINTERS -->|"yes"| FAIL
  POINTERS -->|"no"| DEPLOY_ROOT
  DEPLOY_ROOT -->|"no"| FAIL
  DEPLOY_ROOT -->|"yes"| RSYNC
  RSYNC -->|"no"| FAIL
  RSYNC -->|"yes"| PROMOTE
```

Source anchors:

- Command requirements: [internal/engine/deploy.go](../internal/engine/deploy.go#L35)
- Repo cache behavior: [internal/engine/deploy.go](../internal/engine/deploy.go#L137)
- Fetch and GC: [internal/engine/deploy.go](../internal/engine/deploy.go#L49)
- Ref resolution: [internal/engine/deploy.go](../internal/engine/deploy.go#L194)
- No-op decision: [internal/engine/deploy.go](../internal/engine/deploy.go#L59)
- Worktree cleanup: [internal/engine/deploy.go](../internal/engine/deploy.go#L70)
- Submodule decision: [internal/engine/deploy.go](../internal/engine/deploy.go#L235)
- LFS decision: [internal/engine/deploy.go](../internal/engine/deploy.go#L253)
- Deploy root decision: [internal/engine/deploy.go](../internal/engine/deploy.go#L85)
- Rsync/link-dest: [internal/engine/deploy.go](../internal/engine/deploy.go#L308)

Deploy decisions:

| Decision | Success path | Failure or alternate path | Why it exists |
| --- | --- | --- | --- |
| Required commands | Continue when `git` and `rsync` are present. | Fail before touching state. | Deploy depends on Git object access and rsync staging semantics. |
| Repo cache | Reuse existing bare cache or clone it. | Fail if clone/cache access fails. | Keeps repeated deploys fast while preserving a single source of refs. |
| Fetch | Fetch refs/tags and run best-effort GC. | Fail if fetch fails. | Deploy must compare against the latest remote state. |
| Ref resolution | Resolve branch, tag, or commit to a commit SHA. | Fail if the requested ref is invalid. | Release metadata records the exact commit that was promoted. |
| No-op check | Return `no_op=true` when commit and deploy root match current metadata. | Continue when `--force` is set or metadata differs. | Makes cron-safe deploys cheap while preserving intentional redeploys. |
| Worktree | Add a detached worktree for the target commit. | Fail and clean temp worktree. | Keeps the cache bare and stages from a real filesystem tree. |
| Submodules | Initialize recursively and verify none remain uninitialized. | Fail before rsync. | A served release should not contain unresolved submodule placeholders. |
| Git LFS | Hydrate LFS files and reject remaining pointer files. | Fail before rsync. | Prevents publishing pointer text where binary/site assets are expected. |
| Deploy root | Use configured subdirectory as effective repo root. | Fail if missing or not a directory. | Monorepo/build-output deploys should publish subfolder contents at docroot root. |
| Rsync staging | Copy source into incoming, hardlinking unchanged files where possible. | Fail before promotion. | Incoming must be complete before public claims are touched. |

### Promotion Decision Tree

```mermaid
flowchart TD
  START["Promote"]
  LOCK{"lock acquired?"}
  CLEAN["cleanup exchanged paths and owned maintenance"]
  INCOMING{"incoming exists?"}
  MAINT{"create maintenance marker?"}
  BOUNDARIES["discover boundaries"]
  MOVE["rename incoming to releases/id"]
  CLAIMS["compute new claims"]
  PROTECTED{"claim overlaps protected anchor?"}
  OLD["compute current and materialized old claims"]
  REMOVED["compute removed claims"]
  RECONCILE["reconcile new claims"]
  SWITCH["switch current to releases/id"]
  ASSERT{"public symlinks valid?"}
  HOOK{"post-deploy hook succeeds?"}
  CLEANUP["cleanup maintenance and prune"]
  FAIL["fail"]
  DONE["success"]

  START --> LOCK
  LOCK -->|"no"| FAIL
  LOCK -->|"yes"| CLEAN --> INCOMING
  INCOMING -->|"no"| FAIL
  INCOMING -->|"yes"| MAINT --> BOUNDARIES --> MOVE --> CLAIMS --> PROTECTED
  PROTECTED -->|"yes"| FAIL
  PROTECTED -->|"no"| OLD --> REMOVED --> RECONCILE --> SWITCH --> ASSERT
  ASSERT -->|"no"| FAIL
  ASSERT -->|"yes"| HOOK
  HOOK -->|"no"| FAIL
  HOOK -->|"yes"| CLEANUP --> DONE
```

Source anchors:

- Promotion entry and lock: [internal/engine/promote.go](../internal/engine/promote.go#L45)
- Incoming/release move: [internal/engine/promote.go](../internal/engine/promote.go#L71)
- Maintenance creation: [internal/engine/promote.go](../internal/engine/promote.go#L80)
- Claim computation: [internal/engine/promote.go](../internal/engine/promote.go#L105)
- Protected anchors: [internal/engine/promote.go](../internal/engine/promote.go#L109)
- Materialized claims: [internal/engine/promote.go](../internal/engine/promote.go#L116)
- Reconciliation: [internal/engine/promote.go](../internal/engine/promote.go#L268)
- Current switch: [internal/engine/promote.go](../internal/engine/promote.go#L315)
- Symlink assertion: [internal/publicfs/publicfs.go](../internal/publicfs/publicfs.go#L28)
- Post-deploy hook: [internal/engine/promote.go](../internal/engine/promote.go#L608)
- Pruning: [internal/releases/releases.go](../internal/releases/releases.go#L109)

Promotion phases:

| Phase | Reads | Writes | Intent |
| --- | --- | --- | --- |
| Lock and retry cleanup | `deploy.lock`, `exchanged_paths` | lock file, removed stale exchange temp paths | Ensure only one deploy for a deployment id mutates the namespace and retry previous cleanup. |
| Maintenance marker | configured maintenance path | tool-owned PHP marker | Ask WordPress to serve maintenance mode during promotion and hooks. |
| Incoming to release | `incoming/<id>` | `releases/<id>` | Move complete staged content into the release namespace before public links point at it. |
| Claim computation | new release tree, current release tree, materialized public symlinks | in-memory claim sets | Determine which public paths must exist and which old owned paths can be removed. |
| Protected checks | docroot ownership/writability | none | Refuse to claim root/group-owned protected anchors. |
| Reconciliation | public docroot paths | public symlinks, exchanged path log | Create or atomically reclaim public paths. |
| Current switch | release namespace | `current` relative symlink | Flip active release with a single rename. |
| Assertion and hook | public symlinks, post-deploy script | hook-defined side effects | Validate symlink containment, then run operator hook. |
| Cleanup and prune | maintenance marker, releases dir | removed marker, pruned old releases | Leave the site out of maintenance and bound release retention. |

### Claim Reconciliation Decision Tree

```mermaid
flowchart TD
  START["for each new claim"]
  ANCESTOR{"ancestor is another deployment symlink?"}
  DESCENDANT{"directory contains another deployment symlink?"}
  PARENT["mkdir parent directories"]
  TEMP["create temporary relative symlink"]
  EXISTS{"public path exists?"}
  RENAME["rename temp into place"]
  EXCHANGE["renameat2 exchange temp with existing path"]
  RECORD["record exchanged temp path"]
  REMOVE["remove exchanged-away temp path"]
  FAIL["fail"]
  NEXT["next claim"]

  START --> ANCESTOR
  ANCESTOR -->|"yes"| FAIL
  ANCESTOR -->|"no"| DESCENDANT
  DESCENDANT -->|"yes"| FAIL
  DESCENDANT -->|"no"| PARENT --> TEMP --> EXISTS
  EXISTS -->|"no"| RENAME --> NEXT
  EXISTS -->|"yes"| EXCHANGE --> RECORD --> REMOVE --> NEXT
```

Source anchors:

- Reconcile loop: [internal/engine/promote.go](../internal/engine/promote.go#L268)
- Foreign ancestor rejection: [internal/engine/promote.go](../internal/engine/promote.go#L389)
- Foreign descendant rejection: [internal/engine/promote.go](../internal/engine/promote.go#L407)
- Relative public target: [internal/publicfs/publicfs.go](../internal/publicfs/publicfs.go#L12)
- Atomic exchange: [internal/publicfs/exchange_linux.go](../internal/publicfs/exchange_linux.go#L8)
- Exchanged path cleanup: [internal/engine/promote.go](../internal/engine/promote.go#L468)

Claim reconciliation outcomes:

| Existing public path | Result | Reason |
| --- | --- | --- |
| Missing | Rename new relative symlink into place. | No visible path needs reclaiming. |
| Normal file or directory | Exchange new symlink with existing path, then delete exchanged-away temp path. | Reclaim atomically without a missing-path window. |
| Exact foreign deployment symlink | Exchange and take over exact public path. | Same-path ownership is user intent; no extra policy is imposed. |
| Foreign deployment symlink in an ancestor | Fail. | The new claim would route through another deployment's `current`. |
| Foreign deployment symlink below a real directory being claimed | Fail. | Replacing the directory would engulf another deployment. |

### Claim Computation Decision Tree

```mermaid
flowchart TD
  START["claims.Compute"]
  WALK["walk release tree"]
  ENTRY{"entry is regular file or symlink?"}
  SKIP["skip"]
  PUBLIC["convert to slash public path"]
  RUNTIME{"cache, upgrade, or maintenance path?"}
  MEDIA{"under uploads or blogs.dir?"}
  MEDIA_EXACT{"exact container path?"}
  MEDIA_SYMLINK{"repo symlink?"}
  LEAF["claim exact leaf path"]
  BOUNDARY{"inside sticky boundary?"}
  BOUNDARY_CLAIM["claim boundary child"]
  TOP["claim top-level path"]
  FAIL["fail"]

  START --> WALK --> ENTRY
  ENTRY -->|"no"| SKIP
  ENTRY -->|"yes"| PUBLIC --> RUNTIME
  RUNTIME -->|"yes"| FAIL
  RUNTIME -->|"no"| MEDIA
  MEDIA -->|"yes"| MEDIA_EXACT
  MEDIA_EXACT -->|"yes"| FAIL
  MEDIA_EXACT -->|"no"| MEDIA_SYMLINK
  MEDIA_SYMLINK -->|"yes"| FAIL
  MEDIA_SYMLINK -->|"no"| LEAF
  MEDIA -->|"no"| BOUNDARY
  BOUNDARY -->|"yes"| BOUNDARY_CLAIM
  BOUNDARY -->|"no"| TOP
```

Source anchors:

- Shared path lists: [internal/claims/claims.go](../internal/claims/claims.go#L14)
- Claim walker: [internal/claims/claims.go](../internal/claims/claims.go#L28)
- Shared media rules: [internal/claims/claims.go](../internal/claims/claims.go#L76)
- Boundary compression: [internal/claims/claims.go](../internal/claims/claims.go#L116)

Claim policy table:

| Release-tree path | Claim behavior | Why |
| --- | --- | --- |
| `.git`, `.wpcloud-site-git-deploy` | Skip. | Internal VCS/tool state must never become public claims. |
| `wp-content/cache`, `wp-content/upgrade`, maintenance file | Reject. | Runtime/control paths are not deploy targets. |
| Regular file under `wp-content/uploads` or `wp-content/blogs.dir` | Claim exact leaf path. | WordPress owns the container directory; deploy owns only its leaf file symlink. |
| Symlink under `wp-content/uploads` or `wp-content/blogs.dir` | Reject. | A symlink can behave like a directory or point outside the regular-file safety model. |
| Path under sticky boundary | Claim the first child under the deepest boundary. | Avoid claiming an entire platform-managed boundary. |
| Other nested path | Claim top-level segment. | Normal deploys can publish a compact top-level tree symlink. |

## `rollback`

```mermaid
flowchart TD
  PARSE["parse optional --to"]
  LOAD["load config"]
  TARGET{"--to provided?"}
  SELECT["selectRollbackTarget"]
  ENGINE["engine.Rollback"]
  LOCK["acquire deployment lock"]
  CLAIMS["compute selected release claims"]
  RECONCILE["reconcile public claims"]
  SWITCH["switch current"]
  CLEAN["remove old owned claims and maintenance"]
  PRINT["print rolled_back"]

  PARSE --> LOAD --> TARGET
  TARGET -->|"yes"| ENGINE
  TARGET -->|"no"| SELECT --> ENGINE
  ENGINE --> LOCK --> CLAIMS --> RECONCILE --> SWITCH --> CLEAN --> PRINT
```

Source anchors:

- Rollback flag: [internal/cli/parser.go](../internal/cli/parser.go#L111)
- CLI handler: [internal/cli/run.go](../internal/cli/run.go#L286)
- Default target selection: [internal/cli/run.go](../internal/cli/run.go#L550)
- Engine rollback: [internal/engine/promote.go](../internal/engine/promote.go#L160)

## `releases`

```mermaid
flowchart TD
  LOAD["load config"]
  CURRENT["read current release id"]
  LIST["releases.List"]
  MARK["mark current release"]
  PRINT["print release ids"]

  LOAD --> CURRENT --> LIST --> MARK --> PRINT
```

Source anchors:

- CLI handler: [internal/cli/run.go](../internal/cli/run.go#L306)
- Current release id: [internal/state/state.go](../internal/state/state.go#L55)
- Release listing: [internal/releases/releases.go](../internal/releases/releases.go#L81)

## `branches`, `tags`, And `commits`

```mermaid
flowchart TD
  PARSE["parse --fetch and --limit"]
  LOAD["load config"]
  CACHE["EnsureRepo"]
  FETCH{"--fetch?"}
  LIST{"verb"}
  BRANCHES["engine.Branches"]
  TAGS["engine.Tags"]
  COMMITS["engine.Commits"]
  PRINT["print lines"]

  PARSE --> LOAD --> CACHE --> FETCH --> LIST
  LIST -->|"branches"| BRANCHES --> PRINT
  LIST -->|"tags"| TAGS --> PRINT
  LIST -->|"commits"| COMMITS --> PRINT
```

Source anchors:

- Shared flags: [internal/cli/parser.go](../internal/cli/parser.go#L113)
- CLI handler: [internal/cli/run.go](../internal/cli/run.go#L327)
- Repo cache/fetch: [internal/engine/deploy.go](../internal/engine/deploy.go#L151)
- Branches/tags/commits: [internal/engine/deploy.go](../internal/engine/deploy.go#L167)

## `auth`

```mermaid
flowchart TD
  PARSE["parse auth key source flags"]
  VALIDATE["choose only one key source"]
  LOAD["load config"]
  HTTPS{"repo URL is HTTPS?"}
  CONVERT["convert to git@host:path"]
  ACTION{"auth action"}
  GENERATE["generate or reuse managed key"]
  USE["validate external key path"]
  IMPORT["copy key into managed key path"]
  REMOVE["clear ssh_key_path, optionally purge managed key"]
  VERIFY{"--verify?"}
  REMOTE["git ls-remote with GIT_SSH_COMMAND"]
  SAVE["save config"]
  PRINT["print public key and configured message"]

  PARSE --> VALIDATE --> LOAD --> HTTPS
  HTTPS -->|"yes"| CONVERT --> ACTION
  HTTPS -->|"no"| ACTION
  ACTION -->|"default"| GENERATE --> VERIFY
  ACTION -->|"--use-key"| USE --> VERIFY
  ACTION -->|"--import-key"| IMPORT --> VERIFY
  ACTION -->|"--remove"| REMOVE --> SAVE
  VERIFY -->|"yes"| REMOTE --> SAVE
  VERIFY -->|"no"| SAVE
  SAVE --> PRINT
```

Source anchors:

- Auth flags: [internal/cli/parser.go](../internal/cli/parser.go#L116)
- Key-source validation: [internal/auth/auth.go](../internal/auth/auth.go#L32)
- CLI handler: [internal/cli/run.go](../internal/cli/run.go#L354)
- HTTPS conversion: [internal/auth/auth.go](../internal/auth/auth.go#L19)
- Key generation/import/use: [internal/auth/keys.go](../internal/auth/keys.go#L15)
- Remote verification: [internal/auth/keys.go](../internal/auth/keys.go#L141)

## `doctor`

```mermaid
flowchart TD
  PARSE["parse offline and diagnostic flags"]
  LOAD{"config loads?"}
  REPORT_FAIL["report config FAIL"]
  DOCTOR["doctor.Run"]
  COMMANDS["check git, rsync, ssh, ssh-keygen"]
  DOCROOT["check docroot"]
  KEY["check SSH key or warn ambient SSH"]
  CLAIMS{"--print-claims?"}
  ASSERT{"--assert-public-symlinks?"}
  REMOTE{"offline?"}
  PRINT["print claims and report"]
  EXIT{"any FAIL?"}

  PARSE --> LOAD
  LOAD -->|"no"| REPORT_FAIL --> PRINT
  LOAD -->|"yes"| DOCTOR --> DOCROOT --> COMMANDS --> KEY --> CLAIMS
  CLAIMS --> ASSERT --> REMOTE
  REMOTE -->|"no"| REMOTE_CHECK["git ls-remote"] --> PRINT
  REMOTE -->|"yes"| PRINT
  PRINT --> EXIT
```

Source anchors:

- Doctor flags: [internal/cli/parser.go](../internal/cli/parser.go#L123)
- CLI handler: [internal/cli/run.go](../internal/cli/run.go#L412)
- Doctor checks: [internal/doctor/run.go](../internal/doctor/run.go#L26)
- Claims diagnostic: [internal/engine/promote.go](../internal/engine/promote.go#L237)
- Symlink diagnostic: [internal/engine/promote.go](../internal/engine/promote.go#L250)

## `destroy`

```mermaid
flowchart TD
  PARSE["parse --confirm-destroy"]
  VALIDATE{"confirm equals name?"}
  REMOVE_CONFIG["remove deployment config dir"]
  REMOVE_REPO["remove repo cache"]
  REMOVE_TMP["remove temp worktrees"]
  REMOVE_KEYS["remove managed key files"]
  PRINT["print destroyed"]
  FAIL["fail"]

  PARSE --> VALIDATE
  VALIDATE -->|"no"| FAIL
  VALIDATE -->|"yes"| REMOVE_CONFIG --> REMOVE_REPO --> REMOVE_TMP --> REMOVE_KEYS --> PRINT
```

Source anchors:

- Destroy flag: [internal/cli/parser.go](../internal/cli/parser.go#L127)
- Destroy validation: [internal/cli/parser.go](../internal/cli/parser.go#L228)
- Handler: [internal/cli/run.go](../internal/cli/run.go#L436)
