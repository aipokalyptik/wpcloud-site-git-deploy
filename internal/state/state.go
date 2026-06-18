package state

import (
	"os"
	"path/filepath"
)

const DocrootNamespace = ".wpcloud-site-git-deploy"

type Layout struct {
	Root string
}

func New(root string) Layout {
	return Layout{Root: root}
}

func (l Layout) DeploymentDir(name string) string {
	return filepath.Join(l.Root, "deployments", name)
}

func (l Layout) DeploymentConfig(name string) string {
	return filepath.Join(l.DeploymentDir(name), "config.json")
}

func (l Layout) Repo(name string) string {
	return filepath.Join(l.Root, "repos", name)
}

func (l Layout) Worktree(name, releaseID string) string {
	return filepath.Join(l.Root, "tmp", name, releaseID)
}

func (l Layout) Key(name string) string {
	return filepath.Join(l.Root, "keys", name+"_ed25519")
}

type DocrootLayout struct {
	Docroot      string
	DeploymentID string
}

func NewDocroot(docroot, deploymentID string) DocrootLayout {
	return DocrootLayout{Docroot: docroot, DeploymentID: deploymentID}
}

func (l DocrootLayout) Base() string {
	return filepath.Join(l.Docroot, DocrootNamespace, "deployments", l.DeploymentID)
}

func (l DocrootLayout) Current() string {
	return filepath.Join(l.Base(), "current")
}

func (l DocrootLayout) CurrentReleaseID() (string, bool) {
	target, err := os.Readlink(l.Current())
	if err != nil {
		return "", false
	}
	return filepath.Base(target), true
}

func (l DocrootLayout) Incoming(releaseID string) string {
	return filepath.Join(l.Base(), "incoming", releaseID)
}

func (l DocrootLayout) Release(releaseID string) string {
	return filepath.Join(l.Base(), "releases", releaseID)
}

func (l DocrootLayout) ReleaseMetadata(releaseID string) string {
	return filepath.Join(l.Base(), "metadata", releaseID+".json")
}

func (l DocrootLayout) Lock() string {
	return filepath.Join(l.Base(), "deploy.lock")
}

func (l DocrootLayout) ExchangedPaths() string {
	return filepath.Join(l.Base(), "exchanged_paths")
}
