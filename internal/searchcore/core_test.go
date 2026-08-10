package searchcore

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type localSearchFS struct{ cwd string }

func (f localSearchFS) Getwd() (string, error)                  { return f.cwd, nil }
func (f localSearchFS) Lstat(name string) (os.FileInfo, error)  { return os.Lstat(name) }
func (f localSearchFS) Open(name string) (io.ReadCloser, error) { return os.Open(name) }
func (f localSearchFS) ReadDir(name string) ([]os.FileInfo, error) {
	entries, err := os.ReadDir(name)
	if err != nil {
		return nil, err
	}
	out := make([]os.FileInfo, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	return out, nil
}

func writeSearchFixture(t *testing.T, root, name string, data []byte) {
	t.Helper()
	file := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGrepMatchesContextAndProtectsTraversal(t *testing.T) {
	root := t.TempDir()
	writeSearchFixture(t, root, ".gitignore", []byte("ignored.go\nignored-dir/\n"))
	writeSearchFixture(t, root, "main.go", []byte("before\nNeedleAlpha\nafter\n"))
	writeSearchFixture(t, root, "pkg/main_test.go", []byte("needLEalpha\n"))
	writeSearchFixture(t, root, ".hidden.go", []byte("NeedleAlpha\n"))
	writeSearchFixture(t, root, "ignored.go", []byte("NeedleAlpha\n"))
	writeSearchFixture(t, root, "ignored-dir/file.go", []byte("NeedleAlpha\n"))
	writeSearchFixture(t, root, "binary.go", []byte("NeedleAlpha\x00binary\n"))
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("NeedleAlpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.go")); err != nil {
		t.Fatal(err)
	}

	result, err := Grep(context.Background(), localSearchFS{cwd: root}, GrepOptions{
		Pattern: "needlealpha", Path: ".", Glob: "*.go", IgnoreCase: true, Context: 1, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Engine != "" || result.Warning != "" || result.MatchCount != 3 || result.Truncated {
		t.Fatalf("unexpected result metadata or matches: %+v", result)
	}
	matched := make(map[string]GrepLine)
	for _, line := range result.Lines {
		if line.Match {
			matched[line.Path] = line
		}
	}
	if len(matched) != 3 || matched["main.go"].Line != 2 || matched["main.go"].Column != 1 || !matched["pkg/main_test.go"].Match || !matched[".hidden.go"].Match {
		t.Fatalf("wrong matches: %+v", matched)
	}
	for _, forbidden := range []string{"ignored.go", "ignored-dir/file.go", "binary.go", "linked.go"} {
		if _, exists := matched[forbidden]; exists {
			t.Fatalf("protected or ignored path was searched: %s", forbidden)
		}
	}
	if len(result.Lines) != 5 || result.Lines[0].Path != ".hidden.go" || result.Lines[1].Text != "before" || result.Lines[3].Text != "after" {
		t.Fatalf("context or ordering mismatch: %+v", result.Lines)
	}
}

func TestFindUsesDoubleStarIgnoresSymlinksAndLimits(t *testing.T) {
	root := t.TempDir()
	writeSearchFixture(t, root, ".gitignore", []byte("vendor/\n"))
	writeSearchFixture(t, root, "pkg/main_test.go", []byte("package pkg\n"))
	writeSearchFixture(t, root, "nested/pkg/other_test.go", []byte("package pkg\n"))
	writeSearchFixture(t, root, "vendor/ignored_test.go", []byte("package vendor\n"))
	if err := os.Symlink(filepath.Join(root, "pkg"), filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}

	result, err := Find(context.Background(), localSearchFS{cwd: root}, FindOptions{
		Pattern: "pkg/*_test.go", Path: ".", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Engine != "" || result.Warning != "" || result.Truncated || len(result.Paths) != 2 || result.Paths[0] != "nested/pkg/other_test.go" || result.Paths[1] != "pkg/main_test.go" {
		t.Fatalf("unexpected find result: %+v", result)
	}

	limited, err := Find(context.Background(), localSearchFS{cwd: root}, FindOptions{
		Pattern: "*.go", Path: ".", Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited.Paths) != 1 || !limited.Truncated {
		t.Fatalf("find limit not enforced: %+v", limited)
	}
}

func TestOutputAndTraversalAreBounded(t *testing.T) {
	root := t.TempDir()
	writeSearchFixture(t, root, "long.txt", []byte("Needle"+strings.Repeat("x", MaxOutputBytes)+"\n"))
	result, err := Grep(context.Background(), localSearchFS{cwd: root}, GrepOptions{
		Pattern: "Needle", Path: ".", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || result.MatchCount != 0 || len(result.Lines) != 0 {
		t.Fatalf("output bound not enforced: %+v", result)
	}

	for _, name := range []string{"a", "b", "c"} {
		writeSearchFixture(t, root, name, []byte(name))
	}
	walker := searchWalker{fs: localSearchFS{cwd: root}, root: root, maxEntries: 2}
	visited := 0
	if err = walker.walk(context.Background(), func(string, string, os.FileInfo) (bool, error) {
		visited++
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if !walker.truncated || walker.entries != 3 || visited != 2 {
		t.Fatalf("traversal bound not enforced: entries=%d visited=%d truncated=%v", walker.entries, visited, walker.truncated)
	}
}

func TestSkipsOversizedFilesAndHonorsCancellation(t *testing.T) {
	root := t.TempDir()
	large := filepath.Join(root, "large.txt")
	if err := os.WriteFile(large, []byte("Needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(large, MaxFallbackFileSize+1); err != nil {
		t.Fatal(err)
	}
	result, err := Grep(context.Background(), localSearchFS{cwd: root}, GrepOptions{
		Pattern: "Needle", Path: ".", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || result.MatchCount != 0 {
		t.Fatalf("oversized file was not safely skipped: %+v", result)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Find(ctx, localSearchFS{cwd: root}, FindOptions{Pattern: "*", Path: ".", Limit: 10})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation not preserved: %v", err)
	}
}

func TestIgnoreRulesApplyNestedNegation(t *testing.T) {
	rules, err := parseIgnoreRules("*.go\n!keep.go\ngenerated/\n", "pkg")
	if err != nil {
		t.Fatal(err)
	}
	if !ignoredPath(rules, "pkg/drop.go", false) || ignoredPath(rules, "pkg/keep.go", false) || !ignoredPath(rules, "pkg/generated", true) || ignoredPath(rules, "other/drop.go", false) {
		t.Fatal("nested ignore rule semantics are incorrect")
	}
}

func TestLocalFSUsesRealFilesystemSemantics(t *testing.T) {
	root := t.TempDir()
	writeSearchFixture(t, root, ".gitignore", []byte("ignored/\n"))
	writeSearchFixture(t, root, "main.go", []byte("before\nLocalNeedle\nafter\n"))
	writeSearchFixture(t, root, "pkg/other.go", []byte("LocalNeedle\n"))
	writeSearchFixture(t, root, "ignored/drop.go", []byte("LocalNeedle\n"))
	t.Chdir(root)

	grep, err := Grep(context.Background(), LocalFS{}, GrepOptions{
		Pattern: "localneedle", Path: ".", Glob: "*.go", IgnoreCase: true, Context: 1, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if grep.MatchCount != 2 || grep.Truncated || len(grep.Lines) != 4 {
		t.Fatalf("LocalFS grep 结果错误: %+v", grep)
	}
	for _, line := range grep.Lines {
		if strings.HasPrefix(line.Path, "ignored/") {
			t.Fatalf("LocalFS 未遵守忽略规则: %+v", grep.Lines)
		}
	}

	found, err := Find(context.Background(), LocalFS{}, FindOptions{Pattern: "*.go", Path: ".", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Paths) != 1 || !found.Truncated || found.Paths[0] != "main.go" {
		t.Fatalf("LocalFS find 上限错误: %+v", found)
	}
}
