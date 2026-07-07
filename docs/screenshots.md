# Regenerating README/docs Screenshots and the Hero GIF

`docs/img/` holds the screenshots and hero GIF used in `README.md` and
`docs/overview.md`. This doc is the runbook for retaking them after a UI
change — read it before starting so you don't rediscover the same gotchas.

## 1. Start an isolated demo instance

Never capture screenshots against your normal dev DB — the Dashboard
aggregates cost/run stats across **all** repos in the database, so a real
instance leaks real cost figures, real task titles, and real history into a
public screenshot.

```bash
DB_PATH=demo-screenshots.db ./dev.sh dev
```

This gives you a completely empty board on a throwaway SQLite file (git-
ignored; delete it any time).

## 2. Seed demo data

```bash
./scripts/seed-demo.sh
```

Creates a disposable git repo at `~/code_projects/demo-repo` (must live under
`REPO_BASE_DIR` — a path like `/tmp/...` gets rejected by the backend),
registers it, and creates 6 tasks spread one-per-column across the default
workflow (`not_ready`, `plan`, `work`, `testing`, `review`, `done`). Re-run any
time; it wipes and rebuilds the demo repo from scratch.

## 3. Get one task with a real agent run (logs + diff)

The Task Detail, Logs, and Diff screenshots need a task with genuine run
history — don't hand-fabricate `agent_runs`/`agent_logs` rows, it's fragile
and the UI will look subtly wrong. Instead run a real (cheap) dispatch:

1. Create an agent config targeting `work`, using a task description that
   actually matches the toy demo repo's `src/app.py` (a Python file with one
   `greet()` function) — e.g. "add a `farewell(name)` function". A task
   description that doesn't match the repo (e.g. referencing Go tests) just
   produces a "task doesn't match repo" run with an empty diff.

   ```bash
   curl -s -X POST http://localhost:8080/api/v1/agents -H "Content-Type: application/json" -d '{
     "name": "demo-claude-work",
     "provider": "claude",
     "labels": "[\"work\"]",
     "system_prompt": "Make one minimal, real code change addressing the task description, then call signal_complete."
   }'
   ```

   Note `labels` is a **JSON-encoded string**, not a raw array, and holds
   label *names* not UUIDs.

2. Move the task to `work` (drag it in the UI, or `PATCH /tasks/{id}/label`).
   The dispatcher sweeps every 5s and picks it up automatically.

3. Add one inline review comment so the Diff screenshot shows the "inline
   comment" feature the README calls out:

   ```bash
   curl -s -X POST "http://localhost:8080/api/v1/tasks/$TASK/review-comments" \
     -H "Content-Type: application/json" -d '{
       "file_path": "src/app.py", "side": "new",
       "start_line": 4, "end_line": 5,
       "quoted_text": "def farewell(name):\n    return f\"Goodbye, {name}!\"",
       "body": "Nice, matches the existing style. Can we add a docstring too?"
     }'
   ```

4. **Check the worktree for stray files before screenshotting the diff.** If
   you (or any Claude Code session) `cd` into
   `<demo-repo>/.ate-worktrees/<task-id>/` while a HUD/statusline hook is
   active, it can write files (e.g. `.omc/state/...`) into that directory,
   and the app's auto-commit-per-run will sweep them into the diff — showing
   up as noise (and a real session transcript path!) in the Diff/Logs
   screenshot. `rm -rf .omc && git rm -r --cached .omc 2>/dev/null; echo
   ".omc/" > .gitignore && git commit -am "cleanup"` in the worktree, or
   just don't `cd` into worktrees from an active Claude Code session.

## 4. Capture the static screenshots

Most pages (`board`, `dashboard`, `health`, `workflow`, task detail's default
Overview tab) load fine with a plain single-shot headless capture:

```bash
google-chrome --headless --disable-gpu --no-sandbox --window-size=1440,900 \
  --screenshot=docs/img/board.png http://localhost:5173/board
```

The task detail page's **Logs** and **Diff** tabs are plain `useState`, not a
URL param — a single-shot load always lands on Overview. Use
`scripts/cdp_shot.py` instead, which drives headless Chrome over the
DevTools Protocol to click a tab by its visible text before capturing:

```bash
pip install --user websocket-client requests   # or use a venv
python3 scripts/cdp_shot.py "http://localhost:5173/tasks/$TASK" "Logs" docs/img/task-logs.png
python3 scripts/cdp_shot.py "http://localhost:5173/tasks/$TASK" "Diff" docs/img/diff-viewer.png
```

Gotchas already worked out in that script:
- Needs `--headless=new`, not legacy `--headless` — the old mode's
  `Page.captureScreenshot` can hang forever on this stack.
- Needs `--remote-allow-origins=*` — Chrome otherwise rejects the devtools
  websocket handshake as a foreign origin.

**`pkill -f` footgun**: if you ever clean up a stray headless Chrome process
with `pkill -f "remote-debugging-port=9333"` (or any pattern that appears
verbatim in your *own* shell command), `pkill -f` matches against full
command lines — including the invoking shell's own argv — and can kill your
current shell/session instead of (or in addition to) the target. Prefer
`pkill -f` patterns that can't match your own invocation, or target by PID.

## 5. Blur any leaked local paths

Screenshots of the Health page and agent logs will contain real absolute
paths (`/home/<you>/...`) from wherever the demo repo/backend happen to live
on your machine. Box-blur those specific regions rather than reshooting with
a different path:

```python
from PIL import Image, ImageFilter
img = Image.open("docs/img/health.png")
box = (x0, y0, x1, y1)  # find via a quick crop/inspect
img.paste(img.crop(box).filter(ImageFilter.GaussianBlur(radius=8)), box)
img.save("docs/img/health.png")
```

## 6. Capture the hero GIF

The GIF needs real interaction (drag-and-drop, clicking Approve) that
headless CDP scripting can't easily drive — use the interactive
`claude-in-chrome` browser extension's `gif_creator` tool instead:

1. `start_recording`
2. Drag a task from one column into an agent-triggered column (e.g.
   `review-plan` → `work`)
3. Navigate to a task that already has completed logs + diff (no need to
   wait for a fresh dispatch — reuse the one from step 3 above) and click
   through Logs → Diff
4. Click **Approve** to move it to `done`
5. `stop_recording`, then `export` with `download: true`

This produces a real ~10-15s sequence without waiting through a live 30-60s
agent dispatch for the recording itself.

## 7. Wire into the docs

Screenshots referenced from `README.md` and `docs/overview.md` use
`docs/img/*.png` and `docs/img/hero-demo.gif` — keep filenames stable so
re-running this process is a drop-in replacement, no doc edits needed unless
you're adding a new shot.
