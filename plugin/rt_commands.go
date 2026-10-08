package plugin

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"

	"github.com/sirupsen/logrus"
)

const (
	MvnCmd       = "mvn"
	MvnConfig    = "mvn-config"
	BuildPublish = "build-publish"
	Deploy       = "deploy"
	Publish      = "publish"
	GradleConfig = "gradle-config"
	GradleCmd    = "gradle"
	NpmConfig    = "npm-config"
	NpmCmd       = "npm"
	tmpServerId  = "tmpServerId"
)

func HandleRtCommands(ctx context.Context, args Args) error {
	artifactoryURL, err := normalizeArtifactoryURL(args.URL)
	if err != nil {
		return err
	}
	args.URL = artifactoryURL

	commandsList, err := GetRtCommandsList(args)
	if err != nil {
		logrus.Println("Error Unable to get rt commands list err = ", err)
		return err
	}
	defer cleanupTemporarySpecs(commandsList)

	err = WriteKnownGoodServerCertsForTls(args)
	if err != nil {
		logrus.Println("Error Unable to write TLS certs err = ", err)
		return err
	}

	for index, cmd := range commandsList {
		execArgs := []string{getJfrogBin()}
		execArgs = append(execArgs, cmd...)
		logrus.Printf("Running Artifactory command %d/%d: %s", index+1, len(commandsList), commandLabel(execArgs))
		err := ExecCommand(ctx, args, execArgs)
		if err != nil {
			logrus.Println("Error Unable to run err = ", err)
			return err
		}
	}

	if (args.PublishBuildInfo || args.Command == Publish) && args.Command != "publish-build-info" {
		if err := publishBuildInfo(ctx, args); err != nil {
			logrus.Println("Error publishing build info: ", err)
			return err
		}
	}

	return nil
}

func WriteKnownGoodServerCertsForTls(args Args) error {
	insecure := parseBoolOrDefault(false, args.Insecure)
	if insecure || args.PEMFileContents == "" {
		return nil
	}

	path := args.PEMFilePath
	if path == "" {
		if runtime.GOOS == "windows" {
			path = "C:/users/ContainerAdministrator/.jfrog/security/certs/cert.pem"
		} else {
			path = "/root/.jfrog/security/certs/cert.pem"
		}
	}
	logrus.Printf("Writing pem file at %q\n", path)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("error creating pem folder: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".cert-*.tmp")
	if err != nil {
		return fmt.Errorf("error creating temporary pem file: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0600); err != nil {
		temp.Close()
		return fmt.Errorf("error setting pem permissions: %w", err)
	}
	if _, err := temp.WriteString(args.PEMFileContents); err != nil {
		temp.Close()
		return fmt.Errorf("error writing pem file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("error closing pem file: %w", err)
	}
	// Windows Rename does not replace an existing destination. Remove it only
	// after the complete replacement has been written and closed.
	if runtime.GOOS == "windows" {
		_ = os.Remove(path)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("error replacing pem file: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		return fmt.Errorf("error securing pem file: %w", err)
	}
	logrus.Printf("Successfully wrote pem file at %q\n", path)
	return nil
}

func GetRtCommandsList(args Args) ([][]string, error) {
	logrus.Println("Handling rt command handleRtCommand")
	logrus.Println("Checking GetRtCommandsList args.Command ", args.Command)

	var (
		commandsList [][]string
		err          error
	)
	switch args.Command {
	case "download":
		logIgnoredBuildTool(args)
		logrus.Println("download start")
		commandsList, err = GetDownloadCommandArgs(args)
	case "cleanup":
		logIgnoredBuildTool(args)
		logrus.Println("cleanup start")
		commandsList, err = GetCleanupCommandArgs(args)
	case "scan":
		logIgnoredBuildTool(args)
		logrus.Println("scan start")
		commandsList, err = GetScanCommandArgs(args)
	case "publish-build-info":
		logIgnoredBuildTool(args)
		logrus.Println("publish-build-info start")
		commandsList, err = GetBuildInfoPublishCommandArgs(args)
	case "promote":
		logIgnoredBuildTool(args)
		logrus.Println("promote start")
		commandsList, err = GetPromoteCommandArgs(args)
	case "add-build-dependencies":
		logIgnoredBuildTool(args)
		logrus.Println("add-build-dependencies start")
		commandsList, err = GetAddDependenciesCommandArgs(args)
	case "build-discard":
		logIgnoredBuildTool(args)
		logrus.Println("build-discard start")
		commandsList, err = GetBuildDiscardCommandArgs(args)
	default:
		commandsList, err = getBuildToolCommands(args)
	}
	if err != nil {
		return nil, err
	}
	if len(commandsList) == 0 {
		return nil, fmt.Errorf(
			"no Artifactory commands generated for build_tool/command combination: %q/%q",
			args.BuildTool,
			args.Command,
		)
	}
	return commandsList, nil
}

func logIgnoredBuildTool(args Args) {
	if args.BuildTool != "" {
		logrus.Warnf(
			"build_tool %q is ignored for standalone command %q; remove it because this compatibility behavior is deprecated",
			args.BuildTool,
			args.Command,
		)
	}
}

func getBuildToolCommands(args Args) ([][]string, error) {
	switch {
	case args.BuildTool == MvnCmd && (args.Command == "" || args.Command == "build"):
		logrus.Println("mvn build start")
		return GetMavenBuildCommandArgs(args)
	case args.BuildTool == MvnCmd && args.Command == Publish:
		return GetMavenPublishCommand(args)
	case args.BuildTool == GradleCmd && (args.Command == "" || args.Command == "build"):
		logrus.Println("Gradle build start")
		return GetGradleCommandArgs(args)
	case args.BuildTool == GradleCmd && args.Command == Publish:
		logrus.Println("Gradle publish start")
		return GetGradlePublishCommand(args)
	case args.BuildTool == NpmCmd:
		logrus.Println("npm operation start")
		return GetNpmCommandArgs(args)
	default:
		return nil, fmt.Errorf(
			"unsupported build_tool/command combination: %q/%q",
			args.BuildTool,
			args.Command,
		)
	}
}

func GetShellForOs(osName string) (string, string, error) {
	return resolveShell(osName, exec.LookPath, os.Stat)
}

type JsonTagToExeFlagMapStringItem struct {
	FlagName         string
	PluginArgJsonTag string
	IsMandatory      bool
	StopOnError      bool
}

func PopulateArgs(tmpCommandsList *[]string, args *Args,
	jsonTagToExeFlagMapStringItemList []JsonTagToExeFlagMapStringItem) error {

	for _, jsonTagToExeFlagMapStringItem := range jsonTagToExeFlagMapStringItemList {
		flagName := jsonTagToExeFlagMapStringItem.FlagName
		pluginArgJsonTag := jsonTagToExeFlagMapStringItem.PluginArgJsonTag
		pluginArgValue, err := GetFieldAddress[*Args, string](args, pluginArgJsonTag)

		if err != nil {
			if jsonTagToExeFlagMapStringItem.IsMandatory || jsonTagToExeFlagMapStringItem.StopOnError {
				logrus.Println("GetFieldAddress error: ", err)
				return err
			}
			//logrus.Println("GetFieldAddress error: ", err)
			continue
		}

		if pluginArgValue == nil {
			if jsonTagToExeFlagMapStringItem.IsMandatory || jsonTagToExeFlagMapStringItem.StopOnError {
				logrus.Println("missing mandatory field: ", pluginArgJsonTag)
				return fmt.Errorf("missing mandatory field %s", pluginArgJsonTag)
			}
			logrus.Println("missing mandatory field: ", pluginArgJsonTag)
			continue
		}

		if pluginArgValue == nil &&
			jsonTagToExeFlagMapStringItem.IsMandatory || jsonTagToExeFlagMapStringItem.StopOnError {
			logrus.Println("missing mandatory field: ", pluginArgJsonTag)
			return fmt.Errorf("missing mandatory field %s", pluginArgJsonTag)
		}
		AppendStringArg(tmpCommandsList, flagName, pluginArgValue)
	}

	return nil
}

func AppendStringArg(argsList *[]string, argName string, argValue *string) {

	if argsList == nil {
		logrus.Println("argsList is nil")
		return
	}

	if argValue == nil {
		logrus.Println("argValue is nil")
		return
	}

	if len(*argValue) > 0 {
		*argsList = append(*argsList, argName+*argValue)
	}
}

var tagFieldCache sync.Map

func precomputeTagMapping(structType reflect.Type) map[string]int {
	tagMap := make(map[string]int)
	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)
		tag := field.Tag.Get("envconfig")
		if tag != "" {
			tagMap[tag] = i
		}
	}
	return tagMap
}

func getTagMapping(structType reflect.Type) map[string]int {
	if cachedMapping, ok := tagFieldCache.Load(structType); ok {
		return cachedMapping.(map[string]int)
	}

	tagMap := precomputeTagMapping(structType)
	tagFieldCache.Store(structType, tagMap)
	return tagMap
}

func GetFieldAddress[ST, VT any](args ST, argJsonTag string) (*VT, error) {
	v := reflect.ValueOf(args)
	if v.Kind() != reflect.Ptr {
		return nil, fmt.Errorf("args must be a pointer to a struct; got %T", args)
	}
	if v.Elem().Kind() != reflect.Struct {
		return nil, fmt.Errorf("args must point to a struct; got pointer to %s", v.Elem().Kind())
	}

	v = v.Elem()
	t := v.Type()

	tagMap := getTagMapping(t)

	fieldIndex, found := tagMap[argJsonTag]
	if !found {
		return nil, fmt.Errorf("field with tag '%s' not found in struct type '%s'", argJsonTag, t.Name())
	}

	fieldValue := v.Field(fieldIndex)
	if fieldValue.CanAddr() {
		if fieldValue.Type().AssignableTo(reflect.TypeOf((*VT)(nil)).Elem()) {
			return fieldValue.Addr().Interface().(*VT), nil
		}
		return nil, fmt.Errorf("field with tag '%s' in struct '%s' is not of type '%T'; actual type is '%s'",
			argJsonTag, t.Name(), new(VT), fieldValue.Type().String())
	}

	return nil, fmt.Errorf("field with tag '%s' in struct '%s' cannot be addressed", argJsonTag, t.Name())
}

func GetConfigAddConfigCommandArgs(srvConfigStr, userName, password, url,
	accessToken, apiKey string) ([]string, error) {

	if srvConfigStr == "" {
		srvConfigStr = tmpServerId
	}
	platformURL, err := normalizePlatformURL(url)
	if err != nil {
		return nil, err
	}

	authParams, err := setAuthParams([]string{}, Args{Username: userName,
		Password: password, AccessToken: accessToken, APIKey: apiKey})
	if err != nil {
		logrus.Println("setAuthParams error: ", err)
		return []string{""}, err
	}

	cfgCommand := []string{"config", "add", srvConfigStr, "--url=" + platformURL}
	cfgCommand = append(cfgCommand, authParams...)
	cfgCommand = append(cfgCommand, "--interactive=false")
	return cfgCommand, nil
}

func IsBuildDiscardArgs(args Args) bool {
	if len(args.Async) > 0 ||
		len(args.DeleteArtifacts) > 0 ||
		len(args.ExcludeBuilds) > 0 ||
		len(args.MaxBuilds) > 0 ||
		len(args.MaxDays) > 0 {
		return true
	}
	return false
}
