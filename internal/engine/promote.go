package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/claims"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/config"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/publicfs"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/releases"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/state"
)

type PromoteOptions struct {
	Docroot      string
	DeploymentID string
	ReleaseID    string
	KeepReleases int
	Boundaries   []string
	Home         string
}

type RollbackOptions struct {
	Config    config.Deployment
	ReleaseID string
	Home      string
}

var deploymentTargetPattern = regexp.MustCompile(`(^|/)\.wpcloud-site-git-deploy/deployments/([^/]+)/current($|/)`)

func Promote(options PromoteOptions) error {
	if options.KeepReleases < 1 {
		options.KeepReleases = 1
	}
	layout := state.NewDocroot(options.Docroot, options.DeploymentID)
	incoming := layout.Incoming(options.ReleaseID)
	release := layout.Release(options.ReleaseID)
	if _, err := os.Stat(incoming); err != nil {
		return fmt.Errorf("incoming release does not exist: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(release), 0o755); err != nil {
		return err
	}
	if err := os.Rename(incoming, release); err != nil {
		return err
	}
	newClaims, err := claims.Compute(release, options.Boundaries, true)
	if err != nil {
		return err
	}
	oldClaims, _ := claims.Compute(currentReleasePath(layout), options.Boundaries, false)
	materialized, err := materializedClaims(options.Docroot, options.DeploymentID)
	if err != nil {
		return err
	}
	oldClaims = union(oldClaims, materialized)
	removedClaims := claims.Removed(oldClaims, newClaims)
	if err := reconcileNewClaims(options.Docroot, options.DeploymentID, newClaims); err != nil {
		return err
	}
	if err := switchCurrent(layout, options.ReleaseID); err != nil {
		return err
	}
	for _, claim := range removedClaims {
		removeOwnedClaim(options.Docroot, options.DeploymentID, claim)
	}
	if err := publicfs.AssertClaimSymlinksUnderDocroot(options.Docroot, newClaims, options.Home); err != nil {
		return err
	}
	_, err = releases.Prune(filepath.Dir(release), options.KeepReleases, options.ReleaseID)
	return err
}

func Rollback(options RollbackOptions) error {
	layout := state.NewDocroot(options.Config.Docroot, options.Config.DeploymentID)
	release := layout.Release(options.ReleaseID)
	if info, err := os.Stat(release); err != nil || !info.IsDir() {
		if err == nil {
			err = fmt.Errorf("not a directory")
		}
		return fmt.Errorf("rollback release does not exist: %s: %w", options.ReleaseID, err)
	}
	newClaims, err := claims.Compute(release, nil, true)
	if err != nil {
		return err
	}
	oldClaims, _ := claims.Compute(currentReleasePath(layout), nil, false)
	materialized, err := materializedClaims(options.Config.Docroot, options.Config.DeploymentID)
	if err != nil {
		return err
	}
	removedClaims := claims.Removed(union(oldClaims, materialized), newClaims)
	if err := reconcileNewClaims(options.Config.Docroot, options.Config.DeploymentID, newClaims); err != nil {
		return err
	}
	if err := switchCurrent(layout, options.ReleaseID); err != nil {
		return err
	}
	for _, claim := range removedClaims {
		removeOwnedClaim(options.Config.Docroot, options.Config.DeploymentID, claim)
	}
	return publicfs.AssertClaimSymlinksUnderDocroot(options.Config.Docroot, newClaims, options.Home)
}

func currentReleasePath(layout state.DocrootLayout) string {
	target, err := os.Readlink(layout.Current())
	if err != nil {
		return ""
	}
	return filepath.Join(layout.Base(), target)
}

func reconcileNewClaims(docroot, deploymentID string, newClaims []string) error {
	for _, claim := range newClaims {
		publicPath := filepath.Join(docroot, filepath.FromSlash(claim))
		if err := rejectForeignAncestor(docroot, deploymentID, claim); err != nil {
			return err
		}
		if err := rejectForeignDescendant(deploymentID, claim, publicPath); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(publicPath), 0o755); err != nil {
			return err
		}
		tmpLink := filepath.Join(filepath.Dir(publicPath), "."+filepath.Base(publicPath)+".wpcloud-site-git-deploy.tmp")
		os.Remove(tmpLink)
		if err := os.Symlink(publicfs.PublicSymlinkTarget(deploymentID, claim), tmpLink); err != nil {
			return err
		}
		if _, err := os.Lstat(publicPath); os.IsNotExist(err) {
			if err := os.Rename(tmpLink, publicPath); err != nil {
				os.Remove(tmpLink)
				return err
			}
			continue
		}
		if err := publicfs.Exchange(tmpLink, publicPath); err != nil {
			os.Remove(tmpLink)
			return err
		}
		if err := os.RemoveAll(tmpLink); err != nil {
			return err
		}
	}
	return nil
}

func switchCurrent(layout state.DocrootLayout, releaseID string) error {
	if err := os.MkdirAll(layout.Base(), 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(layout.Base(), ".current.tmp")
	os.Remove(tmp)
	if err := os.Symlink("releases/"+releaseID, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, layout.Current())
}

func removeOwnedClaim(docroot, deploymentID, claim string) {
	publicPath := filepath.Join(docroot, filepath.FromSlash(claim))
	target, err := os.Readlink(publicPath)
	if err != nil {
		return
	}
	if target == publicfs.PublicSymlinkTarget(deploymentID, claim) {
		os.Remove(publicPath)
	}
}

func materializedClaims(docroot, deploymentID string) ([]string, error) {
	var result []string
	err := filepath.WalkDir(docroot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == filepath.Join(docroot, state.DocrootNamespace) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink == 0 {
			return nil
		}
		target, err := os.Readlink(path)
		if err != nil {
			return err
		}
		owner, ok := deploymentOwnerFromTarget(target)
		if !ok || owner != deploymentID {
			return nil
		}
		rel, err := filepath.Rel(docroot, path)
		if err != nil {
			return err
		}
		result = append(result, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func rejectForeignAncestor(docroot, deploymentID, claim string) error {
	parts := strings.Split(filepath.ToSlash(claim), "/")
	ancestor := docroot
	for i := 0; i < len(parts)-1; i++ {
		ancestor = filepath.Join(ancestor, parts[i])
		target, err := os.Readlink(ancestor)
		if err != nil {
			continue
		}
		if owner, ok := deploymentOwnerFromTarget(target); ok && owner != deploymentID {
			return fmt.Errorf("claim owned by another deployment: %s", claim)
		}
	}
	return nil
}

func rejectForeignDescendant(deploymentID, claim, publicPath string) error {
	info, err := os.Lstat(publicPath)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	return filepath.WalkDir(publicPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink == 0 {
			return nil
		}
		target, err := os.Readlink(path)
		if err != nil {
			return err
		}
		if owner, ok := deploymentOwnerFromTarget(target); ok && owner != deploymentID {
			return fmt.Errorf("claim contains another deployment: %s", claim)
		}
		return nil
	})
}

func deploymentOwnerFromTarget(target string) (string, bool) {
	match := deploymentTargetPattern.FindStringSubmatch(filepath.ToSlash(target))
	if match == nil {
		return "", false
	}
	return match[2], true
}

func union(left, right []string) []string {
	values := map[string]struct{}{}
	for _, value := range left {
		values[value] = struct{}{}
	}
	for _, value := range right {
		values[value] = struct{}{}
	}
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	return result
}
