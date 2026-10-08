package plugin

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"
)

const diagnosticLimit = 16 * 1024

type boundedBuffer struct {
	data []byte
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	originalLength := len(p)
	if len(p) >= diagnosticLimit {
		b.data = append(b.data[:0], p[len(p)-diagnosticLimit:]...)
		return originalLength, nil
	}
	if overflow := len(b.data) + len(p) - diagnosticLimit; overflow > 0 {
		copy(b.data, b.data[overflow:])
		b.data = b.data[:len(b.data)-overflow]
	}
	b.data = append(b.data, p...)
	return originalLength, nil
}

func (b *boundedBuffer) String() string {
	return string(b.data)
}

func resolveProjectDir(projectDir string) (string, error) {
	workspace, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("determine workspace: %w", err)
	}
	if projectDir == "" || projectDir == "." {
		return workspace, nil
	}

	resolved := projectDir
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(workspace, resolved)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve project_dir %q: %w", projectDir, err)
	}

	relative, err := filepath.Rel(workspace, resolved)
	if err != nil {
		return "", fmt.Errorf("validate project_dir %q: %w", projectDir, err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("project_dir %q resolves outside the workspace", projectDir)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("project_dir %q is not accessible: %w", projectDir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project_dir %q is not a directory", projectDir)
	}
	return resolved, nil
}

func commandLabel(command []string) string {
	if len(command) == 0 {
		return "command"
	}
	parts := []string{filepath.Base(command[0])}
	for _, argument := range command[1:] {
		if strings.HasPrefix(argument, "-") {
			continue
		}
		parts = append(parts, argument)
		if len(parts) == 3 {
			break
		}
	}
	return strings.Join(parts, " ")
}

func redactSecrets(message string, args Args) string {
	for _, secret := range []string{
		args.Password,
		args.AccessToken,
		args.APIKey,
		args.OidcToken,
	} {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "***")
		}
	}
	return redactCommand(message)
}

func materializeCommand(command []string, args Args) []string {
	materialized := append([]string(nil), command...)
	replacements := map[string]string{
		"$PLUGIN_USERNAME":     args.Username,
		"$PLUGIN_PASSWORD":     args.Password,
		"$PLUGIN_API_KEY":      args.APIKey,
		"$PLUGIN_ACCESS_TOKEN": args.AccessToken,
	}
	for index, argument := range materialized {
		if replacement, ok := replacements[argument]; ok {
			materialized[index] = replacement
		}
	}
	return materialized
}

func runCommand(ctx context.Context, args Args, command []string, captureStdout bool) ([]byte, error) {
	if len(command) == 0 || command[0] == "" {
		return nil, fmt.Errorf("cannot execute an empty command")
	}
	directory, err := resolveProjectDir(args.ProjectDir)
	if err != nil {
		return nil, err
	}

	executionCommand := materializeCommand(command, args)
	cmd := exec.CommandContext(ctx, executionCommand[0], executionCommand[1:]...)
	cmd.Dir = directory
	cmd.Env = append(os.Environ(),
		"JFROG_CLI_OFFER_CONFIG=false",
		"JFROG_CLI_AVOID_NEW_VERSION_WARNING=true",
	)

	var stdout bytes.Buffer
	if captureStdout {
		cmd.Stdout = io.MultiWriter(os.Stdout, &stdout)
	} else {
		cmd.Stdout = os.Stdout
	}
	var stderr boundedBuffer
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
	trace(cmd)

	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(redactSecrets(stderr.String(), args))
		if detail == "" {
			return stdout.Bytes(), fmt.Errorf("%s failed: %w", commandLabel(command), err)
		}
		return stdout.Bytes(), fmt.Errorf("%s failed: %w: %s", commandLabel(command), err, detail)
	}
	return stdout.Bytes(), nil
}

func ExecCommand(ctx context.Context, args Args, command []string) error {
	_, err := runCommand(ctx, args, command, false)
	return err
}

func splitCommandArguments(input string) ([]string, error) {
	var (
		arguments    []string
		current      strings.Builder
		quote        rune
		tokenStarted bool
	)
	flush := func() {
		if tokenStarted {
			arguments = append(arguments, current.String())
			current.Reset()
			tokenStarted = false
		}
	}

	for _, character := range input {
		switch {
		case quote != 0 && character == quote:
			quote = 0
			tokenStarted = true
		case quote != 0:
			current.WriteRune(character)
			tokenStarted = true
		case character == '\'' || character == '"':
			quote = character
			tokenStarted = true
		case unicode.IsSpace(character):
			flush()
		default:
			current.WriteRune(character)
			tokenStarted = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote in command arguments")
	}
	flush()
	return arguments, nil
}
