# `bin/wpcloud-site-git-deploy` Code Flow

This diagram covers the current `main` branch layout. The project is one Bash
CLI with an internal promotion engine. `__remote-deploy` is hidden from the
public help output because it is for tests, promotion, rollback, and diagnostic
audits, not for day-to-day operator use. Line numbers in the diagram refer to
the current starting line for the named function in `bin/wpcloud-site-git-deploy`.

```mermaid
flowchart TD
  A["Process starts"] --> B["Set constants and state paths"]
  B --> C["main() L2960: dispatch command"]

  C -->|init| I["cmd_init L1546"]
  I --> I1["Validate name, repo, docroot, deployment-id, default-ref, keep-releases, deploy-root, maintenance_file"]
  I1 --> I2["ensure_state_dirs L387"]
  I2 --> I3["ensure_helper L561: use mv --exchange when available, otherwise use or install exchange-rename"]
  I3 --> I4["write_config L391: deployments/<name>/cfg-* value files"]

  C -->|config| CFG["cmd_config L1500"]
  CFG --> CFG1["Set or clear cfg-deploy_root, cfg-post_deploy, or cfg-maintenance_file"]

  C -->|auth| AU["cmd_auth L1382"]
  AU --> AU1["load_config L368, optionally remove ssh_key_path, or normalize HTTPS URL to SSH when applicable"]
  AU1 --> AU2["auth_resolve_key L1168: generate/reuse managed key, --use-key external key, or --import-key managed copy"]
  AU2 --> AU3["validate_existing_private_key L1139 and derive_public_key_file L1153"]
  AU3 --> AU4["write_config L391: store cfg-ssh_key_path"]
  AU4 --> AU5["Print public deploy key and host-specific instructions"]

  C -->|doctor| DOC["cmd_doctor L1469"]
  DOC --> DOC1["load_config L368 and doctor_check_commands L1234"]
  DOC1 --> DOC2["doctor_check_docroot L1255, exchange L1283, repo URL L1270, ssh key L1299, git-lfs L1339"]
  DOC2 --> DOC3["doctor_check_git_remote L1357: optionally run git ls-remote through configured GIT_SSH_COMMAND"]

  C -->|deploy| D["cmd_deploy L1593"]
  D --> D1["Parse exactly one ref plus optional --force, --post-deploy, and --maintenance-file"]
  D1 --> DR["deploy_ref L990"]

  C -->|update| U["cmd_update L1617"]
  U --> U1["Parse optional --force, --post-deploy, and --maintenance-file, then load_config"]
  U1 --> DR

  DR --> DR1["load_config L368"]
  DR1 --> DR2["ensure_state_dirs L387 and ensure_helper L561"]
  DR2 --> DR3["fetch_repo L633: clone/cache, fetch tags/prune, git gc --auto"]
  DR3 --> DR4["resolve_ref L641 to commit"]
  DR4 --> NR{"current_release_matches L946 and no --force?"}
  NR -->|yes| NRO["Print no-op release_id ref_mode commit"]
  NR -->|no| DR5["make_release_id L791"]
  DR5 --> DR6["create_worktree L804 via git worktree add --detach"]
  DR6 --> PGF["prepare_git_features L664"]
  PGF --> PGF1["Initialize submodules recursively if .gitmodules exists"]
  PGF1 --> PGF2["Batch git check-attr filter for tracked files"]
  PGF2 --> PGF3{"Any LFS paths?"}
  PGF3 -->|yes| PGF4["Require git-lfs, run lfs pull, reject unresolved pointers"]
  PGF3 -->|no| DR7["copy_worktree_to_incoming L847"]
  PGF4 --> DR7
  DR7 --> DR8["rsync worktree or deploy_root subdir to docroot incoming release, using --link-dest when possible"]
  DR8 --> PR["promote_release L966"]

  PR --> RDE["run_engine_subshell L960 -> cmd_remote_deploy L2863 --release-id"]
  RDE --> EXC["cmd_remote_deploy L2863: probe mv --exchange once, cache ENGINE_MV_EXCHANGE, and set default exchange helper when needed"]
  EXC --> ERQ["require_remote_capabilities L1916"]
  ERQ --> ELOCK["acquire_lock L2067"]
  ELOCK -->|busy| EBUSY["Fail: deployment already running"]
  ELOCK -->|acquired| EMNT["create_maintenance_file L1867 after stale marker cleanup"]
  EMNT --> EPCT["prepare_claim_transition L2641"]
  EPCT --> EPCT1["discover_boundary_claims L2400"]
  EPCT1 --> EPCT2["discover_protected_anchors L2409"]
  EPCT2 --> EPCT3["compute old claims + materialized public claims"]
  EPCT3 --> EPCT4["compute_claims L2577 for new release"]
  EPCT4 --> EPCT5["apply shared media container policy; reject runtime shared paths and protected anchors"]
  EPCT5 --> EPCT6["compute_removed_claims L2199"]
  EPCT6 --> MOVE["Move incoming to releases/<release-id>"]
  MOVE --> EACT["apply_claim_transition L2676"]
  EACT --> EACT1["cleanup_overlapping_removed_claims L2318"]
  EACT1 --> EACT2["reconcile_new_claims L2135"]
  EACT2 --> EACT3["reject foreign deployment ancestor/exact/descendant conflicts"]
  EACT3 --> EACT4["Create public symlink or atomically exchange existing path using cached mv --exchange decision or exchange-rename"]
  EACT4 --> EACT5["switch_current L2047 atomically"]
  EACT5 --> EACT6["cleanup_exchanged_paths L2186 and cleanup_removed_claims L2306"]
  EACT6 --> EACT7["assert_claim_symlinks_under_docroot L1994"]
  EACT7 --> POST["run_post_deploy L2102 when --post-deploy-file was provided"]
  POST --> RMNT["Remove owned maintenance marker"]
  RMNT --> PRUNE["prune_releases L2076"]
  PRUNE --> ENGRET["Engine subshell returns to deploy_ref"]
  ENGRET --> META["write_release_metadata L889 as metadata/<release-id>/cfg-* files"]
  META --> CLEAN["cleanup_worktree L827"]
  CLEAN --> OUT["Print release_id ref_mode commit"]

  C -->|rollback| RB["cmd_rollback L1635"]
  RB --> RB1["load_config L368 and ensure_helper L561"]
  RB1 --> RB2["current_release_id L1036"]
  RB2 --> RB3{"--to provided?"}
  RB3 -->|no| RB4["select_rollback_release L1046 from metadata-backed releases"]
  RB3 -->|yes| RBR["run_engine_subshell L960 -> cmd_remote_deploy L2863 --rollback-to"]
  RB4 --> RBR
  RBR --> REXC["cmd_remote_deploy L2863: probe mv --exchange once, cache ENGINE_MV_EXCHANGE, and set default exchange helper when needed"]
  REXC --> RRC["require_remote_capabilities L1916"]
  RRC --> RLOCK["acquire_lock L2067"]
  RLOCK -->|busy| RBUSY["Fail: deployment already running"]
  RLOCK -->|acquired| RMNT1["rollback_release L2706: clean stale marker and create rollback marker unless disabled"]
  RMNT1 --> RPCT["prepare_claim_transition L2641 for existing release"]
  RPCT --> RACT["apply_claim_transition L2676"]
  RACT --> RACT1["switch_current L2047 atomically and clean removed claims"]
  RACT1 --> RACT2["assert_claim_symlinks_under_docroot L1994"]
  RACT2 --> RMNT2["Remove owned maintenance marker"]
  RMNT2 --> RRET["Engine subshell returns to cmd_rollback"]
  RRET --> RBOUT["Print rolled back release"]

  C -->|releases| REL["cmd_releases L1662"]
  REL --> REL1["load_config L368"]
  REL1 --> REL2["List release dirs, mark current, include metadata"]

  C -->|branches/tags/commits| INS["cmd_branches L1695 / cmd_tags L1718 / cmd_commits L1739"]
  INS --> INS1["load_config L368"]
  INS1 --> INS2{"--fetch?"}
  INS2 -->|yes| DR3
  INS2 -->|no| ERC["ensure_repo_cache L620 only"]
  ERC --> INS3["Read cached refs/log and print limited output"]
  DR3 --> INS3

  C -->|status| ST["cmd_status L1759"]
  ST --> ST1["load_config L368"]
  ST1 --> ST2["Print config and current release"]

  C -->|__remote-deploy| H["Hidden test/internal command"]
  H --> CRD["cmd_remote_deploy L2863"]
  CRD --> CRD1["Parse deploy, rollback, or audit mode, cache exchange capability for engine runs, and run the matching path"]

  C -->|--help / --version| HV["usage L56 or VERSION constant L9"]
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
