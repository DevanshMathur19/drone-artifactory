package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var DownloadCmdJsonTagToExeFlagMapStringItemList = []JsonTagToExeFlagMapStringItem{
	{"--build-name=", "PLUGIN_BUILD_NAME", false, false},
	{"--build-number=", "PLUGIN_BUILD_NUMBER", false, false},
	{"--module=", "PLUGIN_MODULE", false, false},
	{"--project=", "PLUGIN_PROJECT", false, false},
	{"--url=", "PLUGIN_URL", false, false},
	{"--spec=", "PLUGIN_SPEC", false, false},
	{"--spec=", "PLUGIN_SPEC_PATH", false, false},
}

func GetDownloadCommandArgs(args Args) ([][]string, error) {

	var cmdList [][]string
	downloadCommandArgs := []string{"rt", "download"}

	authParams, err := setAuthParams([]string{}, Args{Username: args.Username,
		Password: args.Password, AccessToken: args.AccessToken, APIKey: args.APIKey})
	if err != nil {
		return cmdList, err
	}

	if args.Spec != "" {
		fileName, tempErr := writeTemporarySpec(args.Spec)
		err = tempErr
		if err != nil {
			return cmdList, err
		}
		args.Spec = ""
		args.SpecPath = fileName
	} else if args.SpecPath == "" {
		if args.Source == "" {
			return nil, fmt.Errorf("download source needs to be set when no spec is provided")
		}
		if args.Target == "" {
			return nil, fmt.Errorf("download target needs to be set when no spec is provided")
		}
	}

	downloadCommandArgs = append(downloadCommandArgs, authParams...)
	if args.SpecPath == "" {
		downloadCommandArgs = append(downloadCommandArgs, args.Source, args.Target)
	}

	err = PopulateArgs(&downloadCommandArgs, &args, DownloadCmdJsonTagToExeFlagMapStringItemList)
	if err != nil {
		return cmdList, err
	}

	cmdList = append(cmdList, downloadCommandArgs)
	return cmdList, nil
}

func GetCleanupCommandArgs(args Args) ([][]string, error) {
	var cmdList [][]string
	cleanupCommandArgs := []string{"rt", "build-clean", args.BuildName, args.BuildNumber}
	cmdList = append(cmdList, cleanupCommandArgs)
	return cmdList, nil
}

func writeToFile(filePath, content string) error {
	file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to open file: %v", err)
	}
	defer file.Close()

	_, err = file.WriteString(content)
	if err != nil {
		return fmt.Errorf("failed to write to file: %v", err)
	}

	return nil
}

func writeTemporarySpec(content string) (string, error) {
	file, err := os.CreateTemp("", "drone-artifactory-*.spec.json")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary spec: %w", err)
	}
	path := file.Name()
	if err := file.Chmod(0600); err != nil {
		file.Close()
		os.Remove(path)
		return "", fmt.Errorf("failed to secure temporary spec: %w", err)
	}
	if _, err := file.WriteString(content); err != nil {
		file.Close()
		os.Remove(path)
		return "", fmt.Errorf("failed to write temporary spec: %w", err)
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", fmt.Errorf("failed to close temporary spec: %w", err)
	}
	return path, nil
}

func cleanupTemporarySpecs(commands [][]string) {
	prefix := filepath.Clean(os.TempDir()) + string(os.PathSeparator)
	for _, command := range commands {
		for _, arg := range command {
			if !strings.HasPrefix(arg, "--spec=") {
				continue
			}
			path := filepath.Clean(strings.TrimPrefix(arg, "--spec="))
			if strings.HasPrefix(path, prefix) &&
				strings.HasPrefix(filepath.Base(path), "drone-artifactory-") &&
				strings.HasSuffix(path, ".spec.json") {
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					fmt.Fprintf(os.Stderr, "failed to remove temporary Artifactory spec %q: %v\n", path, err)
				}
			}
		}
	}
}

func getTimestampedFileName() string {
	timestamp := time.Now().Format("20060102_150405.000")
	fileName := fmt.Sprintf("%s_spec.json", timestamp)
	return fileName
}
