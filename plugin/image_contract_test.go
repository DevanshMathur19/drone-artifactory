package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAllPlatformImagesUsePortableEntrypoint(t *testing.T) {
	dockerfiles, err := filepath.Glob(filepath.Join("..", "docker", "Dockerfile.*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(dockerfiles) != 20 {
		t.Fatalf("found %d platform Dockerfiles, want 20", len(dockerfiles))
	}

	for _, dockerfile := range dockerfiles {
		t.Run(filepath.Base(dockerfile), func(t *testing.T) {
			contentBytes, err := os.ReadFile(dockerfile)
			if err != nil {
				t.Fatal(err)
			}
			content := string(contentBytes)
			if !strings.Contains(content, `ENTRYPOINT ["drone-artifactory"]`) {
				t.Fatal("entrypoint is not the shared PATH-resolved command")
			}
			if strings.Contains(filepath.Base(dockerfile), ".windows.") {
				if !strings.Contains(content, "C:/bin/drone-artifactory.exe") {
					t.Fatal("Windows plugin executable is not installed in C:/bin")
				}
				if !strings.Contains(content, `PATH="C:\\bin;`) {
					t.Fatal("Windows plugin directory is not first on PATH")
				}
				return
			}
			if !strings.Contains(content, "/usr/local/bin/drone-artifactory") {
				t.Fatal("Linux plugin executable is not installed on PATH")
			}
		})
	}
}
