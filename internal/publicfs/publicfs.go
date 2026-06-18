package publicfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/state"
)

func PublicSymlinkTarget(deploymentID, claim string) string {
	parent := filepath.ToSlash(filepath.Dir(claim))
	prefix := ""
	if parent != "." && parent != "" {
		for _, component := range strings.Split(parent, "/") {
			if component != "" {
				prefix += "../"
			}
		}
	}
	return prefix + state.DocrootNamespace + "/deployments/" + deploymentID + "/current/" + claim
}

func AssertClaimSymlinksUnderDocroot(docroot string, claims []string, home string) error {
	docrootReal, err := filepath.EvalSymlinks(docroot)
	if err != nil {
		return err
	}
	for _, claim := range claims {
		publicPath := filepath.Join(docroot, filepath.FromSlash(claim))
		info, err := os.Lstat(publicPath)
		if err != nil {
			return fmt.Errorf("public path is not a symlink: %s", claim)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("public path is not a symlink: %s", claim)
		}
		target, err := os.Readlink(publicPath)
		if err != nil {
			return err
		}
		if filepath.IsAbs(target) {
			return fmt.Errorf("public symlink target is absolute: %s", claim)
		}
		if home != "" && strings.Contains(target, home) {
			return fmt.Errorf("public symlink target contains HOME: %s", claim)
		}
		resolved, err := filepath.EvalSymlinks(filepath.Join(filepath.Dir(publicPath), target))
		if err != nil {
			return err
		}
		if resolved != docrootReal && !strings.HasPrefix(resolved, docrootReal+string(os.PathSeparator)) {
			return fmt.Errorf("public symlink resolves outside docroot: %s", claim)
		}
	}
	return nil
}

func AssertAllPublicSymlinksUnderDocroot(docroot, home string) error {
	var claims []string
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
		rel, err := filepath.Rel(docroot, path)
		if err != nil {
			return err
		}
		claims = append(claims, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return err
	}
	return AssertClaimSymlinksUnderDocroot(docroot, claims, home)
}
