package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const SchemaVersion = 1

var deploymentNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type Deployment struct {
	SchemaVersion int         `json:"schema_version"`
	Name          string      `json:"name"`
	RepoURL       string      `json:"repo_url"`
	Docroot       string      `json:"docroot"`
	DeploymentID  string      `json:"deployment_id"`
	DefaultRef    string      `json:"default_ref"`
	DeployRoot    string      `json:"deploy_root,omitempty"`
	KeepReleases  int         `json:"keep_releases"`
	PostDeploy    string      `json:"post_deploy,omitempty"`
	Maintenance   Maintenance `json:"maintenance"`
	SSHKeyPath    string      `json:"ssh_key_path,omitempty"`
}

type Maintenance struct {
	Enabled bool   `json:"enabled"`
	File    string `json:"file,omitempty"`
}

func ValidDeploymentName(name string) bool {
	return deploymentNamePattern.MatchString(name)
}

func (d Deployment) Validate() error {
	if d.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported config schema version: %d", d.SchemaVersion)
	}
	if !ValidDeploymentName(d.Name) {
		return fmt.Errorf("invalid deployment name: %q", d.Name)
	}
	if d.RepoURL == "" {
		return errors.New("repo_url is required")
	}
	if d.Docroot == "" {
		return errors.New("docroot is required")
	}
	if !ValidDeploymentName(d.DeploymentID) {
		return fmt.Errorf("invalid deployment id: %q", d.DeploymentID)
	}
	if d.DefaultRef == "" {
		return errors.New("default_ref is required")
	}
	if d.KeepReleases < 1 {
		return errors.New("keep_releases must be at least 1")
	}
	if d.DeployRoot != "" {
		if err := ValidateRelativePath("deploy_root", d.DeployRoot); err != nil {
			return err
		}
	}
	if d.PostDeploy != "" && strings.Contains(d.PostDeploy, "\n") {
		return errors.New("post_deploy must not contain newlines")
	}
	if d.Maintenance.Enabled && d.Maintenance.File == "" {
		return errors.New("maintenance file is required when maintenance is enabled")
	}
	if d.Maintenance.Enabled {
		if err := ValidateRelativePath("maintenance_file", d.Maintenance.File); err != nil {
			return err
		}
	}
	return nil
}

func ValidateRelativePath(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s must not be empty", name)
	}
	if filepath.IsAbs(value) {
		return fmt.Errorf("%s must be relative", name)
	}
	if strings.Contains(value, "\n") {
		return fmt.Errorf("%s must not contain newlines", name)
	}
	cleaned := filepath.Clean(filepath.FromSlash(value))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("%s must stay under the docroot", name)
	}
	return nil
}

func Save(path string, deployment Deployment) error {
	if err := deployment.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(deployment, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config.json.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func Load(path string) (Deployment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Deployment{}, err
	}
	var deployment Deployment
	if err := json.Unmarshal(data, &deployment); err != nil {
		return Deployment{}, err
	}
	if err := deployment.Validate(); err != nil {
		return Deployment{}, err
	}
	return deployment, nil
}
