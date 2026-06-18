package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/auth"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/config"
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
			if _, ok := configKeySpecs[key]; !ok {
				return fmt.Errorf("unsupported config key: %s", key)
			}
		}
		for _, key := range cmd.Unset {
			if _, ok := configKeySpecs[key]; !ok {
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
