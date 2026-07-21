package health

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// latestGitHubReleaseRepo is the repo whose releases are checked for updates.
const latestGitHubReleaseRepo = "myinisjap/agent-task-editor"

// latestGitHubReleaseViaGH shells out to `gh release view` to fetch the
// latest release tag for latestGitHubReleaseRepo, bounded by a short timeout
// so an offline/hung gh invocation can't stall the caller.
func latestGitHubReleaseViaGH() (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "gh", "release", "view",
		"--repo", latestGitHubReleaseRepo,
		"--json", "tagName",
		"-q", ".tagName",
	).Output()
	if err != nil {
		return "", false
	}
	tag := strings.TrimSpace(string(out))
	if tag == "" {
		return "", false
	}
	return tag, true
}

// ghCheck folds in the existing gh auth status probe.
func ghCheck(d Deps) Check {
	c := Check{ID: "gh_auth", Name: "GitHub CLI auth"}
	authed, note := d.GHAuthStatus()
	if authed {
		c.Status = StatusOK
		c.Detail = "gh authenticated (" + note + ")"
		return c
	}
	c.Status = StatusWarn
	c.Detail = "gh credentials not found"
	c.Hint = "Mount ~/.config/gh or set GITHUB_TOKEN to enable PR creation and PR-state sync."
	return c
}

// repoBaseDirCheck surfaces the REPO_BASE_DIR sandbox setting (startup-only warning today).
func repoBaseDirCheck(in Input, d Deps) Check {
	c := Check{ID: "repo_base_dir", Name: "Repo base directory"}
	if in.RepoBaseDir == "" {
		c.Status = StatusWarn
		c.Detail = "REPO_BASE_DIR is not set — any host path may be registered as a repo"
		c.Hint = "Set REPO_BASE_DIR to restrict repo paths; recommended for production deployments."
		return c
	}
	if !d.FileExists(in.RepoBaseDir) {
		c.Status = StatusError
		c.Detail = "REPO_BASE_DIR is set but does not exist: " + in.RepoBaseDir
		c.Hint = "Create the directory or point REPO_BASE_DIR at an existing path."
		return c
	}
	c.Status = StatusOK
	c.Detail = "repo paths restricted to " + in.RepoBaseDir
	return c
}

// autoBackupCheck surfaces whether the built-in scheduled local-snapshot
// backup (BACKUP_DIR/BACKUP_INTERVAL/BACKUP_KEEP) is enabled. This is
// informational only — the on-demand GET /api/v1/backup endpoint and the
// Health page's "Download backup" button work regardless of this setting.
func autoBackupCheck(in Input) Check {
	c := Check{ID: "auto_backup", Name: "Automatic local backups"}
	if in.BackupDir == "" {
		c.Status = StatusWarn
		c.Detail = "BACKUP_DIR is not set — scheduled local snapshots are disabled"
		c.Hint = "Set BACKUP_DIR to enable scheduled snapshots; see docs/backup.md."
		return c
	}
	c.Status = StatusOK
	c.Detail = fmt.Sprintf("writing snapshots to %s every %s, keeping the newest %d", in.BackupDir, in.BackupInterval, in.BackupKeep)
	return c
}

// dbSizeCheck is purely informational: it surfaces the SQLite file size and
// the agent_logs row count so bloat is visible before it slows down backups
// or log-list queries (see internal/logretention for the opt-in pruner).
// Never StatusError - a large DB isn't a failure state on its own. Status is
// StatusWarn only when the size couldn't be read at all (e.g. DBSizeBytes is
// 0 because the caller's os.Stat failed), so the row still communicates
// something is off instead of silently showing "0 B".
func dbSizeCheck(in Input) Check {
	c := Check{ID: "db_size", Name: "Database size"}
	if in.DBSizeBytes <= 0 {
		c.Status = StatusWarn
		c.Detail = "could not read database file size"
		return c
	}
	c.Status = StatusOK
	c.Detail = fmt.Sprintf("%s, %s agent_logs rows", formatBytes(in.DBSizeBytes), formatCount(in.AgentLogsCount))
	return c
}

// versionCheck is purely informational: it surfaces the running build's
// version so operators can tell at a glance which image/commit is deployed.
// Always StatusOK — an unstamped "dev" build isn't a failure state, just the
// expected result of a local (non-release) build.
func versionCheck(in Input) Check {
	c := Check{ID: "version", Name: "Version", Status: StatusOK}
	v := in.Version
	if v == "" {
		v = "dev"
	}
	if v == "dev" {
		c.Detail = "running dev (unreleased build)"
	} else {
		c.Detail = "running " + v
	}
	return c
}

// updateCheck compares the running version against the latest published
// GitHub release tag. Only called when Input.CheckForUpdates is true (see
// Checks). Never returns StatusError — network/gh failures degrade to
// StatusWarn so a self-hosted, offline deployment doesn't show a false
// "broken" state just because it can't reach GitHub.
func updateCheck(in Input, d Deps) Check {
	c := Check{ID: "update_check", Name: "Update available"}
	if d.LatestGitHubRelease == nil {
		c.Status = StatusWarn
		c.Detail = "could not check for updates (offline or gh unavailable)"
		return c
	}
	latest, ok := d.LatestGitHubRelease()
	if !ok || latest == "" {
		c.Status = StatusWarn
		c.Detail = "could not check for updates (offline or gh unavailable)"
		return c
	}

	current := in.Version
	if current == "" || current == "dev" {
		// A dev build has no meaningful version to compare — just surface the
		// latest release as informational rather than claiming "up to date"
		// or "update available".
		c.Status = StatusWarn
		c.Detail = "running a dev build; latest release is " + latest
		c.Hint = "https://github.com/" + latestGitHubReleaseRepo + "/releases"
		return c
	}

	if semverCompare(current, latest) < 0 {
		c.Status = StatusWarn
		c.Detail = "update available: " + latest + " (running " + current + ")"
		c.Hint = "https://github.com/" + latestGitHubReleaseRepo + "/releases"
		return c
	}

	c.Status = StatusOK
	c.Detail = "up to date (" + current + ")"
	return c
}

// semverCompare does a minimal semver-ish comparison of two version strings,
// each optionally prefixed with "v" (e.g. "v1.4.0", "1.4.0"). Returns -1 if a
// < b, 0 if equal, 1 if a > b. Non-numeric/malformed segments compare as 0
// (equal) rather than erroring, since this only feeds a best-effort,
// never-fatal health check.
func semverCompare(a, b string) int {
	pa := semverParts(a)
	pb := semverParts(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// semverParts parses "vX.Y.Z" (or "X.Y.Z", with any non-numeric suffix like
// "-rc1" ignored) into [X, Y, Z], defaulting missing/unparseable segments to 0.
func semverParts(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	// Drop any pre-release/build metadata suffix (e.g. "1.4.0-rc1+build").
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	segs := strings.SplitN(v, ".", 3)
	var out [3]int
	for i := 0; i < len(segs) && i < 3; i++ {
		if n, err := strconv.Atoi(segs[i]); err == nil {
			out[i] = n
		}
	}
	return out
}

// formatBytes renders n as a human-readable size using binary (1024) units,
// e.g. "42.3 MB". No existing helper for this in the codebase.
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	return fmt.Sprintf("%.1f %s", float64(n)/float64(div), units[exp])
}

// formatCount renders n with thousands separators, e.g. "118,204".
func formatCount(n int64) string {
	s := fmt.Sprintf("%d", n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}
