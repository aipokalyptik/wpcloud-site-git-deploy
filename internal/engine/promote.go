package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/claims"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/config"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/lock"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/publicfs"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/releases"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/state"
	"golang.org/x/sys/unix"
)

type PromoteOptions struct {
	Context      context.Context
	Docroot      string
	DeploymentID string
	ReleaseID    string
	KeepReleases int
	Boundaries   []string
	Home         string
	PostDeploy   string
	Maintenance  config.Maintenance
}

type RollbackOptions struct {
	Context   context.Context
	Config    config.Deployment
	ReleaseID string
	Home      string
}

var deploymentTargetPattern = regexp.MustCompile(`(^|/)\.wpcloud-site-git-deploy/deployments/([^/]+)/current($|/)`)

func Promote(options PromoteOptions) error {
	if options.Context == nil {
		options.Context = context.Background()
	}
	if options.KeepReleases < 1 {
		options.KeepReleases = 1
	}
	layout := state.NewDocroot(options.Docroot, options.DeploymentID)
	incoming := layout.Incoming(options.ReleaseID)
	release := layout.Release(options.ReleaseID)
	if err := os.MkdirAll(layout.Base(), 0o755); err != nil {
		return err
	}
	deployLock, err := lock.Acquire(layout.Lock())
	if err != nil {
		return err
	}
	defer deployLock.Close()
	if err := cleanupExchangedPaths(layout); err != nil {
		return err
	}
	cleanupOwnedMaintenanceFile(options.Docroot, options.DeploymentID, options.Maintenance)
	if _, err := os.Stat(incoming); err != nil {
		return fmt.Errorf("incoming release does not exist: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(release), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(release); err == nil {
		return fmt.Errorf("release already exists: %s", release)
	}
	maintenanceOwned, err := createMaintenanceFile(options.Docroot, options.DeploymentID, options.ReleaseID, options.Maintenance)
	if err != nil {
		return err
	}
	if maintenanceOwned {
		defer cleanupOwnedMaintenanceFile(options.Docroot, options.DeploymentID, options.Maintenance)
	}
	boundaries, err := effectiveBoundaries(options.Docroot, options.Boundaries)
	if err != nil {
		return err
	}
	if err := os.Rename(incoming, release); err != nil {
		return err
	}
	cleanupReleaseOnFailure := true
	defer func() {
		if cleanupReleaseOnFailure {
			_ = os.RemoveAll(release)
		}
	}()
	if err := os.Chtimes(release, time.Now(), time.Now()); err != nil {
		return err
	}
	newClaims, err := claims.Compute(release, boundaries, true)
	if err != nil {
		return err
	}
	protectedAnchors, err := discoverProtectedAnchors(options.Docroot)
	if err != nil {
		return err
	}
	if err := validateClaimsNotProtected(newClaims, protectedAnchors); err != nil {
		return err
	}
	oldClaims, _ := claims.Compute(currentReleasePath(layout), boundaries, false)
	materialized, err := materializedClaims(options.Docroot, options.DeploymentID)
	if err != nil {
		return err
	}
	oldClaims = union(oldClaims, materialized)
	removedClaims := claims.Removed(oldClaims, newClaims)
	cleanupReleaseOnFailure = false
	cleanupOverlappingRemovedClaims(options.Docroot, options.DeploymentID, removedClaims, newClaims)
	if err := reconcileNewClaims(options.Docroot, options.DeploymentID, newClaims); err != nil {
		return err
	}
	if err := switchCurrent(layout, options.ReleaseID); err != nil {
		return err
	}
	if target, err := os.Readlink(layout.Current()); err != nil || target != "releases/"+options.ReleaseID {
		return fmt.Errorf("current does not point to releases/%s", options.ReleaseID)
	}
	if err := cleanupExchangedPaths(layout); err != nil {
		return err
	}
	for _, claim := range removedClaims {
		removeOwnedClaim(options.Docroot, options.DeploymentID, claim)
	}
	if err := publicfs.AssertClaimSymlinksUnderDocroot(options.Docroot, newClaims, options.Home); err != nil {
		return err
	}
	if err := runPostDeploy(options.Context, options.Docroot, options.PostDeploy); err != nil {
		return err
	}
	cleanupOwnedMaintenanceFile(options.Docroot, options.DeploymentID, options.Maintenance)
	maintenanceOwned = false
	_, err = releases.Prune(filepath.Dir(release), options.KeepReleases, options.ReleaseID)
	return err
}

func Rollback(options RollbackOptions) error {
	if options.Context == nil {
		options.Context = context.Background()
	}
	layout := state.NewDocroot(options.Config.Docroot, options.Config.DeploymentID)
	if err := os.MkdirAll(layout.Base(), 0o755); err != nil {
		return err
	}
	deployLock, err := lock.Acquire(layout.Lock())
	if err != nil {
		return err
	}
	defer deployLock.Close()
	if err := cleanupExchangedPaths(layout); err != nil {
		return err
	}
	cleanupOwnedMaintenanceFile(options.Config.Docroot, options.Config.DeploymentID, options.Config.Maintenance)
	release := layout.Release(options.ReleaseID)
	if info, err := os.Stat(release); err != nil || !info.IsDir() {
		if err == nil {
			err = fmt.Errorf("not a directory")
		}
		return fmt.Errorf("rollback release does not exist: %s: %w", options.ReleaseID, err)
	}
	maintenanceOwned, err := createMaintenanceFile(options.Config.Docroot, options.Config.DeploymentID, options.ReleaseID, options.Config.Maintenance)
	if err != nil {
		return err
	}
	if maintenanceOwned {
		defer cleanupOwnedMaintenanceFile(options.Config.Docroot, options.Config.DeploymentID, options.Config.Maintenance)
	}
	boundaries, err := discoverBoundaryClaims(options.Config.Docroot)
	if err != nil {
		return err
	}
	newClaims, err := claims.Compute(release, boundaries, true)
	if err != nil {
		return err
	}
	protectedAnchors, err := discoverProtectedAnchors(options.Config.Docroot)
	if err != nil {
		return err
	}
	if err := validateClaimsNotProtected(newClaims, protectedAnchors); err != nil {
		return err
	}
	oldClaims, _ := claims.Compute(currentReleasePath(layout), boundaries, false)
	materialized, err := materializedClaims(options.Config.Docroot, options.Config.DeploymentID)
	if err != nil {
		return err
	}
	removedClaims := claims.Removed(union(oldClaims, materialized), newClaims)
	cleanupOverlappingRemovedClaims(options.Config.Docroot, options.Config.DeploymentID, removedClaims, newClaims)
	if err := reconcileNewClaims(options.Config.Docroot, options.Config.DeploymentID, newClaims); err != nil {
		return err
	}
	if err := switchCurrent(layout, options.ReleaseID); err != nil {
		return err
	}
	if err := cleanupExchangedPaths(layout); err != nil {
		return err
	}
	for _, claim := range removedClaims {
		removeOwnedClaim(options.Config.Docroot, options.Config.DeploymentID, claim)
	}
	if err := publicfs.AssertClaimSymlinksUnderDocroot(options.Config.Docroot, newClaims, options.Home); err != nil {
		return err
	}
	cleanupOwnedMaintenanceFile(options.Config.Docroot, options.Config.DeploymentID, options.Config.Maintenance)
	maintenanceOwned = false
	return nil
}

func ClaimsForCurrent(docroot, deploymentID string) ([]string, error) {
	layout := state.NewDocroot(docroot, deploymentID)
	current := currentReleasePath(layout)
	if current == "" {
		return nil, nil
	}
	boundaries, err := discoverBoundaryClaims(docroot)
	if err != nil {
		return nil, err
	}
	return claims.Compute(current, boundaries, true)
}

func AssertDeploymentSymlinks(docroot, deploymentID, home string) error {
	ownedClaims, err := materializedClaims(docroot, deploymentID)
	if err != nil {
		return err
	}
	return publicfs.AssertClaimSymlinksUnderDocroot(docroot, ownedClaims, home)
}

func currentReleasePath(layout state.DocrootLayout) string {
	target, err := os.Readlink(layout.Current())
	if err != nil {
		return ""
	}
	return filepath.Join(layout.Base(), target)
}

func reconcileNewClaims(docroot, deploymentID string, newClaims []string) error {
	layout := state.NewDocroot(docroot, deploymentID)
	exchangedPathsFile := layout.ExchangedPaths()
	if err := os.WriteFile(exchangedPathsFile, nil, 0o644); err != nil {
		return err
	}
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
		if err := appendExchangedPath(exchangedPathsFile, tmpLink); err != nil {
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

func cleanupOverlappingRemovedClaims(docroot, deploymentID string, removedClaims, newClaims []string) {
	for _, removedClaim := range removedClaims {
		for _, newClaim := range newClaims {
			if removedClaim == newClaim ||
				strings.HasPrefix(removedClaim, newClaim+"/") ||
				strings.HasPrefix(newClaim, removedClaim+"/") {
				removeOwnedClaim(docroot, deploymentID, removedClaim)
				break
			}
		}
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

func appendExchangedPath(path, value string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = fmt.Fprintln(file, value)
	return err
}

func cleanupExchangedPaths(layout state.DocrootLayout) error {
	data, err := os.ReadFile(layout.ExchangedPaths())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, path := range strings.Split(string(data), "\n") {
		if path == "" {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return os.Remove(layout.ExchangedPaths())
}

func effectiveBoundaries(docroot string, explicit []string) ([]string, error) {
	if len(explicit) > 0 {
		return explicit, nil
	}
	return discoverBoundaryClaims(docroot)
}

func discoverBoundaryClaims(docroot string) ([]string, error) {
	var boundaries []string
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
		if !entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSticky == 0 {
			return nil
		}
		if !rootOwnedOrRootGroup(info) {
			return nil
		}
		rel, err := filepath.Rel(docroot, path)
		if err != nil {
			return err
		}
		boundaries = append(boundaries, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(boundaries)
	return boundaries, err
}

func discoverProtectedAnchors(docroot string) ([]string, error) {
	var anchors []string
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
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("stat data unavailable for %s", path)
		}
		writable := unix.Access(path, unix.W_OK) == nil
		if !protectedAnchorCandidate(stat.Uid, stat.Gid, writable) {
			return nil
		}
		rel, err := filepath.Rel(docroot, path)
		if err != nil {
			return err
		}
		anchors = append(anchors, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(anchors)
	return anchors, err
}

func rootOwnedOrRootGroup(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return stat.Uid == 0 || stat.Gid == 0
}

func protectedAnchorCandidate(uid, gid uint32, writableByCurrentUser bool) bool {
	if uid != 0 && gid != 0 {
		return false
	}
	return !writableByCurrentUser
}

func validateClaimsNotProtected(newClaims, protectedAnchors []string) error {
	for _, claim := range newClaims {
		for _, protected := range protectedAnchors {
			if protected == "." || protected == "" {
				return fmt.Errorf("protected path: %s", claim)
			}
			if claim == protected ||
				strings.HasPrefix(claim, protected+"/") ||
				strings.HasPrefix(protected, claim+"/") {
				return fmt.Errorf("protected path: %s", claim)
			}
		}
	}
	return nil
}

func runPostDeploy(ctx context.Context, docroot, postDeploy string) error {
	if postDeploy == "" {
		return nil
	}
	path := postDeploy
	if !filepath.IsAbs(path) {
		path = filepath.Join(docroot, filepath.FromSlash(postDeploy))
	}
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		if err == nil {
			err = fmt.Errorf("is a directory")
		}
		return fmt.Errorf("post-deploy file does not exist: %s: %w", postDeploy, err)
	}
	cmd := exec.CommandContext(ctx, "bash", "-e", path)
	cmd.Dir = docroot
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("post-deploy failed: %s: %w: %s", postDeploy, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func maintenancePath(docroot string, maintenance config.Maintenance) string {
	if !maintenance.Enabled || maintenance.File == "" {
		return ""
	}
	return filepath.Join(docroot, filepath.FromSlash(maintenance.File))
}

func maintenanceFileIsToolOwned(path, deploymentID string) bool {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || info.IsDir() {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	text := string(data)
	return (strings.Contains(text, "wpcloud-site-git-deploy maintenance") ||
		strings.Contains(text, "// wpcloud-site-git-deploy maintenance")) &&
		(strings.Contains(text, "deployment_id="+deploymentID) ||
			strings.Contains(text, "// deployment_id="+deploymentID))
}

func cleanupOwnedMaintenanceFile(docroot, deploymentID string, maintenance config.Maintenance) {
	path := maintenancePath(docroot, maintenance)
	if path == "" {
		return
	}
	if maintenanceFileIsToolOwned(path, deploymentID) {
		_ = os.Remove(path)
	}
}

func createMaintenanceFile(docroot, deploymentID, releaseID string, maintenance config.Maintenance) (bool, error) {
	path := maintenancePath(docroot, maintenance)
	if path == "" {
		return false, nil
	}
	cleanupOwnedMaintenanceFile(docroot, deploymentID, maintenance)
	if _, err := os.Lstat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	parent := filepath.Dir(path)
	if info, err := os.Stat(parent); err != nil || !info.IsDir() {
		if err == nil {
			err = fmt.Errorf("not a directory")
		}
		return false, fmt.Errorf("maintenance-file parent does not exist: %s: %w", filepath.Dir(maintenance.File), err)
	}
	tmp, err := os.CreateTemp(parent, "."+filepath.Base(path)+".*")
	if err != nil {
		return false, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	content := fmt.Sprintf("<?php\n$upgrading = %d;\n// wpcloud-site-git-deploy maintenance\n// deployment_id=%s\n// release_id=%s\n// created_at=%s\n",
		time.Now().Unix(),
		deploymentID,
		releaseID,
		time.Now().UTC().Format(time.RFC3339),
	)
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return false, err
	}
	return true, nil
}
