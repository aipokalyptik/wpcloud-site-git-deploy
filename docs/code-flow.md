# `bin/wpcloud-site-git-deploy` Code Flow

This diagram covers the current `main` branch layout. The project is one Bash
CLI with an internal promotion engine. `__remote-deploy` is hidden from the
public help output because it is for tests, promotion, rollback, and diagnostic
audits, not for day-to-day operator use.

```mermaid
flowchart TD
  A["Process starts"] --> B["Set constants and state paths"]
  B --> C["main(): dispatch command"]

  C -->|init| I["cmd_init"]
  I --> I1["Validate name, repo, docroot, deployment-id, default-ref, keep-releases, deploy-root, maintenance_file"]
  I1 --> I2["ensure_state_dirs"]
  I2 --> I3["ensure_helper: use mv --exchange when available, otherwise use or install exchange-rename"]
  I3 --> I4["Write deployment config as deployments/<name>/cfg-* value files"]

  C -->|config| CFG["cmd_config"]
  CFG --> CFG1["Set or clear cfg-deploy_root, cfg-post_deploy, or cfg-maintenance_file"]

  C -->|auth| AU["cmd_auth"]
  AU --> AU1["Load deployment config, optionally remove ssh_key_path, or normalize HTTPS URL to SSH when applicable"]
  AU1 --> AU2["Choose auth source: generate/reuse managed key, --use-key external key, or --import-key managed copy"]
  AU2 --> AU3["Validate private key permissions and derive public key without prompting"]
  AU3 --> AU4["Store cfg-ssh_key_path in deployment config"]
  AU4 --> AU5["Print public deploy key and host-specific instructions"]

  C -->|doctor| DOC["cmd_doctor"]
  DOC --> DOC1["Load config and check required local commands"]
  DOC1 --> DOC2["Check docroot, exchange helper, repo URL, and deploy key files"]
  DOC2 --> DOC3["Optionally run git ls-remote through configured GIT_SSH_COMMAND"]

  C -->|deploy| D["cmd_deploy"]
  D --> D1["Parse exactly one ref plus optional --force, --post-deploy, and --maintenance-file"]
  D1 --> DR["deploy_ref"]

  C -->|update| U["cmd_update"]
  U --> U1["Parse optional --force, --post-deploy, and --maintenance-file, then load_config"]
  U1 --> DR

  DR --> DR1["load_config"]
  DR1 --> DR2["ensure_state_dirs and ensure_helper"]
  DR2 --> DR3["fetch_repo: clone/cache, fetch tags/prune, git gc --auto"]
  DR3 --> DR4["resolve_ref to commit"]
  DR4 --> NR{"No --force and metadata commit + deploy_root match?"}
  NR -->|yes| NRO["Print no-op release_id ref_mode commit"]
  NR -->|no| DR5["make_release_id"]
  DR5 --> DR6["create_worktree via git worktree add --detach"]
  DR6 --> PGF["prepare_git_features"]
  PGF --> PGF1["Initialize submodules recursively if .gitmodules exists"]
  PGF1 --> PGF2["Batch git check-attr filter for tracked files"]
  PGF2 --> PGF3{"Any LFS paths?"}
  PGF3 -->|yes| PGF4["Require git-lfs, run lfs pull, reject unresolved pointers"]
  PGF3 -->|no| DR7["copy_worktree_to_incoming"]
  PGF4 --> DR7
  DR7 --> DR8["rsync worktree or deploy_root subdir to docroot incoming release, using --link-dest when possible"]
  DR8 --> PR["promote_release"]

  PR --> RDE["run_engine_subshell -> cmd_remote_deploy --release-id"]
  RDE --> EXC["Probe mv --exchange once, cache ENGINE_MV_EXCHANGE, and set default exchange helper when needed"]
  EXC --> ERQ["require_remote_capabilities"]
  ERQ --> ELOCK["Acquire deploy lock"]
  ELOCK -->|busy| EBUSY["Fail: deployment already running"]
  ELOCK -->|acquired| EMNT["Clean stale owned maintenance marker and create current marker unless disabled"]
  EMNT --> EPCT["prepare_claim_transition"]
  EPCT --> EPCT1["discover_boundary_claims"]
  EPCT1 --> EPCT2["discover_protected_anchors"]
  EPCT2 --> EPCT3["compute old claims + materialized public claims"]
  EPCT3 --> EPCT4["compute new claims"]
  EPCT4 --> EPCT5["apply shared media container policy; reject runtime shared paths and protected anchors"]
  EPCT5 --> EPCT6["compute removed claims"]
  EPCT6 --> MOVE["Move incoming to releases/<release-id>"]
  MOVE --> EACT["apply_claim_transition"]
  EACT --> EACT1["cleanup overlapping removed symlinks"]
  EACT1 --> EACT2["reconcile_new_claims"]
  EACT2 --> EACT3["reject foreign deployment ancestor/exact/descendant conflicts"]
  EACT3 --> EACT4["Create public symlink or atomically exchange existing path using cached mv --exchange decision or exchange-rename"]
  EACT4 --> EACT5["switch_current atomically"]
  EACT5 --> EACT6["cleanup exchanged paths and removed claims"]
  EACT6 --> EACT7["assert_claim_symlinks_under_docroot"]
  EACT7 --> POST["run_post_deploy when --post-deploy-file was provided"]
  POST --> RMNT["Remove owned maintenance marker"]
  RMNT --> PRUNE["prune_releases"]
  PRUNE --> ENGRET["Engine subshell returns to deploy_ref"]
  ENGRET --> META["write_release_metadata as metadata/<release-id>/cfg-* files"]
  META --> CLEAN["cleanup_worktree"]
  CLEAN --> OUT["Print release_id ref_mode commit"]

  C -->|rollback| RB["cmd_rollback"]
  RB --> RB1["load_config and ensure_helper"]
  RB1 --> RB2["current_release_id"]
  RB2 --> RB3{"--to provided?"}
  RB3 -->|no| RB4["select_rollback_release from metadata-backed releases"]
  RB3 -->|yes| RBR["run_engine_subshell -> cmd_remote_deploy --rollback-to"]
  RB4 --> RBR
  RBR --> REXC["Probe mv --exchange once, cache ENGINE_MV_EXCHANGE, and set default exchange helper when needed"]
  REXC --> RRC["require_remote_capabilities"]
  RRC --> RLOCK["Acquire deploy lock"]
  RLOCK -->|busy| RBUSY["Fail: deployment already running"]
  RLOCK -->|acquired| RMNT1["Clean stale owned maintenance marker and create rollback marker unless disabled"]
  RMNT1 --> RPCT["prepare_claim_transition for existing release"]
  RPCT --> RACT["apply_claim_transition"]
  RACT --> RACT1["switch_current atomically and clean removed claims"]
  RACT1 --> RACT2["assert_claim_symlinks_under_docroot"]
  RACT2 --> RMNT2["Remove owned maintenance marker"]
  RMNT2 --> RRET["Engine subshell returns to cmd_rollback"]
  RRET --> RBOUT["Print rolled back release"]

  C -->|releases| REL["cmd_releases"]
  REL --> REL1["load_config"]
  REL1 --> REL2["List release dirs, mark current, include metadata"]

  C -->|branches/tags/commits| INS["cmd_branches/cmd_tags/cmd_commits"]
  INS --> INS1["load_config"]
  INS1 --> INS2{"--fetch?"}
  INS2 -->|yes| DR3
  INS2 -->|no| ERC["ensure_repo_cache only"]
  ERC --> INS3["Read cached refs/log and print limited output"]
  DR3 --> INS3

  C -->|status| ST["cmd_status"]
  ST --> ST1["load_config"]
  ST1 --> ST2["Print config and current release"]

  C -->|__remote-deploy| H["Hidden test/internal command"]
  H --> CRD["cmd_remote_deploy"]
  CRD --> CRD1["Parse deploy, rollback, or audit mode, cache exchange capability for engine runs, and run the matching path"]

  C -->|--help / --version| HV["Print usage/version"]
```

The public commands and hidden diagnostic/internal command share the same final
dispatcher. Promotion and rollback call the internal engine through
`run_engine_subshell` so engine exits remain contained and caller cleanup paths
stay live. The hidden `__remote-deploy` command calls `cmd_remote_deploy`
directly for tests and audits.

The deploy lock is non-blocking: if another deploy, update, or rollback is
already promoting the same deployment id, the later command fails with
`deployment already running` instead of waiting.

The production entry points are `init`, `config`, `deploy`, `update`,
`rollback`, `releases`, `branches`, `tags`, `commits`, `status`, `auth`, and
`doctor`. The hidden command is documented here only so maintainers can follow
the embedded code path.
