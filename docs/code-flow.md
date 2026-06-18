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
  B --> C["main() dispatch case L3533"]

  C -->|init| I["main calls cmd_init L3535"]
  I --> I1["cmd_init validates name, repo, docroot, deployment-id, default-ref, keep-releases, deploy-root, maintenance_file L1849-L1881"]
  I1 --> I2["ensure_state_dirs L1883"]
  I2 --> I3["ensure_helper L1884: use mv --exchange when available, otherwise use or install exchange-rename"]
  I3 --> I4["write_config L1893: deployments/<name>/cfg-* value files"]

  C -->|config| CFG["main calls cmd_config L3538"]
  CFG --> CFG1["cmd_config sets or clears cfg-deploy_root, cfg-post_deploy, or cfg-maintenance_file L1806-L1830"]

  C -->|auth| AU["main calls cmd_auth L3565"]
  AU --> AU1["load_config L1650, optionally remove ssh_key_path, or normalize HTTPS URL to SSH when applicable"]
  AU1 --> AU2["auth_resolve_key L1709: generate/reuse managed key, --use-key external key, or --import-key managed copy"]
  AU2 --> AU3["auth_resolve_key L1392/L1397/L1433 validates private keys and L1364/L1425 derives public keys"]
  AU3 --> AU4["write_config L1715: store cfg-ssh_key_path"]
  AU4 --> AU5["Print public deploy key and host-specific instructions L1724-L1729"]

  C -->|doctor| DOC["main calls cmd_doctor L3568"]
  DOC --> DOC1["load_config L1756 and doctor_check_commands L1763"]
  DOC1 --> DOC2["doctor_check_docroot L1766, repo URL L1769, exchange L1772, ssh key L1775, git-lfs L1778"]
  DOC2 --> DOC3["doctor_check_git_remote L1781: optionally run git ls-remote through configured GIT_SSH_COMMAND"]

  C -->|deploy| D["main calls cmd_deploy L3541"]
  D --> D1["cmd_deploy parses exactly one ref plus optional --force, --post-deploy, and --maintenance-file L1909-L1919"]
  D1 --> DR["deploy_ref L1153"]

  C -->|update| U["main calls cmd_update L3544"]
  U --> U1["cmd_update parses optional --force, --post-deploy, and --maintenance-file, then load_config L1940-L1948"]
  U1 --> DR

  DR --> DR1["load_config L1170"]
  DR1 --> DR2["ensure_state_dirs L1171 and ensure_helper L1172"]
  DR2 --> DR3["fetch_repo L1175: clone/cache, fetch tags/prune, git gc --auto"]
  DR3 --> DR4["resolve_ref L1176 to commit"]
  DR4 --> NR{"current_release_matches L1178 and no --force?"}
  NR -->|yes| NRO["Print no-op release_id ref_mode commit L1179"]
  NR -->|no| DR5["make_release_id L1183"]
  DR5 --> DR6["create_worktree L1184; git worktree add happens at L953"]
  DR6 --> PGF["prepare_git_features L955"]
  PGF --> PGF1["Initialize submodules recursively at L800 if .gitmodules exists"]
  PGF1 --> PGF2["Batch git check-attr filter for tracked files at L810"]
  PGF2 --> PGF3{"Any LFS paths? L822"}
  PGF3 -->|yes| PGF4["Require git-lfs L823, run lfs pull L832, reject unresolved pointers L841"]
  PGF3 -->|no| DR7["copy_worktree_to_incoming L1185"]
  PGF4 --> DR7
  DR7 --> DR8["rsync worktree or deploy_root subdir to docroot incoming release at L1030, using --link-dest when possible"]
  DR8 --> PR["promote_release L1189"]

  PR --> RDE["run_engine_subshell L1123 -> cmd_remote_deploy --release-id"]
  RDE --> EXC["cmd_remote_deploy L3498-L3505: probe mv --exchange once, cache ENGINE_MV_EXCHANGE, and set default exchange helper when needed"]
  EXC --> ERQ["require_remote_capabilities L3512"]
  ERQ --> ELOCK["acquire_lock L3386"]
  ELOCK -->|busy| EBUSY["Fail: deployment already running L2495"]
  ELOCK -->|acquired| EMNT["cleanup stale marker L3391 and create_maintenance_file L3401"]
  EMNT --> EPCT["prepare_claim_transition L3402"]
  EPCT --> EPCT1["discover_boundary_claims L3193"]
  EPCT1 --> EPCT2["discover_protected_anchors L3194"]
  EPCT2 --> EPCT3["compute old claims L3198 + materialized public claims L3206-L3207"]
  EPCT3 --> EPCT4["compute_claims L3208 for new release"]
  EPCT4 --> EPCT5["apply shared media container policy in compute_claims L3143-L3159 and reject protected anchors L3209"]
  EPCT5 --> EPCT6["compute_removed_claims L3210"]
  EPCT6 --> MOVE["Move incoming to releases/<release-id> at L3403"]
  MOVE --> EACT["apply_claim_transition L3407"]
  EACT --> EACT1["cleanup_overlapping_removed_claims L3230"]
  EACT1 --> EACT2["reconcile_new_claims L3231"]
  EACT2 --> EACT3["reject foreign deployment ancestor/exact/descendant conflicts L2603-L2606"]
  EACT3 --> EACT4["Create public symlink or atomically exchange existing path using cached mv --exchange decision or exchange-rename L2611-L2630"]
  EACT4 --> EACT5["switch_current L3232 atomically"]
  EACT5 --> EACT6["cleanup_exchanged_paths L3236 and cleanup_removed_claims L3241"]
  EACT6 --> EACT7["assert_claim_symlinks_under_docroot L3242"]
  EACT7 --> POST["run_post_deploy L3408 when --post-deploy-file was provided"]
  POST --> RMNT["Remove owned maintenance marker L3413"]
  RMNT --> PRUNE["prune_releases L3415"]
  PRUNE --> ENGRET["Engine subshell returns to deploy_ref L1189"]
  ENGRET --> META["write_release_metadata L1193 as metadata/<release-id>/cfg-* files"]
  META --> CLEAN["cleanup_worktree L1197"]
  CLEAN --> OUT["Print release_id ref_mode commit L1198"]

  C -->|rollback| RB["main calls cmd_rollback L3547"]
  RB --> RB1["load_config L1966 and ensure_helper L1967"]
  RB1 --> RB2["current_release_id L1968"]
  RB2 --> RB3{"--to provided? L1971"}
  RB3 -->|no| RB4["select_rollback_release L1972 from metadata-backed releases"]
  RB3 -->|yes| RBR["run_engine_subshell L1981 -> cmd_remote_deploy --rollback-to"]
  RB4 --> RBR
  RBR --> REXC["cmd_remote_deploy L3498-L3505: probe mv --exchange once, cache ENGINE_MV_EXCHANGE, and set default exchange helper when needed"]
  REXC --> RRC["require_remote_capabilities L3512"]
  RRC --> RLOCK["acquire_lock L3267"]
  RLOCK -->|busy| RBUSY["Fail: deployment already running L2495"]
  RLOCK -->|acquired| RMNT1["rollback_release L3271/L3278: clean stale marker and create rollback marker unless disabled"]
  RMNT1 --> RPCT["prepare_claim_transition L3279 for existing release"]
  RPCT --> RACT["apply_claim_transition L3280"]
  RACT --> RACT1["switch_current L3232 atomically and clean removed claims"]
  RACT1 --> RACT2["assert_claim_symlinks_under_docroot L3242"]
  RACT2 --> RMNT2["Remove owned maintenance marker L3281"]
  RMNT2 --> RRET["Engine subshell returns to cmd_rollback L1981"]
  RRET --> RBOUT["Print rolled back release L1982"]

  C -->|releases| REL["main calls cmd_releases L3550"]
  REL --> REL1["load_config L1996"]
  REL1 --> REL2["List release dirs, mark current, include metadata L1999-L2018"]

  C -->|branches/tags/commits| INS["main calls cmd_branches L3553 / cmd_tags L3556 / cmd_commits L3559"]
  INS --> INS1["load_config L2039/L2066/L2091"]
  INS1 --> INS2{"--fetch? L2040/L2067/L2092"}
  INS2 -->|yes| DR3
  INS2 -->|no| ERC["ensure_repo_cache L2043/L2070/L2095 only"]
  ERC --> INS3["Read cached refs/log and print limited output L2045-L2048/L2072-L2073/L2097"]
  DR3 --> INS3

  C -->|status| ST["main calls cmd_status L3562"]
  ST --> ST1["load_config L2102"]
  ST1 --> ST2["Print config and current release L2103-L2115"]

  C -->|__remote-deploy| H["Hidden test/internal command L3571"]
  H --> CRD["cmd_remote_deploy L3571"]
  CRD --> CRD1["Parse deploy, rollback, or audit mode L3439-L3487, cache exchange capability L3498-L3505, and run the matching path L3514-L3525"]

  C -->|--help / --version| HV["usage L3574 or VERSION output L3577"]
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
