package cli

import (
	"strings"
	"testing"
)

func TestParseRejectsMissingCommand(t *testing.T) {
	_, err := Parse(nil)
	if err == nil {
		t.Fatal("expected missing command to fail")
	}
	if !strings.Contains(err.Error(), "command is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRejectsStrayArguments(t *testing.T) {
	_, err := Parse([]string{"deploy", "--name", "site", "extra"})
	if err == nil {
		t.Fatal("expected stray argument to fail")
	}
	if !strings.Contains(err.Error(), "unexpected argument") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseDeployRequiresName(t *testing.T) {
	_, err := Parse([]string{"deploy", "--branch", "main"})
	if err == nil {
		t.Fatal("expected missing name to fail")
	}
	if !strings.Contains(err.Error(), "--name is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseDeployAllowsDefaultRef(t *testing.T) {
	cmd, err := Parse([]string{"deploy", "--name", "site"})
	if err != nil {
		t.Fatalf("deploy with default ref should parse: %v", err)
	}
	if cmd.Verb != "deploy" || cmd.Name != "site" || cmd.RefMode != "" || cmd.RefValue != "" {
		t.Fatalf("unexpected command: %#v", cmd)
	}
}

func TestParseDeployRejectsMultipleRefs(t *testing.T) {
	_, err := Parse([]string{"deploy", "--name", "site", "--branch", "main", "--tag", "v1"})
	if err == nil {
		t.Fatal("expected multiple refs to fail")
	}
	if !strings.Contains(err.Error(), "choose only one ref") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseConfigSetUnset(t *testing.T) {
	cmd, err := Parse([]string{"config", "--name", "site", "--set", "default_ref=main", "--unset", "post_deploy"})
	if err != nil {
		t.Fatalf("config should parse: %v", err)
	}
	if len(cmd.Set) != 1 || cmd.Set[0] != "default_ref=main" {
		t.Fatalf("unexpected set values: %#v", cmd.Set)
	}
	if len(cmd.Unset) != 1 || cmd.Unset[0] != "post_deploy" {
		t.Fatalf("unexpected unset values: %#v", cmd.Unset)
	}
}

func TestParseConfigRejectsUnknownKey(t *testing.T) {
	_, err := Parse([]string{"config", "--name", "site", "--set", "unknown=value"})
	if err == nil {
		t.Fatal("expected unknown config key to fail")
	}
	if !strings.Contains(err.Error(), "unsupported config key") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseDestroyRequiresMatchingConfirmation(t *testing.T) {
	_, err := Parse([]string{"destroy", "--name", "site", "--confirm-destroy", "wrong"})
	if err == nil {
		t.Fatal("expected mismatched confirmation to fail")
	}
	if !strings.Contains(err.Error(), "--confirm-destroy must match --name") {
		t.Fatalf("unexpected error: %v", err)
	}

	cmd, err := Parse([]string{"destroy", "--name", "site", "--confirm-destroy", "site"})
	if err != nil {
		t.Fatalf("matching confirmation should parse: %v", err)
	}
	if cmd.Verb != "destroy" || cmd.Name != "site" || cmd.ConfirmDestroy != "site" {
		t.Fatalf("unexpected command: %#v", cmd)
	}
}

func TestParseDoctorDiagnostics(t *testing.T) {
	cmd, err := Parse([]string{"doctor", "--name", "site", "--offline", "--print-claims", "--assert-public-symlinks"})
	if err != nil {
		t.Fatalf("doctor diagnostics should parse: %v", err)
	}
	if !cmd.Offline || !cmd.PrintClaims || !cmd.AssertPublicSymlinks {
		t.Fatalf("diagnostic flags were not captured: %#v", cmd)
	}
}

func TestParseNameValidation(t *testing.T) {
	_, err := Parse([]string{"status", "--name", "../site"})
	if err == nil {
		t.Fatal("expected invalid name to fail")
	}
	if !strings.Contains(err.Error(), "invalid deployment name") {
		t.Fatalf("unexpected error: %v", err)
	}
}
