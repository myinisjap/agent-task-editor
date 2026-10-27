package skills

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/myinisjap/agent-task-editor/backend/internal/gitcmd"
)

// markerFile is written into every materialized skill folder so a later run
// can tell its own copy (refresh it) from a skill the repo commits (leave it).
const markerFile = ".ate-managed-skill"

// providerDirs maps a provider to the worktree-relative directory its CLI
// scans for project skills, verified against the versions pinned in
// backend/Dockerfile (claude 2.1.301, codex 0.163.0, qwen 0.25.2, opencode
// 1.19.1).
var providerDirs = map[string]string{
	"claude":    ".claude/skills",
	"codex_cli": ".agents/skills",
	"qwen_code": ".qwen/skills",
	"opencode":  ".opencode/skills",
}

// ProviderDir returns the worktree-relative skills directory for provider and
// whether the provider supports skills.
func ProviderDir(provider string) (string, bool) {
	d, ok := providerDirs[provider]
	return d, ok
}

// Materialize copies the named skills from store into the directory provider
// scans inside worktree, and adds each materialized path to the git exclude
// file so it is never committed. Skills whose folder already exists in the
// worktree and was not placed by us (i.e. committed by the repo) are skipped
// with a log line. Returns the worktree-relative paths written.
func Materialize(ctx context.Context, store Store, worktree, provider string, names []string, log *slog.Logger) ([]string, error) {
	base, ok := ProviderDir(provider)
	if !ok || len(names) == 0 {
		return nil, nil
	}
	if log == nil {
		log = slog.Default()
	}
	var written []string
	for _, name := range names {
		if !ValidName(name) {
			log.Warn("skills: skip invalid skill name", "skill", name)
			continue
		}
		rel := base + "/" + name
		dst := filepath.Join(worktree, filepath.FromSlash(rel))
		if _, err := os.Lstat(dst); err == nil {
			if _, merr := os.Stat(filepath.Join(dst, markerFile)); merr != nil {
				log.Info("skills: repo already provides this skill; not overwriting", "skill", name, "path", rel)
				continue
			}
			if err := os.RemoveAll(dst); err != nil {
				return written, err
			}
		}
		if err := copySkill(store.Path(name), dst); err != nil {
			_ = os.RemoveAll(dst)
			log.Warn("skills: materialize failed", "skill", name, "err", err)
			continue
		}
		written = append(written, rel)
	}
	if len(written) > 0 {
		if err := excludePaths(ctx, worktree, written); err != nil {
			return written, fmt.Errorf("git exclude: %w", err)
		}
	}
	return written, nil
}

func copySkill(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() { // never follow or copy symlinks
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dst, markerFile), []byte("managed by agent-task-editor\n"), 0o644)
}

// excludePaths appends "/<rel>/" lines to the git exclude file governing
// worktree (the common repo's info/exclude for a linked worktree).
func excludePaths(ctx context.Context, worktree string, rels []string) error {
	out, err := gitcmd.Output(ctx, worktree, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	excludePath := strings.TrimSpace(string(out))
	if excludePath == "" {
		return fmt.Errorf("empty exclude path")
	}
	existing, _ := os.ReadFile(excludePath)
	have := map[string]bool{}
	for _, l := range strings.Split(string(existing), "\n") {
		have[strings.TrimSpace(l)] = true
	}
	var add strings.Builder
	for _, rel := range rels {
		line := "/" + rel + "/"
		if !have[line] {
			add.WriteString(line + "\n")
		}
	}
	if add.Len() == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(excludePath), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(excludePath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	prefix := ""
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		prefix = "\n"
	}
	_, err = f.WriteString(prefix + add.String())
	return err
}
