package agentuser

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func withConfig(t *testing.T, c Config) {
	t.Helper()
	old := current.Load()
	current.Store(&c)
	t.Cleanup(func() { current.Store(old) })
}

func TestServerCommandWrapsWhenIsolationOn(t *testing.T) {
	withConfig(t, Config{Enabled: true, UID: 10001, GID: 1000, SetprivPath: "/usr/bin/setpriv"})
	for name, cmd := range map[string]*exec.Cmd{
		"plain":   ServerCommand("sh", "-c", "true"),
		"context": ServerCommandContext(context.Background(), "sh", "-c", "true"),
	} {
		want := []string{"/usr/bin/setpriv", "--inh-caps=-all", "--ambient-caps=-all", "--"}
		if cmd.Path != "/usr/bin/setpriv" || len(cmd.Args) < 6 || strings.Join(cmd.Args[:4], " ") != strings.Join(want, " ") {
			t.Fatalf("%s: not wrapped: path=%q args=%q", name, cmd.Path, cmd.Args)
		}
		if !strings.HasSuffix(cmd.Args[4], "sh") || cmd.Args[5] != "-c" || cmd.Args[6] != "true" {
			t.Fatalf("%s: original argv lost: %q", name, cmd.Args)
		}
		// Server children must keep the server uid: no Credential switch.
		if cmd.SysProcAttr != nil && cmd.SysProcAttr.Credential != nil {
			t.Fatalf("%s: server command must not switch uid", name)
		}
	}
}

func TestServerCommandNoopWhenIsolationOff(t *testing.T) {
	withConfig(t, Config{Reason: "off"})
	cmd := ServerCommand("sh", "-c", "true")
	if strings.Contains(strings.Join(cmd.Args, " "), "setpriv") || len(cmd.Args) != 3 {
		t.Fatalf("expected untouched command, got %q", cmd.Args)
	}
	DropCaps(nil) // must not panic
}

// TestServerChildrenDropCapsEndToEnd emulates the container: a process holding
// ambient CAP_SETUID/SETGID/KILL as a non-root uid spawns git via ServerCommand;
// a git hook (core.fsmonitor, as a malicious repo would plant) records its own
// capability sets, which must all be zero. Needs root + setpriv; skipped otherwise.
func TestServerChildrenDropCapsEndToEnd(t *testing.T) {
	if out := os.Getenv("AGENTUSER_E2E_OUT"); out != "" {
		e2eChild(t, out)
		return
	}
	sp, err := exec.LookPath("setpriv")
	if os.Geteuid() != 0 || err != nil {
		t.Skip("needs root and setpriv")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir, err := os.MkdirTemp("", "agentuser-e2e")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	self, _ := os.Executable()
	bin := filepath.Join(dir, "test.bin")
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, data, 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if err := os.Mkdir(out, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(out, 0o777); err != nil {
		t.Fatal(err)
	}
	caps := "+setuid,+setgid,+kill"
	cmd := exec.Command(sp, "--reuid=1000", "--regid=1000", "--clear-groups",
		"--inh-caps="+caps, "--ambient-caps="+caps, "--bounding-set=+setuid,+setgid,+kill", "--",
		bin, "-test.run=^TestServerChildrenDropCapsEndToEnd$", "-test.v")
	cmd.Env = append(os.Environ(), "AGENTUSER_E2E_OUT="+out, "HOME="+out, "TMPDIR="+out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child failed: %v\n%s", err, b)
	}
	for _, f := range []string{"wrapped.status", "unwrapped.status"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Fatalf("missing %s: %v", f, err)
		}
	}
}

func capsOf(t *testing.T, status string) map[string]string {
	m := map[string]string{}
	for _, l := range strings.Split(status, "\n") {
		for _, set := range []string{"CapInh", "CapPrm", "CapEff", "CapAmb"} {
			if v, ok := strings.CutPrefix(l, set+":"); ok {
				m[set] = strings.TrimSpace(v)
			}
		}
	}
	if len(m) != 4 {
		t.Fatalf("no cap sets in %q", status)
	}
	return m
}

func e2eChild(t *testing.T, out string) {
	self, _ := os.ReadFile("/proc/self/status")
	if capsOf(t, string(self))["CapAmb"] == "0000000000000000" {
		t.Fatal("test setup: server process has no ambient caps")
	}
	repo := filepath.Join(out, "repo")
	if o, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, o)
	}
	run := func(name, hook string) {
		script := filepath.Join(out, name+".sh")
		body := "#!/bin/sh\ncat /proc/self/status > " + filepath.Join(out, name+".status") + "\nexit 0\n"
		if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		cfg := exec.Command("git", "-C", repo, "config", "core.fsmonitor", script)
		if o, err := cfg.CombinedOutput(); err != nil {
			t.Fatalf("git config: %v %s", err, o)
		}
		c := hookCmd(name, repo)
		if o, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git status (%s): %v %s", name, err, o)
		}
		b, err := os.ReadFile(filepath.Join(out, name+".status"))
		if err != nil {
			t.Fatalf("hook for %s did not run: %v", name, err)
		}
		_ = hook
		caps := capsOf(t, string(b))
		if name == "unwrapped" {
			if caps["CapAmb"] == "0000000000000000" {
				t.Fatalf("control: unwrapped child unexpectedly has no ambient caps (%v)", caps)
			}
			return
		}
		for set, v := range caps {
			if strings.Trim(v, "0") != "" {
				t.Fatalf("server child leaked %s=%s", set, v)
			}
		}
	}
	withConfig(t, Config{Enabled: true, UID: 10001, GID: 1000, SetprivPath: mustLook(t, "setpriv")})
	run("unwrapped", "")
	run("wrapped", "")
}

func hookCmd(name, repo string) *exec.Cmd {
	if name == "wrapped" {
		return ServerCommand("git", "-C", repo, "status")
	}
	return exec.Command("git", "-C", repo, "status")
}

func mustLook(t *testing.T, n string) string {
	p, err := exec.LookPath(n)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestNoRawExecInServerCode fails when non-test backend code calls
// os/exec's Command/CommandContext directly. Server-side children must go
// through agentuser.ServerCommand[Context] (capability-dropping); agent-facing
// children use exec + agentuser.Apply and are allowlisted by file.
func TestNoRawExecInServerCode(t *testing.T) {
	allow := map[string]bool{
		"internal/agentuser/agentuser.go":   true, // the helpers + the isolation probe (runs through apply)
		"internal/agent/providers/cli.go":   true, // agent CLI; agentuser.Apply
		"internal/agent/providers/tools.go": true, // deprecated providers' run_bash/search; agentuser.Apply
		"internal/agent/terminal.go":        true, // chat terminal CLI; agentuser.Apply
	}
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); n == "vendor" || n == "node_modules" || n == "testdata" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if allow[rel] {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		local := ""
		for _, im := range f.Imports {
			if im.Path.Value == `"os/exec"` {
				local = "exec"
				if im.Name != nil {
					local = im.Name.Name
				}
			}
		}
		if local == "" {
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == local &&
				(sel.Sel.Name == "Command" || sel.Sel.Name == "CommandContext") {
				t.Errorf("%s: raw exec.%s; use agentuser.ServerCommand[Context] (or add an Apply-based allowlist entry)", fset.Position(sel.Pos()), sel.Sel.Name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
