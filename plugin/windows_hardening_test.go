package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestArgvHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_ARTIFACTORY_ARGV_HELPER") != "1" {
		return
	}
	if delay := os.Getenv("ARTIFACTORY_ARGV_HELPER_DELAY"); delay != "" {
		duration, err := time.ParseDuration(delay)
		if err != nil {
			os.Exit(2)
		}
		time.Sleep(duration)
	}
	separator := -1
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 {
		os.Exit(3)
	}
	if cwdOutput := os.Getenv("ARTIFACTORY_ARGV_HELPER_CWD_OUTPUT"); cwdOutput != "" {
		cwd, err := os.Getwd()
		if err != nil {
			os.Exit(6)
		}
		if err := os.WriteFile(cwdOutput, []byte(cwd), 0600); err != nil {
			os.Exit(7)
		}
	}
	payload, err := json.Marshal(os.Args[separator+1:])
	if err != nil {
		os.Exit(4)
	}
	if err := os.WriteFile(os.Getenv("ARTIFACTORY_ARGV_HELPER_OUTPUT"), payload, 0600); err != nil {
		os.Exit(5)
	}
	if message := os.Getenv("ARTIFACTORY_ARGV_HELPER_ERROR"); message != "" {
		_, _ = os.Stderr.WriteString(message)
		os.Exit(8)
	}
	os.Exit(0)
}

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
	} {
		if commands, err := GetRtCommandsList(args); err == nil || len(commands) != 0 {
			t.Fatalf("expected unsupported combination to fail: %#v, commands=%v err=%v", args, commands, err)
		}
	}
}

func TestStandaloneCommandsIgnoreStaleBuildTool(t *testing.T) {
	for _, command := range []string{
		"download",
		"cleanup",
		"scan",
		"publish-build-info",
		"promote",
		"add-build-dependencies",
		"build-discard",
	} {
		t.Run(command, func(t *testing.T) {
			args := Args{
				BuildTool:   GradleCmd,
				Command:     command,
				Username:    "user",
				Password:    "password",
				URL:         RtUrlTestStr,
				BuildName:   RtBuildName,
				BuildNumber: RtBuildNumber,
				Source:      "generic-local/source.zip",
				Target:      "generic-local/target/",
			}
			commands, err := GetRtCommandsList(args)
			if err != nil {
				t.Fatalf("standalone command rejected a stale build_tool: %v", err)
			}
			if len(commands) == 0 {
				t.Fatal("standalone command generated no commands")
			}
		})
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

func TestExecCommandPreservesArgumentsWithoutShellReparsing(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "argv.json")
	t.Setenv("GO_WANT_ARTIFACTORY_ARGV_HELPER", "1")
	t.Setenv("ARTIFACTORY_ARGV_HELPER_OUTPUT", output)

	expected := []string{
		"rt",
		"download",
		`generic-local/releases/**/*.zip`,
		`C:\workspace\files with spaces\`,
		`quoted"value`,
		`ampersand&value`,
		`semicolon;value`,
		`parentheses(value)`,
	}
	command := []string{executable, "-test.run=TestArgvHelperProcess", "--"}
	command = append(command, expected...)
	if err := ExecCommand(context.Background(), Args{}, command); err != nil {
		t.Fatalf("ExecCommand returned an error: %v", err)
	}

	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var actual []string
	if err := json.Unmarshal(data, &actual); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("argv changed during execution:\nwant: %#v\n got: %#v", expected, actual)
	}
}

func TestExecCommandUsesValidatedProjectDirectory(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	projectDir, err := os.MkdirTemp(workspace, "project with spaces-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(projectDir)

	argvOutput := filepath.Join(t.TempDir(), "argv.json")
	cwdOutput := filepath.Join(t.TempDir(), "cwd.txt")
	t.Setenv("GO_WANT_ARTIFACTORY_ARGV_HELPER", "1")
	t.Setenv("ARTIFACTORY_ARGV_HELPER_OUTPUT", argvOutput)
	t.Setenv("ARTIFACTORY_ARGV_HELPER_CWD_OUTPUT", cwdOutput)

	relativeProjectDir, err := filepath.Rel(workspace, projectDir)
	if err != nil {
		t.Fatal(err)
	}
	command := []string{executable, "-test.run=TestArgvHelperProcess", "--", "mvn", "-f", "pom with spaces.xml"}
	if err := ExecCommand(context.Background(), Args{ProjectDir: relativeProjectDir}, command); err != nil {
		t.Fatalf("ExecCommand returned an error: %v", err)
	}
	actualCWD, err := os.ReadFile(cwdOutput)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(string(actualCWD)) != filepath.Clean(projectDir) {
		t.Fatalf("command ran in %q, want %q", actualCWD, projectDir)
	}
}

func TestProjectDirectoryRejectsWorkspaceEscape(t *testing.T) {
	_, err := resolveProjectDir(filepath.Join("..", "outside"))
	if err == nil || !strings.Contains(err.Error(), "outside the workspace") {
		t.Fatalf("expected workspace escape error, got %v", err)
	}
}

func TestCommandFailureIsActionableAndRedacted(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GO_WANT_ARTIFACTORY_ARGV_HELPER", "1")
	t.Setenv("ARTIFACTORY_ARGV_HELPER_OUTPUT", filepath.Join(t.TempDir(), "argv.json"))
	t.Setenv("ARTIFACTORY_ARGV_HELPER_ERROR", "build-info rejected secret-value")
	command := []string{
		executable,
		"-test.run=TestArgvHelperProcess",
		"--",
		"rt",
		"build-publish",
		"--password",
		"$PLUGIN_PASSWORD",
	}
	err = ExecCommand(context.Background(), Args{Password: "secret-value"}, command)
	if err == nil {
		t.Fatal("expected command failure")
	}
	if !strings.Contains(err.Error(), "build-info rejected ***") {
		t.Fatalf("failure did not preserve redacted diagnostics: %v", err)
	}
	if strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("failure exposed the password: %v", err)
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
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GO_WANT_ARTIFACTORY_ARGV_HELPER", "1")
	t.Setenv("ARTIFACTORY_ARGV_HELPER_OUTPUT", filepath.Join(t.TempDir(), "argv.json"))
	t.Setenv("ARTIFACTORY_ARGV_HELPER_DELAY", "5s")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = ExecCommand(ctx, Args{}, []string{executable, "-test.run=TestArgvHelperProcess", "--"})
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
