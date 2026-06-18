package auth

import (
	"errors"
	"net/url"
	"strings"
)

func GitSSHCommand(keyPath string) string {
	// IdentitiesOnly prevents ssh-agent from offering unrelated keys first.
	// BatchMode keeps deploy/update cron-safe by failing instead of prompting.
	// accept-new gives first-contact trust-on-first-use without editing ~/.ssh.
	return "ssh -i " + shellQuote(keyPath) +
		" -o IdentitiesOnly=yes" +
		" -o BatchMode=yes" +
		" -o StrictHostKeyChecking=accept-new"
}

func HTTPSURLToSSH(rawURL string) (string, bool) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	if parsed.Scheme != "https" || parsed.Host == "" || parsed.Path == "" || parsed.Path == "/" {
		return "", false
	}
	// Most Git hosts accept the mechanical SSH shape git@host:path for the same
	// repository path. Keep the path verbatim rather than adding provider rules.
	return "git@" + parsed.Host + ":" + strings.TrimPrefix(parsed.Path, "/"), true
}

func ValidateKeySource(useKey, importKey string, remove bool) error {
	count := 0
	if useKey != "" {
		count++
	}
	if importKey != "" {
		count++
	}
	if remove {
		count++
	}
	if count > 1 {
		return errors.New("choose only one auth key source")
	}
	return nil
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return (r < 'A' || r > 'Z') &&
			(r < 'a' || r > 'z') &&
			(r < '0' || r > '9') &&
			!strings.ContainsRune("@%_+=:,./-", r)
	}) == -1 {
		return value
	}
	// GIT_SSH_COMMAND is interpreted by a shell. Single-quote the path and
	// escape embedded quotes using the standard '\'' sequence.
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
