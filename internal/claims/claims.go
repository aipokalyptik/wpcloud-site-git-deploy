package claims

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/state"
)

var rejectedSharedPaths = []string{
	"wp-content/cache",
	"wp-content/upgrade",
	".maintenance",
}

// These WordPress media containers are persistent runtime-owned directories.
// The deployer may add or remove exact regular-file leaf symlinks inside them,
// but it must not replace the container directories or claim whole subtrees.
var sharedContainerPaths = []string{
	"wp-content/uploads",
	"wp-content/blogs.dir",
}

func Compute(releaseTree string, boundaries []string, rejectSharedPaths bool) ([]string, error) {
	info, err := os.Stat(releaseTree)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("release tree is not a directory: %s", releaseTree)
	}

	claims := map[string]struct{}{}
	err = filepath.WalkDir(releaseTree, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == releaseTree {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		entryType := entry.Type()
		if !entryType.IsRegular() && entryType&os.ModeSymlink == 0 {
			return nil
		}
		rel, err := filepath.Rel(releaseTree, path)
		if err != nil {
			return err
		}
		publicPath := filepath.ToSlash(rel)
		if strings.Contains(publicPath, "\n") {
			return fmt.Errorf("unsupported newline in release path")
		}
		if publicPath == ".git" || strings.HasPrefix(publicPath, ".git/") {
			return nil
		}
		if publicPath == state.DocrootNamespace || strings.HasPrefix(publicPath, state.DocrootNamespace+"/") {
			return nil
		}
		for _, rejected := range rejectedSharedPaths {
			if publicPath == rejected || strings.HasPrefix(publicPath, rejected+"/") {
				if rejectSharedPaths {
					return fmt.Errorf("shared path cannot be deployed: %s", rejected)
				}
			}
		}
		if sharedPath, ok := sharedContainerFor(publicPath); ok {
			// A file at the exact container path would replace WordPress' runtime
			// directory, so treat it like the fully rejected shared paths.
			if rejectSharedPaths && publicPath == sharedPath {
				return fmt.Errorf("shared path cannot be deployed: %s", sharedPath)
			}
			// Symlinks can point at directories or outside the regular-file model,
			// which would undermine the "leaf files only" safety rule.
			if rejectSharedPaths && entryType&os.ModeSymlink != 0 {
				return fmt.Errorf("shared container symlink cannot be deployed: %s", publicPath)
			}
			// Do not compress media-container paths through sticky boundaries:
			// each deployed media file is claimed independently so WordPress can
			// keep managing sibling uploads and generated directories.
			claims[publicPath] = struct{}{}
			return nil
		}
		claims[claimForPath(publicPath, boundaries)] = struct{}{}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return sortedKeys(claims), nil
}

func Removed(oldClaims, newClaims []string) []string {
	newSet := map[string]struct{}{}
	for _, claim := range newClaims {
		newSet[claim] = struct{}{}
	}
	removed := map[string]struct{}{}
	for _, claim := range oldClaims {
		if _, ok := newSet[claim]; !ok {
			removed[claim] = struct{}{}
		}
	}
	return sortedKeys(removed)
}

func claimForPath(publicPath string, boundaries []string) string {
	bestBoundary := ""
	for _, boundary := range boundaries {
		// Boundaries are stored as slash-style public paths. Trim surrounding
		// slashes before comparing so callers can pass either "a/b" or "/a/b/".
		boundary = strings.Trim(boundary, "/")
		if boundary == "" {
			continue
		}
		if strings.HasPrefix(publicPath, boundary+"/") && len(boundary) > len(bestBoundary) {
			bestBoundary = boundary
		}
	}
	if bestBoundary != "" {
		// A sticky/protected boundary claims only the first path segment under
		// that boundary. For example, under wp-content, a theme file claims
		// wp-content/themes rather than all of wp-content.
		remainder := strings.TrimPrefix(publicPath, bestBoundary+"/")
		nextSegment, _, _ := strings.Cut(remainder, "/")
		return bestBoundary + "/" + nextSegment
	}
	// Outside a boundary, the top-level path segment is the public claim. This
	// keeps normal deploys compact while still allowing nested release content.
	first, _, _ := strings.Cut(publicPath, "/")
	return first
}

func sharedContainerFor(publicPath string) (string, bool) {
	for _, shared := range sharedContainerPaths {
		if publicPath == shared || strings.HasPrefix(publicPath, shared+"/") {
			return shared, true
		}
	}
	return "", false
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
