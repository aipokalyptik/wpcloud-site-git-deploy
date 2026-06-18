# `bin/wpcloud-site-git-deploy` Code Flow

This document covers the current `main` branch layout. The project is one Bash
CLI with an internal promotion engine. `__remote-deploy` is hidden from the
public help output because it is for tests, promotion, rollback, and diagnostic
audits, not for day-to-day operator use.

Each diagram focuses on one major path. Handler entry nodes use function start
lines; other line numbers point to where the diagrammed action happens in
`bin/wpcloud-site-git-deploy`.

## Process Entry And Dispatch

```mermaid
flowchart TD
  A["Process starts L1"] --> B["Set constants and state paths L4-L57"]
  B --> C["main() L3551"]
  C --> D["Dispatch on first argument L3557"]

  D -->|init| INIT["cmd_init L1850"]
  D -->|config| CONFIG["cmd_config L1802"]
  D -->|deploy| DEPLOY["cmd_deploy L1911"]
  D -->|update| UPDATE["cmd_update L1944"]
  D -->|rollback| ROLLBACK["cmd_rollback L1966"]
  D -->|releases| RELEASES["cmd_releases L1999"]
  D -->|branches| BRANCHES["cmd_branches L2038"]
  D -->|tags| TAGS["cmd_tags L2065"]
  D -->|commits| COMMITS["cmd_commits L2090"]
  D -->|status| STATUS["cmd_status L2114"]
  D -->|auth| AUTH["cmd_auth L1629"]
  D -->|doctor| DOCTOR["cmd_doctor L1753"]
  D -->|__remote-deploy| HIDDEN["cmd_remote_deploy L3438"]
  D -->|--help| HELP["usage L3598"]
  D -->|--version| VERSION["Print VERSION L3601"]
```

The final dispatcher is the only public entry point. Deploy and rollback call the
internal engine through `run_engine_subshell` so engine exits remain contained
and caller cleanup paths stay live. The hidden `__remote-deploy` command calls
`cmd_remote_deploy` directly for tests and audits.

## Initialize Or Reconfigure A Deployment

```mermaid
flowchart TD
  INIT["cmd_init L1850"] --> INIT1["Parse and validate --repo, --docroot, --deployment-id, --default-ref, --keep-releases, --deploy-root, --maintenance-file L1863-L1895"]
  INIT1 --> INIT2["ensure_state_dirs L1897"]
  INIT2 --> INIT3["ensure_helper L1898"]
  INIT3 --> INIT4{"mv --exchange available?"}
  INIT4 -->|yes| INIT5["Do not require exchange-rename helper"]
  INIT4 -->|no| INIT6["Use PATH/source/managed exchange-rename helper"]
  INIT5 --> INIT7["write_config L1907"]
  INIT6 --> INIT7
  INIT7 --> INIT8["Store deployments/<name>/cfg-* value files"]

  CONFIG["cmd_config L1802"] --> CONFIG1["Parse exactly one config action L1809-L1819"]
  CONFIG1 --> CONFIG2["load_config L1820"]
  CONFIG2 --> CONFIG3{"Action"}
  CONFIG3 -->|--deploy-root| CONFIG4["Set cfg-deploy_root L1825"]
  CONFIG3 -->|--clear-deploy-root| CONFIG5["Clear cfg-deploy_root L1830"]
  CONFIG3 -->|--post-deploy| CONFIG6["Set cfg-post_deploy L1835"]
  CONFIG3 -->|--clear-post-deploy| CONFIG7["Clear cfg-post_deploy L1840"]
  CONFIG3 -->|--maintenance-file| CONFIG8["Set cfg-maintenance_file L1845"]
  CONFIG4 --> CONFIG9["write_config and print result"]
  CONFIG5 --> CONFIG9
  CONFIG6 --> CONFIG9
  CONFIG7 --> CONFIG9
  CONFIG8 --> CONFIG9
```

`init` creates the durable deployment configuration. `config` updates only the
supported mutable options instead of rewriting the whole deployment definition.

## Auth Setup

```mermaid
flowchart TD
  AUTH["cmd_auth L1629"] --> AUTH1["Parse --remove, --purge-key, --force-new-key, --verify, --use-key, --import-key L1643-L1661"]
  AUTH1 --> AUTH2["load_config L1664"]
  AUTH2 --> AUTH3{"--remove?"}

  AUTH3 -->|yes| AUTH4["Optionally remove managed key when --purge-key was passed L1678-L1684"]
  AUTH4 --> AUTH5["Clear ssh_key_path and write_config L1687-L1689"]

  AUTH3 -->|no| AUTH6["Normalize HTTPS repository URL to SSH form when applicable L1694-L1701"]
  AUTH6 --> AUTH7{"Repository URL is SSH?"}
  AUTH7 -->|no| AUTH8["Fail with actionable URL guidance L1704-L1706"]
  AUTH7 -->|yes| AUTH9["auth_resolve_key L1723"]

  AUTH9 --> AUTH10{"Key mode"}
  AUTH10 -->|generate/reuse| AUTH11["Create or reuse managed ed25519 key L1455-L1464"]
  AUTH10 -->|--use-key| AUTH12["Validate external private key without copying it L1406"]
  AUTH10 -->|--import-key| AUTH13["Copy key into managed key dir and chmod it L1411-L1435"]
  AUTH11 --> AUTH14["Derive public key L1378"]
  AUTH12 --> AUTH14
  AUTH13 --> AUTH14
  AUTH14 --> AUTH15["write_config L1729: store cfg-ssh_key_path"]
  AUTH15 --> AUTH16["Print deploy-key instructions L1738-L1743"]
  AUTH16 --> AUTH17{"--verify?"}
  AUTH17 -->|yes| AUTH18["git ls-remote through configured GIT_SSH_COMMAND L1746"]
  AUTH17 -->|no| AUTH19["Finish after instructions"]
```

Auth never edits global SSH configuration. Git operations receive a
tool-managed `GIT_SSH_COMMAND` when `ssh_key_path` is configured.

## Doctor Diagnostics

```mermaid
flowchart TD
  DOCTOR["cmd_doctor L1753"] --> DOCTOR1["Parse --offline L1761-L1767"]
  DOCTOR1 --> DOCTOR2["load_config L1770"]
  DOCTOR2 --> DOCTOR3["doctor_check_commands L1777"]
  DOCTOR3 --> DOCTOR4["doctor_check_docroot L1780"]
  DOCTOR4 --> DOCTOR5["doctor_check_repo_url L1783"]
  DOCTOR5 --> DOCTOR6["doctor_check_exchange L1786"]
  DOCTOR6 --> DOCTOR7["doctor_check_ssh_key L1789"]
  DOCTOR7 --> DOCTOR8["doctor_check_git_lfs L1792"]
  DOCTOR8 --> DOCTOR9{"--offline?"}
  DOCTOR9 -->|yes| DOCTOR10["Skip remote Git access check"]
  DOCTOR9 -->|no| DOCTOR11["doctor_check_git_remote L1795"]
  DOCTOR10 --> DOCTOR12["Return success only if no FAIL checks were recorded L1798"]
  DOCTOR11 --> DOCTOR12
```

Doctor reports all discovered failures before exiting, so a first-time user gets
one actionable checklist instead of a one-error-at-a-time setup loop.

## Deploy Or Update

```mermaid
flowchart TD
  DEPLOY["cmd_deploy L1911"] --> DEPLOY1["Parse exactly one ref plus --force, --post-deploy, --maintenance-file L1923-L1933"]
  DEPLOY1 --> DEPLOY2["deploy_ref L1936"]

  UPDATE["cmd_update L1944"] --> UPDATE1["Parse --force, --post-deploy, --maintenance-file L1954-L1962"]
  UPDATE1 --> UPDATE2["load_config L1963"]
  UPDATE2 --> UPDATE3["Use configured default_ref L1964"]
  UPDATE3 --> DEPLOY2

  DEPLOY2 --> REF["deploy_ref L1167"]
  REF --> REF1["load_config L1184"]
  REF1 --> REF2["ensure_state_dirs L1185 and ensure_helper L1186"]
  REF2 --> REF3["fetch_repo L1189: clone/cache, fetch tags/prune, git gc --auto"]
  REF3 --> REF4["resolve_ref L1190"]
  REF4 --> MATCH{"current_release_matches and no --force? L1192"}
  MATCH -->|yes| NOOP["Print no-op release_id, ref_mode, commit L1193"]
  MATCH -->|no| RID["make_release_id L1197"]
  RID --> WORKTREE["create_worktree L1198"]
  WORKTREE --> COPY["copy_worktree_to_incoming L1199"]
  COPY --> PROMOTE["promote_release L1203"]
  PROMOTE --> META["write_release_metadata L1207"]
  META --> CLEAN["cleanup_worktree L1211"]
  CLEAN --> OUT["Print release_id, ref_mode, commit L1212"]
```

`deploy` chooses an explicit ref. `update` deploys the configured default ref.
Without `--force`, both become no-ops when the resolved commit already matches
the current release metadata.

## Worktree Preparation

```mermaid
flowchart TD
  WORKTREE["create_worktree L945"] --> WT1["git worktree add --detach L962"]
  WT1 --> FEATURES["prepare_git_features L964"]
  FEATURES --> SUB{"Root .gitmodules exists? L798"}
  SUB -->|yes| SUB1["git submodule update --init --recursive L799"]
  SUB -->|no| ATTR["Batch git check-attr filter for tracked files L809"]
  SUB1 --> ATTR
  ATTR --> LFS{"Any LFS-tracked paths? L831"}
  LFS -->|no| DONE["Worktree ready"]
  LFS -->|yes| LFS1["Require git-lfs L833"]
  LFS1 --> LFS2["git lfs pull L841"]
  LFS2 --> LFS3["Reject unresolved LFS pointer files L850"]
  LFS3 --> DONE

  COPY["copy_worktree_to_incoming L1025"] --> ROOT{"deploy_root configured? L1031"}
  ROOT -->|yes| ROOT1["Use repo subdirectory as effective root L1032-L1039"]
  ROOT -->|no| ROOT2["Use repository root"]
  ROOT1 --> RSYNC["rsync to incoming release L1044"]
  ROOT2 --> RSYNC
  RSYNC --> LINKDEST{"Current release exists? L1020-L1022"}
  LINKDEST -->|yes| HARDLINK["Use --link-dest for unchanged files L1042"]
  LINKDEST -->|no| NOLINK["Copy without link-dest"]
```

The deploy root is applied before rsync. When configured, only that subdirectory
is copied into the incoming release tree, so its contents become the docroot
root for that deployment.

## Internal Engine: Promotion

```mermaid
flowchart TD
  PROMOTE["promote_release L1143"] --> SUBSHELL["run_engine_subshell L1137"]
  SUBSHELL --> ENGINE["cmd_remote_deploy --release-id L3438"]
  ENGINE --> PARSE["Parse deploy mode arguments L3456-L3492"]
  PARSE --> EXCHANGE["Probe mv --exchange once and cache ENGINE_MV_EXCHANGE L3516-L3523"]
  EXCHANGE --> REQ["require_remote_capabilities L3530"]
  REQ --> PATH["Run promote_release engine path L3532-L3534"]

  PATH --> LOCK["acquire_lock L3404"]
  LOCK -->|busy| BUSY["Fail: deployment already running L2512"]
  LOCK -->|acquired| STALE["cleanup_owned_maintenance_file L3409"]
  STALE --> MARKER["create_maintenance_file L3419"]
  MARKER --> PREPARE["prepare_claim_transition L3420"]
  PREPARE --> MOVE["Move incoming to releases/<release-id> L3421"]
  MOVE --> APPLY["apply_claim_transition L3425"]
  APPLY --> POST["run_post_deploy L3426"]
  POST --> CLEAR["Remove owned maintenance marker L3431"]
  CLEAR --> PRUNE["prune_releases L3433"]
  PRUNE --> RETURN["Engine subshell returns to deploy_ref L1203"]
```

The deploy lock is non-blocking. If another deploy, update, or rollback is
already promoting the same deployment id, the later command fails with
`deployment already running` instead of waiting.

## Internal Engine: Claim Preparation

```mermaid
flowchart TD
  PREPARE["prepare_claim_transition L3196"] --> BOUNDARY["discover_boundary_claims L3211"]
  BOUNDARY --> PROTECTED["discover_protected_anchors L3212"]
  PROTECTED --> CURRENT{"Current release exists? L3214"}
  CURRENT -->|yes| OLD["compute_claims for old release L3216"]
  CURRENT -->|no| EMPTY["Create empty old-claims file L3218"]
  OLD --> MATERIALIZED["discover_materialized_public_claims L3224-L3225"]
  EMPTY --> MATERIALIZED
  MATERIALIZED --> NEW["compute_claims for new release L3226"]
  NEW --> SHARED{"Path policy inside compute_claims L3165-L3182"}
  SHARED -->|shared media regular file| LEAF["Keep exact leaf claim under uploads/blogs.dir"]
  SHARED -->|shared media symlink| REJECT1["Reject shared container symlink"]
  SHARED -->|cache/upgrade/.maintenance| REJECT2["Reject fully shared runtime path"]
  SHARED -->|normal path| NORMAL["Apply normal sticky-boundary compression"]
  LEAF --> ANCHORS["reject_protected_anchors L3227"]
  NORMAL --> ANCHORS
  ANCHORS --> REMOVED["compute_removed_claims L3228"]
```

`wp-content/uploads` and `wp-content/blogs.dir` are WordPress-managed persistent
containers. The engine can claim regular files inside them as individual leaf
symlinks, but it never replaces those container directories and rejects repo
symlinks inside them.

## Internal Engine: Claim Application

```mermaid
flowchart TD
  APPLY["apply_claim_transition L3231"] --> EXCHANGED["Create exchanged_paths file L3241"]
  EXCHANGED --> OVERLAP["cleanup_overlapping_removed_claims L3248"]
  OVERLAP --> RECONCILE["reconcile_new_claims L3249"]
  RECONCILE --> FOREIGN["Reject foreign deployment ancestor, exact, and descendant conflicts L2618-L2621"]
  FOREIGN --> CREATE["Create public symlink or reclaim existing path L2625-L2649"]
  CREATE --> SWITCH["switch_current L3250"]
  SWITCH --> VERIFYCURRENT["Verify current points to releases/<release-id> L3251-L3253"]
  VERIFYCURRENT --> CLEANEX["cleanup_exchanged_paths L3254"]
  CLEANEX --> CLEANREMOVED["cleanup_removed_claims L3259"]
  CLEANREMOVED --> ASSERT["assert_claim_symlinks_under_docroot L3260"]
```

Promotion creates docroot-relative symlinks for owned claims and switches the
`current` symlink atomically. Existing public paths are reclaimed with
`mv --exchange` when available, otherwise with the `exchange-rename` helper.

## Rollback

```mermaid
flowchart TD
  ROLLBACK["cmd_rollback L1966"] --> RB1["Parse optional --to and --maintenance-file L1974-L1979"]
  RB1 --> RB2["load_config L1980 and ensure_helper L1981"]
  RB2 --> RB3["current_release_id L1982"]
  RB3 --> TARGET{"--to provided? L1985"}
  TARGET -->|no| SELECT["select_rollback_release L1986"]
  TARGET -->|yes| CHOSEN["Use requested release id"]
  SELECT --> RUN["run_engine_subshell --rollback-to L1995"]
  CHOSEN --> RUN

  RUN --> ENGINE["cmd_remote_deploy rollback mode L3438"]
  ENGINE --> PARSE["Parse rollback mode arguments L3456-L3492"]
  PARSE --> EXCHANGE["Probe mv --exchange once and cache ENGINE_MV_EXCHANGE L3516-L3523"]
  EXCHANGE --> REQ["require_remote_capabilities L3530"]
  REQ --> RBENG["rollback_release L3263"]
  RBENG --> LOCK["acquire_lock L3285"]
  LOCK -->|busy| BUSY["Fail: deployment already running L2512"]
  LOCK -->|acquired| MARKER["Clean stale marker and create rollback marker L3287-L3296"]
  MARKER --> PREPARE["prepare_claim_transition L3297"]
  PREPARE --> APPLY["apply_claim_transition L3298"]
  APPLY --> CLEAR["Remove owned maintenance marker L3299"]
  CLEAR --> PRINT["Print rolled back release L1996"]
```

Rollback reuses the claim preparation and application engine path, but it does
not create a new incoming release, write release metadata, or prune releases.

## Inspection And Status Commands

```mermaid
flowchart TD
  RELEASES["cmd_releases L1999"] --> REL1["load_config L2010"]
  REL1 --> REL2["Read release directories and current symlink L2015-L2022"]
  REL2 --> REL3["Print release metadata when present L2024-L2035"]

  BRANCHES["cmd_branches L2038"] --> B1["Parse --fetch and --limit L2046-L2052"]
  B1 --> B2["load_config L2053"]
  B2 --> B3{"--fetch? L2054"}
  B3 -->|yes| FETCHB["fetch_repo L2055"]
  B3 -->|no| CACHEB["ensure_repo_cache L2057"]
  FETCHB --> B4["Print cached branch refs L2059-L2062"]
  CACHEB --> B4

  TAGS["cmd_tags L2065"] --> T1["Parse --fetch and --limit L2073-L2079"]
  T1 --> T2["load_config L2080"]
  T2 --> T3{"--fetch? L2081"}
  T3 -->|yes| FETCHT["fetch_repo L2082"]
  T3 -->|no| CACHET["ensure_repo_cache L2084"]
  FETCHT --> T4["Print cached tag refs L2086-L2087"]
  CACHET --> T4

  COMMITS["cmd_commits L2090"] --> C1["Parse --fetch and --limit L2098-L2104"]
  C1 --> C2["load_config L2105"]
  C2 --> C3{"--fetch? L2106"}
  C3 -->|yes| FETCHC["fetch_repo L2107"]
  C3 -->|no| CACHEC["ensure_repo_cache L2109"]
  FETCHC --> C4["Print cached commit log L2111"]
  CACHEC --> C4

  STATUS["cmd_status L2114"] --> S1["load_config L2116"]
  S1 --> S2["Print config and current release state L2117-L2129"]
```

Inspection commands use cached repository data by default. `--fetch` refreshes
the cache before printing.

## Hidden Internal Command And Full Audit

```mermaid
flowchart TD
  HIDDEN["cmd_remote_deploy L3438"] --> PARSE["Parse deploy, rollback, or audit arguments L3456-L3492"]
  PARSE --> MODE{"Mode"}
  MODE -->|deploy| PROMOTE["promote_release engine path L3532-L3534"]
  MODE -->|rollback| ROLLBACK["rollback_release engine path L3537-L3540"]
  MODE -->|audit| AUDIT["assert_public_symlinks_under_docroot L3543"]

  AUDIT --> A1["Walk all public symlinks outside .wpcloud-site-git-deploy namespace L2367"]
  A1 --> A2["Reject absolute targets, HOME-containing targets, and targets resolving outside docroot L2377-L2399"]
```

The hidden audit path intentionally performs a full-docroot scan. Deploy and
rollback use the scoped claim assertion in the engine hot path; the audit remains
available for diagnostics and tests.
