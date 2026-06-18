# `bin/wpcloud-site-git-deploy` Code Flow

This diagram covers the current `main` branch layout. The project is one Bash
CLI with an internal promotion engine. `__remote-deploy` is hidden from the
public help output because it is for tests, promotion, rollback, and diagnostic
audits, not for day-to-day operator use. Handler entry nodes use function start
lines; other line numbers point to where the diagrammed action is executed in
`bin/wpcloud-site-git-deploy`.

```mermaid
flowchart TD
  A["Process starts L1"] --> B["Set constants and state paths L4-L48"]
  B --> C["main() dispatch case L2964"]

  C -->|init| I["main calls cmd_init L2966"]
  I --> I1["cmd_init validates name, repo, docroot, deployment-id, default-ref, keep-releases, deploy-root, maintenance_file L1557-L1577"]
  I1 --> I2["ensure_state_dirs L1579"]
  I2 --> I3["ensure_helper L1580: use mv --exchange when available, otherwise use or install exchange-rename"]
  I3 --> I4["write_config L1589: deployments/<name>/cfg-* value files"]

  C -->|config| CFG["main calls cmd_config L2969"]
  CFG --> CFG1["cmd_config sets or clears cfg-deploy_root, cfg-post_deploy, or cfg-maintenance_file L1516-L1541"]

  C -->|auth| AU["main calls cmd_auth L2996"]
  AU --> AU1["load_config L1407, optionally remove ssh_key_path, or normalize HTTPS URL to SSH when applicable"]
  AU1 --> AU2["auth_resolve_key L1447: generate/reuse managed key, --use-key external key, or --import-key managed copy"]
  AU2 --> AU3["auth_resolve_key L1179/L1184/L1221 validates private keys and L1203/L1225 derives public keys"]
  AU3 --> AU4["write_config L1452: store cfg-ssh_key_path"]
  AU4 --> AU5["Print public deploy key and host-specific instructions L1455-L1461"]

  C -->|doctor| DOC["main calls cmd_doctor L2999"]
  DOC --> DOC1["load_config L1482 and doctor_check_commands L1489"]
  DOC1 --> DOC2["doctor_check_docroot L1490, repo URL L1491, exchange L1492, ssh key L1493, git-lfs L1494"]
  DOC2 --> DOC3["doctor_check_git_remote L1495: optionally run git ls-remote through configured GIT_SSH_COMMAND"]

  C -->|deploy| D["main calls cmd_deploy L2972"]
  D --> D1["cmd_deploy parses exactly one ref plus optional --force, --post-deploy, and --maintenance-file L1602-L1613"]
  D1 --> DR["deploy_ref L990"]

  C -->|update| U["main calls cmd_update L2975"]
  U --> U1["cmd_update parses optional --force, --post-deploy, and --maintenance-file, then load_config L1624-L1631"]
  U1 --> DR

  DR --> DR1["load_config L1007"]
  DR1 --> DR2["ensure_state_dirs L1008 and ensure_helper L1009"]
  DR2 --> DR3["fetch_repo L1012: clone/cache, fetch tags/prune, git gc --auto"]
  DR3 --> DR4["resolve_ref L1013 to commit"]
  DR4 --> NR{"current_release_matches L1014 and no --force?"}
  NR -->|yes| NRO["Print no-op release_id ref_mode commit L1015"]
  NR -->|no| DR5["make_release_id L1018"]
  DR5 --> DR6["create_worktree L1019; git worktree add happens at L817"]
  DR6 --> PGF["prepare_git_features L818"]
  PGF --> PGF1["Initialize submodules recursively at L677 if .gitmodules exists"]
  PGF1 --> PGF2["Batch git check-attr filter for tracked files at L687"]
  PGF2 --> PGF3{"Any LFS paths? L697"}
  PGF3 -->|yes| PGF4["Require git-lfs L698, run lfs pull L700, reject unresolved pointers L707"]
  PGF3 -->|no| DR7["copy_worktree_to_incoming L1020"]
  PGF4 --> DR7
  DR7 --> DR8["rsync worktree or deploy_root subdir to docroot incoming release at L885, using --link-dest when possible"]
  DR8 --> PR["promote_release L1024"]

  PR --> RDE["run_engine_subshell L987 -> cmd_remote_deploy --release-id"]
  RDE --> EXC["cmd_remote_deploy L2925-L2930: probe mv --exchange once, cache ENGINE_MV_EXCHANGE, and set default exchange helper when needed"]
  EXC --> ERQ["require_remote_capabilities L2939"]
  ERQ --> ELOCK["acquire_lock L2833"]
  ELOCK -->|busy| EBUSY["Fail: deployment already running L2073"]
  ELOCK -->|acquired| EMNT["cleanup stale marker L2839 and create_maintenance_file L2844"]
  EMNT --> EPCT["prepare_claim_transition L2845"]
  EPCT --> EPCT1["discover_boundary_claims L2656"]
  EPCT1 --> EPCT2["discover_protected_anchors L2657"]
  EPCT2 --> EPCT3["compute old claims L2661 + materialized public claims L2669-L2670"]
  EPCT3 --> EPCT4["compute_claims L2671 for new release"]
  EPCT4 --> EPCT5["apply shared media container policy in compute_claims L2603-L2616 and reject protected anchors L2672"]
  EPCT5 --> EPCT6["compute_removed_claims L2673"]
  EPCT6 --> MOVE["Move incoming to releases/<release-id> at L2846"]
  MOVE --> EACT["apply_claim_transition L2850"]
  EACT --> EACT1["cleanup_overlapping_removed_claims L2693"]
  EACT1 --> EACT2["reconcile_new_claims L2694"]
  EACT2 --> EACT3["reject foreign deployment ancestor/exact/descendant conflicts L2155-L2158"]
  EACT3 --> EACT4["Create public symlink or atomically exchange existing path using cached mv --exchange decision or exchange-rename L2161-L2179"]
  EACT4 --> EACT5["switch_current L2695 atomically"]
  EACT5 --> EACT6["cleanup_exchanged_paths L2697 and cleanup_removed_claims L2702"]
  EACT6 --> EACT7["assert_claim_symlinks_under_docroot L2703"]
  EACT7 --> POST["run_post_deploy L2851 when --post-deploy-file was provided"]
  POST --> RMNT["Remove owned maintenance marker L2856"]
  RMNT --> PRUNE["prune_releases L2858"]
  PRUNE --> ENGRET["Engine subshell returns to deploy_ref L1024"]
  ENGRET --> META["write_release_metadata L1028 as metadata/<release-id>/cfg-* files"]
  META --> CLEAN["cleanup_worktree L1032"]
  CLEAN --> OUT["Print release_id ref_mode commit L1033"]

  C -->|rollback| RB["main calls cmd_rollback L2978"]
  RB --> RB1["load_config L1647 and ensure_helper L1648"]
  RB1 --> RB2["current_release_id L1649"]
  RB2 --> RB3{"--to provided? L1650"}
  RB3 -->|no| RB4["select_rollback_release L1651 from metadata-backed releases"]
  RB3 -->|yes| RBR["run_engine_subshell L1658 -> cmd_remote_deploy --rollback-to"]
  RB4 --> RBR
  RBR --> REXC["cmd_remote_deploy L2925-L2930: probe mv --exchange once, cache ENGINE_MV_EXCHANGE, and set default exchange helper when needed"]
  REXC --> RRC["require_remote_capabilities L2939"]
  RRC --> RLOCK["acquire_lock L2728"]
  RLOCK -->|busy| RBUSY["Fail: deployment already running L2073"]
  RLOCK -->|acquired| RMNT1["rollback_release L2730/L2737: clean stale marker and create rollback marker unless disabled"]
  RMNT1 --> RPCT["prepare_claim_transition L2738 for existing release"]
  RPCT --> RACT["apply_claim_transition L2739"]
  RACT --> RACT1["switch_current L2695 atomically and clean removed claims"]
  RACT1 --> RACT2["assert_claim_symlinks_under_docroot L2703"]
  RACT2 --> RMNT2["Remove owned maintenance marker L2740"]
  RMNT2 --> RRET["Engine subshell returns to cmd_rollback L1658"]
  RRET --> RBOUT["Print rolled back release L1659"]

  C -->|releases| REL["main calls cmd_releases L2981"]
  REL --> REL1["load_config L1673"]
  REL1 --> REL2["List release dirs, mark current, include metadata L1676-L1691"]

  C -->|branches/tags/commits| INS["main calls cmd_branches L2984 / cmd_tags L2987 / cmd_commits L2990"]
  INS --> INS1["load_config L1706/L1729/L1750"]
  INS1 --> INS2{"--fetch? L1707/L1730/L1751"}
  INS2 -->|yes| DR3
  INS2 -->|no| ERC["ensure_repo_cache L1710/L1733/L1754 only"]
  ERC --> INS3["Read cached refs/log and print limited output L1712-L1715/L1735-L1736/L1756"]
  DR3 --> INS3

  C -->|status| ST["main calls cmd_status L2993"]
  ST --> ST1["load_config L1761"]
  ST1 --> ST2["Print config and current release L1762-L1772"]

  C -->|__remote-deploy| H["Hidden test/internal command L3002"]
  H --> CRD["cmd_remote_deploy L3002"]
  CRD --> CRD1["Parse deploy, rollback, or audit mode L2881-L2903, cache exchange capability L2925-L2930, and run the matching path L2941-L2952"]

  C -->|--help / --version| HV["usage L3005 or VERSION output L3008"]
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
