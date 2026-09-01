package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

var browserTitlePattern = regexp.MustCompile(`(?is)<title(?:\s[^>]*)?>(.*?)</title\s*>`)

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not locate the test source")
	}
	return filepath.Dir(filename)
}

func trackedTextFiles(t *testing.T, root string) map[string][]byte {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("list tracked files: %v", err)
	}

	files := make(map[string][]byte)
	for _, rawPath := range bytes.Split(output, []byte{0}) {
		if len(rawPath) == 0 {
			continue
		}
		relative := filepath.FromSlash(string(rawPath))
		if relative == "verification_test.go" {
			continue
		}
		path := filepath.Join(root, relative)
		contents, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("read tracked file %q: %v", relative, err)
		}
		if bytes.IndexByte(contents, 0) >= 0 || !utf8.Valid(contents) {
			continue
		}
		files[relative] = contents
	}
	return files
}

func staleIdentifier(contents string) string {
	lower := strings.ToLower(contents)

	// These are canonical repository identities, not deployment defaults.
	for _, allowed := range []string{
		"github.com/carter2099/herdr-web-client",
		"github.com/carter2099/herdr-web-client.git",
		"carter2099/herdr-web-client",
		"carter2099/herdr-web-client.git",
		"carter2099@pm.me",
	} {
		lower = strings.ReplaceAll(lower, allowed, "")
	}
	if strings.Contains(lower, "carter") {
		return "personal Carter identifier"
	}

	oldName := "herdr" + "-web"
	for start := 0; ; {
		relative := strings.Index(lower[start:], oldName)
		if relative < 0 {
			break
		}
		index := start + relative
		suffix := lower[index+len(oldName):]
		if !strings.HasPrefix(suffix, "-client") {
			return "legacy herdr-web identifier"
		}
		start = index + len(oldName)
	}

	oldEnvironmentPrefix := "herdr" + "_web_"
	for start := 0; ; {
		relative := strings.Index(lower[start:], oldEnvironmentPrefix)
		if relative < 0 {
			break
		}
		index := start + relative
		suffix := lower[index+len(oldEnvironmentPrefix):]
		if !strings.HasPrefix(suffix, "client_") {
			return "legacy HERDR_WEB_ environment prefix"
		}
		start = index + len(oldEnvironmentPrefix)
	}
	return ""
}

func TestSourceContainsNoStalePersonalOrLegacyIdentifiers(t *testing.T) {
	root := repositoryRoot(t)
	files := trackedTextFiles(t, root)
	paths := make([]string, 0, len(files))
	for relative := range files {
		paths = append(paths, relative)
	}
	sort.Strings(paths)
	for _, relative := range paths {
		if reason := staleIdentifier(string(files[relative])); reason != "" {
			t.Errorf("%s contains %s", relative, reason)
		}
	}
}

func TestBrowserTitleIsExactlyHerdrWeb(t *testing.T) {
	root := repositoryRoot(t)
	for _, relative := range []string{"web/index.html", "web/dist/index.html"} {
		contents, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			t.Fatalf("read %s: %v", relative, err)
		}
		matches := browserTitlePattern.FindAllSubmatch(contents, -1)
		if len(matches) != 1 {
			t.Fatalf("%s has %d title elements, want exactly one", relative, len(matches))
		}
		if got := string(matches[0][1]); got != "Herdr Web" {
			t.Errorf("%s title = %q, want %q", relative, got, "Herdr Web")
		}
	}
}
