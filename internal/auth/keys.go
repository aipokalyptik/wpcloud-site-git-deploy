package auth

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/config"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/execx"
	"github.com/aipokalyptik/wpcloud-site-git-deploy/internal/state"
)

func ValidatePrivateKeyPath(ctx context.Context, path string) error {
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
	_, err = DerivePublicKey(ctx, path)
	return err
}

func GenerateOrReuseKey(ctx context.Context, layout state.Layout, name string, force bool) (string, error) {
	keyPath := layout.Key(name)
	if !force {
		if _, err := os.Stat(keyPath); err == nil {
			return keyPath, ValidatePrivateKeyPath(ctx, keyPath)
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

func ImportPrivateKey(ctx context.Context, layout state.Layout, name, sourcePath string, force bool) (string, error) {
	if err := ValidatePrivateKeyPath(ctx, sourcePath); err != nil {
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

func DerivePublicKey(ctx context.Context, keyPath string) (string, error) {
	if err := execx.RequireCommands(ctx, []string{"ssh-keygen"}); err != nil {
		return "", err
	}
	result, err := execx.Run(ctx, execx.Command{Name: "ssh-keygen", Args: []string{"-y", "-f", keyPath}})
	if err != nil {
		return "", fmt.Errorf("private key cannot be used without prompting or is not a valid private key: %s: %w", keyPath, err)
	}
	return result.Stdout, nil
}

func PublicKeyLine(ctx context.Context, keyPath string) (string, error) {
	publicKey, err := DerivePublicKey(ctx, keyPath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(publicKey), nil
}

func derivePublicKeyFile(ctx context.Context, keyPath string) error {
	publicKey, err := DerivePublicKey(ctx, keyPath)
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

func VerifyRemoteAccess(ctx context.Context, deployment config.Deployment) error {
	env := []string(nil)
	if deployment.SSHKeyPath != "" {
		env = []string{"GIT_SSH_COMMAND=" + GitSSHCommand(deployment.SSHKeyPath)}
	}
	_, err := execx.Run(ctx, execx.Command{
		Name: "git",
		Args: []string{"ls-remote", "--heads", deployment.RepoURL},
		Env:  env,
	})
	return err
}
