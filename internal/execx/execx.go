package execx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
)

type Command struct {
	Name  string
	Args  []string
	Dir   string
	Env   []string
	Stdin io.Reader
}

type Result struct {
	Stdout string
	Stderr string
}

func RequireCommands(ctx context.Context, names []string) error {
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			return fmt.Errorf("required command not found: %s", name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	return nil
}

func Run(ctx context.Context, command Command) (Result, error) {
	cmd := exec.CommandContext(ctx, command.Name, command.Args...)
	cmd.Dir = command.Dir
	cmd.Stdin = command.Stdin
	if len(command.Env) > 0 {
		cmd.Env = append(os.Environ(), command.Env...)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		return result, fmt.Errorf("%s failed: %w: %s", command.Name, err, result.Stderr)
	}
	return result, nil
}
