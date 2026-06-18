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

type Entry struct {
	Name    string
	ModTime time.Time
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

func List(releasesDir string) ([]Entry, error) {
	entries, err := os.ReadDir(releasesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var releases []Entry
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		releases = append(releases, Entry{Name: entry.Name(), ModTime: info.ModTime()})
	}
	sort.Slice(releases, func(i, j int) bool {
		if releases[i].ModTime.Equal(releases[j].ModTime) {
			return strings.Compare(releases[i].Name, releases[j].Name) > 0
		}
		return releases[i].ModTime.After(releases[j].ModTime)
	})
	return releases, nil
}

func Prune(releasesDir string, keepReleases int, activeRelease string) ([]string, error) {
	releases, err := List(releasesDir)
	if err != nil {
		return nil, err
	}
	keep := map[string]struct{}{activeRelease: {}}
	for _, release := range releases {
		if len(keep) >= keepReleases {
			break
		}
		keep[release.Name] = struct{}{}
	}
	var removed []string
	for _, release := range releases {
		if _, ok := keep[release.Name]; ok {
			continue
		}
		if err := os.RemoveAll(filepath.Join(releasesDir, release.Name)); err != nil {
			return nil, err
		}
		removed = append(removed, release.Name)
	}
	sort.Strings(removed)
	return removed, nil
}
