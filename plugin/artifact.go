// Copyright 2020 the Drone Authors. All rights reserved.
// Use of this source code is governed by the Blue Oak Model License
// that can be found in the LICENSE file.

package plugin

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
)

type artifactFile struct {
	Kind string           `json:"kind"`
	Data artifactFileData `json:"data"`
}

type artifactFileData struct {
	FileArtifacts []fileArtifactEntry `json:"fileArtifacts"`
}

type fileArtifactEntry struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	FilePath string `json:"filePath,omitempty"`
	Digest   string `json:"digest,omitempty"`
}

// writeArtifactFile writes the uploaded file metadata to the path in
// PLUGIN_ARTIFACT_FILE so that CI manager can pick it up and store it.
// All errors are soft-failed with a warning so the step is never failed.
func writeArtifactFile(entries []fileArtifactEntry) {
	path := os.Getenv("PLUGIN_ARTIFACT_FILE")
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		logrus.Warnf("artifact: failed to create directory %s: %v", filepath.Dir(path), err)
		return
	}
	payload := artifactFile{
		Kind: "fileUpload/v1",
		Data: artifactFileData{FileArtifacts: entries},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		logrus.Warnf("artifact: failed to marshal metadata: %v", err)
		return
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		logrus.Warnf("artifact: failed to write file %s: %v", path, err)
	}
}

// computeFileSHA256 returns "sha256:<hex>" for the file at path.
// Returns an empty string on error (soft-fail).
func computeFileSHA256(path string) string {
	f, err := os.Open(path)
	if err != nil {
		logrus.Warnf("artifact: failed to open %s for SHA256 computation: %v", path, err)
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		logrus.Warnf("artifact: failed to compute SHA256 for %s: %v", path, err)
		return ""
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// jfrogUploadSummary is the JSON structure emitted by `jf rt u --detailed-summary=true`.
type jfrogUploadSummary struct {
	Status string             `json:"status"`
	Files  []jfrogUploadEntry `json:"files"`
}

type jfrogUploadEntry struct {
	// JFrog CLI's --detailed-summary JSON uses "source"/"target" (not
	// "sourcePath"/"targetPath") for the local and remote paths.
	SourcePath string `json:"source"`
	TargetPath string `json:"target"`
	SHA256     string `json:"sha256"`
}

// findOutermostJSONObject locates the start of the outermost JSON object that
// contains the "status" key — the marker JFrog CLI uses in its upload summary.
// It walks backward from the last occurrence of "status", counting braces to
// skip nested objects, and returns the index of the enclosing '{'.
func findOutermostJSONObject(output []byte) (start int, ok bool) {
	idx := bytes.LastIndex(output, []byte(`"status"`))
	if idx == -1 {
		return 0, false
	}
	depth := 0
	for i := idx - 1; i >= 0; i-- {
		switch output[i] {
		case '}':
			depth++
		case '{':
			if depth == 0 {
				return i, true
			}
			depth--
		}
	}
	return 0, false
}

// parseJFrogDetailedSummary extracts artifact entries from the raw stdout of a
// `jf rt u --detailed-summary=true` run.
// The CLI appends a JSON block; we locate the outermost `{` that contains the
// `"status"` key using brace-counting so nested objects don't throw us off.
// Returns nil on any parse failure (soft-fail).
func parseJFrogDetailedSummary(output []byte, baseURL string) []fileArtifactEntry {
	start, ok := findOutermostJSONObject(output)
	if !ok {
		return nil
	}

	// Use json.Decoder so trailing non-JSON content (more log lines) is ignored.
	dec := json.NewDecoder(bytes.NewReader(output[start:]))
	var summary jfrogUploadSummary
	if err := dec.Decode(&summary); err != nil {
		snippet := output[start:]
		if len(snippet) > 512 {
			snippet = snippet[:512]
		}
		logrus.Warnf("artifact: failed to parse JFrog detailed summary: %v — snippet: %s", err, snippet)
		return nil
	}
	if summary.Status != "success" || len(summary.Files) == 0 {
		return nil
	}
	base := strings.TrimRight(baseURL, "/")
	var entries []fileArtifactEntry
	for _, f := range summary.Files {
		digest := ""
		if f.SHA256 != "" {
			digest = "sha256:" + f.SHA256
		}
		if f.SourcePath == "" || f.TargetPath == "" {
			logrus.Warnf("artifact: JFrog summary entry has empty paths (sourcePath=%q targetPath=%q) — skipping", f.SourcePath, f.TargetPath)
			continue
		}
		artifactURL, filePath := resolveArtifactURLAndPath(base, f.TargetPath)
		entries = append(entries, fileArtifactEntry{
			Name:     filepath.Base(f.SourcePath),
			URL:      artifactURL,
			FilePath: filePath,
			Digest:   digest,
		})
	}
	return entries
}

// resolveArtifactURLAndPath returns the artifact URL and the repo-relative file
// path for a JFrog detailed-summary target. JFrog CLI v2 returns a fully-qualified
// URL in target for uploads, so the connector base URL must not be prepended again;
// the file path is then derived by stripping that base (CI-24977).
func resolveArtifactURLAndPath(base, target string) (string, string) {
	target = strings.TrimSpace(target)
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		if rel, found := strings.CutPrefix(target, base+"/"); found {
			return target, rel
		}
		if u, err := url.Parse(target); err == nil && u.Path != "" {
			return target, strings.TrimLeft(u.Path, "/")
		}
		return target, target
	}
	return base + "/" + strings.TrimLeft(target, "/"), target
}

func collectArtifactEntries(args Args) []fileArtifactEntry {
	if args.Spec != "" || args.Source == "" || args.Target == "" {
		return nil
	}

	flat := parseBoolOrDefault(false, args.Flat)
	baseURL := strings.TrimRight(args.URL, "/")
	targetDir := strings.TrimRight(strings.TrimLeft(filepath.ToSlash(args.Target), "/"), "/")

	files, sourceRoot := resolveSource(args.Source)
	if len(files) == 0 {
		return nil
	}

	var entries []fileArtifactEntry
	for _, f := range files {
		f = filepath.ToSlash(f)
		var relPath string
		if flat {
			relPath = filepath.Base(f)
		} else if sourceRoot != "" {
			// Strip the source directory prefix so myfolder/sub/file.jar → sub/file.jar
			relPath = strings.TrimLeft(strings.TrimPrefix(f, sourceRoot), "/")
			if relPath == "" {
				relPath = filepath.Base(f)
			}
		} else {
			relPath = f
		}
		artifactoryPath := targetDir + "/" + relPath
		entries = append(entries, fileArtifactEntry{
			Name:     filepath.Base(f),
			URL:      baseURL + "/" + artifactoryPath,
			FilePath: artifactoryPath,
			Digest:   computeFileSHA256(f),
		})
	}
	return entries
}

// resolveSource handles three cases:
//  1. Directory path (with or without trailing slash): walk the directory.
//  2. Plain file path (no wildcards): return that single file.
//  3. Glob pattern: expand with filepath.Glob.
//
// Returns the list of matched regular files and the source root directory
// (used to compute relative paths for flat=false uploads).
func resolveSource(pattern string) (files []string, sourceRoot string) {
	// Normalise separators and trim trailing slash for directory detection.
	pattern = filepath.ToSlash(pattern)
	trimmed := strings.TrimRight(pattern, "/")

	// Case 1: explicit directory path (trailing slash) or a plain path that is a directory.
	if strings.HasSuffix(pattern, "/") || func() bool {
		if strings.ContainsAny(trimmed, "*?[") {
			return false
		}
		info, err := os.Stat(trimmed)
		return err == nil && info.IsDir()
	}() {
		sourceRoot = trimmed + "/"
		_ = filepath.Walk(trimmed, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				files = append(files, filepath.ToSlash(path))
			}
			return nil
		})
		return
	}

	// Case 2 & 3: glob pattern or plain file.
	matches, err := filepath.Glob(trimmed)
	if err != nil {
		return
	}
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil {
			continue
		}
		if info.IsDir() {
			// Glob matched a directory — walk it.
			if sourceRoot == "" {
				sourceRoot = filepath.ToSlash(m) + "/"
			}
			_ = filepath.Walk(m, func(path string, fi os.FileInfo, werr error) error {
				if werr == nil && !fi.IsDir() {
					files = append(files, filepath.ToSlash(path))
				}
				return nil
			})
		} else {
			files = append(files, filepath.ToSlash(m))
		}
	}
	return
}
