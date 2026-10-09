package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNpmInstallCommand(t *testing.T) {
	commands, err := GetNpmCommandArgs(Args{
		BuildTool:        NpmCmd,
		Command:          "ci",
		URL:              RtUrlTestStr,
		AccessToken:      RtAccessToken,
		RepoResolve:      "npm-remote",
		RepoDeploy:       "npm-local",
		ResolverId:       "npm-resolver",
		DeployerId:       "npm-deployer",
		BuildName:        RtBuildName,
		BuildNumber:      RtBuildNumber,
		Module:           "frontend",
		Project:          "customer-project",
		PublishBuildInfo: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := flattenCommands(commands)
	want := []string{
		"config add npm-resolver --url=https://artifactory.test.io --access-token $PLUGIN_ACCESS_TOKEN --interactive=false --overwrite=true",
		"config add npm-deployer --url=https://artifactory.test.io --access-token $PLUGIN_ACCESS_TOKEN --interactive=false --overwrite=true",
		"npm-config --repo-resolve=npm-remote --repo-deploy=npm-local --server-id-resolve=npm-resolver --server-id-deploy=npm-deployer",
		"npm ci --build-name=t2 --build-number=v1.0 --project=customer-project --module=frontend",
	}
	if len(got) != len(want) {
		t.Fatalf("generated %d commands, want %d: %#v", len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("command %d:\nwant %q\n got %q", index, want[index], got[index])
		}
	}
}

func TestNpmConfigIsRetrySafeAndCarriesInsecureTLS(t *testing.T) {
	commands, err := GetNpmCommandArgs(Args{
		Command:     "install",
		URL:         RtUrlTestStr,
		AccessToken: RtAccessToken,
		RepoResolve: "npm-remote",
		RepoDeploy:  "npm-local",
		Insecure:    "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	config := strings.Join(commands[0], " ")
	for _, expected := range []string{"--overwrite=true", "--insecure-tls=true"} {
		if !strings.Contains(config, expected) {
			t.Fatalf("npm server configuration is missing %s: %s", expected, config)
		}
	}
}

func TestNpmPublishDefersBuildInfoToExecutor(t *testing.T) {
	commands, err := GetNpmCommandArgs(Args{
		BuildTool:   NpmCmd,
		Command:     Publish,
		URL:         RtUrlTestStr,
		Username:    "user",
		Password:    "secret",
		RepoResolve: "npm-remote",
		RepoDeploy:  "npm-local",
		BuildName:   RtBuildName,
		BuildNumber: RtBuildNumber,
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(flattenCommands(commands), "\n")
	if strings.Contains(joined, "build-publish") {
		t.Fatalf("npm command generation published build info directly: %s", joined)
	}
	if strings.Contains(joined, "secret") {
		t.Fatalf("npm command generation exposed a credential: %s", joined)
	}
	if !strings.Contains(joined, "npm publish --build-name=t2 --build-number=v1.0") {
		t.Fatalf("npm publish command is incomplete: %s", joined)
	}
}

func TestNpmBuildInfoPublicationRequiresExplicitSetting(t *testing.T) {
	if shouldPublishBuildInfo(Args{BuildTool: NpmCmd, Command: Publish}) {
		t.Fatal("npm publish enabled build-info without publish_build_info")
	}
	if !shouldPublishBuildInfo(Args{
		BuildTool:        NpmCmd,
		Command:          Publish,
		PublishBuildInfo: true,
	}) {
		t.Fatal("npm publish_build_info setting was ignored")
	}
	for _, buildTool := range []string{MvnCmd, GradleCmd} {
		if !shouldPublishBuildInfo(Args{BuildTool: buildTool, Command: Publish}) {
			t.Fatalf("%s publish no longer preserves the existing build-info contract", buildTool)
		}
	}
	if shouldPublishBuildInfo(Args{Command: "publish-build-info"}) {
		t.Fatal("standalone publish-build-info would be published twice")
	}
	if shouldPublishBuildInfo(Args{
		Command:          "add-build-dependencies",
		PublishBuildInfo: true,
	}) {
		t.Fatal("add-build-dependencies would publish build info twice")
	}
}

func TestNpmBuildInfoPublicationKeepsJFrogProject(t *testing.T) {
	command, err := getCentralBuildInfoPublishCommandArgs(Args{
		URL:         RtUrlTestStr,
		AccessToken: RtAccessToken,
		BuildName:   RtBuildName,
		BuildNumber: RtBuildNumber,
		Project:     "customer-project",
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(command, " ")
	if !strings.Contains(joined, "--project=customer-project") {
		t.Fatalf("build-info publication lost the JFrog project: %s", joined)
	}
	if strings.Contains(joined, RtAccessToken) {
		t.Fatalf("build-info command exposed the access token: %s", joined)
	}
}

func TestNpmRejectsUnsupportedOperations(t *testing.T) {
	for _, command := range []string{"", "run arbitrary-script", "delete"} {
		_, err := GetNpmCommandArgs(Args{
			Command:     command,
			RepoResolve: "npm-remote",
			RepoDeploy:  "npm-local",
		})
		if err == nil {
			t.Fatalf("unsupported npm command %q was accepted", command)
		}
	}
}

func TestNpmRequiresCompleteBuildCoordinates(t *testing.T) {
	for _, args := range []Args{
		{BuildName: "build-without-number"},
		{BuildNumber: "number-without-build"},
	} {
		args.Command = "ci"
		args.RepoResolve = "npm-remote"
		args.RepoDeploy = "npm-local"
		args.AccessToken = RtAccessToken
		args.URL = RtUrlTestStr
		if _, err := GetNpmCommandArgs(args); err == nil ||
			!strings.Contains(err.Error(), "must be set together") {
			t.Fatalf("expected incomplete build coordinates to fail, got %v", err)
		}
	}
}

func TestPrepareNpmEnvironmentSelectsApprovedAlternate(t *testing.T) {
	root := t.TempDir()
	cli := filepath.Join(root, "6.14.8", "bin", "npm-cli.js")
	if err := os.MkdirAll(filepath.Dir(cli), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cli, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARTIFACTORY_NPM_ROOT", root)
	t.Setenv("PATH", "original-path")

	environment, cleanup, err := prepareNpmEnvironment(
		[]string{"jf", "npm", "ci"},
		Args{NpmVersion: "6.14.8"},
		[]string{"PATH=original-path"},
	)
	if err != nil {
		t.Fatal(err)
	}
	pathValue := ""
	for _, entry := range environment {
		if strings.HasPrefix(entry, "PATH=") {
			pathValue = strings.TrimPrefix(entry, "PATH=")
		}
	}
	parts := strings.Split(pathValue, string(os.PathListSeparator))
	if len(parts) < 2 || parts[1] != "original-path" {
		t.Fatalf("selected npm path did not precede the original PATH: %q", pathValue)
	}
	shimDirectory := parts[0]
	shimName := "npm"
	if os.PathSeparator == '\\' {
		shimName = "npm.cmd"
	}
	if _, err := os.Stat(filepath.Join(shimDirectory, shimName)); err != nil {
		t.Fatalf("npm selector shim is missing: %v", err)
	}
	cleanup()
	if _, err := os.Stat(shimDirectory); !os.IsNotExist(err) {
		t.Fatalf("npm selector shim was not cleaned up: %v", err)
	}
}

func TestPrepareNpmEnvironmentRejectsUnknownVersion(t *testing.T) {
	_, _, err := prepareNpmEnvironment(
		[]string{"jf", "npm", "ci"},
		Args{NpmVersion: "9.9.9"},
		os.Environ(),
	)
	if err == nil || !strings.Contains(err.Error(), "unsupported npm_version") {
		t.Fatalf("expected an unsupported npm version error, got %v", err)
	}
}
