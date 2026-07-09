package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMatchCommandPattern(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		s       string
		want    bool
	}{
		{"exact match", "git status", "git status", true},
		{"exact mismatch", "git status", "git status --short", false},
		{"empty pattern never matches", "", "anything", false},
		{"prefix wildcard", "git *", "git commit -m x", true},
		{"prefix wildcard no match", "git *", "echo git commit", false},
		{"suffix wildcard", "* --force", "git push --force", true},
		{"suffix wildcard no match", "* --force", "git push --force-with-lease", false},
		{"middle wildcard", "npm * test", "npm run test", true},
		{"leading and trailing wildcard", "*rm -rf*", "sudo rm -rf /", true},
		{"bare wildcard matches anything", "*", "literally anything", true},
		{"no wildcard exact only", "go build", "go build ./...", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchCommandPattern(tc.pattern, tc.s); got != tc.want {
				t.Fatalf("matchCommandPattern(%q, %q) = %v, want %v", tc.pattern, tc.s, got, tc.want)
			}
		})
	}
}

func TestCommandPolicy_Allowed(t *testing.T) {
	t.Run("empty policy allows everything", func(t *testing.T) {
		p := CommandPolicy{}
		if ok, reason := p.Allowed("rm -rf /"); !ok {
			t.Fatalf("expected allowed, got denied: %s", reason)
		}
	})

	t.Run("denylist blocks even if allowlist would allow", func(t *testing.T) {
		p := CommandPolicy{
			Allowlist: []string{"rm *"},
			Denylist:  []string{"rm -rf *"},
		}
		if ok, _ := p.Allowed("rm -rf /"); ok {
			t.Fatalf("expected denied")
		}
		if ok, reason := p.Allowed("rm somefile"); !ok {
			t.Fatalf("expected allowed for non-denylisted allowlisted command, got denied: %s", reason)
		}
	})

	t.Run("allowlist-only blocks non-matching commands", func(t *testing.T) {
		p := CommandPolicy{Allowlist: []string{"git *", "npm test"}}
		if ok, _ := p.Allowed("git status"); !ok {
			t.Fatalf("expected allowed")
		}
		if ok, _ := p.Allowed("npm test"); !ok {
			t.Fatalf("expected allowed")
		}
		if ok, _ := p.Allowed("curl http://evil"); ok {
			t.Fatalf("expected denied")
		}
	})

	t.Run("allowlist and denylist combined", func(t *testing.T) {
		p := CommandPolicy{
			Allowlist: []string{"git *"},
			Denylist:  []string{"git push --force*"},
		}
		if ok, _ := p.Allowed("git status"); !ok {
			t.Fatalf("expected allowed")
		}
		if ok, _ := p.Allowed("git push --force"); ok {
			t.Fatalf("expected denied by denylist")
		}
		if ok, _ := p.Allowed("npm install"); ok {
			t.Fatalf("expected denied: not in allowlist")
		}
	})

	t.Run("trims whitespace before matching", func(t *testing.T) {
		p := CommandPolicy{Allowlist: []string{"git status"}}
		if ok, _ := p.Allowed("  git status  \n"); !ok {
			t.Fatalf("expected allowed after trimming")
		}
	})
}

func TestExecuteLLMTool_ListDir(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "a.txt"), "a")
	mustWriteFile(t, filepath.Join(root, "sub", "b.txt"), "b")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main")
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "pkg"), 0755); err != nil {
		t.Fatal(err)
	}

	out, res := executeLLMTool(context.Background(), root, CommandPolicy{}, "list_dir", map[string]string{"path": ""}, nil)
	if res != nil {
		t.Fatalf("expected nil result, got %+v", res)
	}
	if !strings.Contains(out, "a.txt") || !strings.Contains(out, "sub/") || !strings.Contains(out, "sub/b.txt") {
		t.Fatalf("expected listing to contain a.txt and sub/b.txt, got: %q", out)
	}
	if strings.Contains(out, ".git") {
		t.Fatalf("expected .git to be skipped, got: %q", out)
	}
	if strings.Contains(out, "node_modules") {
		t.Fatalf("expected node_modules to be skipped, got: %q", out)
	}

	t.Run("path escape rejected", func(t *testing.T) {
		out, res := executeLLMTool(context.Background(), root, CommandPolicy{}, "list_dir", map[string]string{"path": "../../etc"}, nil)
		if res != nil {
			t.Fatalf("expected nil result, got %+v", res)
		}
		if !strings.HasPrefix(out, "error:") {
			t.Fatalf("expected error for path escape, got: %q", out)
		}
	})
}

func TestExecuteLLMTool_StrReplace(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file.txt")
	mustWriteFile(t, path, "hello world\nhello world\n")

	t.Run("zero matches errors", func(t *testing.T) {
		out, res := executeLLMTool(context.Background(), root, CommandPolicy{}, "str_replace", map[string]string{
			"path": "file.txt", "old": "not present", "new": "x",
		}, nil)
		if res != nil {
			t.Fatalf("expected nil result, got %+v", res)
		}
		if !strings.HasPrefix(out, "error:") {
			t.Fatalf("expected error for zero matches, got: %q", out)
		}
	})

	t.Run("multiple matches errors", func(t *testing.T) {
		out, res := executeLLMTool(context.Background(), root, CommandPolicy{}, "str_replace", map[string]string{
			"path": "file.txt", "old": "hello world", "new": "x",
		}, nil)
		if res != nil {
			t.Fatalf("expected nil result, got %+v", res)
		}
		if !strings.HasPrefix(out, "error:") {
			t.Fatalf("expected error for ambiguous match, got: %q", out)
		}
		// File must be untouched.
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "hello world\nhello world\n" {
			t.Fatalf("file was modified despite ambiguous match: %q", data)
		}
	})

	t.Run("exact match replaces", func(t *testing.T) {
		mustWriteFile(t, path, "unique line here\nother line\n")
		out, res := executeLLMTool(context.Background(), root, CommandPolicy{}, "str_replace", map[string]string{
			"path": "file.txt", "old": "unique line here", "new": "replaced line",
		}, nil)
		if res != nil {
			t.Fatalf("expected nil result, got %+v", res)
		}
		if out != "ok" {
			t.Fatalf("expected ok, got: %q", out)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "replaced line\nother line\n" {
			t.Fatalf("unexpected file content: %q", data)
		}
	})

	t.Run("path escape rejected", func(t *testing.T) {
		out, res := executeLLMTool(context.Background(), root, CommandPolicy{}, "str_replace", map[string]string{
			"path": "../../etc/passwd", "old": "x", "new": "y",
		}, nil)
		if res != nil {
			t.Fatalf("expected nil result, got %+v", res)
		}
		if !strings.HasPrefix(out, "error:") {
			t.Fatalf("expected error for path escape, got: %q", out)
		}
	})
}

func TestExecuteLLMTool_Search_RgNotOnPath(t *testing.T) {
	// Empty PATH guarantees exec.LookPath("rg") fails regardless of whether
	// rg happens to be installed on the machine running this test.
	t.Setenv("PATH", "")
	out, res := executeLLMTool(context.Background(), t.TempDir(), CommandPolicy{}, "search", map[string]string{"pattern": "x"}, nil)
	if res != nil {
		t.Fatalf("expected nil result, got %+v", res)
	}
	if out != "error: ripgrep (rg) not found on PATH" {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestExecuteLLMTool_Search(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep (rg) not installed, skipping search integration test")
	}

	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "needle.go"), "package main\n\nfunc findme() {}\n")
	mustWriteFile(t, filepath.Join(root, "other.txt"), "nothing interesting\n")

	out, res := executeLLMTool(context.Background(), root, CommandPolicy{}, "search", map[string]string{"pattern": "findme"}, nil)
	if res != nil {
		t.Fatalf("expected nil result, got %+v", res)
	}
	if !strings.Contains(out, "needle.go") || !strings.Contains(out, "findme") {
		t.Fatalf("expected match in needle.go, got: %q", out)
	}

	t.Run("glob restricts search", func(t *testing.T) {
		out, res := executeLLMTool(context.Background(), root, CommandPolicy{}, "search", map[string]string{"pattern": "nothing", "glob": "*.go"}, nil)
		if res != nil {
			t.Fatalf("expected nil result, got %+v", res)
		}
		if out != "no matches found" {
			t.Fatalf("expected no matches restricted to *.go, got: %q", out)
		}
	})

	t.Run("no matches", func(t *testing.T) {
		out, res := executeLLMTool(context.Background(), root, CommandPolicy{}, "search", map[string]string{"pattern": "zzz_not_present_zzz"}, nil)
		if res != nil {
			t.Fatalf("expected nil result, got %+v", res)
		}
		if out != "no matches found" {
			t.Fatalf("expected no matches found, got: %q", out)
		}
	})
}

func TestExecuteLLMTool_GetTaskTransitions(t *testing.T) {
	t.Run("empty transitions", func(t *testing.T) {
		out, res := executeLLMTool(context.Background(), t.TempDir(), CommandPolicy{}, "get_task_transitions", map[string]string{}, nil)
		if res != nil {
			t.Fatalf("expected nil result, got %+v", res)
		}
		if out != "No transitions configured for this label." {
			t.Fatalf("unexpected output: %q", out)
		}
	})

	t.Run("marshals transitions as JSON", func(t *testing.T) {
		transitions := []TransitionHint{{ToLabel: "review", Path: "success"}, {ToLabel: "failed", Path: "failure"}}
		out, res := executeLLMTool(context.Background(), t.TempDir(), CommandPolicy{}, "get_task_transitions", map[string]string{}, transitions)
		if res != nil {
			t.Fatalf("expected nil result, got %+v", res)
		}
		var got []TransitionHint
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("expected valid JSON, got %q: %v", out, err)
		}
		if len(got) != 2 || got[0].ToLabel != "review" || got[1].Path != "failure" {
			t.Fatalf("unexpected transitions: %+v", got)
		}
	})
}

func TestExecuteLLMTool_SignalComplete_ReadsOutcome(t *testing.T) {
	out, res := executeLLMTool(context.Background(), t.TempDir(), CommandPolicy{}, "signal_complete", map[string]string{
		"outcome": "success", "summary": "did the thing",
	}, nil)
	if out != "acknowledged" {
		t.Fatalf("unexpected output: %q", out)
	}
	if res == nil {
		t.Fatalf("expected non-nil result")
	}
	if res.Outcome != "success" {
		t.Fatalf("expected Outcome=success, got %q", res.Outcome)
	}
	if res.Message == nil || *res.Message != "did the thing" {
		t.Fatalf("unexpected message: %v", res.Message)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
