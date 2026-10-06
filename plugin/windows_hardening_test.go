package plugin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestResolveShellPrefersPwshFromPath(t *testing.T) {
	lookPath := func(name string) (string, error) {
		if name == "pwsh" {
			return `C:\PowerShell\pwsh.exe`, nil
		}
		return "", os.ErrNotExist
	}
	stat := func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	shell, argument, err := resolveShell("windows", lookPath, stat)
	if err != nil {
		t.Fatalf("resolveShell returned an error: %v", err)
	}
	if shell != `C:\PowerShell\pwsh.exe` || argument != "-Command" {
		t.Fatalf("unexpected shell resolution: %q %q", shell, argument)
	}
}

func TestResolveShellFailsWithoutSupportedPowerShell(t *testing.T) {
	lookPath := func(string) (string, error) { return "", os.ErrNotExist }
	stat := func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	_, _, err := resolveShell("windows", lookPath, stat)
	if err == nil || !strings.Contains(err.Error(), "no supported PowerShell") {
		t.Fatalf("expected a clear shell resolution error, got %v", err)
	}
}

func TestProxySettingsApplyHarnessValues(t *testing.T) {
	t.Setenv(harnessHTTPProxy, "http://proxy.example:8080")
	t.Setenv(harnessHTTPSProxy, "https://proxy.example:8443")
	t.Setenv(harnessNoProxy, "localhost,.example.internal")
	t.Setenv(httpProxy, "")
	t.Setenv(httpsProxy, "")
	t.Setenv(noProxy, "")

	setSecureConnectProxies()

	if got := os.Getenv(httpProxy); got != "http://proxy.example:8080" {
		t.Fatalf("HTTP proxy mismatch: %q", got)
	}
	if got := os.Getenv(httpsProxy); got != "https://proxy.example:8443" {
		t.Fatalf("HTTPS proxy mismatch: %q", got)
	}
	if got := os.Getenv(noProxy); got != "localhost,.example.internal" {
		t.Fatalf("NO_PROXY mismatch: %q", got)
	}
}

func TestUnknownBuildToolCommandFailsClosed(t *testing.T) {
	for _, args := range []Args{
		{BuildTool: "ant", Command: "build"},
		{BuildTool: MvnCmd, Command: "delete-everything"},
		{Command: "unknown"},
		{BuildTool: GradleCmd, Command: "download"},
	} {
		if commands, err := GetRtCommandsList(args); err == nil || len(commands) != 0 {
			t.Fatalf("expected unsupported combination to fail: %#v, commands=%v err=%v", args, commands, err)
		}
	}
}

func TestGradlePublishNeverEmbedsCredentials(t *testing.T) {
	commands, err := GetGradlePublishCommand(Args{
		Command:     Publish,
		BuildTool:   GradleCmd,
		URL:         RtUrlTestStr,
		Username:    "customer-user",
		Password:    "customer-secret",
		BuildName:   RtBuildName,
		BuildNumber: RtBuildNumber,
		DeployerId:  RtDeployerId,
	})
	if err != nil {
		t.Fatalf("GetGradlePublishCommand returned an error: %v", err)
	}
	joined := strings.Join(flattenCommands(commands), "\n")
	if strings.Contains(joined, "customer-secret") || strings.Contains(joined, "-Ppassword") {
		t.Fatalf("Gradle command contains a plaintext password: %s", joined)
	}
	if strings.Count(joined, "build-publish") != 0 {
		t.Fatalf("build-info must be published once by the executor, not command generation: %s", joined)
	}
}

func TestMavenPublishDefersBuildInfoToExecutor(t *testing.T) {
	commands, err := GetMavenPublishCommand(Args{
		Command:     Publish,
		BuildTool:   MvnCmd,
		URL:         RtUrlTestStr,
		AccessToken: RtAccessToken,
		BuildName:   RtBuildName,
		BuildNumber: RtBuildNumber,
		DeployerId:  RtDeployerId,
	})
	if err != nil {
		t.Fatalf("GetMavenPublishCommand returned an error: %v", err)
	}
	if joined := strings.Join(flattenCommands(commands), "\n"); strings.Contains(joined, "build-publish") {
		t.Fatalf("build-info must be published once by the executor: %s", joined)
	}
}

func TestDownloadPreservesSourceTargetOrderAndWindowsPaths(t *testing.T) {
	source := `generic-local/releases/**/*.zip`
	target := `C:\workspace\files with spaces\`
	commands, err := GetDownloadCommandArgs(Args{
		Command:     "download",
		AccessToken: RtAccessToken,
		URL:         RtUrlTestStr,
		Source:      source,
		Target:      target,
	})
	if err != nil {
		t.Fatalf("GetDownloadCommandArgs returned an error: %v", err)
	}
	command := commands[0]
	sourceIndex, targetIndex := -1, -1
	for index, argument := range command {
		switch argument {
		case source:
			sourceIndex = index
		case target:
			targetIndex = index
		}
	}
	if sourceIndex < 0 || targetIndex != sourceIndex+1 {
		t.Fatalf("source and target order is wrong: %#v", command)
	}
}

func TestDownloadRequiresSourceAndTargetWithoutSpec(t *testing.T) {
	base := Args{Command: "download", AccessToken: RtAccessToken, URL: RtUrlTestStr}
	if _, err := GetDownloadCommandArgs(base); err == nil || !strings.Contains(err.Error(), "source") {
		t.Fatalf("expected missing source error, got %v", err)
	}
	base.Source = "repo/file.txt"
	if _, err := GetDownloadCommandArgs(base); err == nil || !strings.Contains(err.Error(), "target") {
		t.Fatalf("expected missing target error, got %v", err)
	}
}

func TestInlineSpecIsRestrictedAndCleanedUp(t *testing.T) {
	commands, err := GetDownloadCommandArgs(Args{
		Command:     "download",
		AccessToken: RtAccessToken,
		URL:         RtUrlTestStr,
		Spec:        `{"files":[{"pattern":"generic-local/**/*.zip","target":"C:/files with spaces/"}]}`,
	})
	if err != nil {
		t.Fatalf("GetDownloadCommandArgs returned an error: %v", err)
	}
	var path string
	for _, argument := range commands[0] {
		if strings.HasPrefix(argument, "--spec=") {
			path = strings.TrimPrefix(argument, "--spec=")
		}
	}
	if path == "" {
		t.Fatal("temporary spec path was not generated")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("temporary spec is missing: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("temporary spec permissions are %o, want 600", info.Mode().Perm())
	}
	cleanupTemporarySpecs(commands)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("temporary spec was not removed: %v", err)
	}
}

func TestPEMRotationOverwritesWithoutLeavingTemporaryFiles(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "security", "cert.pem")
	args := Args{PEMFileContents: "old certificate", PEMFilePath: path}
	if err := WriteKnownGoodServerCertsForTls(args); err != nil {
		t.Fatalf("initial PEM write failed: %v", err)
	}
	args.PEMFileContents = "rotated certificate"
	if err := WriteKnownGoodServerCertsForTls(args); err != nil {
		t.Fatalf("rotated PEM write failed: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read PEM: %v", err)
	}
	if string(content) != "rotated certificate" {
		t.Fatalf("PEM rotation did not replace content: %q", content)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("cannot stat PEM: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("PEM permissions are %o, want 600", info.Mode().Perm())
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".cert-*.tmp"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary PEM files remain: %v, %v", matches, err)
	}
}

func TestCommandTraceRedactsKnownCredentialFlags(t *testing.T) {
	input := "jf config add --password secret -Ppassword=gradle-secret --access-token token --apikey key"
	output := redactCommand(input)
	for _, secret := range []string{" secret", "gradle-secret", " token", " key"} {
		if strings.Contains(output, secret) {
			t.Fatalf("redacted command still contains %q: %s", secret, output)
		}
	}
	if strings.Count(output, "***") != 4 {
		t.Fatalf("expected all four credential forms to be redacted: %s", output)
	}
}

func TestExecCommandHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := ExecCommand(ctx, Args{}, []string{"sleep", "5"})
	if err == nil {
		t.Fatal("expected cancelled command to fail")
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("expected context deadline, got %v", ctx.Err())
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("cancelled command ran too long: %s", elapsed)
	}
}

func flattenCommands(commands [][]string) []string {
	result := make([]string, 0, len(commands))
	for _, command := range commands {
		result = append(result, strings.Join(command, " "))
	}
	return result
}
