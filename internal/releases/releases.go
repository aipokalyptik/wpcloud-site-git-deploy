package releases

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Metadata struct {
	ReleaseID  string    `json:"release_id"`
	RefMode    string    `json:"ref_mode"`
	RefValue   string    `json:"ref_value"`
	Commit     string    `json:"commit"`
	DeployRoot string    `json:"deploy_root,omitempty"`
	DeployedAt time.Time `json:"deployed_at"`
}

func NewID(now time.Time, commit string) (string, error) {
	shortCommit := commit
	if len(shortCommit) > 12 {
		shortCommit = shortCommit[:12]
	}
	random := make([]byte, 2)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return now.UTC().Format("20060102150405") + "-" + shortCommit + "-" + hex.EncodeToString(random), nil
}

func CurrentMatches(metadata Metadata, commit, deployRoot string) bool {
	return metadata.Commit == commit && metadata.DeployRoot == deployRoot
}

func SaveMetadata(path string, metadata Metadata) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".metadata.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func LoadMetadata(path string) (Metadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Metadata{}, err
	}
	var metadata Metadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return Metadata{}, err
	}
	return metadata, nil
}

func Prune(releasesDir string, keepReleases int, activeRelease string) ([]string, error) {
	entries, err := os.ReadDir(releasesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	type releaseEntry struct {
		name    string
		modTime time.Time
	}
	var releases []releaseEntry
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		releases = append(releases, releaseEntry{name: entry.Name(), modTime: info.ModTime()})
	}
	sort.Slice(releases, func(i, j int) bool {
		if releases[i].modTime.Equal(releases[j].modTime) {
			return strings.Compare(releases[i].name, releases[j].name) > 0
		}
		return releases[i].modTime.After(releases[j].modTime)
	})
	keep := map[string]struct{}{activeRelease: {}}
	for _, release := range releases {
		if len(keep) >= keepReleases {
			break
		}
		keep[release.name] = struct{}{}
	}
	var removed []string
	for _, release := range releases {
		if _, ok := keep[release.name]; ok {
			continue
		}
		if err := os.RemoveAll(filepath.Join(releasesDir, release.name)); err != nil {
			return nil, err
		}
		removed = append(removed, release.name)
	}
	sort.Strings(removed)
	return removed, nil
}
