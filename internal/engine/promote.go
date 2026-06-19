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

type docrootFacts struct {
	boundaries       []string
	protectedAnchors []string
	materialized     []string
}

func Promote(options PromoteOptions) error {
	layout := state.NewDocroot(options.Docroot, options.DeploymentID)
	if err := os.MkdirAll(layout.Base(), 0o755); err != nil {
		return err
	}
	deployLock, err := lock.Acquire(layout.Lock())
	if err != nil {
		return err
	}
	defer deployLock.Close()
	return promoteLocked(options, layout)
}

func promoteLocked(options PromoteOptions, layout state.DocrootLayout) error {
	if options.Context == nil {
		options.Context = context.Background()
	}
	if options.KeepReleases < 1 {
		options.KeepReleases = 1
	}
	incoming := layout.Incoming(options.ReleaseID)
	release := layout.Release(options.ReleaseID)
	if err := os.MkdirAll(layout.Base(), 0o755); err != nil {
		return err
	}
	// A previous failure can leave the temporary side of an exchange behind.
	// Clean it before doing new work so the retry path is idempotent.
	if err := cleanupExchangedPaths(layout); err != nil {
		return err
	}
	// Remove stale maintenance markers owned by this deployment, but never a
	// marker owned by another overlapping deployment or by WordPress itself.
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
	facts, err := discoverDocrootFacts(options.Docroot, options.DeploymentID, len(options.Boundaries) == 0, true)
	if err != nil {
		return err
	}
	boundaries := options.Boundaries
	if len(boundaries) == 0 {
		boundaries = facts.boundaries
	}
	if err := os.Rename(incoming, release); err != nil {
		return err
	}
	// Until claim validation and reconciliation prove this release is usable,
	// a failed promotion should not leave a rollback-selectable release dir.
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
	if err := validateClaimsNotProtected(newClaims, facts.protectedAnchors); err != nil {
		return err
	}
	oldClaims, _ := claims.Compute(currentReleasePath(layout), boundaries, false)
	// Materialized public symlinks are included because earlier failed or manual
	// repairs can leave owned claims that are no longer derivable from current.
	oldClaims = union(oldClaims, facts.materialized)
	removedClaims := claims.Removed(oldClaims, newClaims)
	cleanupReleaseOnFailure = false
	// If a path changes shape, such as file -> directory, remove the old owned
	// symlink before creating the new claim so reconciliation can proceed.
	cleanupOverlappingRemovedClaims(options.Docroot, options.DeploymentID, removedClaims, newClaims)
	if err := reconcileNewClaims(options.Docroot, options.DeploymentID, newClaims); err != nil {
		return err
	}
	if err := switchCurrent(layout, options.ReleaseID); err != nil {
		return err
	}
	// The public "current" pointer must stay relative to the docroot-visible
	// release namespace; an absolute target would break WP Cloud HTTP requests.
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
	// Hooks run while maintenance is active. This explicit success cleanup pairs
	// with the deferred failure cleanup so owned markers are not left behind.
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
	// Rollback uses the same exchange cleanup and lock as deploy because it
	// rewrites public claims and current in the same docroot namespace.
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
	facts, err := discoverDocrootFacts(options.Config.Docroot, options.Config.DeploymentID, true, true)
	if err != nil {
		return err
	}
	newClaims, err := claims.Compute(release, facts.boundaries, true)
	if err != nil {
		return err
	}
	if err := validateClaimsNotProtected(newClaims, facts.protectedAnchors); err != nil {
		return err
	}
	oldClaims, _ := claims.Compute(currentReleasePath(layout), facts.boundaries, false)
	// Rollback does not create metadata or prune releases; it only recomputes
	// the public claims needed to make the selected existing release active.
	removedClaims := claims.Removed(union(oldClaims, facts.materialized), newClaims)
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
	// current is intentionally relative ("releases/<id>"), so join it to the
	// deployment base before reading release content.
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
		// Exact path takeover is allowed, but this deployment must not route
		// through or engulf another deployment's subtree.
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
		// Existing public paths are atomically exchanged rather than removed and
		// recreated, so readers never observe a missing path during promotion.
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
	// Rename over the current symlink is the atomic release flip. Do not replace
	// this with remove-then-create, which would expose a missing current pointer.
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
	newClaimIndex := newPathOverlapIndex(newClaims)
	for _, removedClaim := range removedClaims {
		if newClaimIndex.overlaps(removedClaim) {
			removeOwnedClaim(docroot, deploymentID, removedClaim)
		}
	}
}

func rejectForeignAncestor(docroot, deploymentID, claim string) error {
	parts := strings.Split(filepath.ToSlash(claim), "/")
	ancestor := docroot
	for i := 0; i < len(parts)-1; i++ {
		ancestor = filepath.Join(ancestor, parts[i])
		// Walk public ancestors to catch cases where a new nested claim would be
		// served through another deployment's public symlink.
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
	// A real directory at the claimed path may contain another deployment's
	// symlink. Replacing it would engulf that deployment, so reject descendants.
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
	// Public deployment links all point into .wpcloud-site-git-deploy/deployments
	// and include the owner id in the path; extract that id from any relative
	// target shape before making cross-deployment decisions.
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
		// The recorded path is the temporary side of a prior exchange. Removing
		// it is safe and makes retry after interruption deterministic.
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return os.Remove(layout.ExchangedPaths())
}

func discoverBoundaryClaims(docroot string) ([]string, error) {
	facts, err := discoverDocrootFacts(docroot, "", true, false)
	return facts.boundaries, err
}

func discoverProtectedAnchors(docroot string) ([]string, error) {
	facts, err := discoverDocrootFacts(docroot, "", false, true)
	return facts.protectedAnchors, err
}

// discoverDocrootFacts is the promotion-time docroot scan. Deploy and rollback
// need boundaries, protected anchors, and owned materialized symlinks together,
// so collecting them in one walk avoids repeatedly traversing large uploads
// trees. Focused diagnostic callers can disable facts they do not need.
func discoverDocrootFacts(docroot, deploymentID string, collectBoundaries, collectProtectedAnchors bool) (docrootFacts, error) {
	var facts docrootFacts
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
			if deploymentID == "" {
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
			facts.materialized = append(facts.materialized, filepath.ToSlash(rel))
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(docroot, path)
		if err != nil {
			return err
		}

		if collectBoundaries && entry.IsDir() && info.Mode()&os.ModeSticky != 0 && rootOwnedOrRootGroup(info) {
			// Match the Bash spec: only root-owned or root-group sticky
			// directories become claim boundaries. Ordinary sticky dirs owned by
			// the site user are not treated as protected WordPress platform
			// structure.
			facts.boundaries = append(facts.boundaries, filepath.ToSlash(rel))
		}

		if collectProtectedAnchors {
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok {
				return fmt.Errorf("stat data unavailable for %s", path)
			}
			// unix.Access asks whether the current site user can write the path.
			// That differs from inspecting mode bits and catches root-owned 0644
			// anchors.
			writable := unix.Access(path, unix.W_OK) == nil
			if protectedAnchorCandidate(stat.Uid, stat.Gid, writable) {
				facts.protectedAnchors = append(facts.protectedAnchors, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	sort.Strings(facts.boundaries)
	sort.Strings(facts.protectedAnchors)
	sort.Strings(facts.materialized)
	return facts, err
}

func materializedClaims(docroot, deploymentID string) ([]string, error) {
	facts, err := discoverDocrootFacts(docroot, deploymentID, false, false)
	return facts.materialized, err
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
	for _, protected := range protectedAnchors {
		if protected == "." || protected == "" {
			if len(newClaims) == 0 {
				return nil
			}
			return fmt.Errorf("protected path: %s", newClaims[0])
		}
	}
	protectedIndex := newPathOverlapIndex(protectedAnchors)
	for _, claim := range newClaims {
		// Both ancestor and descendant overlaps are rejected so a deploy cannot
		// replace a protected anchor or claim a parent that contains it.
		if protectedIndex.overlaps(claim) {
			return fmt.Errorf("protected path: %s", claim)
		}
	}
	return nil
}

type pathOverlapIndex struct {
	exact  map[string]struct{}
	sorted []string
}

// pathOverlapIndex checks whether slash-separated public claims overlap without
// comparing every candidate against every other candidate. An overlap means:
// the same path, an indexed ancestor of the path, or an indexed descendant of
// the path.
func newPathOverlapIndex(paths []string) pathOverlapIndex {
	index := pathOverlapIndex{
		exact:  make(map[string]struct{}, len(paths)),
		sorted: make([]string, 0, len(paths)),
	}
	for _, path := range paths {
		index.exact[path] = struct{}{}
		index.sorted = append(index.sorted, path)
	}
	sort.Strings(index.sorted)
	return index
}

func (index pathOverlapIndex) overlaps(path string) bool {
	if _, ok := index.exact[path]; ok {
		return true
	}
	// Walk from "a/b/c" to "a/b" to "a" so ancestor checks cost path depth,
	// not the size of the other claim set.
	for ancestor := path; ; {
		lastSlash := strings.LastIndex(ancestor, "/")
		if lastSlash <= 0 {
			break
		}
		ancestor = ancestor[:lastSlash]
		if _, ok := index.exact[ancestor]; ok {
			return true
		}
	}
	descendantPrefix := path + "/"
	// The first sorted value at or after "path/" is enough to know whether any
	// indexed path lives under that prefix.
	position := sort.SearchStrings(index.sorted, descendantPrefix)
	return position < len(index.sorted) && strings.HasPrefix(index.sorted[position], descendantPrefix)
}

func runPostDeploy(ctx context.Context, docroot, postDeploy string) error {
	if postDeploy == "" {
		return nil
	}
	path := postDeploy
	if !filepath.IsAbs(path) {
		// Relative hook paths are resolved from the docroot because operators
		// think of post-deploy scripts as site files, not shell HOME files.
		path = filepath.Join(docroot, filepath.FromSlash(postDeploy))
	}
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		if err == nil {
			err = fmt.Errorf("is a directory")
		}
		return fmt.Errorf("post-deploy file does not exist: %s: %w", postDeploy, err)
	}
	cmd := exec.CommandContext(ctx, "bash", "-e", path)
	// Hooks run with docroot as cwd so WordPress-oriented scripts can address
	// site files with normal relative paths.
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
	// Accept both current PHP-comment markers and older plain marker text so a
	// successful later deploy can clean up a stale marker from either format.
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
		// Never remove an unowned maintenance file; overlapping deployments and
		// WordPress core updates may create their own markers at the same path.
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
		// Another owner already has a maintenance file in place. Leave it alone
		// and continue without claiming ownership of cleanup.
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
	// WordPress maintenance mode requires PHP that sets $upgrading to a recent
	// timestamp. Plain text would be included as response output and ignored.
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
