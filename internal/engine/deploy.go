package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/auth"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/config"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/execx"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/lock"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/releases"
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
}

type DeployResult struct {
	ReleaseID string
	Commit    string
	NoOp      bool
}

func Deploy(ctx context.Context, options DeployOptions) (DeployResult, error) {
	if err := execx.RequireCommands(ctx, []string{"git", "rsync"}); err != nil {
		return DeployResult{}, err
	}
	if options.RefMode == "" {
		options.RefMode = "branch"
		options.RefValue = options.Config.DefaultRef
	}
	layout := state.New(options.StateRoot)
	docrootLayout := state.NewDocroot(options.Config.Docroot, options.Config.DeploymentID)
	repoDir := layout.Repo(options.Config.Name)
	env := gitEnvironment(options.Config)
	if err := ensureRepo(ctx, repoDir, options.Config.RepoURL, env); err != nil {
		return DeployResult{}, err
	}
	if _, err := git(ctx, repoDir, env, "fetch", "--tags", "--prune", "origin", "+refs/heads/*:refs/heads/*", "+refs/heads/*:refs/remotes/origin/*"); err != nil {
		return DeployResult{}, err
	}
	// The repository cache is long-lived, so let Git opportunistically maintain
	// it after network fetches without making deployment depend on GC success.
	_, _ = git(ctx, repoDir, nil, "gc", "--auto")
	commit, err := resolveRef(ctx, repoDir, options.RefMode, options.RefValue)
	if err != nil {
		return DeployResult{}, err
	}
	if err := os.MkdirAll(docrootLayout.Base(), 0o755); err != nil {
		return DeployResult{}, err
	}
	deployLock, err := lock.Acquire(docrootLayout.Lock())
	if err != nil {
		return DeployResult{}, err
	}
	defer deployLock.Close()
	// The lock is held before the no-op check so a cron deploy can still clean
	// staging left by a killed earlier process even when there is no new commit.
	if err := cleanupStaleStaging(ctx, repoDir, layout, docrootLayout, options.Config.Name, ""); err != nil {
		return DeployResult{}, err
	}
	if !options.Force {
		// A cron-safe deploy is a no-op only when both the resolved commit and
		// deploy root match the currently active release metadata.
		if currentMeta, ok := loadCurrentMetadata(docrootLayout); ok && releases.CurrentMatches(currentMeta, commit, options.Config.DeployRoot) {
			return DeployResult{ReleaseID: currentMeta.ReleaseID, Commit: commit, NoOp: true}, nil
		}
	}
	releaseID, err := releases.NewID(time.Now(), commit)
	if err != nil {
		return DeployResult{}, err
	}
	worktree := layout.Worktree(options.Config.Name, releaseID)
	if err := os.MkdirAll(filepath.Dir(worktree), 0o755); err != nil {
		return DeployResult{}, err
	}
	defer os.RemoveAll(worktree)
	if _, err := git(ctx, repoDir, env, "worktree", "add", "--detach", worktree, commit); err != nil {
		return DeployResult{}, err
	}
	defer git(ctx, repoDir, nil, "worktree", "remove", "--force", worktree)
	defer git(ctx, repoDir, nil, "worktree", "prune")

	if err := prepareGitFeatures(ctx, worktree, env); err != nil {
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
		return DeployResult{}, fmt.Errorf("deploy root does not exist or is not a directory: %s: %w", emptyAsDot(options.Config.DeployRoot), err)
	}
	incoming := docrootLayout.Incoming(releaseID)
	if err := os.RemoveAll(incoming); err != nil {
		return DeployResult{}, err
	}
	if err := os.MkdirAll(incoming, 0o755); err != nil {
		return DeployResult{}, err
	}
	defer os.RemoveAll(incoming)
	if err := copySourceToIncoming(ctx, source, incoming, docrootLayout); err != nil {
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
	}, docrootLayout); err != nil {
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
	if err := releases.SaveMetadata(docrootLayout.ReleaseMetadata(releaseID), meta); err != nil {
		return DeployResult{}, err
	}
	return DeployResult{ReleaseID: releaseID, Commit: commit}, nil
}

func cleanupStaleStaging(ctx context.Context, repoDir string, layout state.Layout, docrootLayout state.DocrootLayout, name, currentReleaseID string) error {
	// A SIGKILL or host failure can leave both the worktree directory and Git's
	// admin record behind. Remove stale directories first, then ask Git to prune
	// any now-dangling admin records from the bare repo cache.
	if err := removeStaleChildren(layout.WorktreeRoot(name), currentReleaseID); err != nil {
		return err
	}
	_, _ = git(ctx, repoDir, nil, "worktree", "prune")
	// Incoming directories live under the docroot namespace and are safe to
	// remove before staging because each deploy uses a fresh release id.
	if err := removeStaleChildren(docrootLayout.IncomingRoot(), currentReleaseID); err != nil {
		return err
	}
	return nil
}

func removeStaleChildren(root, keepName string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.Name() == keepName {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
			return err
		}
	}
	return nil
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

func prepareGitFeatures(ctx context.Context, worktree string, env []string) error {
	if _, err := os.Stat(filepath.Join(worktree, ".gitmodules")); err == nil {
		// Submodules are part of the deployed tree contract. After initializing,
		// verify none remain with the leading "-" status marker Git uses for an
		// uninitialized submodule.
		if _, err := runCommand(ctx, "git", []string{"submodule", "update", "--init", "--recursive"}, worktree, env); err != nil {
			return err
		}
		status, err := runCommand(ctx, "git", []string{"submodule", "status", "--recursive"}, worktree, nil)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(status.Stdout, "\n") {
			if strings.HasPrefix(line, "-") {
				return fmt.Errorf("one or more submodules are uninitialized")
			}
		}
	}

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
	if len(lfsPaths) == 0 {
		return nil
	}
	if err := execx.RequireCommands(ctx, []string{"git-lfs"}); err != nil {
		return fmt.Errorf("git-lfs is required for repositories using Git LFS: %w", err)
	}
	if _, err := runCommand(ctx, "git-lfs", []string{"install", "--local"}, worktree, env); err != nil {
		return err
	}
	if _, err := runCommand(ctx, "git-lfs", []string{"pull"}, worktree, env); err != nil {
		return err
	}
	for _, path := range lfsPaths {
		data, err := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(path)))
		if err != nil {
			return err
		}
		if strings.HasPrefix(string(data), "version https://git-lfs.github.com/spec/v1\n") {
			// Only LFS-tracked paths are checked for pointer content. That catches
			// failed hydration without rejecting an ordinary text file that happens
			// to contain similar-looking content.
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

func copySourceToIncoming(ctx context.Context, source, incoming string, layout state.DocrootLayout) error {
	excludes, err := os.CreateTemp("", "wpcloud-site-git-deploy-excludes.*")
	if err != nil {
		return err
	}
	excludePath := excludes.Name()
	defer os.Remove(excludePath)
	if _, err := excludes.WriteString(defaultExcludes()); err != nil {
		excludes.Close()
		return err
	}
	if err := excludes.Close(); err != nil {
		return err
	}
	args := []string{"-a", "--delete", "--exclude-from=" + excludePath}
	if current := currentReleasePath(layout); current != "" {
		if info, err := os.Stat(current); err == nil && info.IsDir() {
			// Link-dest hardlinks unchanged files against the active release. The
			// checksum/no-times pair avoids timestamp drift deciding reuse.
			args = append(args, "--checksum", "--no-times", "--link-dest="+current)
		}
	}
	args = append(args, source+string(os.PathSeparator), incoming+string(os.PathSeparator))
	_, err = runCommand(ctx, "rsync", args, "", nil)
	return err
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
