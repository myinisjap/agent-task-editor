# Skills library

A provider-neutral library of [Agent Skills](https://agentskills.io): a folder
with a `SKILL.md` (YAML frontmatter with `name` and `description`, then
instructions) plus optional files such as scripts or references. Claude Code,
Codex CLI, Qwen Code and opencode all read this format, so one skill can serve
every provider (unlike Claude plugins, which are Claude-only).

Skills are **trusted admin config**: they are managed through the normal API
token only and are never reachable with the MCP sidecar's scoped tokens (run or
chat), so an agent cannot create or change skills.

## Storage

Skill folders live on disk under `SKILLS_DIR` (default `skills`, next to the
database / `UPLOAD_DIR`) as `<SKILLS_DIR>/<name>/SKILL.md` + files. Back this
directory up together with the database. The `skills` table indexes them
(`name` unique slug, `description` from frontmatter, `enabled`), and the join
tables `agent_config_skills` and `repo_skills` attach skills to agent configs
and repos.

## Validation

- `SKILL.md` must start with a `---` frontmatter block containing `name`
  (lowercase slug: `a-z`, `0-9`, single hyphens, max 64 chars) and
  `description` (non-empty, max 1024 chars).
- Zip uploads: max 1 MB (compressed and uncompressed), at most 200 files;
  `SKILL.md` at the zip root or inside a single top-level folder. Entries with
  `..` segments, absolute paths, backslashes, drive letters or symlinks reject
  the whole upload.

## Attaching skills

`PUT /agents/{id}/skills` and `PUT /repos/{id}/skills` replace the attached set
with `{"skill_ids": [...]}`. At run start the **union** of the agent config's
and the repo's *enabled* skills is used. See [api.md](api.md#skills).

## Materialization

At run start (new or reused worktree) each skill is copied into the directory
the run's provider scans, relative to the worktree root. The directories were
checked against the CLI versions pinned in `backend/Dockerfile`:

| Provider | Directory |
|---|---|
| `claude` | `.claude/skills/<name>/` |
| `codex_cli` | `.agents/skills/<name>/` |
| `qwen_code` | `.qwen/skills/<name>/` |
| `opencode` | `.opencode/skills/<name>/` |

- **Repo skills win.** If the worktree already contains that skill folder (the
  repo commits its own), it is left untouched and a log line is written.
  Folders we placed carry a `.ate-managed-skill` marker and are refreshed on
  the next run.
- **Never committed.** Each materialized path (`/.agents/skills/<name>/`, …) is
  appended to the repo's `.git/info/exclude` (shared by all its worktrees), so
  it never shows in `git status`, commits or PR diffs.
- Failures are logged and never fail the run.

Capability support per provider is tracked in
`frontend/src/lib/providerCapabilities.ts` (the "Skills library" row of the
[agents.md](agents.md) matrix).
