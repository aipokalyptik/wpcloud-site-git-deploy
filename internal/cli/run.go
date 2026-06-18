package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/auth"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/config"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/doctor"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/engine"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/releases"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/state"
)

type configKeySpec struct {
	set   func(*config.Deployment, string) error
	unset func(*config.Deployment) error
}

var Version = "dev"

// Each config key is registered once with its setter and optional unsetter. The
// parser, config command, and validation rules share this table to avoid drift.
var configKeySpecs = map[string]configKeySpec{
	"repo_url": {
		set: func(deployment *config.Deployment, value string) error {
			deployment.RepoURL = value
			return nil
		},
	},
	"docroot": {
		set: func(deployment *config.Deployment, value string) error {
			deployment.Docroot = value
			return nil
		},
	},
	"deployment_id": {
		set: func(deployment *config.Deployment, value string) error {
			if !config.ValidDeploymentName(value) {
				return fmt.Errorf("invalid deployment id: %q", value)
			}
			deployment.DeploymentID = value
			return nil
		},
	},
	"default_ref": {
		set: func(deployment *config.Deployment, value string) error {
			deployment.DefaultRef = value
			return nil
		},
	},
	"deploy_root": {
		set: func(deployment *config.Deployment, value string) error {
			deployment.DeployRoot = value
			return nil
		},
		unset: func(deployment *config.Deployment) error {
			deployment.DeployRoot = ""
			return nil
		},
	},
	"keep_releases": {
		set: func(deployment *config.Deployment, value string) error {
			keep, err := strconv.Atoi(value)
			if err != nil {
				return err
			}
			deployment.KeepReleases = keep
			return nil
		},
	},
	"post_deploy": {
		set: func(deployment *config.Deployment, value string) error {
			deployment.PostDeploy = value
			return nil
		},
		unset: func(deployment *config.Deployment) error {
			deployment.PostDeploy = ""
			return nil
		},
	},
	"maintenance_file": {
		set: func(deployment *config.Deployment, value string) error {
			deployment.Maintenance = config.Maintenance{Enabled: true, File: value}
			return nil
		},
		unset: func(deployment *config.Deployment) error {
			deployment.Maintenance = config.Maintenance{Enabled: false}
			return nil
		},
	},
	"maintenance": {
		set: func(deployment *config.Deployment, value string) error {
			enabled, err := strconv.ParseBool(value)
			if err != nil {
				return err
			}
			deployment.Maintenance.Enabled = enabled
			if enabled && deployment.Maintenance.File == "" {
				deployment.Maintenance.File = ".maintenance"
			}
			return nil
		},
	},
	"ssh_key_path": {
		set: func(deployment *config.Deployment, value string) error {
			deployment.SSHKeyPath = value
			return nil
		},
		unset: func(deployment *config.Deployment) error {
			deployment.SSHKeyPath = ""
			return nil
		},
	},
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	cmd, err := Parse(args)
	if err != nil {
		return err
	}
	layout, err := defaultLayout()
	if err != nil {
		return err
	}
	switch cmd.Verb {
	case "--help", "-h", "help":
		return runHelp(stdout)
	case "--version", "version":
		return runVersion(stdout)
	case "init":
		return runInit(layout, cmd, stdout)
	case "list":
		return runList(layout, stdout)
	case "status":
		return runStatus(layout, cmd, stdout)
	case "config":
		return runConfig(layout, cmd, stdout)
	case "deploy":
		return runDeploy(ctx, layout, cmd, stdout)
	case "rollback":
		return runRollback(ctx, layout, cmd, stdout)
	case "releases":
		return runReleases(layout, cmd, stdout)
	case "branches", "tags", "commits":
		return runInspection(ctx, layout, cmd, stdout)
	case "auth":
		return runAuth(ctx, layout, cmd, stdout)
	case "doctor":
		return runDoctor(ctx, layout, cmd, stdout)
	case "destroy":
		return runDestroy(layout, cmd, stdout)
	default:
		_ = stderr
		return fmt.Errorf("%s is not implemented yet", cmd.Verb)
	}
}

func runHelp(stdout io.Writer) error {
	_, _ = fmt.Fprint(stdout, `wpcloud-site-git-deploy

Usage:
  wpcloud-site-git-deploy init --name NAME --repo URL --docroot PATH --deployment-id ID --default-ref REF [options]
  wpcloud-site-git-deploy deploy --name NAME [--branch REF | --tag TAG | --commit SHA] [--force] [options]
  wpcloud-site-git-deploy rollback --name NAME [--to RELEASE_ID]
  wpcloud-site-git-deploy releases --name NAME
  wpcloud-site-git-deploy branches --name NAME [--fetch] [--limit N]
  wpcloud-site-git-deploy tags --name NAME [--fetch] [--limit N]
  wpcloud-site-git-deploy commits --name NAME [--fetch] [--limit N]
  wpcloud-site-git-deploy status --name NAME
  wpcloud-site-git-deploy config --name NAME --set KEY=VALUE
  wpcloud-site-git-deploy config --name NAME --unset KEY
  wpcloud-site-git-deploy auth --name NAME [--use-key PATH | --import-key PATH | --remove] [options]
  wpcloud-site-git-deploy doctor --name NAME [--offline] [--print-claims] [--assert-public-symlinks]
  wpcloud-site-git-deploy list
  wpcloud-site-git-deploy destroy --name NAME --confirm-destroy=NAME
  wpcloud-site-git-deploy --help
  wpcloud-site-git-deploy --version

Common options:
  --maintenance-file PATH
  --no-maintenance-file

`)
	return nil
}

func runVersion(stdout io.Writer) error {
	_, _ = fmt.Fprintln(stdout, Version)
	return nil
}

func runInit(layout state.Layout, cmd Command, stdout io.Writer) error {
	deployment := config.Deployment{
		SchemaVersion: config.SchemaVersion,
		Name:          cmd.Name,
		RepoURL:       cmd.RepoURL,
		Docroot:       cmd.Docroot,
		DeploymentID:  cmd.DeploymentID,
		DefaultRef:    cmd.DefaultRef,
		DeployRoot:    cmd.DeployRoot,
		KeepReleases:  cmd.KeepReleases,
		PostDeploy:    cmd.PostDeploy,
		Maintenance: config.Maintenance{
			Enabled: !cmd.NoMaintenanceFile,
			File:    ".maintenance",
		},
	}
	if cmd.MaintenanceFile != "" {
		deployment.Maintenance = config.Maintenance{Enabled: true, File: cmd.MaintenanceFile}
	}
	if err := config.Save(layout.DeploymentConfig(cmd.Name), deployment); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "initialized %s\n", cmd.Name)
	return nil
}

func runList(layout state.Layout, stdout io.Writer) error {
	names, err := listDeployments(layout)
	if err != nil {
		return err
	}
	for _, name := range names {
		_, _ = fmt.Fprintln(stdout, name)
	}
	return nil
}

func runStatus(layout state.Layout, cmd Command, stdout io.Writer) error {
	deployment, err := config.Load(layout.DeploymentConfig(cmd.Name))
	if err != nil {
		return err
	}
	printStatus(stdout, deployment)
	return nil
}

func runConfig(layout state.Layout, cmd Command, stdout io.Writer) error {
	deployment, err := config.Load(layout.DeploymentConfig(cmd.Name))
	if err != nil {
		return err
	}
	if err := applyConfigChanges(&deployment, cmd.Set, cmd.Unset); err != nil {
		return err
	}
	if err := config.Save(layout.DeploymentConfig(cmd.Name), deployment); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "configured %s\n", cmd.Name)
	return nil
}

func runDeploy(ctx context.Context, layout state.Layout, cmd Command, stdout io.Writer) error {
	deployment, err := config.Load(layout.DeploymentConfig(cmd.Name))
	if err != nil {
		return err
	}
	result, err := engine.Deploy(ctx, engine.DeployOptions{
		StateRoot:           layout.Root,
		Config:              deployment,
		RefMode:             cmd.RefMode,
		RefValue:            cmd.RefValue,
		Force:               cmd.Force,
		PostDeployOverride:  cmd.PostDeploy,
		MaintenanceOverride: maintenanceOverride(cmd),
	})
	if err != nil {
		return err
	}
	if result.NoOp {
		_, _ = fmt.Fprintf(stdout, "no_op=true release_id=%s commit=%s\n", result.ReleaseID, result.Commit)
	} else {
		_, _ = fmt.Fprintf(stdout, "release_id=%s commit=%s\n", result.ReleaseID, result.Commit)
	}
	return nil
}

func runRollback(ctx context.Context, layout state.Layout, cmd Command, stdout io.Writer) error {
	deployment, err := config.Load(layout.DeploymentConfig(cmd.Name))
	if err != nil {
		return err
	}
	to := cmd.To
	if to == "" {
		selected, err := selectRollbackTarget(deployment)
		if err != nil {
			return err
		}
		to = selected
	}
	if err := engine.Rollback(engine.RollbackOptions{Context: ctx, Config: deployment, ReleaseID: to}); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "rolled_back=%s\n", to)
	return nil
}

func runReleases(layout state.Layout, cmd Command, stdout io.Writer) error {
	deployment, err := config.Load(layout.DeploymentConfig(cmd.Name))
	if err != nil {
		return err
	}
	docrootLayout := state.NewDocroot(deployment.Docroot, deployment.DeploymentID)
	current, _ := docrootLayout.CurrentReleaseID()
	entries, err := releases.List(filepath.Join(docrootLayout.Base(), "releases"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		marker := ""
		if entry.Name == current {
			marker = " current"
		}
		_, _ = fmt.Fprintf(stdout, "%s%s\n", entry.Name, marker)
	}
	return nil
}

func runInspection(ctx context.Context, layout state.Layout, cmd Command, stdout io.Writer) error {
	deployment, err := config.Load(layout.DeploymentConfig(cmd.Name))
	if err != nil {
		return err
	}
	repoDir := layout.Repo(cmd.Name)
	if err := engine.EnsureRepo(ctx, repoDir, deployment, cmd.Fetch); err != nil {
		return err
	}
	var lines []string
	switch cmd.Verb {
	case "branches":
		lines, err = engine.Branches(ctx, repoDir, cmd.Limit)
	case "tags":
		lines, err = engine.Tags(ctx, repoDir, cmd.Limit)
	case "commits":
		lines, err = engine.Commits(ctx, repoDir, cmd.Limit)
	}
	if err != nil {
		return err
	}
	for _, line := range lines {
		_, _ = fmt.Fprintln(stdout, line)
	}
	return nil
}

func runAuth(ctx context.Context, layout state.Layout, cmd Command, stdout io.Writer) error {
	deployment, err := config.Load(layout.DeploymentConfig(cmd.Name))
	if err != nil {
		return err
	}
	if converted, ok := auth.HTTPSURLToSSH(deployment.RepoURL); ok {
		deployment.RepoURL = converted
	}
	switch {
	case cmd.Remove:
		if cmd.Verify {
			return errors.New("--remove cannot be combined with --verify")
		}
		managed := deployment.SSHKeyPath == layout.Key(cmd.Name)
		deployment.SSHKeyPath = ""
		if cmd.PurgeKey && managed {
			// Purge deletes only tool-managed keys. External --use-key files are
			// never removed by this CLI.
			_ = os.Remove(layout.Key(cmd.Name))
			_ = os.Remove(layout.Key(cmd.Name) + ".pub")
		}
	case cmd.UseKey != "":
		if err := auth.ValidatePrivateKeyPath(ctx, cmd.UseKey); err != nil {
			return err
		}
		deployment.SSHKeyPath = cmd.UseKey
	case cmd.ImportKey != "":
		keyPath, err := auth.ImportPrivateKey(ctx, layout, cmd.Name, cmd.ImportKey, cmd.ForceNewKey)
		if err != nil {
			return err
		}
		deployment.SSHKeyPath = keyPath
	default:
		keyPath, err := auth.GenerateOrReuseKey(ctx, layout, cmd.Name, cmd.ForceNewKey)
		if err != nil {
			return err
		}
		deployment.SSHKeyPath = keyPath
	}
	if cmd.Verify {
		if err := auth.VerifyRemoteAccess(ctx, deployment); err != nil {
			return err
		}
	}
	if err := config.Save(layout.DeploymentConfig(cmd.Name), deployment); err != nil {
		return err
	}
	if deployment.SSHKeyPath != "" {
		publicKey, err := auth.PublicKeyLine(ctx, deployment.SSHKeyPath)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stdout, "%s\n", publicKey)
	}
	_, _ = fmt.Fprintf(stdout, "configured auth for %s\n", cmd.Name)
	return nil
}

func runDoctor(ctx context.Context, layout state.Layout, cmd Command, stdout io.Writer) error {
	deployment, err := config.Load(layout.DeploymentConfig(cmd.Name))
	if err != nil {
		report := doctor.NewReport()
		report.Fail("config", err.Error())
		printDoctorReport(stdout, report)
		return errors.New("doctor found failures")
	}
	result := doctor.Run(ctx, deployment, doctor.Options{
		Offline:              cmd.Offline,
		PrintClaims:          cmd.PrintClaims,
		AssertPublicSymlinks: cmd.AssertPublicSymlinks,
		Home:                 os.Getenv("HOME"),
	})
	for _, claim := range result.Claims {
		_, _ = fmt.Fprintln(stdout, claim)
	}
	printDoctorReport(stdout, result.Report)
	if !result.Report.Success() {
		return errors.New("doctor found failures")
	}
	return nil
}

func runDestroy(layout state.Layout, cmd Command, stdout io.Writer) error {
	if err := os.RemoveAll(layout.DeploymentDir(cmd.Name)); err != nil {
		return err
	}
	if err := os.RemoveAll(layout.Repo(cmd.Name)); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(layout.Root, "tmp", cmd.Name)); err != nil {
		return err
	}
	if err := os.Remove(layout.Key(cmd.Name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(layout.Key(cmd.Name) + ".pub"); err != nil && !os.IsNotExist(err) {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "destroyed %s\n", cmd.Name)
	return nil
}

func defaultLayout() (state.Layout, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return state.Layout{}, err
	}
	return state.New(filepath.Join(home, ".wpcloud-site-git-deploy")), nil
}

func listDeployments(layout state.Layout) ([]string, error) {
	root := filepath.Join(layout.Root, "deployments")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func printStatus(stdout io.Writer, deployment config.Deployment) {
	_, _ = fmt.Fprintf(stdout, "name=%s\n", deployment.Name)
	_, _ = fmt.Fprintf(stdout, "repo_url=%s\n", deployment.RepoURL)
	_, _ = fmt.Fprintf(stdout, "docroot=%s\n", deployment.Docroot)
	_, _ = fmt.Fprintf(stdout, "deployment_id=%s\n", deployment.DeploymentID)
	_, _ = fmt.Fprintf(stdout, "default_ref=%s\n", deployment.DefaultRef)
	_, _ = fmt.Fprintf(stdout, "deploy_root=%s\n", deployment.DeployRoot)
	_, _ = fmt.Fprintf(stdout, "keep_releases=%d\n", deployment.KeepReleases)
	_, _ = fmt.Fprintf(stdout, "post_deploy=%s\n", deployment.PostDeploy)
	if deployment.Maintenance.Enabled {
		_, _ = fmt.Fprintf(stdout, "maintenance_file=%s\n", deployment.Maintenance.File)
	} else {
		_, _ = fmt.Fprintln(stdout, "maintenance_file=")
	}
	_, _ = fmt.Fprintf(stdout, "ssh_key_path=%s\n", deployment.SSHKeyPath)
}

func applyConfigChanges(deployment *config.Deployment, setValues, unsetValues []string) error {
	for _, item := range setValues {
		key, value, _ := strings.Cut(item, "=")
		if err := setConfigValue(deployment, key, value); err != nil {
			return err
		}
	}
	for _, key := range unsetValues {
		if err := unsetConfigValue(deployment, key); err != nil {
			return err
		}
	}
	return nil
}

func setConfigValue(deployment *config.Deployment, key, value string) error {
	spec, ok := configKeySpecs[key]
	if !ok {
		return fmt.Errorf("unsupported config key: %s", key)
	}
	return spec.set(deployment, value)
}

func unsetConfigValue(deployment *config.Deployment, key string) error {
	spec, ok := configKeySpecs[key]
	if !ok {
		return fmt.Errorf("unsupported config key: %s", key)
	}
	if spec.unset == nil {
		return fmt.Errorf("config key cannot be unset: %s", key)
	}
	return spec.unset(deployment)
}

func maintenanceOverride(cmd Command) *config.Maintenance {
	if cmd.NoMaintenanceFile {
		return &config.Maintenance{Enabled: false}
	}
	if cmd.MaintenanceFile != "" {
		return &config.Maintenance{Enabled: true, File: cmd.MaintenanceFile}
	}
	return nil
}

func printDoctorReport(stdout io.Writer, report *doctor.Report) {
	for _, check := range report.Checks {
		_, _ = fmt.Fprintf(stdout, "%s %s %s\n", check.Status, check.Name, check.Message)
	}
}

func selectRollbackTarget(deployment config.Deployment) (string, error) {
	layout := state.NewDocroot(deployment.Docroot, deployment.DeploymentID)
	current, _ := layout.CurrentReleaseID()
	entries, err := releases.List(filepath.Join(layout.Base(), "releases"))
	if err != nil {
		return "", err
	}
	var metadataCandidates []releases.Entry
	var fallbackCandidates []releases.Entry
	for _, entry := range entries {
		if entry.Name == current {
			continue
		}
		if _, err := releases.LoadMetadata(layout.ReleaseMetadata(entry.Name)); err == nil {
			metadataCandidates = append(metadataCandidates, entry)
		} else {
			fallbackCandidates = append(fallbackCandidates, entry)
		}
	}
	// Default rollback should prefer releases with metadata so a failed partial
	// deploy directory cannot be selected while any known-good release remains.
	candidates := metadataCandidates
	if len(candidates) == 0 {
		candidates = fallbackCandidates
	}
	if len(candidates) == 0 {
		return "", errors.New("no rollback target available")
	}
	return candidates[0].Name, nil
}
