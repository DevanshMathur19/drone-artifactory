package plugin

import (
	"fmt"
)

var supportedNpmCommands = map[string]struct{}{
	"install": {},
	"ci":      {},
	Publish:   {},
}

func GetNpmCommandArgs(args Args) ([][]string, error) {
	if _, supported := supportedNpmCommands[args.Command]; !supported {
		return nil, fmt.Errorf("unsupported npm command %q; expected install, ci, or publish", args.Command)
	}
	if args.RepoResolve == "" {
		return nil, fmt.Errorf("repo_resolve needs to be set for npm")
	}
	if args.RepoDeploy == "" {
		return nil, fmt.Errorf("repo_deploy needs to be set for npm")
	}
	if args.PublishBuildInfo && (args.BuildName == "" || args.BuildNumber == "") {
		return nil, fmt.Errorf("build_name and build_number need to be set when publishing npm build info")
	}

	resolverID := args.ResolverId
	if resolverID == "" {
		resolverID = tmpServerId + "-npm-resolve"
	}
	deployerID := args.DeployerId
	if deployerID == "" {
		deployerID = resolverID
	}

	commands := make([][]string, 0, 4)
	resolverConfig, err := GetConfigAddConfigCommandArgs(
		resolverID,
		args.Username,
		args.Password,
		args.URL,
		args.AccessToken,
		args.APIKey,
	)
	if err != nil {
		return nil, err
	}
	commands = append(commands, resolverConfig)
	if deployerID != resolverID {
		deployerConfig, err := GetConfigAddConfigCommandArgs(
			deployerID,
			args.Username,
			args.Password,
			args.URL,
			args.AccessToken,
			args.APIKey,
		)
		if err != nil {
			return nil, err
		}
		commands = append(commands, deployerConfig)
	}

	commands = append(commands, []string{
		NpmConfig,
		"--repo-resolve=" + args.RepoResolve,
		"--repo-deploy=" + args.RepoDeploy,
		"--server-id-resolve=" + resolverID,
		"--server-id-deploy=" + deployerID,
	})

	npmCommand := []string{NpmCmd, args.Command}
	if args.BuildName != "" {
		npmCommand = append(npmCommand, "--build-name="+args.BuildName)
	}
	if args.BuildNumber != "" {
		npmCommand = append(npmCommand, "--build-number="+args.BuildNumber)
	}
	if args.Project != "" {
		npmCommand = append(npmCommand, "--project="+args.Project)
	}
	if args.Module != "" {
		npmCommand = append(npmCommand, "--module="+args.Module)
	}
	commands = append(commands, npmCommand)
	return commands, nil
}
