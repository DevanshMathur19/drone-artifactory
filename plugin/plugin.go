// Copyright 2020 the Drone Authors. All rights reserved.
// Use of this source code is governed by the Blue Oak Model License
// that can be found in the LICENSE file.

package plugin

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
)

const (
	harnessHTTPProxy  = "HARNESS_HTTP_PROXY"
	harnessHTTPSProxy = "HARNESS_HTTPS_PROXY"
	harnessNoProxy    = "HARNESS_NO_PROXY"
	httpProxy         = "HTTP_PROXY"
	httpsProxy        = "HTTPS_PROXY"
	noProxy           = "NO_PROXY"
)

// Args provides plugin execution arguments.
type Args struct {
	Pipeline

	// Level defines the plugin log level.
	Level string `envconfig:"PLUGIN_LOG_LEVEL"`

	// TODO replace or remove
	Username         string `envconfig:"PLUGIN_USERNAME"`
	Password         string `envconfig:"PLUGIN_PASSWORD"`
	APIKey           string `envconfig:"PLUGIN_API_KEY"`
	AccessToken      string `envconfig:"PLUGIN_ACCESS_TOKEN"`
	URL              string `envconfig:"PLUGIN_URL"`
	Source           string `envconfig:"PLUGIN_SOURCE"`
	Target           string `envconfig:"PLUGIN_TARGET"`
	Retries          int    `envconfig:"PLUGIN_RETRIES"`
	Flat             string `envconfig:"PLUGIN_FLAT"`
	Spec             string `envconfig:"PLUGIN_SPEC"`
	Threads          int    `envconfig:"PLUGIN_THREADS"`
	SpecVars         string `envconfig:"PLUGIN_SPEC_VARS"`
	TargetProps      string `envconfig:"PLUGIN_TARGET_PROPS"`
	Insecure         string `envconfig:"PLUGIN_INSECURE"`
	PEMFileContents  string `envconfig:"PLUGIN_PEM_FILE_CONTENTS"`
	PEMFilePath      string `envconfig:"PLUGIN_PEM_FILE_PATH"`
	BuildNumber      string `envconfig:"PLUGIN_BUILD_NUMBER"`
	BuildName        string `envconfig:"PLUGIN_BUILD_NAME"`
	PublishBuildInfo bool   `envconfig:"PLUGIN_PUBLISH_BUILD_INFO"`
	EnableProxy      string `envconfig:"PLUGIN_ENABLE_PROXY"`

	// RT commands
	BuildTool string `envconfig:"PLUGIN_BUILD_TOOL"`
	Command   string `envconfig:"PLUGIN_COMMAND"`

	// Mvn commands
	ResolveReleaseRepo  string `envconfig:"PLUGIN_RESOLVE_RELEASE_REPO"`
	ResolveSnapshotRepo string `envconfig:"PLUGIN_RESOLVE_SNAPSHOT_REPO"`
	DeployReleaseRepo   string `envconfig:"PLUGIN_DEPLOY_RELEASE_REPO"`
	DeploySnapshotRepo  string `envconfig:"PLUGIN_DEPLOY_SNAPSHOT_REPO"`
	DeployRepo          string `envconfig:"PLUGIN_DEPLOY_REPO"`
	MvnGoals            string `envconfig:"PLUGIN_GOALS"`
	MvnPomFile          string `envconfig:"PLUGIN_POM_FILE"`
	DeployerId          string `envconfig:"PLUGIN_DEPLOYER_ID"`
	ResolverId          string `envconfig:"PLUGIN_RESOLVER_ID"`

	// Gradle commands
	GradleTasks string `envconfig:"PLUGIN_TASKS"`
	BuildFile   string `envconfig:"PLUGIN_BUILD_FILE"`
	RepoDeploy  string `envconfig:"PLUGIN_REPO_DEPLOY"`
	RepoResolve string `envconfig:"PLUGIN_REPO_RESOLVE"`

	// Upload Download commands
	SpecPath string `envconfig:"PLUGIN_SPEC_PATH"`
	Module   string `envconfig:"PLUGIN_MODULE"`
	Project  string `envconfig:"PLUGIN_PROJECT"`

	// Promote commands
	Copy string `envconfig:"PLUGIN_COPY"`

	// Add Dependencies to build commands
	Exclusions        string `envconfig:"PLUGIN_EXCLUSIONS"`
	FromRt            string `envconfig:"PLUGIN_FROM_RT"`
	Recursive         string `envconfig:"PLUGIN_RECURSIVE"`
	Regexp            string `envconfig:"PLUGIN_REGEXP"`
	DependencyPattern string `envconfig:"PLUGIN_DEPENDENCY"`

	// Build Discard commands
	Async           string `envconfig:"PLUGIN_ASYNC"`
	DeleteArtifacts string `envconfig:"PLUGIN_DELETE_ARTIFACTS"`
	ExcludeBuilds   string `envconfig:"PLUGIN_EXCLUDE_BUILDS"`
	MaxBuilds       string `envconfig:"PLUGIN_MAX_BUILDS"`
	MaxDays         string `envconfig:"PLUGIN_MAX_DAYS"`

	// Working directory for build-tool commands.
	ProjectDir string `envconfig:"PLUGIN_PROJECT_DIR"`

	// OIDC authentication
	OidcToken        string `envconfig:"ARTIFACTORY_OIDC_TOKEN"`
	OidcProviderName string `envconfig:"ARTIFACTORY_OIDC_PROVIDER_NAME"`
	OidcProjectKey   string `envconfig:"ARTIFACTORY_OIDC_PROJECT_KEY"`
}

// Exec executes the plugin.
func Exec(ctx context.Context, args Args) error {
	enableProxy := parseBoolOrDefault(false, args.EnableProxy)
	if enableProxy {
		logrus.Printf("setting proxy config for Artifactory command")
		setSecureConnectProxies()
	}

	if args.OidcToken != "" {
		if args.URL == "" {
			return fmt.Errorf("JFrog Artifactory URL is required for OIDC authentication")
		}
		if args.OidcProviderName == "" {
			return fmt.Errorf("OIDC provider name is required for OIDC authentication")
		}
		logrus.Println("OIDC authentication detected, exchanging token for JFrog access token")
		accessToken, err := exchangeOidcToken(args.URL, args.OidcToken, args.OidcProviderName, args.OidcProjectKey)
		if err != nil {
			return fmt.Errorf("OIDC token exchange failed: %w", err)
		}
		args.AccessToken = accessToken
	}

	logrus.Println("Checking RT commands")
	if args.BuildTool != "" || args.Command != "" {
		logrus.Println("Handling rt command handleRtCommand")
		return HandleRtCommands(ctx, args)
	}

	// write code here
	if args.URL == "" {
		return fmt.Errorf("JFrog Artifactory URL must be set, or anonymous access is not permitted")
	}
	artifactoryURL, err := normalizeArtifactoryURL(args.URL)
	if err != nil {
		return err
	}
	args.URL = artifactoryURL

	cmdArgs := []string{getJfrogBin(), "rt", "u", "--url=" + args.URL, "--detailed-summary=true"}
	if args.Retries != 0 {
		cmdArgs = append(cmdArgs, fmt.Sprintf("--retries=%d", args.Retries))
	}

	// Set authentication params
	cmdArgs, err = setAuthParams(cmdArgs, args)
	if err != nil {
		return err
	}

	flat := parseBoolOrDefault(false, args.Flat)
	cmdArgs = append(cmdArgs, fmt.Sprintf("--flat=%s", strconv.FormatBool(flat)))

	if args.Threads > 0 {
		cmdArgs = append(cmdArgs, fmt.Sprintf("--threads=%d", args.Threads))
	}
	// Set insecure flag
	insecure := parseBoolOrDefault(false, args.Insecure)
	if insecure {
		cmdArgs = append(cmdArgs, "--insecure-tls")
	}

	// Add --build-number and --build-name flags if provided
	if args.BuildNumber != "" {
		cmdArgs = append(cmdArgs, fmt.Sprintf("--build-number=%s", args.BuildNumber))
	}
	if args.BuildName != "" {
		cmdArgs = append(cmdArgs, "--build-name="+args.BuildName)
	}

	if err := WriteKnownGoodServerCertsForTls(args); err != nil {
		return err
	}
	// Take in spec file or use source/target arguments
	if args.Spec != "" {
		cmdArgs = append(cmdArgs, "--spec="+args.Spec)
		if args.SpecVars != "" {
			cmdArgs = append(cmdArgs, "--spec-vars="+args.SpecVars)
		}
	} else {
		filteredTargetProps := filterTargetProps(args.TargetProps)
		if filteredTargetProps != "" {
			cmdArgs = append(cmdArgs, "--target-props="+filteredTargetProps)
		}
		if args.Source == "" {
			return fmt.Errorf("source file needs to be set")
		}
		if args.Target == "" {
			return fmt.Errorf("target path needs to be set")
		}
		cmdArgs = append(cmdArgs, args.Source, args.Target)
	}

	summary, err := runCommand(ctx, args, cmdArgs, true)
	if err != nil {
		return err
	}

	// Prefer the authoritative list from JFrog CLI's --detailed-summary output;
	// fall back to local glob resolution if parsing fails.
	entries := parseJFrogDetailedSummary(summary, args.URL)
	if len(entries) == 0 {
		entries = collectArtifactEntries(args)
	}
	if len(entries) > 0 {
		writeArtifactFile(entries)
	}

	// Call publishBuildInfo if PLUGIN_PUBLISH_BUILD_INFO is set to true
	if args.PublishBuildInfo {
		if err := publishBuildInfo(ctx, args); err != nil {
			return err
		}
	}

	return nil
}

func publishBuildInfo(ctx context.Context, args Args) error {
	if args.BuildName == "" || args.BuildNumber == "" {
		return fmt.Errorf("both build name and build number need to be set when publishing build info")
	}

	sanitizedURL, err := normalizeArtifactoryURL(args.URL)
	if err != nil {
		return err
	}

	publishCmdArgs := []string{
		getJfrogBin(),
		"rt",
		"build-publish",
		args.BuildName,
		args.BuildNumber,
		"--url=" + sanitizedURL,
	}

	publishCmdArgs, err = setAuthParams(publishCmdArgs, args)
	if err != nil {
		return err
	}

	if err := ExecCommand(ctx, args, publishCmdArgs); err != nil {
		return fmt.Errorf("error publishing build info: %w", err)
	}

	return nil
}

// Function to filter TargetProps based on criteria
func filterTargetProps(rawProps string) string {
	keyValuePairs := strings.Split(rawProps, ",")
	validPairs := []string{}

	for _, pair := range keyValuePairs {
		keyValuePair := strings.SplitN(pair, "=", 2)
		if len(keyValuePair) != 2 {
			continue // skip if it's not a valid key-value pair
		}

		key := strings.TrimSpace(keyValuePair[0])
		value := strings.TrimSpace(keyValuePair[1])

		// Remove single or double quotes from value
		trimmedValue := strings.Trim(value, "\"'")

		// Check value is not empty, not "null", and not just whitespace
		if trimmedValue != "" && strings.ToLower(trimmedValue) != "null" {
			validPairs = append(validPairs, key+"="+value)
		}
	}

	return strings.Join(validPairs, ",")
}

func parseJFrogURL(inputURL string) (*url.URL, string, error) {
	parsedURL, err := url.Parse(inputURL)
	if err != nil {
		return nil, "", fmt.Errorf("invalid URL: %s", inputURL)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" || parsedURL.Host == "" {
		return nil, "", fmt.Errorf("invalid URL: %s", inputURL)
	}
	if parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		return nil, "", fmt.Errorf("invalid Artifactory URL with query or fragment: %s", inputURL)
	}
	path := strings.TrimRight(parsedURL.Path, "/")
	lowerPath := strings.ToLower(path)
	index := strings.Index(lowerPath, "/artifactory")
	if index >= 0 {
		path = path[:index]
	}
	parsedURL.Path = path
	parsedURL.RawPath = ""
	return parsedURL, path, nil
}

func normalizePlatformURL(inputURL string) (string, error) {
	parsedURL, _, err := parseJFrogURL(inputURL)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(parsedURL.String(), "/"), nil
}

func normalizeArtifactoryURL(inputURL string) (string, error) {
	platformURL, err := normalizePlatformURL(inputURL)
	if err != nil {
		return "", err
	}
	return platformURL + "/artifactory/", nil
}

// sanitizeURL is retained for compatibility with callers and tests.
func sanitizeURL(inputURL string) (string, error) {
	return normalizeArtifactoryURL(inputURL)
}

// setAuthParams appends authentication parameters to cmdArgs based on the provided credentials.
func setAuthParams(cmdArgs []string, args Args) ([]string, error) {
	// Set authentication params
	if args.Username != "" && args.Password != "" {
		cmdArgs = append(cmdArgs, "--user", "$PLUGIN_USERNAME")
		cmdArgs = append(cmdArgs, "--password", "$PLUGIN_PASSWORD")
	} else if args.APIKey != "" {
		cmdArgs = append(cmdArgs, "--apikey", "$PLUGIN_API_KEY")
	} else if args.AccessToken != "" {
		cmdArgs = append(cmdArgs, "--access-token", "$PLUGIN_ACCESS_TOKEN")
	} else {
		return nil, fmt.Errorf("either username/password, api key or access token needs to be set")
	}
	return cmdArgs, nil
}

func getShell() (string, string, error) {
	return resolveShell(runtime.GOOS, exec.LookPath, os.Stat)
}

func resolveShell(
	osName string,
	lookPath func(string) (string, error),
	stat func(string) (os.FileInfo, error),
) (string, string, error) {
	if osName != "windows" {
		return "sh", "-c", nil
	}

	for _, name := range []string{"pwsh", "pwsh.exe"} {
		if path, err := lookPath(name); err == nil {
			return path, "-Command", nil
		}
	}
	for _, path := range []string{
		"C:/PowerShell/pwsh.exe",
		"C:/Program Files/PowerShell/7/pwsh.exe",
		"C:/Program Files/PowerShell/pwsh.exe",
	} {
		if _, err := stat(path); err == nil {
			return path, "-Command", nil
		}
	}
	for _, name := range []string{"powershell", "powershell.exe"} {
		if path, err := lookPath(name); err == nil {
			return path, "-Command", nil
		}
	}
	for _, path := range []string{
		"C:/Windows/System32/WindowsPowerShell/v1.0/powershell.exe",
		"C:/Windows/SysWOW64/WindowsPowerShell/v1.0/powershell.exe",
	} {
		if _, err := stat(path); err == nil {
			return path, "-Command", nil
		}
	}
	return "", "", fmt.Errorf("no supported PowerShell executable found; install pwsh or Windows PowerShell")
}

func getJfrogBin() string {
	if runtime.GOOS == "windows" {
		if _, err := os.Stat("C:/bin/jfrog.exe"); err == nil {
			return "C:/bin/jfrog.exe"
		}
	}
	return "jf"
}

func getEnvPrefix() string {
	if runtime.GOOS == "windows" {
		return "$Env:"
	}
	return "$"
}

func parseBoolOrDefault(defaultValue bool, s string) (result bool) {
	var err error
	result, err = strconv.ParseBool(s)
	if err != nil {
		result = defaultValue
	}

	return
}

// trace writes each command to stdout with the command wrapped in an xml
// tag so that it can be extracted and displayed in the logs.
func trace(cmd *exec.Cmd) {
	fmt.Fprintf(os.Stdout, "+ %s\n", redactCommand(strings.Join(cmd.Args, " ")))
}

func redactCommand(command string) string {
	for _, pattern := range []*regexp.Regexp{
		regexp.MustCompile(`(?i)(--password(?:=|\s+))\S+`),
		regexp.MustCompile(`(?i)(-Ppassword=)\S+`),
		regexp.MustCompile(`(?i)(--access-token(?:=|\s+))\S+`),
		regexp.MustCompile(`(?i)(--apikey(?:=|\s+))\S+`),
	} {
		command = pattern.ReplaceAllString(command, `${1}***`)
	}
	return command
}

func setSecureConnectProxies() {
	copyEnvVariableIfExists(harnessHTTPProxy, httpProxy)
	copyEnvVariableIfExists(harnessHTTPSProxy, httpsProxy)
	copyEnvVariableIfExists(harnessNoProxy, noProxy)
}

func copyEnvVariableIfExists(src string, dest string) {
	srcValue := os.Getenv(src)
	if srcValue == "" {
		return
	}
	err := os.Setenv(dest, srcValue)
	if err != nil {
		logrus.Printf("Failed to copy env variable from %s to %s with error %v", src, dest, err)
	}
}
