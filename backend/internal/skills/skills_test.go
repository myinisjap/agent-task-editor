package skills

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const goodMD = "---\nname: my-skill\ndescription: Does a thing.\n---\n# Body\n"

func TestParseFrontmatter(t *testing.T) {
	m, err := ParseFrontmatter([]byte(goodMD))
	if err != nil || m.Name != "my-skill" || m.Description != "Does a thing." {
		t.Fatalf("got %+v %v", m, err)
	}
	if _, err := ParseFrontmatter([]byte("\r\n")); err == nil {
		t.Error("empty must fail")
	}
	bad := map[string]string{
		"no frontmatter": "# hi",
		"unclosed":       "---\nname: a\ndescription: b\n",
		"no name":        "---\ndescription: b\n---\n",
		"no desc":        "---\nname: a\n---\n",
		"bad slug":       "---\nname: My Skill\ndescription: b\n---\n",
		"traversal name": "---\nname: ../x\ndescription: b\n---\n",
		"long desc":      "---\nname: a\ndescription: " + strings.Repeat("x", MaxDescriptionLen+1) + "\n---\n",
		"bad yaml":       "---\nname: [\n---\n",
	}
	for n, md := range bad {
		if _, err := ParseFrontmatter([]byte(md)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", n, err)
		}
	}
}

func mkZip(t *testing.T, entries map[string]string, mutate func(*zip.FileHeader)) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		if mutate != nil {
			mutate(h)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	_ = zw.Close()
	return buf.Bytes()
}

func TestBundleFromZip(t *testing.T) {
	b, err := BundleFromZip(mkZip(t, map[string]string{"SKILL.md": goodMD, "scripts/run.sh": "echo"}, nil))
	if err != nil || len(b.Files) != 2 || b.Name != "my-skill" {
		t.Fatalf("root zip: %+v %v", b, err)
	}
	// Single wrapping folder is stripped.
	b, err = BundleFromZip(mkZip(t, map[string]string{"my-skill/SKILL.md": goodMD, "my-skill/ref.md": "x"}, nil))
	if err != nil || b.Files["ref.md"] == nil {
		t.Fatalf("wrapped zip: %+v %v", b, err)
	}
}

func TestBundleFromZip_Rejects(t *testing.T) {
	cases := map[string][]byte{
		"traversal":        mkZip(t, map[string]string{"SKILL.md": goodMD, "../evil": "x"}, nil),
		"nested traversal": mkZip(t, map[string]string{"SKILL.md": goodMD, "a/../../evil": "x"}, nil),
		"absolute":         mkZip(t, map[string]string{"SKILL.md": goodMD, "/etc/passwd": "x"}, nil),
		"backslash":        mkZip(t, map[string]string{"SKILL.md": goodMD, "a\\b": "x"}, nil),
		"drive":            mkZip(t, map[string]string{"SKILL.md": goodMD, "C:/x": "x"}, nil),
		"symlink": mkZip(t, map[string]string{"SKILL.md": goodMD, "link": "/etc/passwd"}, func(h *zip.FileHeader) {
			if h.Name == "link" {
				h.SetMode(os.ModeSymlink | 0o777)
			}
		}),
		"no skill.md":     mkZip(t, map[string]string{"README.md": "x"}, nil),
		"two roots":       mkZip(t, map[string]string{"a/SKILL.md": goodMD, "b/x": "x"}, nil),
		"oversize":        mkZip(t, map[string]string{"SKILL.md": goodMD, "big": strings.Repeat("a", MaxBundleBytes+1)}, nil),
		"bad frontmatter": mkZip(t, map[string]string{"SKILL.md": "nope"}, nil),
		"not a zip":       []byte("hello"),
	}
	for n, data := range cases {
		if _, err := BundleFromZip(data); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", n, err)
		}
	}
	// Zip bomb: tiny compressed, huge uncompressed.
	bomb := mkZip(t, map[string]string{"SKILL.md": goodMD, "z": strings.Repeat("\x00", 4*MaxBundleBytes)}, nil)
	if len(bomb) > MaxBundleBytes {
		t.Fatalf("bomb should be small, got %d", len(bomb))
	}
	if _, err := BundleFromZip(bomb); !errors.Is(err, ErrInvalid) {
		t.Errorf("bomb: want ErrInvalid, got %v", err)
	}
	// Too many files.
	many := map[string]string{"SKILL.md": goodMD}
	for i := 0; i <= MaxFiles; i++ {
		many[filepath.Join("d", strings.Repeat("f", 1)+string(rune('a'+i%26))+strings.Repeat("g", i))] = "x"
	}
	if _, err := BundleFromZip(mkZip(t, many, nil)); !errors.Is(err, ErrInvalid) {
		t.Errorf("many files: want ErrInvalid, got %v", err)
	}
}

func TestStoreWriteReplaceRemove(t *testing.T) {
	s := Store{Dir: filepath.Join(t.TempDir(), "skills")}
	b, _ := BundleFromSkillMD([]byte(goodMD))
	if err := s.Write(b); err != nil {
		t.Fatal(err)
	}
	b2, _ := BundleFromZip(mkZip(t, map[string]string{"SKILL.md": goodMD, "x.txt": "1"}, nil))
	if err := s.Write(b2); err != nil {
		t.Fatal(err)
	}
	files, _ := s.ReadFiles("my-skill")
	if len(files) != 2 {
		t.Fatalf("files=%v", files)
	}
	if err := s.Remove("my-skill"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.Path("my-skill")); !os.IsNotExist(err) {
		t.Error("not removed")
	}
	ents, _ := os.ReadDir(s.Dir)
	if len(ents) != 0 {
		t.Errorf("leftover temp entries: %v", ents)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func TestMaterialize_PerProvider(t *testing.T) {
	want := map[string]string{
		"claude":    ".claude/skills",
		"codex_cli": ".agents/skills",
		"qwen_code": ".qwen/skills",
		"opencode":  ".opencode/skills",
	}
	store := Store{Dir: t.TempDir()}
	b, _ := BundleFromZip(mkZip(t, map[string]string{"SKILL.md": goodMD, "scripts/run.sh": "echo"}, nil))
	if err := store.Write(b); err != nil {
		t.Fatal(err)
	}
	for provider, dir := range want {
		t.Run(provider, func(t *testing.T) {
			repo := t.TempDir()
			git(t, repo, "init", "-q", "-b", "main")
			git(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
			wt := filepath.Join(t.TempDir(), "wt")
			git(t, repo, "worktree", "add", "-q", "-b", "task", wt)

			got, err := Materialize(context.Background(), store, wt, provider, []string{"my-skill"}, nil)
			if err != nil || len(got) != 1 || got[0] != dir+"/my-skill" {
				t.Fatalf("got %v err %v", got, err)
			}
			for _, f := range []string{"SKILL.md", "scripts/run.sh"} {
				if _, err := os.Stat(filepath.Join(wt, dir, "my-skill", f)); err != nil {
					t.Errorf("missing %s: %v", f, err)
				}
			}
			// Never committed or visible to git.
			if st := git(t, wt, "status", "--porcelain"); strings.TrimSpace(st) != "" {
				t.Errorf("worktree not clean: %q", st)
			}
			git(t, wt, "add", "-A")
			if st := git(t, wt, "diff", "--cached", "--name-only"); strings.TrimSpace(st) != "" {
				t.Errorf("skill staged: %q", st)
			}
			// Re-materialize refreshes our own copy.
			if got, err := Materialize(context.Background(), store, wt, provider, []string{"my-skill"}, nil); err != nil || len(got) != 1 {
				t.Errorf("second run: %v %v", got, err)
			}
		})
	}
	if got, err := Materialize(context.Background(), store, t.TempDir(), "llm", []string{"my-skill"}, nil); err != nil || got != nil {
		t.Errorf("unsupported provider: %v %v", got, err)
	}
}

func TestMaterialize_DoesNotOverwriteCommitted(t *testing.T) {
	store := Store{Dir: t.TempDir()}
	b, _ := BundleFromSkillMD([]byte(goodMD))
	_ = store.Write(b)
	repo := t.TempDir()
	git(t, repo, "init", "-q", "-b", "main")
	dir := filepath.Join(repo, ".agents", "skills", "my-skill")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: my-skill\ndescription: repo's own\n---\n"), 0o644)
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "init")

	got, err := Materialize(context.Background(), store, repo, "codex_cli", []string{"my-skill"}, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v %v", got, err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if !strings.Contains(string(data), "repo's own") {
		t.Error("committed skill was overwritten")
	}
	if st := git(t, repo, "status", "--porcelain"); strings.TrimSpace(st) != "" {
		t.Errorf("dirty: %q", st)
	}
}

func TestStoreWriteRejectsEscape(t *testing.T) {
	st := Store{Dir: t.TempDir()}
	for _, p := range []string{"../evil", "a/../../evil", "/abs"} {
		b := &Bundle{Meta: Meta{Name: "ok-skill"}, Files: map[string][]byte{"SKILL.md": []byte("x"), p: []byte("y")}}
		if err := st.Write(b); err == nil {
			t.Errorf("expected error for %q", p)
		}
	}
}
