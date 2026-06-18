package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/config"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/execx"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/releases"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/state"
)

type DeployOptions struct {
	StateRoot string
	Config    config.Deployment
	RefMode   string
	RefValue  string
	Force     bool
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
	if err := ensureRepo(ctx, repoDir, options.Config.RepoURL); err != nil {
		return DeployResult{}, err
	}
	if _, err := git(ctx, repoDir, "fetch", "--tags", "--prune", "origin", "+refs/heads/*:refs/heads/*", "+refs/heads/*:refs/remotes/origin/*"); err != nil {
		return DeployResult{}, err
	}
	_, _ = git(ctx, repoDir, "gc", "--auto")
	commit, err := resolveRef(ctx, repoDir, options.RefMode, options.RefValue)
	if err != nil {
		return DeployResult{}, err
	}
	if !options.Force {
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
	if _, err := git(ctx, repoDir, "worktree", "add", "--detach", worktree, commit); err != nil {
		return DeployResult{}, err
	}
	defer git(ctx, repoDir, "worktree", "remove", "--force", worktree)
	defer git(ctx, repoDir, "worktree", "prune")

	source := worktree
	if options.Config.DeployRoot != "" {
		source = filepath.Join(worktree, filepath.FromSlash(options.Config.DeployRoot))
	}
	incoming := docrootLayout.Incoming(releaseID)
	if err := os.RemoveAll(incoming); err != nil {
		return DeployResult{}, err
	}
	if err := os.MkdirAll(incoming, 0o755); err != nil {
		return DeployResult{}, err
	}
	if _, err := runCommand(ctx, "rsync", []string{"-a", "--delete", source + "/", incoming + "/"}, "", nil); err != nil {
		return DeployResult{}, err
	}
	if err := Promote(PromoteOptions{
		Docroot:      options.Config.Docroot,
		DeploymentID: options.Config.DeploymentID,
		ReleaseID:    releaseID,
		KeepReleases: options.Config.KeepReleases,
	}); err != nil {
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

func ensureRepo(ctx context.Context, repoDir, repoURL string) error {
	if _, err := os.Stat(filepath.Join(repoDir, "HEAD")); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(repoDir), 0o755); err != nil {
		return err
	}
	_, err := runCommand(ctx, "git", []string{"clone", "--bare", repoURL, repoDir}, "", nil)
	return err
}

func EnsureRepo(ctx context.Context, repoDir, repoURL string, fetch bool) error {
	if err := ensureRepo(ctx, repoDir, repoURL); err != nil {
		return err
	}
	if fetch {
		if _, err := git(ctx, repoDir, "fetch", "--tags", "--prune", "origin", "+refs/heads/*:refs/heads/*", "+refs/heads/*:refs/remotes/origin/*"); err != nil {
			return err
		}
		_, _ = git(ctx, repoDir, "gc", "--auto")
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
	result, err := git(ctx, repoDir, args...)
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
	result, err := git(ctx, repoDir, "rev-parse", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Stdout), nil
}

func git(ctx context.Context, repoDir string, args ...string) (execx.Result, error) {
	return runCommand(ctx, "git", args, repoDir, nil)
}

func runCommand(ctx context.Context, name string, args []string, dir string, env []string) (execx.Result, error) {
	return execx.Run(ctx, execx.Command{Name: name, Args: args, Dir: dir, Env: env})
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
