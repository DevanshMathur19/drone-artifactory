// Copyright 2020 the Drone Authors. All rights reserved.
// Use of this source code is governed by the Blue Oak Model License
// that can be found in the LICENSE file.

package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// writeArtifactFile
// ---------------------------------------------------------------------------

func TestWriteArtifactFile_WritesCorrectJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact.json")
	t.Setenv("PLUGIN_ARTIFACT_FILE", path)

	entries := []fileArtifactEntry{
		{Name: "app.jar", URL: "https://example.jfrog.io/artifactory/libs/app.jar", FilePath: "libs/app.jar", Digest: "sha256:abc123"},
	}
	writeArtifactFile(entries)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("artifact file not written: %v", err)
	}
	var got artifactFile
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got.Kind != "fileUpload/v1" {
		t.Errorf("kind: want fileUpload/v1, got %q", got.Kind)
	}
	if len(got.Data.FileArtifacts) != 1 {
		t.Fatalf("fileArtifacts length: want 1, got %d", len(got.Data.FileArtifacts))
	}
	e := got.Data.FileArtifacts[0]
	if e.Name != "app.jar" {
		t.Errorf("name: want app.jar, got %q", e.Name)
	}
	if e.Digest != "sha256:abc123" {
		t.Errorf("digest: want sha256:abc123, got %q", e.Digest)
	}
}

func TestWriteArtifactFile_NoOpWhenEnvUnset(t *testing.T) {
	t.Setenv("PLUGIN_ARTIFACT_FILE", "")
	// Should not panic or create any file.
	writeArtifactFile([]fileArtifactEntry{{Name: "x", URL: "y"}})
}

func TestWriteArtifactFile_CreatesParentDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "artifact.json")
	t.Setenv("PLUGIN_ARTIFACT_FILE", path)

	writeArtifactFile([]fileArtifactEntry{{Name: "f", URL: "u"}})

	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected file at %s, got: %v", path, err)
	}
}

// ---------------------------------------------------------------------------
// computeFileSHA256
// ---------------------------------------------------------------------------

func TestComputeFileSHA256_KnownHash(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(f, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	// echo -n "hello" | sha256sum → 2cf24dba...
	const want = "sha256:2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	got := computeFileSHA256(f)
	if got != want {
		t.Errorf("SHA256: want %q, got %q", want, got)
	}
}

func TestComputeFileSHA256_ReturnsEmptyOnMissingFile(t *testing.T) {
	got := computeFileSHA256("/nonexistent/path/file.txt")
	if got != "" {
		t.Errorf("expected empty string for missing file, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// parseJFrogDetailedSummary
// ---------------------------------------------------------------------------

const baseURL = "https://example.jfrog.io/artifactory/"

// jfrogOutput builds a realistic --detailed-summary stdout that contains log
// lines before and after the JSON block.
func jfrogOutput(json string) []byte {
	return []byte("[Info] Uploading artifact: local/file.jar\n" +
		"[Info] Done uploading 1 artifact.\n" +
		json + "\n" +
		"[Info] Some trailing log line.\n")
}

func TestParseJFrogDetailedSummary_FlatSHA256(t *testing.T) {
	raw := jfrogOutput(`{
  "status": "success",
  "totals": {"success": 1, "failure": 0},
  "files": [{
    "source": "myfolder/app.jar",
    "target": "libs-release/app.jar",
    "sha256": "deadbeef"
  }]
}`)

	got := parseJFrogDetailedSummary(raw, baseURL)
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	e := got[0]
	if e.Name != "app.jar" {
		t.Errorf("name: want app.jar, got %q", e.Name)
	}
	if e.FilePath != "libs-release/app.jar" {
		t.Errorf("filePath: want libs-release/app.jar, got %q", e.FilePath)
	}
	if e.URL != "https://example.jfrog.io/artifactory/libs-release/app.jar" {
		t.Errorf("url: got %q", e.URL)
	}
	if e.Digest != "sha256:deadbeef" {
		t.Errorf("digest: want sha256:deadbeef, got %q", e.Digest)
	}
}

func TestParseJFrogDetailedSummary_RealJFrogCLIShape(t *testing.T) {
	// Mirrors the actual `jf rt u --detailed-summary=true` JSON shape (source/
	// target/sha256 at the top level of each file entry - see
	// https://docs.jfrog.com/artifactory/docs/jf-rt-output-format).
	raw := jfrogOutput(`{
  "status": "success",
  "totals": {"success": 1, "failure": 0},
  "files": [{
    "source": "build/lib.jar",
    "target": "repo/lib.jar",
    "sha256": "cafebabe"
  }]
}`)

	got := parseJFrogDetailedSummary(raw, baseURL)
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	if got[0].Digest != "sha256:cafebabe" {
		t.Errorf("digest: want sha256:cafebabe, got %q", got[0].Digest)
	}
}

func TestParseJFrogDetailedSummary_MultipleFiles(t *testing.T) {
	raw := jfrogOutput(`{
  "status": "success",
  "totals": {"success": 3, "failure": 0},
  "files": [
    {"source": "myfolder/a.jar", "target": "pcf/newdevtest/a.jar", "sha256": "aaa"},
    {"source": "myfolder/b.jar", "target": "pcf/newdevtest/b.jar", "sha256": "bbb"},
    {"source": "myfolder/c.jar", "target": "pcf/newdevtest/c.jar", "sha256": "ccc"}
  ]
}`)

	got := parseJFrogDetailedSummary(raw, baseURL)
	if len(got) != 3 {
		t.Fatalf("want 3 entries, got %d", len(got))
	}
	names := []string{got[0].Name, got[1].Name, got[2].Name}
	for i, want := range []string{"a.jar", "b.jar", "c.jar"} {
		if names[i] != want {
			t.Errorf("entry[%d].name: want %s, got %s", i, want, names[i])
		}
	}
}

func TestParseJFrogDetailedSummary_SkipsEmptyPaths(t *testing.T) {
	// An entry without source/target should be silently skipped.
	raw := jfrogOutput(`{
  "status": "success",
  "totals": {"success": 2, "failure": 0},
  "files": [
    {"source": "", "target": "", "sha256": "aaa"},
    {"source": "src/good.jar", "target": "repo/good.jar", "sha256": "bbb"}
  ]
}`)

	got := parseJFrogDetailedSummary(raw, baseURL)
	if len(got) != 1 {
		t.Fatalf("want 1 entry (empty-path entry skipped), got %d", len(got))
	}
	if got[0].Name != "good.jar" {
		t.Errorf("name: want good.jar, got %q", got[0].Name)
	}
}

func TestParseJFrogDetailedSummary_ReturnsNilOnFailure(t *testing.T) {
	cases := []struct {
		name   string
		output []byte
	}{
		{"no status key", []byte(`{"files":[]}`)},
		{"status not success", jfrogOutput(`{"status":"failure","files":[]}`)},
		{"empty files array", jfrogOutput(`{"status":"success","files":[]}`)},
		{"no JSON at all", []byte("[Info] just log output\n")},
		{"malformed JSON", jfrogOutput(`{broken json`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseJFrogDetailedSummary(tc.output, baseURL)
			if got != nil {
				t.Errorf("want nil, got %v", got)
			}
		})
	}
}

func TestParseJFrogDetailedSummary_BaseURLTrailingSlashNormalised(t *testing.T) {
	raw := jfrogOutput(`{"status":"success","files":[{"source":"src/f.jar","target":"repo/f.jar","sha256":"x"}]}`)

	// Pass base URL both with and without trailing slash — result should be the same.
	withSlash := parseJFrogDetailedSummary(raw, "https://example.jfrog.io/artifactory/")
	withoutSlash := parseJFrogDetailedSummary(raw, "https://example.jfrog.io/artifactory")

	if len(withSlash) != 1 || len(withoutSlash) != 1 {
		t.Fatal("expected 1 entry each")
	}
	if withSlash[0].URL != withoutSlash[0].URL {
		t.Errorf("URL mismatch: %q vs %q", withSlash[0].URL, withoutSlash[0].URL)
	}
}

func TestParseJFrogDetailedSummary_AbsoluteTargetURL(t *testing.T) {
	raw := jfrogOutput(`{
  "status": "success",
  "totals": {"success": 1, "failure": 0},
  "files": [{
    "source": "myfolder/app.jar",
    "target": "https://example.jfrog.io/artifactory/libs-release/app.jar",
    "sha256": "deadbeef"
  }]
}`)

	got := parseJFrogDetailedSummary(raw, baseURL)
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	e := got[0]
	if e.URL != "https://example.jfrog.io/artifactory/libs-release/app.jar" {
		t.Errorf("url: connector base URL must not be duplicated, got %q", e.URL)
	}
	if e.FilePath != "libs-release/app.jar" {
		t.Errorf("filePath: want repo-relative path, got %q", e.FilePath)
	}
	if e.Name != "app.jar" {
		t.Errorf("name: want app.jar, got %q", e.Name)
	}
	if e.Digest != "sha256:deadbeef" {
		t.Errorf("digest: want sha256:deadbeef, got %q", e.Digest)
	}
}

func TestParseJFrogDetailedSummary_AbsoluteTargetURLDifferentBase(t *testing.T) {
	// Absolute target URL not rooted at the connector base URL: use it as-is
	// for the URL and derive filePath from the URL path portion.
	raw := jfrogOutput(`{
  "status": "success",
  "files": [{
    "source": "dist/app.jar",
    "target": "https://other.jfrog.io/artifactory/repo/app.jar",
    "sha256": "abc"
  }]
}`)

	got := parseJFrogDetailedSummary(raw, baseURL)
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	if got[0].URL != "https://other.jfrog.io/artifactory/repo/app.jar" {
		t.Errorf("url: got %q", got[0].URL)
	}
	if got[0].FilePath != "artifactory/repo/app.jar" {
		t.Errorf("filePath: want URL path portion, got %q", got[0].FilePath)
	}
}

func TestResolveArtifactURLAndPath(t *testing.T) {
	cases := []struct {
		name     string
		base     string
		target   string
		wantURL  string
		wantPath string
	}{
		{
			name:     "relative target",
			base:     "https://example.jfrog.io/artifactory",
			target:   "repo/f.jar",
			wantURL:  "https://example.jfrog.io/artifactory/repo/f.jar",
			wantPath: "repo/f.jar",
		},
		{
			name:     "relative target with leading slash",
			base:     "https://example.jfrog.io/artifactory",
			target:   "/repo/f.jar",
			wantURL:  "https://example.jfrog.io/artifactory/repo/f.jar",
			wantPath: "/repo/f.jar",
		},
		{
			name:     "absolute target under base",
			base:     "https://example.jfrog.io/artifactory",
			target:   "https://example.jfrog.io/artifactory/repo/f.jar",
			wantURL:  "https://example.jfrog.io/artifactory/repo/f.jar",
			wantPath: "repo/f.jar",
		},
		{
			name:     "absolute target outside base",
			base:     "https://example.jfrog.io/artifactory",
			target:   "https://other.io/artifactory/repo/f.jar",
			wantURL:  "https://other.io/artifactory/repo/f.jar",
			wantPath: "artifactory/repo/f.jar",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotURL, gotPath := resolveArtifactURLAndPath(tc.base, tc.target)
			if gotURL != tc.wantURL {
				t.Errorf("url: want %q, got %q", tc.wantURL, gotURL)
			}
			if gotPath != tc.wantPath {
				t.Errorf("filePath: want %q, got %q", tc.wantPath, gotPath)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// resolveSource
// ---------------------------------------------------------------------------

func TestResolveSource_DirectoryWithTrailingSlash(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "a")
	writeFile(t, filepath.Join(dir, "b.txt"), "b")

	files, root := resolveSource(dir + "/")
	if len(files) != 2 {
		t.Errorf("want 2 files, got %d", len(files))
	}
	if root == "" {
		t.Error("sourceRoot should not be empty for a directory source")
	}
}

func TestResolveSource_DirectoryWithoutTrailingSlash(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "x.jar"), "x")

	files, root := resolveSource(dir)
	if len(files) != 1 {
		t.Errorf("want 1 file, got %d", len(files))
	}
	if root == "" {
		t.Error("sourceRoot should not be empty")
	}
}

func TestResolveSource_SingleFilePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "only.jar")
	writeFile(t, path, "data")

	files, root := resolveSource(path)
	if len(files) != 1 {
		t.Fatalf("want 1, got %d", len(files))
	}
	if root != "" {
		t.Errorf("sourceRoot should be empty for a single file, got %q", root)
	}
}

func TestResolveSource_GlobPattern(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "lib1.jar"), "1")
	writeFile(t, filepath.Join(dir, "lib2.jar"), "2")
	writeFile(t, filepath.Join(dir, "readme.txt"), "r")

	files, _ := resolveSource(filepath.Join(dir, "*.jar"))
	if len(files) != 2 {
		t.Errorf("want 2 .jar files, got %d", len(files))
	}
}

func TestResolveSource_NonexistentPath(t *testing.T) {
	files, root := resolveSource("/nonexistent/path/")
	if len(files) != 0 {
		t.Errorf("want 0 files, got %d", len(files))
	}
	if root == "" {
		// root is set even for nonexistent dirs (by design), that's fine
	}
}

// ---------------------------------------------------------------------------
// collectArtifactEntries
// ---------------------------------------------------------------------------

func TestCollectArtifactEntries_Directory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "file1.txt"), "hello")
	writeFile(t, filepath.Join(dir, "file2.txt"), "world")

	args := Args{
		URL:    "https://jfrog.example.com/artifactory/",
		Source: dir + "/",
		Target: "myrepo/builds/",
	}
	entries := collectArtifactEntries(args)
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.Name == "" {
			t.Error("name should not be empty")
		}
		if e.URL == "" {
			t.Error("url should not be empty")
		}
		if e.FilePath == "" {
			t.Error("filePath should not be empty")
		}
		if e.Digest == "" {
			t.Error("digest should not be empty for existing files")
		}
	}
}

func TestCollectArtifactEntries_URLContainsTarget(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "app.jar"), "data")

	args := Args{
		URL:    "https://jfrog.example.com/artifactory/",
		Source: dir + "/",
		Target: "libs-release/",
	}
	entries := collectArtifactEntries(args)
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	e := entries[0]
	want := "https://jfrog.example.com/artifactory/libs-release/app.jar"
	if e.URL != want {
		t.Errorf("url: want %q, got %q", want, e.URL)
	}
	if e.FilePath != "libs-release/app.jar" {
		t.Errorf("filePath: want libs-release/app.jar, got %q", e.FilePath)
	}
}

func TestCollectArtifactEntries_FlatMode(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(sub, "deep.jar"), "d")

	args := Args{
		URL:    "https://jfrog.example.com/artifactory/",
		Source: dir + "/",
		Target: "repo/",
		Flat:   "true",
	}
	entries := collectArtifactEntries(args)
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	// With flat=true the subdirectory path is stripped; only the filename remains.
	if entries[0].FilePath != "repo/deep.jar" {
		t.Errorf("filePath: want repo/deep.jar, got %q", entries[0].FilePath)
	}
}

func TestCollectArtifactEntries_ReturnsNilWhenSpecSet(t *testing.T) {
	args := Args{URL: "https://x.io/", Source: "src/", Target: "repo/", Spec: "/tmp/spec.json"}
	if got := collectArtifactEntries(args); got != nil {
		t.Errorf("want nil when Spec is set, got %v", got)
	}
}

func TestCollectArtifactEntries_ReturnsNilWhenSourceEmpty(t *testing.T) {
	args := Args{URL: "https://x.io/", Target: "repo/"}
	if got := collectArtifactEntries(args); got != nil {
		t.Errorf("want nil when Source is empty, got %v", got)
	}
}

func TestCollectArtifactEntries_ReturnsNilWhenTargetEmpty(t *testing.T) {
	args := Args{URL: "https://x.io/", Source: "src/"}
	if got := collectArtifactEntries(args); got != nil {
		t.Errorf("want nil when Target is empty, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
