package plugin

import (
	"strings"
	"testing"
)

const (
	RtModule  = "backend_module"
	RtProject = "backend_project"
)

func TestGetDownloadCommandUserPassword(t *testing.T) {
	args := Args{
		Username:    "ab",
		Password:    "cd",
		Command:     "download",
		BuildName:   RtBuildName,
		BuildNumber: RtBuildNumber,
		URL:         RtUrlTestStr,
		Module:      RtModule,
		Project:     RtProject,
		SpecPath:    "spec.json",
	}
	cmdList, err := GetDownloadCommandArgs(args)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	wantCmds := []string{
		"rt download --user $PLUGIN_USERNAME --password $PLUGIN_PASSWORD --flat=false " + "--build-name=t2 --build-number=v1.0 " +
			"--module=backend_module --project=backend_project --url=https://artifactory.test.io/artifactory/ --spec=spec.json",
	}

	for i, cmd := range cmdList {
		cmdStr := strings.Join(cmd, " ")
		if !strings.Contains(normalizeCommandForTest(cmdStr, wantCmds[i]), wantCmds[i]) {
			t.Errorf("Expected: |%s|, Got: |%s|", wantCmds[i], cmdStr)
		}
	}
}

func TestGetDownloadCommandUserAccessToken(t *testing.T) {
	args := Args{
		AccessToken: RtAccessToken,
		Command:     "download",
		BuildName:   RtBuildName,
		BuildNumber: RtBuildNumber,
		URL:         RtUrlTestStr,
		Module:      RtModule,
		Project:     RtProject,
		SpecPath:    "spec.json",
	}
	cmdList, err := GetDownloadCommandArgs(args)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	wantCmds := []string{
		"rt download --access-token $PLUGIN_ACCESS_TOKEN --flat=false --build-name=t2 --build-number=v1.0 --module=backend_module" +
			" --project=backend_project --url=https://artifactory.test.io/artifactory/ --spec=spec.json",
	}

	for i, cmd := range cmdList {
		cmdStr := strings.Join(cmd, " ")
		if !strings.Contains(normalizeCommandForTest(cmdStr, wantCmds[i]), wantCmds[i]) {
			t.Errorf("Expected: |%s|, Got: |%s|", wantCmds[i], cmdStr)
		}
	}
}

func TestGetDownloadCommandCarriesFlatSetting(t *testing.T) {
	for _, test := range []struct {
		name string
		flat string
		want string
	}{
		{name: "true", flat: "true", want: "--flat=true"},
		{name: "false", flat: "false", want: "--flat=false"},
		{name: "invalid defaults false", flat: "not-a-boolean", want: "--flat=false"},
	} {
		t.Run(test.name, func(t *testing.T) {
			commands, err := GetDownloadCommandArgs(Args{
				AccessToken: RtAccessToken,
				URL:         RtUrlTestStr,
				Source:      "generic-local/nested/*.zip",
				Target:      "downloads/",
				Flat:        test.flat,
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(commands[0], " "); !strings.Contains(got, test.want) {
				t.Fatalf("download command does not contain %s: %s", test.want, got)
			}
		})
	}
}

func TestGetCleanupCommandUserPassword(t *testing.T) {
	args := Args{
		Username:    "ab",
		Password:    "cd",
		Command:     "cleanup",
		BuildName:   RtBuildName,
		BuildNumber: RtBuildNumber,
		URL:         RtUrlTestStr,
	}
	cmdList, err := GetCleanupCommandArgs(args)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	wantCmds := []string{
		"rt build-clean t2 v1.0",
	}

	for i, cmd := range cmdList {
		cmdStr := strings.Join(cmd, " ")
		if !strings.Contains(cmdStr, wantCmds[i]) {
			t.Errorf("Expected: |%s|, Got: |%s|", wantCmds[i], cmdStr)
		}
	}
}
