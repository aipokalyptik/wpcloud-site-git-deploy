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
			if rejectSharedPaths && publicPath == sharedPath {
				return fmt.Errorf("shared path cannot be deployed: %s", sharedPath)
			}
			if rejectSharedPaths && entryType&os.ModeSymlink != 0 {
				return fmt.Errorf("shared container symlink cannot be deployed: %s", publicPath)
			}
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
		boundary = strings.Trim(boundary, "/")
		if boundary == "" {
			continue
		}
		if strings.HasPrefix(publicPath, boundary+"/") && len(boundary) > len(bestBoundary) {
			bestBoundary = boundary
		}
	}
	if bestBoundary != "" {
		remainder := strings.TrimPrefix(publicPath, bestBoundary+"/")
		nextSegment, _, _ := strings.Cut(remainder, "/")
		return bestBoundary + "/" + nextSegment
	}
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
