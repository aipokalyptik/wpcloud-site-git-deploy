# `bin/wpcloud-site-git-deploy` Code Flow

This diagram covers the current `main` branch layout. The project is a Bash CLI
with an embedded remote deployment engine. `__remote-deploy` is hidden from the
public help output because it is for tests, promotion, rollback, and diagnostic
audits, not for day-to-day operator use.

```mermaid
flowchart TD
  A["Process starts"] --> B["Set constants and state paths"]
  B --> C["main(): dispatch command"]

  C -->|init| I["cmd_init"]
  I --> I1["Validate name, repo, docroot, deployment-id, default-ref, keep-releases, deploy-root"]
  I1 --> I2["ensure_state_dirs"]
  I2 --> I3["ensure_helper: install exchange-rename if needed"]
  I3 --> I4["Write deployment config under $HOME/.wpcloud-site-git-deploy/deployments"]

  C -->|config| CFG["cmd_config"]
  CFG --> CFG1["Set or clear deploy_root in deployment config"]

  C -->|auth| AU["cmd_auth"]
  AU --> AU1["Load deployment config, optionally remove ssh_key_path, or normalize GitHub HTTPS URL to SSH"]
  AU1 --> AU2["Create or reuse $HOME/.wpcloud-site-git-deploy/keys/<name>_ed25519"]
  AU2 --> AU3["Store ssh_key_path in deployment config"]
  AU3 --> AU4["Print public deploy key and host-specific instructions"]

  C -->|doctor| DOC["cmd_doctor"]
  DOC --> DOC1["Load config and check required local commands"]
  DOC1 --> DOC2["Check docroot, exchange helper, repo URL, and deploy key files"]
  DOC2 --> DOC3["Optionally run git ls-remote through configured GIT_SSH_COMMAND"]

  C -->|deploy| D["cmd_deploy"]
  D --> D1["Parse exactly one ref: --branch, --tag, or --commit"]
  D1 --> DR["deploy_ref"]

  C -->|update| U["cmd_update"]
  U --> U1["load_config"]
  U1 --> DR

  DR --> DR1["load_config"]
  DR1 --> DR2["ensure_state_dirs and ensure_helper"]
  DR2 --> DR3["fetch_repo: clone/cache, fetch tags/prune, git gc --auto"]
  DR3 --> DR4["resolve_ref to commit"]
  DR4 --> NR{"Current metadata commit + deploy_root match?"}
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

  PR --> RDE["remote_deploy_entry --release-id"]
  RDE --> RC["require_remote_capabilities"]
  RC --> LOCK["Acquire deploy lock"]
  LOCK --> PCT["prepare_claim_transition"]
  PCT --> PCT1["discover_boundary_claims"]
  PCT1 --> PCT2["discover_protected_anchors"]
  PCT2 --> PCT3["compute old claims + materialized public claims"]
  PCT3 --> PCT4["compute new claims"]
  PCT4 --> PCT5["validate_claims_not_protected"]
  PCT5 --> PCT6["compute removed claims"]
  PCT6 --> MOVE["Move incoming to releases/<release-id>"]
  MOVE --> ACT["apply_claim_transition"]
  ACT --> ACT1["cleanup overlapping removed symlinks"]
  ACT1 --> ACT2["reconcile_new_claims"]
  ACT2 --> ACT3["reject foreign deployment ancestor/exact/descendant conflicts"]
  ACT3 --> ACT4["Create public symlink or atomically exchange existing path with exchange-rename"]
  ACT4 --> ACT5["switch_current atomically"]
  ACT5 --> ACT6["cleanup exchanged paths and removed claims"]
  ACT6 --> ACT7["assert_claim_symlinks_under_docroot"]
  ACT7 --> PRUNE["prune_releases"]
  PRUNE --> META["write_release_metadata"]
  META --> CLEAN["cleanup_worktree"]
  CLEAN --> OUT["Print release_id ref_mode commit"]

  C -->|rollback| RB["cmd_rollback"]
  RB --> RB1["load_config and ensure_helper"]
  RB1 --> RB2["current_release_id"]
  RB2 --> RB3{"--to provided?"}
  RB3 -->|no| RB4["select_rollback_release from metadata-backed releases"]
  RB3 -->|yes| RBR["remote_deploy_entry --rollback-to"]
  RB4 --> RBR
  RBR --> RBR1["rollback_release"]
  RBR1 --> LOCK
  ACT7 --> RBOUT["Print rolled back release"]

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
  H --> RDE

  C -->|--help / --version| HV["Print usage/version"]
```

The public commands stay in the top-level CLI. The former remote deployment
engine now lives inside `remote_deploy_entry()` and is reachable through the
hidden `__remote-deploy` command for tests and internal promotion/rollback use.

The production entry points are `init`, `config`, `deploy`, `update`,
`rollback`, `releases`, `branches`, `tags`, `commits`, `status`, `auth`, and
`doctor`. The hidden command is documented here only so maintainers can follow
the embedded code path.
