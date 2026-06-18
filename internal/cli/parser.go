package cli

import (
	"context"
	"errors"
	"flag"
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
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/execx"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/releases"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/state"
)

type Command struct {
	Verb                 string
	Name                 string
	RepoURL              string
	Docroot              string
	DeploymentID         string
	DefaultRef           string
	DeployRoot           string
	KeepReleases         int
	Branch               string
	Tag                  string
	Commit               string
	RefMode              string
	RefValue             string
	Force                bool
	To                   string
	Fetch                bool
	Limit                int
	Offline              bool
	PrintClaims          bool
	AssertPublicSymlinks bool
	Set                  []string
	Unset                []string
	MaintenanceFile      string
	NoMaintenanceFile    bool
	PostDeploy           string
	ConfirmDestroy       string
	UseKey               string
	ImportKey            string
	ForceNewKey          bool
	Verify               bool
	Remove               bool
	PurgeKey             bool
}

var deploymentScopedVerbs = map[string]bool{
	"init":     true,
	"config":   true,
	"deploy":   true,
	"rollback": true,
	"releases": true,
	"branches": true,
	"tags":     true,
	"commits":  true,
	"status":   true,
	"auth":     true,
	"doctor":   true,
	"destroy":  true,
}

var supportedConfigKeys = map[string]bool{
	"repo_url":         true,
	"docroot":          true,
	"deployment_id":    true,
	"default_ref":      true,
	"deploy_root":      true,
	"keep_releases":    true,
	"post_deploy":      true,
	"maintenance_file": true,
	"maintenance":      true,
	"ssh_key_path":     true,
}

type repeatedStrings []string

func (r *repeatedStrings) String() string {
	return strings.Join(*r, ",")
}

func (r *repeatedStrings) Set(value string) error {
	*r = append(*r, value)
	return nil
}

func Parse(args []string) (Command, error) {
	if len(args) == 0 {
		return Command{}, errors.New("command is required")
	}
	cmd := Command{Verb: args[0], KeepReleases: 3, Limit: 20}
	if cmd.Verb == "--help" || cmd.Verb == "-h" || cmd.Verb == "help" {
		return cmd, nil
	}
	if cmd.Verb == "--version" || cmd.Verb == "version" {
		return cmd, nil
	}
	if !knownVerb(cmd.Verb) {
		return Command{}, fmt.Errorf("unknown command: %s", cmd.Verb)
	}

	fs := flag.NewFlagSet(cmd.Verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	addCommonFlags(fs, &cmd)
	switch cmd.Verb {
	case "init":
		fs.StringVar(&cmd.RepoURL, "repo", "", "repository URL")
		fs.StringVar(&cmd.Docroot, "docroot", "", "docroot")
		fs.StringVar(&cmd.DeploymentID, "deployment-id", "", "deployment id")
		fs.StringVar(&cmd.DefaultRef, "default-ref", "", "default ref")
		fs.StringVar(&cmd.DeployRoot, "deploy-root", "", "deploy root")
		fs.IntVar(&cmd.KeepReleases, "keep-releases", 3, "kept releases")
		fs.StringVar(&cmd.PostDeploy, "post-deploy", "", "post deploy")
	case "config":
		fs.Var((*repeatedStrings)(&cmd.Set), "set", "set key=value")
		fs.Var((*repeatedStrings)(&cmd.Unset), "unset", "unset key")
	case "deploy":
		fs.StringVar(&cmd.Branch, "branch", "", "branch")
		fs.StringVar(&cmd.Tag, "tag", "", "tag")
		fs.StringVar(&cmd.Commit, "commit", "", "commit")
		fs.BoolVar(&cmd.Force, "force", false, "force deploy")
		fs.StringVar(&cmd.PostDeploy, "post-deploy", "", "post deploy")
	case "rollback":
		fs.StringVar(&cmd.To, "to", "", "release id")
	case "branches", "tags", "commits":
		fs.BoolVar(&cmd.Fetch, "fetch", false, "fetch first")
		fs.IntVar(&cmd.Limit, "limit", 20, "limit")
	case "auth":
		fs.StringVar(&cmd.UseKey, "use-key", "", "external private key")
		fs.StringVar(&cmd.ImportKey, "import-key", "", "import private key")
		fs.BoolVar(&cmd.ForceNewKey, "force-new-key", false, "force new key")
		fs.BoolVar(&cmd.Verify, "verify", false, "verify access")
		fs.BoolVar(&cmd.Remove, "remove", false, "remove auth")
		fs.BoolVar(&cmd.PurgeKey, "purge-key", false, "purge managed key")
	case "doctor":
		fs.BoolVar(&cmd.Offline, "offline", false, "offline")
		fs.BoolVar(&cmd.PrintClaims, "print-claims", false, "print claims")
		fs.BoolVar(&cmd.AssertPublicSymlinks, "assert-public-symlinks", false, "assert symlinks")
	case "destroy":
		fs.StringVar(&cmd.ConfirmDestroy, "confirm-destroy", "", "confirmation")
	}

	if err := fs.Parse(args[1:]); err != nil {
		return Command{}, err
	}
	if fs.NArg() != 0 {
		return Command{}, fmt.Errorf("unexpected argument: %s", fs.Arg(0))
	}
	if err := validateCommand(&cmd); err != nil {
		return Command{}, err
	}
	return cmd, nil
}

func addCommonFlags(fs *flag.FlagSet, cmd *Command) {
	fs.StringVar(&cmd.Name, "name", "", "deployment name")
	fs.StringVar(&cmd.MaintenanceFile, "maintenance-file", "", "maintenance file")
	fs.BoolVar(&cmd.NoMaintenanceFile, "no-maintenance-file", false, "disable maintenance file")
}

func knownVerb(verb string) bool {
	if deploymentScopedVerbs[verb] {
		return true
	}
	return verb == "list"
}

func validateCommand(cmd *Command) error {
	if deploymentScopedVerbs[cmd.Verb] {
		if cmd.Name == "" {
			return errors.New("--name is required")
		}
		if !config.ValidDeploymentName(cmd.Name) {
			return fmt.Errorf("invalid deployment name: %q", cmd.Name)
		}
	}
	if cmd.MaintenanceFile != "" && cmd.NoMaintenanceFile {
		return errors.New("choose --maintenance-file or --no-maintenance-file")
	}
	switch cmd.Verb {
	case "init":
		if cmd.RepoURL == "" {
			return errors.New("--repo is required")
		}
		if cmd.Docroot == "" {
			return errors.New("--docroot is required")
		}
		if cmd.DeploymentID == "" {
			return errors.New("--deployment-id is required")
		}
		if !config.ValidDeploymentName(cmd.DeploymentID) {
			return fmt.Errorf("invalid deployment id: %q", cmd.DeploymentID)
		}
		if cmd.DefaultRef == "" {
			return errors.New("--default-ref is required")
		}
		if cmd.KeepReleases < 1 {
			return errors.New("--keep-releases must be at least 1")
		}
	case "config":
		if len(cmd.Set) == 0 && len(cmd.Unset) == 0 {
			return errors.New("config requires --set or --unset")
		}
		for _, item := range cmd.Set {
			key, _, ok := strings.Cut(item, "=")
			if !ok || key == "" {
				return fmt.Errorf("--set requires key=value: %s", item)
			}
			if !supportedConfigKeys[key] {
				return fmt.Errorf("unsupported config key: %s", key)
			}
		}
		for _, key := range cmd.Unset {
			if !supportedConfigKeys[key] {
				return fmt.Errorf("unsupported config key: %s", key)
			}
		}
	case "deploy":
		count := 0
		for _, value := range []string{cmd.Branch, cmd.Tag, cmd.Commit} {
			if value != "" {
				count++
			}
		}
		if count > 1 {
			return errors.New("choose only one ref")
		}
		switch {
		case cmd.Branch != "":
			cmd.RefMode, cmd.RefValue = "branch", cmd.Branch
		case cmd.Tag != "":
			cmd.RefMode, cmd.RefValue = "tag", cmd.Tag
		case cmd.Commit != "":
			cmd.RefMode, cmd.RefValue = "commit", cmd.Commit
		}
	case "auth":
		if err := auth.ValidateKeySource(cmd.UseKey, cmd.ImportKey, cmd.Remove); err != nil {
			return err
		}
	case "destroy":
		if cmd.ConfirmDestroy == "" {
			return errors.New("--confirm-destroy is required")
		}
		if cmd.ConfirmDestroy != cmd.Name {
			return errors.New("--confirm-destroy must match --name")
		}
	}
	return nil
}

func Run(ctx context.Context, args []string, stdout, _ io.Writer) error {
	cmd, err := Parse(args)
	if err != nil {
		return err
	}
	layout, err := defaultLayout()
	if err != nil {
		return err
	}
	switch cmd.Verb {
	case "--help", "-h", "help", "":
		_, _ = fmt.Fprintln(stdout, "wpcloud-site-git-deploy")
	case "--version", "version":
		_, _ = fmt.Fprintln(stdout, "dev")
	case "init":
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
	case "list":
		names, err := listDeployments(layout)
		if err != nil {
			return err
		}
		for _, name := range names {
			_, _ = fmt.Fprintln(stdout, name)
		}
	case "status":
		deployment, err := config.Load(layout.DeploymentConfig(cmd.Name))
		if err != nil {
			return err
		}
		printStatus(stdout, deployment)
	case "config":
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
	case "deploy":
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
	case "rollback":
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
	case "releases":
		deployment, err := config.Load(layout.DeploymentConfig(cmd.Name))
		if err != nil {
			return err
		}
		docrootLayout := state.NewDocroot(deployment.Docroot, deployment.DeploymentID)
		current := ""
		if target, err := os.Readlink(docrootLayout.Current()); err == nil {
			current = filepath.Base(target)
		}
		entries, err := os.ReadDir(filepath.Join(docrootLayout.Base(), "releases"))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			marker := ""
			if entry.Name() == current {
				marker = " current"
			}
			_, _ = fmt.Fprintf(stdout, "%s%s\n", entry.Name(), marker)
		}
	case "branches", "tags", "commits":
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
	case "auth":
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
				_ = os.Remove(layout.Key(cmd.Name))
				_ = os.Remove(layout.Key(cmd.Name) + ".pub")
			}
		case cmd.UseKey != "":
			if err := validatePrivateKeyPath(ctx, cmd.UseKey); err != nil {
				return err
			}
			deployment.SSHKeyPath = cmd.UseKey
		case cmd.ImportKey != "":
			keyPath, err := importPrivateKey(ctx, layout, cmd.Name, cmd.ImportKey, cmd.ForceNewKey)
			if err != nil {
				return err
			}
			deployment.SSHKeyPath = keyPath
		default:
			keyPath, err := generateOrReuseKey(ctx, layout, cmd.Name, cmd.ForceNewKey)
			if err != nil {
				return err
			}
			deployment.SSHKeyPath = keyPath
		}
		if cmd.Verify {
			if err := verifyRemoteAccess(ctx, deployment); err != nil {
				return err
			}
		}
		if err := config.Save(layout.DeploymentConfig(cmd.Name), deployment); err != nil {
			return err
		}
		if deployment.SSHKeyPath != "" {
			publicKey, err := derivePublicKey(ctx, deployment.SSHKeyPath)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(stdout, "%s\n", strings.TrimSpace(publicKey))
		}
		_, _ = fmt.Fprintf(stdout, "configured auth for %s\n", cmd.Name)
	case "doctor":
		deployment, err := config.Load(layout.DeploymentConfig(cmd.Name))
		report := doctor.NewReport()
		if err != nil {
			report.Fail("config", err.Error())
		} else {
			report.OK("config", "loaded")
			if _, statErr := os.Stat(deployment.Docroot); statErr != nil {
				report.Fail("docroot", statErr.Error())
			} else {
				report.OK("docroot", "accessible")
			}
			for _, command := range []string{"git", "rsync", "ssh", "ssh-keygen"} {
				if err := execx.RequireCommands(ctx, []string{command}); err != nil {
					report.Fail(command, err.Error())
				} else {
					report.OK(command, "found")
				}
			}
			if deployment.SSHKeyPath == "" {
				report.Warn("ssh-key", "no configured SSH key; Git will use ambient SSH configuration")
			} else if err := validatePrivateKeyPath(ctx, deployment.SSHKeyPath); err != nil {
				report.Fail("ssh-key", err.Error())
			} else {
				report.OK("ssh-key", "usable")
			}
			if cmd.PrintClaims {
				claimList, err := engine.ClaimsForCurrent(deployment.Docroot, deployment.DeploymentID)
				if err != nil {
					report.Fail("claims", err.Error())
				} else {
					for _, claim := range claimList {
						_, _ = fmt.Fprintln(stdout, claim)
					}
					report.OK("claims", fmt.Sprintf("%d claims", len(claimList)))
				}
			}
			if cmd.AssertPublicSymlinks {
				if err := engine.AssertDeploymentSymlinks(deployment.Docroot, deployment.DeploymentID, os.Getenv("HOME")); err != nil {
					report.Fail("public-symlinks", err.Error())
				} else {
					report.OK("public-symlinks", "valid")
				}
			}
			if !cmd.Offline {
				if err := verifyRemoteAccess(ctx, deployment); err != nil {
					report.Fail("git-remote", err.Error())
				} else {
					report.OK("git-remote", "accessible")
				}
			}
		}
		for _, check := range report.Checks {
			_, _ = fmt.Fprintf(stdout, "%s %s %s\n", check.Status, check.Name, check.Message)
		}
		if !report.Success() {
			return errors.New("doctor found failures")
		}
	case "destroy":
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
	default:
		return fmt.Errorf("%s is not implemented yet", cmd.Verb)
	}
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
	switch key {
	case "repo_url":
		deployment.RepoURL = value
	case "docroot":
		deployment.Docroot = value
	case "deployment_id":
		if !config.ValidDeploymentName(value) {
			return fmt.Errorf("invalid deployment id: %q", value)
		}
		deployment.DeploymentID = value
	case "default_ref":
		deployment.DefaultRef = value
	case "deploy_root":
		deployment.DeployRoot = value
	case "keep_releases":
		keep, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		deployment.KeepReleases = keep
	case "post_deploy":
		deployment.PostDeploy = value
	case "maintenance_file":
		deployment.Maintenance = config.Maintenance{Enabled: true, File: value}
	case "maintenance":
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		deployment.Maintenance.Enabled = enabled
		if enabled && deployment.Maintenance.File == "" {
			deployment.Maintenance.File = ".maintenance"
		}
	case "ssh_key_path":
		deployment.SSHKeyPath = value
	default:
		return fmt.Errorf("unsupported config key: %s", key)
	}
	return nil
}

func unsetConfigValue(deployment *config.Deployment, key string) error {
	switch key {
	case "deploy_root":
		deployment.DeployRoot = ""
	case "post_deploy":
		deployment.PostDeploy = ""
	case "maintenance_file":
		deployment.Maintenance = config.Maintenance{Enabled: false}
	case "ssh_key_path":
		deployment.SSHKeyPath = ""
	default:
		return fmt.Errorf("config key cannot be unset: %s", key)
	}
	return nil
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

func validatePrivateKeyPath(ctx context.Context, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("private key is a directory: %s", path)
	}
	mode := info.Mode().Perm()
	if mode&0o077 != 0 {
		return fmt.Errorf("private key permissions are too open: %s", path)
	}
	_, err = derivePublicKey(ctx, path)
	return err
}

func generateOrReuseKey(ctx context.Context, layout state.Layout, name string, force bool) (string, error) {
	keyPath := layout.Key(name)
	if !force {
		if _, err := os.Stat(keyPath); err == nil {
			return keyPath, validatePrivateKeyPath(ctx, keyPath)
		}
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return "", err
	}
	if force {
		_ = os.Remove(keyPath)
		_ = os.Remove(keyPath + ".pub")
	}
	if _, err := execx.Run(ctx, execx.Command{
		Name: "ssh-keygen",
		Args: []string{"-t", "ed25519", "-N", "", "-f", keyPath},
	}); err != nil {
		return "", err
	}
	if err := os.Chmod(keyPath, 0o600); err != nil {
		return "", err
	}
	if err := derivePublicKeyFile(ctx, keyPath); err != nil {
		return "", err
	}
	return keyPath, nil
}

func importPrivateKey(ctx context.Context, layout state.Layout, name, sourcePath string, force bool) (string, error) {
	if err := validatePrivateKeyPath(ctx, sourcePath); err != nil {
		return "", err
	}
	keyPath := layout.Key(name)
	sourceReal, _ := filepath.EvalSymlinks(sourcePath)
	keyReal, _ := filepath.EvalSymlinks(keyPath)
	if sourceReal != "" && keyReal != "" && sourceReal == keyReal {
		return "", fmt.Errorf("--import-key source is already the managed key; use --use-key instead")
	}
	if _, err := os.Stat(keyPath); err == nil && !force {
		return "", fmt.Errorf("managed key already exists: %s", keyPath)
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(keyPath, data, 0o600); err != nil {
		return "", err
	}
	if err := derivePublicKeyFile(ctx, keyPath); err != nil {
		return "", err
	}
	return keyPath, nil
}

func derivePublicKey(ctx context.Context, keyPath string) (string, error) {
	if err := execx.RequireCommands(ctx, []string{"ssh-keygen"}); err != nil {
		return "", err
	}
	result, err := execx.Run(ctx, execx.Command{Name: "ssh-keygen", Args: []string{"-y", "-f", keyPath}})
	if err != nil {
		return "", fmt.Errorf("private key cannot be used without prompting or is not a valid private key: %s: %w", keyPath, err)
	}
	return result.Stdout, nil
}

func derivePublicKeyFile(ctx context.Context, keyPath string) error {
	publicKey, err := derivePublicKey(ctx, keyPath)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(keyPath), "."+filepath.Base(keyPath)+".pub.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(publicKey); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, keyPath+".pub")
}

func verifyRemoteAccess(ctx context.Context, deployment config.Deployment) error {
	env := []string(nil)
	if deployment.SSHKeyPath != "" {
		env = []string{"GIT_SSH_COMMAND=" + auth.GitSSHCommand(deployment.SSHKeyPath)}
	}
	_, err := execx.Run(ctx, execx.Command{
		Name: "git",
		Args: []string{"ls-remote", "--heads", deployment.RepoURL},
		Env:  env,
	})
	return err
}

func selectRollbackTarget(deployment config.Deployment) (string, error) {
	layout := state.NewDocroot(deployment.Docroot, deployment.DeploymentID)
	current := ""
	if target, err := os.Readlink(layout.Current()); err == nil {
		current = filepath.Base(target)
	}
	entries, err := os.ReadDir(filepath.Join(layout.Base(), "releases"))
	if err != nil {
		return "", err
	}
	type releaseEntry struct {
		name    string
		modTime int64
	}
	var metadataCandidates []releaseEntry
	var fallbackCandidates []releaseEntry
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == current {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return "", err
		}
		candidate := releaseEntry{name: entry.Name(), modTime: info.ModTime().UnixNano()}
		if _, err := releases.LoadMetadata(layout.ReleaseMetadata(entry.Name())); err == nil {
			metadataCandidates = append(metadataCandidates, candidate)
		} else {
			fallbackCandidates = append(fallbackCandidates, candidate)
		}
	}
	candidates := metadataCandidates
	if len(candidates) == 0 {
		candidates = fallbackCandidates
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].modTime == candidates[j].modTime {
			return candidates[i].name > candidates[j].name
		}
		return candidates[i].modTime > candidates[j].modTime
	})
	if len(candidates) == 0 {
		return "", errors.New("no rollback target available")
	}
	return candidates[0].name, nil
}
