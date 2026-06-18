package doctor

import (
	"context"
	"fmt"
	"os"

	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/auth"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/config"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/engine"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/execx"
)

type Options struct {
	Offline              bool
	PrintClaims          bool
	AssertPublicSymlinks bool
	Home                 string
}

type Result struct {
	Report *Report
	Claims []string
}

func Run(ctx context.Context, deployment config.Deployment, options Options) Result {
	report := NewReport()
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
	} else if err := auth.ValidatePrivateKeyPath(ctx, deployment.SSHKeyPath); err != nil {
		report.Fail("ssh-key", err.Error())
	} else {
		report.OK("ssh-key", "usable")
	}
	result := Result{Report: report}
	if options.PrintClaims {
		claimList, err := engine.ClaimsForCurrent(deployment.Docroot, deployment.DeploymentID)
		if err != nil {
			report.Fail("claims", err.Error())
		} else {
			result.Claims = claimList
			report.OK("claims", fmt.Sprintf("%d claims", len(claimList)))
		}
	}
	if options.AssertPublicSymlinks {
		if err := engine.AssertDeploymentSymlinks(deployment.Docroot, deployment.DeploymentID, options.Home); err != nil {
			report.Fail("public-symlinks", err.Error())
		} else {
			report.OK("public-symlinks", "valid")
		}
	}
	if !options.Offline {
		if err := auth.VerifyRemoteAccess(ctx, deployment); err != nil {
			report.Fail("git-remote", err.Error())
		} else {
			report.OK("git-remote", "accessible")
		}
	}
	return result
}
