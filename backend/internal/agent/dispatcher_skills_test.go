package agent

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/skills"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// A skill attached to a codex_cli agent (and a repo-level one) shows up in the
// task worktree's .agents/skills at run time; disabled and unattached skills
// don't; and none of it appears in git status.
func TestMaterializeSkills_CodexAgentAndRepoUnion(t *testing.T) {
	repoPath := initRepo(t)
	f, _ := os.CreateTemp("", "dispatcher-skills-*.db")
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })
	db, err := storage.Open(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	q := gen.New(db.SQL())

	repoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: repoID, Name: "r", Path: repoPath}); err != nil {
		t.Fatal(err)
	}
	pc, _ := q.CreateProviderConfig(ctx, gen.CreateProviderConfigParams{ID: uuid.NewString(), Name: "p", Provider: "codex_cli", Model: "m", Env: `{}`})
	ac, err := q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{ID: uuid.NewString(), Name: "a", ProviderConfigID: pc.ID, Labels: `["plan"]`, MaxRetries: 1, RetryBackoffSecs: 1})
	if err != nil {
		t.Fatal(err)
	}

	skillsDir := t.TempDir()
	store := skills.Store{Dir: skillsDir}
	mk := func(name string, enabled int64) string {
		b, _ := skills.BundleFromSkillMD([]byte("---\nname: " + name + "\ndescription: d\n---\nbody\n"))
		if err := store.Write(b); err != nil {
			t.Fatal(err)
		}
		s, err := q.CreateSkill(ctx, gen.CreateSkillParams{ID: uuid.NewString(), Name: name, Description: "d", Enabled: enabled})
		if err != nil {
			t.Fatal(err)
		}
		return s.ID
	}
	agentSkill, repoSkill, disabled := mk("agent-skill", 1), mk("repo-skill", 1), mk("off-skill", 0)
	mk("unattached", 1)
	_ = q.AddAgentConfigSkill(ctx, gen.AddAgentConfigSkillParams{AgentConfigID: ac.ID, SkillID: agentSkill})
	_ = q.AddAgentConfigSkill(ctx, gen.AddAgentConfigSkillParams{AgentConfigID: ac.ID, SkillID: disabled})
	_ = q.AddRepoSkill(ctx, gen.AddRepoSkillParams{RepoID: repoID, SkillID: repoSkill})
	_ = q.AddRepoSkill(ctx, gen.AddRepoSkillParams{RepoID: repoID, SkillID: agentSkill}) // dup across scopes

	wt, _, _, err := provisionWorktree(ctx, repoPath, "task-skills", "skills")
	if err != nil {
		t.Fatal(err)
	}
	d := &Dispatcher{q: q}
	d.SetSkillsDir(skillsDir)
	d.materializeSkills(ctx, wt, "codex_cli", ac.ID, repoID, slog.Default())

	for name, want := range map[string]bool{"agent-skill": true, "repo-skill": true, "off-skill": false, "unattached": false} {
		_, err := os.Stat(filepath.Join(wt, ".agents", "skills", name, "SKILL.md"))
		if (err == nil) != want {
			t.Errorf("%s present=%v want %v", name, err == nil, want)
		}
	}
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = wt
	out, _ := cmd.Output()
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("worktree dirty: %q", out)
	}
}
