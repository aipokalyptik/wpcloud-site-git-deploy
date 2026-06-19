package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/auth"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/config"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/execx"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/lock"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/releases"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/report"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/state"
)

type DeployOptions struct {
	StateRoot           string
	Config              config.Deployment
	RefMode             string
	RefValue            string
	Force               bool
	PostDeployOverride  string
	MaintenanceOverride *config.Maintenance
	ToolVersion         string
}

type DeployResult struct {
	ReleaseID string
	Commit    string
	NoOp      bool
	Report    string
}

func Deploy(ctx context.Context, options DeployOptions) (result DeployResult, err error) {
	if options.RefMode == "" {
		options.RefMode = "branch"
		options.RefValue = options.Config.DefaultRef
	}
	layout := state.New(options.StateRoot)
	docrootLayout := state.NewDocroot(options.Config.Docroot, options.Config.DeploymentID)
	collector := report.New(report.Options{
		ToolVersion:  options.ToolVersion,
		Name:         options.Config.Name,
		DeploymentID: options.Config.DeploymentID,
		RefMode:      options.RefMode,
		RefValue:     options.RefValue,
		DeployRoot:   options.Config.DeployRoot,
		Force:        options.Force,
		RunsPath:     layout.Runs(options.Config.Name),
		LatestPath:   layout.LatestRun(options.Config.Name),
	})
	var deployErr error
	status := "failed"
	var deployLock *lock.Lock
	defer func() {
		if err != nil && deployErr == nil {
			deployErr = err
		}
		if err := collector.Finish(status, deployErr); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not write deploy report: %v\n", err)
		}
		if deployLock != nil {
			_ = deployLock.Close()
		}
	}()

	if err := runPhase(collector, "require_commands", func() error {
		return execx.RequireCommands(ctx, []string{"git", "rsync"})
	}); err != nil {
		deployErr = err
		return DeployResult{}, err
	}
	repoDir := layout.Repo(options.Config.Name)
	env := gitEnvironment(options.Config)
	if err := runPhase(collector, "ensure_repo", func() error {
		return ensureRepo(ctx, repoDir, options.Config.RepoURL, env)
	}); err != nil {
		deployErr = err
		return DeployResult{}, err
	}
	if err := runPhase(collector, "fetch", func() error {
		_, err := git(ctx, repoDir, env, "fetch", "--tags", "--prune", "origin", "+refs/heads/*:refs/heads/*", "+refs/heads/*:refs/remotes/origin/*")
		return err
	}); err != nil {
		deployErr = err
		return DeployResult{}, err
	}
	// The repository cache is long-lived, so let Git opportunistically maintain
	// it after network fetches without making deployment depend on GC success.
	stopGC := collector.Phase("git_gc")
	_, _ = git(ctx, repoDir, nil, "gc", "--auto")
	stopGC()
	var commit string
	if err := runPhase(collector, "resolve_ref", func() error {
		var err error
		commit, err = resolveRef(ctx, repoDir, options.RefMode, options.RefValue)
		return err
	}); err != nil {
		deployErr = err
		return DeployResult{}, err
	}
	collector.SetCommit(commit)
	if err := os.MkdirAll(docrootLayout.Base(), 0o755); err != nil {
		deployErr = err
		collector.SetFailedPhase("prepare_docroot_namespace")
		return DeployResult{}, err
	}
	if err := runPhase(collector, "lock_acquire", func() error {
		var err error
		deployLock, err = lock.Acquire(docrootLayout.Lock())
		return err
	}); err != nil {
		deployErr = err
		return DeployResult{}, err
	}
	// The lock is held before the no-op check so a cron deploy can still clean
	// staging left by a killed earlier process even when there is no new commit.
	var sweepStats report.StagingSweepStats
	if err := runPhase(collector, "stale_staging_cleanup", func() error {
		var err error
		sweepStats, err = cleanupStaleStaging(ctx, repoDir, layout, docrootLayout, options.Config.Name, "")
		return err
	}); err != nil {
		deployErr = err
		return DeployResult{}, err
	}
	collector.Stats().StagingSweep = sweepStats
	if !options.Force {
		// A cron-safe deploy is a no-op only when both the resolved commit and
		// deploy root match the currently active release metadata.
		var currentMeta releases.Metadata
		var noop bool
		stopNoOp := collector.Phase("noop_check")
		if meta, ok := loadCurrentMetadata(docrootLayout); ok && releases.CurrentMatches(meta, commit, options.Config.DeployRoot) {
			currentMeta = meta
			noop = true
		}
		stopNoOp()
		if noop {
			collector.SetRelease(currentMeta.ReleaseID)
			status = "no_op"
			return DeployResult{ReleaseID: currentMeta.ReleaseID, Commit: commit, NoOp: true, Report: collector.ReportPath()}, nil
		}
	}
	releaseID, err := releases.NewID(time.Now(), commit)
	if err != nil {
		deployErr = err
		return DeployResult{}, err
	}
	collector.SetRelease(releaseID)
	collector.SetSidecar(docrootLayout.ReleaseStats(releaseID))
	worktree := layout.Worktree(options.Config.Name, releaseID)
	if err := os.MkdirAll(filepath.Dir(worktree), 0o755); err != nil {
		deployErr = err
		collector.SetFailedPhase("worktree_add")
		return DeployResult{}, err
	}
	defer timedCleanup(collector, "worktree_cleanup", func() {
		_ = os.RemoveAll(worktree)
		_, _ = git(ctx, repoDir, nil, "worktree", "remove", "--force", worktree)
		_, _ = git(ctx, repoDir, nil, "worktree", "prune")
	})
	if err := runPhase(collector, "worktree_add", func() error {
		_, err := git(ctx, repoDir, env, "worktree", "add", "--detach", worktree, commit)
		return err
	}); err != nil {
		deployErr = err
		return DeployResult{}, err
	}

	if err := prepareGitFeatures(ctx, worktree, env, collector); err != nil {
		deployErr = err
		return DeployResult{}, err
	}

	source := worktree
	if options.Config.DeployRoot != "" {
		source = filepath.Join(worktree, filepath.FromSlash(options.Config.DeployRoot))
	}
	if info, err := os.Stat(source); err != nil || !info.IsDir() {
		if err == nil {
			err = fmt.Errorf("not a directory")
		}
		deployErr = fmt.Errorf("deploy root does not exist or is not a directory: %s: %w", emptyAsDot(options.Config.DeployRoot), err)
		collector.SetFailedPhase("deploy_root")
		return DeployResult{}, deployErr
	}
	incoming := docrootLayout.Incoming(releaseID)
	if err := os.RemoveAll(incoming); err != nil {
		deployErr = err
		collector.SetFailedPhase("rsync_incoming")
		return DeployResult{}, err
	}
	if err := os.MkdirAll(incoming, 0o755); err != nil {
		deployErr = err
		collector.SetFailedPhase("rsync_incoming")
		return DeployResult{}, err
	}
	defer os.RemoveAll(incoming)
	if err := runPhase(collector, "rsync_incoming", func() error {
		rsyncStats, err := copySourceToIncoming(ctx, source, incoming, docrootLayout)
		collector.SetRsync(rsyncStats)
		return err
	}); err != nil {
		deployErr = err
		return DeployResult{}, err
	}
	maintenance := options.Config.Maintenance
	if options.MaintenanceOverride != nil {
		maintenance = *options.MaintenanceOverride
	}
	postDeploy := options.Config.PostDeploy
	if options.PostDeployOverride != "" {
		postDeploy = options.PostDeployOverride
	}
	if err := promoteLocked(PromoteOptions{
		Context:      ctx,
		Docroot:      options.Config.Docroot,
		DeploymentID: options.Config.DeploymentID,
		ReleaseID:    releaseID,
		KeepReleases: options.Config.KeepReleases,
		PostDeploy:   postDeploy,
		Maintenance:  maintenance,
		Report:       collector,
	}, docrootLayout); err != nil {
		deployErr = err
		return DeployResult{}, err
	}
	meta := releases.Metadata{
		ReleaseID:  releaseID,
		RefMode:    options.RefMode,
		RefValue:   options.RefValue,
		Commit:     commit,
		DeployRoot: options.Config.DeployRoot,
		DeployedAt: time.Now().UTC(),
	}
	if err := runPhase(collector, "metadata_write", func() error {
		return releases.SaveMetadata(docrootLayout.ReleaseMetadata(releaseID), meta)
	}); err != nil {
		deployErr = err
		return DeployResult{}, err
	}
	status = "success"
	return DeployResult{ReleaseID: releaseID, Commit: commit, Report: collector.ReportPath()}, nil
}

func cleanupStaleStaging(ctx context.Context, repoDir string, layout state.Layout, docrootLayout state.DocrootLayout, name, currentReleaseID string) (report.StagingSweepStats, error) {
	// A SIGKILL or host failure can leave both the worktree directory and Git's
	// admin record behind. Remove stale directories first, then ask Git to prune
	// any now-dangling admin records from the bare repo cache.
	worktreesRemoved, err := removeStaleChildren(layout.WorktreeRoot(name), currentReleaseID)
	if err != nil {
		return report.StagingSweepStats{}, err
	}
	_, _ = git(ctx, repoDir, nil, "worktree", "prune")
	// Incoming directories live under the docroot namespace and are safe to
	// remove before staging because each deploy uses a fresh release id.
	incomingRemoved, err := removeStaleChildren(docrootLayout.IncomingRoot(), currentReleaseID)
	if err != nil {
		return report.StagingSweepStats{}, err
	}
	return report.StagingSweepStats{
		StaleWorktreesRemoved: worktreesRemoved,
		StaleIncomingRemoved:  incomingRemoved,
	}, nil
}

func removeStaleChildren(root, keepName string) (int, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	for _, entry := range entries {
		if entry.Name() == keepName {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

func ensureRepo(ctx context.Context, repoDir, repoURL string, env []string) error {
	if _, err := os.Stat(filepath.Join(repoDir, "HEAD")); err == nil {
		// Existing caches are reused across deploys, but the remote URL follows
		// current config so repo moves do not require manual cache deletion.
		_, _ = runCommand(ctx, "git", []string{"remote", "set-url", "origin", repoURL}, repoDir, env)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(repoDir), 0o755); err != nil {
		return err
	}
	_, err := runCommand(ctx, "git", []string{"clone", "--bare", repoURL, repoDir}, "", env)
	return err
}

func EnsureRepo(ctx context.Context, repoDir string, deployment config.Deployment, fetch bool) error {
	env := gitEnvironment(deployment)
	if err := ensureRepo(ctx, repoDir, deployment.RepoURL, env); err != nil {
		return err
	}
	if fetch {
		if _, err := git(ctx, repoDir, env, "fetch", "--tags", "--prune", "origin", "+refs/heads/*:refs/heads/*", "+refs/heads/*:refs/remotes/origin/*"); err != nil {
			return err
		}
		// Inspection commands with --fetch share the same long-lived cache as
		// deploys, so they get the same best-effort maintenance pass.
		_, _ = git(ctx, repoDir, nil, "gc", "--auto")
	}
	return nil
}

func Branches(ctx context.Context, repoDir string, limit int) ([]string, error) {
	return gitLines(ctx, repoDir, limit, "for-each-ref", "--format=%(refname:short)", "refs/heads", "refs/remotes/origin")
}

func Tags(ctx context.Context, repoDir string, limit int) ([]string, error) {
	return gitLines(ctx, repoDir, limit, "tag", "--list")
}

func Commits(ctx context.Context, repoDir string, limit int) ([]string, error) {
	return gitLines(ctx, repoDir, limit, "log", "--oneline", fmt.Sprintf("-%d", limit))
}

func gitLines(ctx context.Context, repoDir string, limit int, args ...string) ([]string, error) {
	result, err := git(ctx, repoDir, nil, args...)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}
	if limit > 0 && len(lines) > limit {
		lines = lines[:limit]
	}
	return lines, nil
}

func resolveRef(ctx context.Context, repoDir, refMode, refValue string) (string, error) {
	switch refMode {
	case "branch":
		commit, err := revParse(ctx, repoDir, "refs/remotes/origin/"+refValue)
		if err == nil {
			return commit, nil
		}
		return revParse(ctx, repoDir, "refs/heads/"+refValue)
	case "tag":
		return revParse(ctx, repoDir, "refs/tags/"+refValue)
	case "commit":
		return revParse(ctx, repoDir, refValue)
	default:
		return "", fmt.Errorf("unsupported ref mode: %s", refMode)
	}
}

func revParse(ctx context.Context, repoDir, ref string) (string, error) {
	result, err := git(ctx, repoDir, nil, "rev-parse", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Stdout), nil
}

func git(ctx context.Context, repoDir string, env []string, args ...string) (execx.Result, error) {
	return runCommand(ctx, "git", args, repoDir, env)
}

func runCommand(ctx context.Context, name string, args []string, dir string, env []string) (execx.Result, error) {
	return execx.Run(ctx, execx.Command{Name: name, Args: args, Dir: dir, Env: env})
}

func gitEnvironment(deployment config.Deployment) []string {
	if deployment.SSHKeyPath == "" {
		return nil
	}
	return []string{"GIT_SSH_COMMAND=" + auth.GitSSHCommand(deployment.SSHKeyPath)}
}

func prepareGitFeatures(ctx context.Context, worktree string, env []string, collector *report.Collector) error {
	stopSubmodules := phaseOrNoop(collector, "git_features.submodules")
	hasSubmodules := false
	if _, err := os.Stat(filepath.Join(worktree, ".gitmodules")); err == nil {
		hasSubmodules = true
	} else if err != nil && !os.IsNotExist(err) {
		stopSubmodules()
		setFailedPhase(collector, "git_features.submodules")
		return err
	}
	if hasSubmodules {
		// Submodules are part of the deployed tree contract. After initializing,
		// verify none remain with the leading "-" status marker Git uses for an
		// uninitialized submodule.
		result, err := runCommand(ctx, "git", []string{"submodule", "update", "--init", "--recursive"}, worktree, env)
		if err != nil {
			stopSubmodules()
			setFailedPhase(collector, "git_features.submodules")
			return err
		}
		_ = result
		status, err := runCommand(ctx, "git", []string{"submodule", "status", "--recursive"}, worktree, nil)
		if err != nil {
			stopSubmodules()
			setFailedPhase(collector, "git_features.submodules")
			return err
		}
		for _, line := range strings.Split(status.Stdout, "\n") {
			if strings.HasPrefix(line, "-") {
				stopSubmodules()
				setFailedPhase(collector, "git_features.submodules")
				return fmt.Errorf("one or more submodules are uninitialized")
			}
			if collector != nil && strings.TrimSpace(line) != "" {
				collector.Stats().Git.Submodules++
			}
		}
	}
	stopSubmodules()

	// Use Git's attribute parser in one NUL-delimited batch so LFS detection
	// follows Git's effective .gitattributes rules without per-file processes.
	files, err := runCommand(ctx, "git", []string{"ls-files", "-z"}, worktree, nil)
	if err != nil {
		return err
	}
	attrs, err := execx.Run(ctx, execx.Command{
		Name:  "git",
		Args:  []string{"check-attr", "filter", "--stdin", "-z"},
		Dir:   worktree,
		Stdin: strings.NewReader(files.Stdout),
	})
	if err != nil {
		return err
	}
	lfsPaths := lfsPathsFromCheckAttr(attrs.Stdout)
	if collector != nil {
		collector.Stats().Git.LFSPaths = len(lfsPaths)
	}
	stopLFS := phaseOrNoop(collector, "git_features.lfs_pull")
	if len(lfsPaths) == 0 {
		stopLFS()
		return nil
	}
	if collector != nil {
		collector.Stats().Git.UsedLFS = true
	}
	if err := execx.RequireCommands(ctx, []string{"git-lfs"}); err != nil {
		stopLFS()
		setFailedPhase(collector, "git_features.lfs_pull")
		return fmt.Errorf("git-lfs is required for repositories using Git LFS: %w", err)
	}
	if _, err := runCommand(ctx, "git-lfs", []string{"install", "--local"}, worktree, env); err != nil {
		stopLFS()
		setFailedPhase(collector, "git_features.lfs_pull")
		return err
	}
	if _, err := runCommand(ctx, "git-lfs", []string{"pull"}, worktree, env); err != nil {
		stopLFS()
		setFailedPhase(collector, "git_features.lfs_pull")
		return err
	}
	stopLFS()
	for _, path := range lfsPaths {
		data, err := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(path)))
		if err != nil {
			setFailedPhase(collector, "git_features.lfs_pull")
			return err
		}
		if strings.HasPrefix(string(data), "version https://git-lfs.github.com/spec/v1\n") {
			// Only LFS-tracked paths are checked for pointer content. That catches
			// failed hydration without rejecting an ordinary text file that happens
			// to contain similar-looking content.
			setFailedPhase(collector, "git_features.lfs_pull")
			return fmt.Errorf("Git LFS pointer files remain after git lfs pull")
		}
	}
	return nil
}

func lfsPathsFromCheckAttr(output string) []string {
	// "git check-attr -z" emits triples: path, attribute name, attribute value.
	fields := strings.Split(output, "\x00")
	var paths []string
	for i := 0; i+2 < len(fields); i += 3 {
		if fields[i+2] == "lfs" {
			paths = append(paths, fields[i])
		}
	}
	return paths
}

func copySourceToIncoming(ctx context.Context, source, incoming string, layout state.DocrootLayout) (*report.RsyncStats, error) {
	excludes, err := os.CreateTemp("", "wpcloud-site-git-deploy-excludes.*")
	if err != nil {
		return nil, err
	}
	excludePath := excludes.Name()
	defer os.Remove(excludePath)
	if _, err := excludes.WriteString(defaultExcludes()); err != nil {
		excludes.Close()
		return nil, err
	}
	if err := excludes.Close(); err != nil {
		return nil, err
	}
	args := []string{"-a", "--delete", "--stats", "--exclude-from=" + excludePath}
	if current := currentReleasePath(layout); current != "" {
		if info, err := os.Stat(current); err == nil && info.IsDir() {
			// Link-dest hardlinks unchanged files against the active release. The
			// checksum/no-times pair avoids timestamp drift deciding reuse.
			args = append(args, "--checksum", "--no-times", "--link-dest="+current)
		}
	}
	args = append(args, source+string(os.PathSeparator), incoming+string(os.PathSeparator))
	result, err := execx.Run(ctx, execx.Command{Name: "rsync", Args: args, Env: []string{"LC_ALL=C"}})
	stats := parseRsyncStats(result.Stdout)
	return stats, err
}

func parseRsyncStats(output string) *report.RsyncStats {
	stats := &report.RsyncStats{}
	found := false
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		normalized := strings.TrimSpace(key)
		text := strings.TrimSpace(value)
		switch normalized {
		case "Number of regular files transferred":
			if parsed, ok := parseRsyncInt(text); ok {
				stats.FilesTransferred = int(parsed)
				found = true
			}
		case "Number of files":
			if parsed, ok := parseRsyncInt(strings.Split(text, " ")[0]); ok {
				stats.TotalFiles = int(parsed)
				found = true
			}
		case "Literal data":
			if parsed, ok := parseRsyncInt(text); ok {
				stats.LiteralDataBytes = parsed
				found = true
			}
		case "Matched data":
			if parsed, ok := parseRsyncInt(text); ok {
				stats.MatchedDataBytes = parsed
				found = true
			}
		case "Total file size":
			if parsed, ok := parseRsyncInt(text); ok {
				stats.TotalSizeBytes = parsed
				found = true
			}
		case "speedup":
			if parsed, err := strconv.ParseFloat(strings.TrimSpace(text), 64); err == nil {
				stats.Speedup = parsed
				found = true
			}
		}
	}
	if !found {
		return nil
	}
	return stats
}

var rsyncNumberPattern = regexp.MustCompile(`[^0-9-]`)

func parseRsyncInt(value string) (int64, bool) {
	cleaned := rsyncNumberPattern.ReplaceAllString(value, "")
	if cleaned == "" {
		return 0, false
	}
	parsed, err := strconv.ParseInt(cleaned, 10, 64)
	return parsed, err == nil
}

func runPhase(collector *report.Collector, name string, fn func() error) error {
	stop := phaseOrNoop(collector, name)
	err := fn()
	stop()
	if err != nil {
		setFailedPhase(collector, name)
	}
	return err
}

func timedCleanup(collector *report.Collector, name string, fn func()) {
	stop := phaseOrNoop(collector, name)
	fn()
	stop()
}

func phaseOrNoop(collector *report.Collector, name string) func() {
	if collector == nil {
		return func() {}
	}
	return collector.Phase(name)
}

func setFailedPhase(collector *report.Collector, name string) {
	if collector != nil {
		collector.SetFailedPhase(name)
	}
}

func defaultExcludes() string {
	return strings.Join([]string{
		".git",
		".git/",
		".gitignore",
		".gitattributes",
		".gitmodules",
		".github/",
		".svn/",
		".hg/",
		".bzr/",
		".aws/",
		".ssh/",
		".env",
		".env.*",
		".npmrc",
		".pypirc",
		".netrc",
		".DS_Store",
	}, "\n") + "\n"
}

func emptyAsDot(value string) string {
	if value == "" {
		return "."
	}
	return value
}

func loadCurrentMetadata(layout state.DocrootLayout) (releases.Metadata, bool) {
	target, err := os.Readlink(layout.Current())
	if err != nil {
		return releases.Metadata{}, false
	}
	releaseID := filepath.Base(target)
	meta, err := releases.LoadMetadata(layout.ReleaseMetadata(releaseID))
	if err != nil {
		return releases.Metadata{}, false
	}
	return meta, true
}
