package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateDeploymentConfig(t *testing.T) {
	cfg := Deployment{
		SchemaVersion: 1,
		Name:          "site",
		RepoURL:       "git@example.com:team/site.git",
		Docroot:       "/srv/htdocs",
		DeploymentID:  "site",
		DefaultRef:    "main",
		KeepReleases:  3,
		Maintenance:   Maintenance{Enabled: true, File: ".maintenance"},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid config should pass: %v", err)
	}
}

func TestValidateDeploymentConfigRejectsBadName(t *testing.T) {
	cfg := Deployment{SchemaVersion: 1, Name: "../bad"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected bad name to fail")
	}
}

func TestSaveLoadDeploymentConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := Deployment{
		SchemaVersion: 1,
		Name:          "site",
		RepoURL:       "git@example.com:team/site.git",
		Docroot:       "/srv/htdocs",
		DeploymentID:  "site",
		DefaultRef:    "main",
		KeepReleases:  3,
		Maintenance:   Maintenance{Enabled: true, File: ".maintenance"},
	}

	if err := Save(path, cfg); err != nil {
		t.Fatalf("save failed: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if !json.Valid(raw) {
		t.Fatalf("saved config is not valid json: %s", raw)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if loaded.Name != cfg.Name || loaded.RepoURL != cfg.RepoURL || loaded.Maintenance.File != ".maintenance" {
		t.Fatalf("loaded config mismatch: %#v", loaded)
	}
}
