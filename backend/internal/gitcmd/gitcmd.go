// Package gitcmd is the single place the server shells out to the git binary.
// Every invocation goes through agentuser.ServerCommandContext (capabilities
// dropped, see TestNoRawExecInServerCode). It also owns the shared git-ref
// validator and a tiny directory helper. Leaf package: imports only agentuser
// and the standard library.
package gitcmd

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/myinisjap/agent-task-editor/backend/internal/agentuser"
)

// command builds the git invocation. A non-empty dir becomes `git -C dir`;
// an empty dir runs git in the server's own working directory.
func command(ctx context.Context, dir string, args []string) *exec.Cmd {
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	return agentuser.ServerCommandContext(ctx, "git", args...)
}

// Output runs git and returns its stdout. Errors are the raw *exec.ExitError
// (stderr is available on it via ExitError.Stderr) or a start failure.
func Output(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return command(ctx, dir, args).Output()
}

// Run runs git, discarding output.
func Run(ctx context.Context, dir string, args ...string) error {
	return command(ctx, dir, args).Run()
}

// Combined runs git and returns interleaved stdout+stderr.
func Combined(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return command(ctx, dir, args).CombinedOutput()
}

// NoPromptEnv returns the process environment with interactive credential
// prompts suppressed, so a missing credential fails fast instead of blocking
// on stdin forever.
func NoPromptEnv() []string {
	return append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
		"GCM_INTERACTIVE=never",
	)
}

// CombinedNoPrompt is Combined with NoPromptEnv applied.
func CombinedNoPrompt(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := command(ctx, dir, args)
	cmd.Env = NoPromptEnv()
	return cmd.CombinedOutput()
}

// validRef matches HEAD, HEAD~N, HEAD^N, 40/64-char hex SHAs, and safe
// branch/tag names. First char must be alphanumeric to prevent flag injection
// (e.g. --no-index).
var validRef = regexp.MustCompile(`^(HEAD([~^][0-9]+)?|[0-9a-f]{40,64}|[a-zA-Z0-9][a-zA-Z0-9._/-]*)$`)

// IsValidRef reports whether ref is safe to pass to git as a revision argument.
func IsValidRef(ref string) bool {
	return validRef.MatchString(ref) && !strings.Contains(ref, "..")
}

// DirExists reports whether p exists and is a directory.
func DirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
