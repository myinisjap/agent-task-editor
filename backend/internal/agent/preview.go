package agent

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/gitcmd"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"
)

// DefaultPreviewGlobs decide when a diff counts as a UI change if the repo has
// no preview_paths_glob.
var DefaultPreviewGlobs = []string{"frontend/**", "web/**", "src/**/*.{tsx,jsx,vue,svelte,css}"}

// Viewport is one screenshot size.
type Viewport struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Name   string `json:"name"`
}

// PreviewViewports are the sizes every route is captured at.
var PreviewViewports = []Viewport{{1280, 800, "desktop"}, {390, 844, "phone"}}

const (
	previewKeepCaptures = 3
	previewMaxImageSize = 10 << 20
	previewOverallLimit = 5 * time.Minute
	previewStartTimeout = 120 * time.Second
	previewMaxErrLen    = 300
)

// PreviewRequest describes one preview capture.
type PreviewRequest struct {
	Command      string
	Dir          string
	Routes       []string
	Viewports    []Viewport
	OutDir       string
	StartTimeout time.Duration
	Runtime      *RuntimeSpec
}

// PreviewShot is one captured screenshot; File is relative to OutDir.
type PreviewShot struct {
	Route         string
	Width, Height int
	File          string
	// Err is set when this single shot failed.
	Err string
}

// PreviewResult is the outcome of a capture. Shots may be non-empty alongside
// Err (partial success).
type PreviewResult struct {
	Shots []PreviewShot
	Err   error
}

// PreviewCapturer starts the repo preview command, screenshots the routes and
// stops the process tree. The concrete implementation lives in providers.
type PreviewCapturer interface {
	Capture(ctx context.Context, req PreviewRequest) PreviewResult
}

// expandBraces expands one level of {a,b} alternation in a glob.
func expandBraces(pattern string) []string {
	open := strings.IndexByte(pattern, '{')
	if open < 0 {
		return []string{pattern}
	}
	end := strings.IndexByte(pattern[open:], '}')
	if end < 0 {
		return []string{pattern}
	}
	end += open
	var out []string
	for _, alt := range strings.Split(pattern[open+1:end], ",") {
		out = append(out, expandBraces(pattern[:open]+alt+pattern[end+1:])...)
	}
	return out
}

func splitSettingLines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r", "\n"), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// PreviewGlobs returns the repo's configured globs, or the defaults.
func PreviewGlobs(setting string) []string {
	if g := splitSettingLines(setting); len(g) > 0 {
		return g
	}
	return DefaultPreviewGlobs
}

// ParsePreviewRoutes splits the newline-separated routes setting.
func ParsePreviewRoutes(setting string) []string { return splitSettingLines(setting) }

// MatchesPreviewGlobs reports whether any changed file matches any glob.
func MatchesPreviewGlobs(files, globs []string) bool {
	for _, g := range globs {
		for _, p := range expandBraces(g) {
			for _, f := range files {
				if matchLessonGlob(p, f) {
					return true
				}
			}
		}
	}
	return false
}

// shouldCapturePreview decides whether a diff warrants a preview and returns
// the routes to capture. Unset command or routes is a no-op.
func shouldCapturePreview(repo gen.Repo, changed []string) ([]string, bool) {
	if strings.TrimSpace(repo.PreviewCommand) == "" {
		return nil, false
	}
	routes := ParsePreviewRoutes(repo.PreviewRoutes)
	if len(routes) == 0 {
		return nil, false
	}
	if !MatchesPreviewGlobs(changed, PreviewGlobs(repo.PreviewPathsGlob)) {
		return nil, false
	}
	return routes, true
}

func truncateErr(s string) string {
	s = strings.TrimSpace(strings.ToValidUTF8(s, ""))
	if len(s) > previewMaxErrLen {
		s = s[:previewMaxErrLen] + "..."
	}
	return s
}

// maybeCapturePreview kicks off an asynchronous screenshot capture for a
// successful run whose task landed on a human gate. It never changes the task
// label, failure count or run status; every failure becomes a failed capture.
func (p *Pool) maybeCapturePreview(ctx context.Context, job Job, resolvedLabel string, log *slog.Logger) {
	if p.Previewer == nil || p.UploadDir == "" {
		return
	}
	task, err := p.q.GetTask(ctx, job.Input.Task.ID)
	if err != nil {
		return
	}
	repo, err := p.q.GetRepo(ctx, task.RepoID)
	if err != nil || strings.TrimSpace(repo.PreviewCommand) == "" {
		return
	}
	labels, err := p.q.ListWorkflowLabels(ctx, task.WorkflowID)
	if err != nil {
		return
	}
	transitions, err := p.q.ListWorkflowTransitions(ctx, task.WorkflowID)
	if err != nil {
		return
	}
	if !workflow.IsHumanGateLabel(labels, transitions, resolvedLabel) {
		return
	}
	dir := verifyDir(job)
	branch := task.Branch
	if branch == "" {
		branch = job.Input.Task.Branch
	}
	if dir == "" || branch == "" || task.BaseRef == "" || !gitcmd.IsValidRef(branch) || !gitcmd.IsValidRef(task.BaseRef) {
		return
	}
	changed, err := changedFilesAgainstBase(ctx, dir, task.BaseRef, branch)
	if err != nil {
		log.Warn("pool: preview: list changed files", "err", err)
		return
	}
	routes, ok := shouldCapturePreview(repo, changed)
	if !ok {
		return
	}
	runID := job.RunID
	capture, err := p.q.CreatePreviewCapture(ctx, gen.CreatePreviewCaptureParams{
		ID: uuid.NewString(), TaskID: task.ID, RunID: &runID,
	})
	if err != nil {
		log.Warn("pool: preview: create capture", "err", err)
		return
	}
	p.publishPreview(task.ID, capture.ID, "running")

	command := repo.PreviewCommand
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		// Detach from the run's lifetime but stay tied to the pool's ctx.
		cctx, cancel := context.WithTimeout(ctx, previewOverallLimit)
		defer cancel()
		status, errMsg := p.runPreviewCapture(cctx, task.ID, capture.ID, PreviewRequest{
			Command: command, Dir: dir, Routes: routes, Viewports: PreviewViewports,
			StartTimeout: previewStartTimeout, Runtime: job.Input.Runtime,
		}, log)
		// The server context may be cancelled; record the outcome regardless.
		fctx, fcancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer fcancel()
		if err := p.q.FinishPreviewCapture(fctx, gen.FinishPreviewCaptureParams{
			Status: status, Error: truncateErr(errMsg), ID: capture.ID,
		}); err != nil {
			log.Warn("pool: preview: finish capture", "err", err)
		}
		p.prunePreviews(fctx, task.ID, log)
		p.publishPreview(task.ID, capture.ID, status)
	}()
}

func (p *Pool) publishPreview(taskID, captureID, status string) {
	if p.pub != nil {
		p.pub.Publish(wsevent.TypeTaskPreview, wsevent.TaskPreview{TaskID: taskID, CaptureID: captureID, Status: status})
	}
}

// runPreviewCapture runs the capturer and stores its images. It returns the
// final capture status and an error line (possibly with status ok for partial
// success).
func (p *Pool) runPreviewCapture(ctx context.Context, taskID, captureID string, req PreviewRequest, log *slog.Logger) (string, string) {
	outDir, err := os.MkdirTemp("", "ate-preview-*")
	if err != nil {
		return "failed", err.Error()
	}
	defer os.RemoveAll(outDir)
	// The capture runs as the agent user, which is in the server user's group.
	_ = os.Chmod(outDir, 0o770)
	req.OutDir = outDir

	res := p.Previewer.Capture(ctx, req)

	destDir := filepath.Join(p.UploadDir, taskID)
	var failures []string
	saved := 0
	for _, shot := range res.Shots {
		if shot.Err != "" || shot.File == "" {
			failures = append(failures, fmt.Sprintf("%s@%d: %s", shot.Route, shot.Width, shot.Err))
			continue
		}
		rel := filepath.Base(shot.File) // never trust the script's path
		data, err := readLimited(filepath.Join(outDir, rel), previewMaxImageSize)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s@%d: %v", shot.Route, shot.Width, err))
			continue
		}
		if http.DetectContentType(data) != "image/png" {
			failures = append(failures, fmt.Sprintf("%s@%d: not a PNG", shot.Route, shot.Width))
			continue
		}
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			return "failed", err.Error()
		}
		name := "preview-" + uuid.NewString() + ".png"
		if err := os.WriteFile(filepath.Join(destDir, name), data, 0o644); err != nil {
			failures = append(failures, err.Error())
			continue
		}
		if err := p.q.CreatePreviewImage(context.WithoutCancel(ctx), gen.CreatePreviewImageParams{
			ID: uuid.NewString(), CaptureID: captureID, TaskID: taskID,
			Route: shot.Route, Variant: "after", Width: int64(shot.Width), Height: int64(shot.Height),
			Path: taskID + "/" + name,
		}); err != nil {
			_ = os.Remove(filepath.Join(destDir, name))
			failures = append(failures, err.Error())
			continue
		}
		saved++
	}
	if saved == 0 {
		msg := ""
		if res.Err != nil {
			msg = res.Err.Error()
		} else if len(failures) > 0 {
			msg = strings.Join(failures, "; ")
		} else {
			msg = "no screenshots were captured"
		}
		return "failed", strings.TrimPrefix(msg, "preview failed: ")
	}
	if res.Err != nil {
		failures = append(failures, res.Err.Error())
	}
	return "ok", strings.Join(failures, "; ")
}

func readLimited(path string, max int64) ([]byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > max {
		return nil, fmt.Errorf("screenshot missing or too large")
	}
	return os.ReadFile(path)
}

// prunePreviews keeps the newest previewKeepCaptures captures for a task.
func (p *Pool) prunePreviews(ctx context.Context, taskID string, log *slog.Logger) {
	old, err := p.q.ListOldPreviewCaptures(ctx, gen.ListOldPreviewCapturesParams{TaskID: taskID, Limit: previewKeepCaptures})
	if err != nil {
		log.Warn("pool: preview: list old captures", "err", err)
		return
	}
	for _, c := range old {
		paths, _ := p.q.ListPreviewImagePathsByCapture(ctx, c.ID)
		for _, rel := range paths {
			// Stored paths are server-generated "<task>/<file>"; re-derive from
			// the base name so a corrupt row cannot escape the task dir.
			_ = os.Remove(filepath.Join(p.UploadDir, taskID, filepath.Base(rel)))
		}
		if err := p.q.DeletePreviewCapture(ctx, c.ID); err != nil {
			log.Warn("pool: preview: delete old capture", "err", err)
		}
	}
}
