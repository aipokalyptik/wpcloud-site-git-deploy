package auth

import (
	"strings"
	"testing"
)

func TestGitSSHCommandUsesConfiguredKey(t *testing.T) {
	command := GitSSHCommand("/home/site/key")
	for _, expected := range []string{
		"ssh",
		"-i /home/site/key",
		"-o IdentitiesOnly=yes",
		"-o BatchMode=yes",
		"-o StrictHostKeyChecking=accept-new",
	} {
		if !strings.Contains(command, expected) {
			t.Fatalf("ssh command %q missing %q", command, expected)
		}
	}
}

func TestHTTPSURLToSSH(t *testing.T) {
	got, ok := HTTPSURLToSSH("https://gitlab.example.com/team/site.git")
	if !ok {
		t.Fatal("expected https URL to convert")
	}
	if got != "git@gitlab.example.com:team/site.git" {
		t.Fatalf("unexpected conversion: %s", got)
	}
}

func TestHTTPSURLToSSHRejectsNonHTTPS(t *testing.T) {
	if _, ok := HTTPSURLToSSH("git@example.com:team/site.git"); ok {
		t.Fatal("ssh URL should not convert")
	}
}

func TestKeySourceChoice(t *testing.T) {
	if err := ValidateKeySource("a", "b", false); err == nil {
		t.Fatal("expected multiple key sources to fail")
	}
	if err := ValidateKeySource("", "", true); err != nil {
		t.Fatalf("remove-only auth should be valid: %v", err)
	}
}
